package tls

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// PreflightResult is one check's outcome.
type PreflightResult struct {
	Check  string `json:"check"`
	OK     bool   `json:"ok"`
	Fatal  bool   `json:"fatal"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// proxyRanges are the networks a CDN answers on. HTTP-01 sent to one of these
// never reaches this server, which is the single most common cause of a failed
// first issuance — and it looks like a ratline bug rather than a DNS setting.
var proxyRanges = []struct {
	name     string
	prefixes []string
}{
	{"Cloudflare", []string{
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
		"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
		"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
		"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
		"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
	}},
	{"Fastly", []string{"151.101.0.0/16", "199.232.0.0/16", "23.235.32.0/20", "43.249.72.0/22"}},
	{"Akamai", []string{"23.32.0.0/11", "23.64.0.0/14", "104.64.0.0/10"}},
}

// Preflight runs every check that can be made before an ACME attempt is spent.
//
// All of them run, and all the problems are reported together. Fixing one per
// attempt is a poor way to spend a rate-limit budget, and the CA counts failed
// validations.
func (m *Manager) Preflight(ctx context.Context, opts *IssueOptions, names []string) ([]PreflightResult, error) {
	var results []PreflightResult
	add := func(check string, ok, fatal bool, detail, fix string) {
		results = append(results, PreflightResult{Check: check, OK: ok, Fatal: fatal && !ok, Detail: detail, Fix: fix})
	}
	timeout := m.Cfg.ACME.PreflightTimeout.D()
	if timeout <= 0 {
		timeout = 20 * time.Second
	}

	// 1. The site exists and is enabled.
	bareDomain := strings.TrimPrefix(opts.Domain, "*.")
	site, siteErr := m.State.FindSiteByName(ctx, bareDomain)
	switch {
	case siteErr != nil:
		add("site", false, true, fmt.Sprintf("no site is configured for %s", bareDomain),
			fmt.Sprintf("create it first: ratline site add %s --user <user> --runtime static", bareDomain))
	case !site.Enabled:
		// Not fatal: a disabled site still answers the challenge on purpose, so
		// that a paused site can still renew.
		add("site", true, false, "the site is disabled, but its vhost still answers the ACME challenge", "")
	default:
		add("site", true, false, "configured and enabled", "")
	}

	// 6. Tooling, checked early because nothing else matters without it.
	if !m.Bins("certbot") {
		add("tooling", false, true, "certbot is not installed",
			"apt-get install certbot")
	} else {
		add("tooling", true, false, "certbot is installed", "")
	}
	if opts.Challenge == "dns" {
		plugin := "python3-certbot-dns-" + opts.DNSProvider
		if !m.dnsPluginPresent(ctx, opts.DNSProvider) {
			add("dns-plugin", false, true,
				fmt.Sprintf("the certbot DNS plugin for %s is not installed", opts.DNSProvider),
				"apt-get install "+plugin)
		} else {
			add("dns-plugin", true, false, opts.DNSProvider+" plugin present", "")
		}
		// Credentials for a DNS API are as good as the domain itself.
		if fi, err := os.Stat(opts.DNSCredentials); err != nil {
			add("dns-credentials", false, true, "cannot read "+opts.DNSCredentials, "check the path")
		} else if fi.Mode().Perm()&0o077 != 0 {
			add("dns-credentials", false, true,
				fmt.Sprintf("%s is mode %04o; certbot refuses anything more open than 0600", opts.DNSCredentials, fi.Mode().Perm()),
				"chmod 0600 "+opts.DNSCredentials)
		} else {
			add("dns-credentials", true, false, "present and 0600", "")
		}
	}

	// 2. DNS, and 3. proxy detection.
	serverIPs, err := m.PublicAddresses(ctx)
	if err != nil {
		m.Log.Debug("could not determine this server's public addresses", "err", err)
	}
	if opts.Challenge == "http" {
		for _, name := range names {
			if validate.IsWildcard(name) {
				continue
			}
			m.checkDNS(ctx, name, serverIPs, timeout, add)
		}
	} else {
		add("dns", true, false, "not checked: DNS-01 does not require the name to resolve to this server", "")
	}

	// 5. Conflicts.
	conflicted := false
	for _, name := range names {
		if validate.IsWildcard(name) {
			continue
		}
		conflict, err := m.Nginx.ConflictingServerName(name, bareDomain)
		switch {
		case err != nil:
			// Reported, not swallowed. A preflight that says "no other vhost claims
			// these names" when it could not look is worse than one that admits it.
			add("conflict", false, false,
				fmt.Sprintf("could not be checked for %s: %s", name, firstLine(err.Error())),
				"check that the nginx sites-enabled directory is readable")
			conflicted = true
		case conflict != "":
			add("conflict", false, true,
				fmt.Sprintf("%s is already claimed by the nginx configuration %s", name, conflict),
				"remove the duplicate server_name; nginx resolves a collision unpredictably")
			conflicted = true
		}
	}
	if len(names) > 0 && !conflicted {
		add("conflict", true, false, "no other vhost claims these names", "")
	}

	// 4. Reachability, only meaningful for HTTP-01.
	if opts.Challenge == "http" && siteErr == nil {
		m.checkReachability(ctx, names[0], timeout, add)
	}

	// 7. Rate-limit budget.
	m.checkRateLimits(ctx, opts, names, add)

	// 8. Wildcards.
	if validate.IsWildcard(opts.Domain) {
		if opts.Challenge != "dns" {
			add("wildcard", false, true, "a wildcard cannot be validated over HTTP-01",
				"use --challenge dns with a provider plugin")
		} else {
			add("wildcard", true, false, "using DNS-01, which is the only option for a wildcard", "")
		}
	}
	return results, nil
}

// checkDNS resolves a name and compares it with this server's addresses.
func (m *Manager) checkDNS(ctx context.Context, name string, serverIPs []netip.Addr, timeout time.Duration, add func(string, bool, bool, string, string)) {
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	addrs, err := net.DefaultResolver.LookupNetIP(rctx, "ip", name)
	if err != nil {
		add("dns:"+name, false, true, "does not resolve: "+firstLine(err.Error()),
			"add an A or AAAA record for "+name+" pointing at this server")
		return
	}
	if proxy := detectProxy(addrs); proxy != "" {
		add("dns:"+name, false, true,
			fmt.Sprintf("resolves to a %s address (%s), so the HTTP-01 challenge will never reach this server", proxy, addrs[0]),
			"either turn the proxy off for this record while you issue, or use --challenge dns, "+
				"or import an origin certificate with 'ratline cert import'")
		return
	}
	if len(serverIPs) == 0 {
		add("dns:"+name, true, false,
			fmt.Sprintf("resolves to %s; this server's own address could not be determined, so no comparison was made", addrs[0]), "")
		return
	}
	for _, a := range addrs {
		for _, mine := range serverIPs {
			if a.Unmap() == mine.Unmap() {
				add("dns:"+name, true, false, "resolves to this server ("+a.String()+")", "")
				return
			}
		}
	}
	observed := make([]string, 0, len(addrs))
	for _, a := range addrs {
		observed = append(observed, a.String())
	}
	expected := make([]string, 0, len(serverIPs))
	for _, a := range serverIPs {
		expected = append(expected, a.String())
	}
	add("dns:"+name, false, true,
		fmt.Sprintf("resolves to %s, but this server is %s", strings.Join(observed, ", "), strings.Join(expected, ", ")),
		"point the DNS record at this server, wait for the TTL to pass, then try again — or --force if you are sure")
}

// checkReachability writes a token into the webroot and fetches it.
//
// The request is sent to the resolved address with the Host header set, rather
// than by hostname: a hairpin through the router can fail on hosts where the
// public internet would succeed, and /etc/hosts can make it succeed where the
// internet would fail. Connecting to the address the CA will use is the closest
// this can get to the CA's own view without involving a third party.
func (m *Manager) checkReachability(ctx context.Context, name string, timeout time.Duration, add func(string, bool, bool, string, string)) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		add("reachability", true, false, "skipped: could not generate a token", "")
		return
	}
	token := hex.EncodeToString(raw[:])
	dir := filepath.Join(m.Cfg.Paths.ACMEWebroot, ".well-known", "acme-challenge")
	if _, err := system.EnsureDir(dir, 0o755, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		add("reachability", false, false, "cannot write the challenge directory: "+err.Error(),
			"check that "+dir+" exists and is writable by root")
		return
	}
	path := filepath.Join(dir, token)
	if err := system.WriteFileAtomic(path, []byte(token), 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		add("reachability", false, false, "cannot write a challenge file: "+err.Error(), "")
		return
	}
	defer os.Remove(path)

	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	addrs, err := net.DefaultResolver.LookupNetIP(rctx, "ip", name)
	if err != nil || len(addrs) == 0 {
		add("reachability", false, true, "cannot test: "+name+" does not resolve", "fix DNS first")
		return
	}
	target := net.JoinHostPort(addrs[0].String(), "80")
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", target)
			},
		},
		// A redirect would mean the challenge location is being shadowed, which is
		// the failure worth reporting rather than following.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	url := "http://" + name + "/.well-known/acme-challenge/" + token
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, url, nil)
	if err != nil {
		add("reachability", false, false, "internal error building the request", "")
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		add("reachability", false, true,
			fmt.Sprintf("could not fetch %s: %s", url, firstLine(err.Error())),
			"open port 80 in the firewall; the ACME challenge is served there and renewal fails without it")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound {
		add("reachability", false, true,
			fmt.Sprintf("the challenge path redirects (HTTP %d to %s)", resp.StatusCode, resp.Header.Get("Location")),
			"something is redirecting /.well-known/acme-challenge/ — check "+
				filepath.Join(m.Cfg.Paths.NginxCustom, name+".conf"))
		return
	}
	body := make([]byte, len(token)+16)
	n, _ := resp.Body.Read(body)
	got := strings.TrimSpace(string(body[:n]))
	switch {
	case resp.StatusCode != http.StatusOK:
		add("reachability", false, true,
			fmt.Sprintf("%s returned HTTP %d", url, resp.StatusCode),
			"the vhost must serve "+m.Cfg.Paths.ACMEWebroot+" at /.well-known/acme-challenge/")
	case got != token:
		// Something is answering, but not this server — a proxy, or another vhost.
		add("reachability", false, true,
			"the challenge path is served by something else: the token did not come back",
			"another server or a proxy is answering for this name")
	default:
		add("reachability", true, false, "the challenge path serves this server's files", "")
	}
}

// checkRateLimits refuses an attempt that would exceed the CA's published limits.
func (m *Manager) checkRateLimits(ctx context.Context, opts *IssueOptions, names []string, add func(string, bool, bool, string, string)) {
	if opts.Staging || opts.CertbotDryRun {
		add("rate-limit", true, false, "not counted: staging and dry runs have their own, far larger limits", "")
		return
	}
	registered, err := validate.RegisteredDomain(opts.Domain)
	if err != nil {
		return
	}
	sanSet := state.SANSetKey(names)
	usage, err := m.State.ACMEUsageFor(ctx, registered, sanSet, time.Now())
	if err != nil {
		return
	}
	limits := m.Cfg.ACME.RateLimits

	// A countdown rather than a bare refusal: the operator needs to know when
	// they can try again, not just that they cannot now.
	if usage.CertsThisWeek >= limits.CertsPerRegisteredDomainPerWeek {
		add("rate-limit", false, true,
			fmt.Sprintf("%d certificates already issued for %s this week; the limit is %d",
				usage.CertsThisWeek, registered, limits.CertsPerRegisteredDomainPerWeek),
			"the oldest attempt ages out "+resetsIn(usage.OldestThisWeek, 7*24*time.Hour))
		return
	}
	if usage.DuplicatesThisWeek >= limits.DuplicateCertsPerWeek {
		add("rate-limit", false, true,
			fmt.Sprintf("%d certificates for this exact set of names already issued this week; the limit is %d",
				usage.DuplicatesThisWeek, limits.DuplicateCertsPerWeek),
			"add or remove a SAN to make it a different certificate, or wait "+
				resetsIn(usage.OldestThisWeek, 7*24*time.Hour))
		return
	}
	if usage.FailuresThisHour >= limits.FailedValidationsPerHour {
		add("rate-limit", false, true,
			fmt.Sprintf("%d validations for %s have failed in the last hour; the limit is %d",
				usage.FailuresThisHour, registered, limits.FailedValidationsPerHour),
			"fix the cause first, then wait "+resetsIn(usage.OldestFailure, time.Hour)+
				"; use --dry-run meanwhile, which costs nothing")
		return
	}
	if usage.OrdersLast3Hours >= limits.NewOrdersPer3Hours {
		add("rate-limit", false, true,
			fmt.Sprintf("%d orders in the last three hours; the limit is %d", usage.OrdersLast3Hours, limits.NewOrdersPer3Hours),
			"wait "+resetsIn(usage.OldestOrder, 3*time.Hour))
		return
	}
	add("rate-limit", true, false,
		fmt.Sprintf("%d/%d certificates and %d/%d duplicates used for %s this week",
			usage.CertsThisWeek, limits.CertsPerRegisteredDomainPerWeek,
			usage.DuplicatesThisWeek, limits.DuplicateCertsPerWeek, registered), "")
}

func resetsIn(oldest time.Time, window time.Duration) string {
	if oldest.IsZero() {
		return "when the window rolls over"
	}
	remaining := time.Until(oldest.Add(window))
	if remaining <= 0 {
		return "now"
	}
	if remaining < time.Hour {
		return fmt.Sprintf("in %d minute(s)", int(remaining.Minutes())+1)
	}
	return fmt.Sprintf("in %d hour(s)", int(remaining.Hours())+1)
}

// PublicAddresses reports this server's public addresses, cached in state
// because detecting them costs a network round trip and they rarely change.
func (m *Manager) PublicAddresses(ctx context.Context) ([]netip.Addr, error) {
	var out []netip.Addr
	// A configured value wins: on a host behind NAT or with a floating address,
	// only the operator knows the truth.
	for _, s := range append(m.Cfg.Server.PublicIPv4, m.Cfg.Server.PublicIPv6...) {
		if a, err := netip.ParseAddr(s); err == nil {
			out = append(out, a)
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	if cached, err := m.State.GetServerValue(ctx, "public_addresses"); err == nil && cached != "" {
		for _, s := range strings.Split(cached, ",") {
			if a, err := netip.ParseAddr(s); err == nil {
				out = append(out, a)
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}

	out = localAddresses()
	if len(out) > 0 && !m.DryRun {
		parts := make([]string, 0, len(out))
		for _, a := range out {
			parts = append(parts, a.String())
		}
		if err := m.State.SetServerValue(ctx, "public_addresses", strings.Join(parts, ",")); err != nil {
			m.Log.Debug("could not cache the public addresses", "err", err)
		}
	}
	return out, nil
}

// localAddresses lists the globally routable addresses on this host's interfaces.
func localAddresses() []netip.Addr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			addr, ok := netip.AddrFromSlice(ipnet.IP)
			if !ok {
				continue
			}
			addr = addr.Unmap()
			// A private address is not what a CA will connect to, so it is not
			// evidence either way.
			if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
				addr.IsUnspecified() || addr.IsMulticast() {
				continue
			}
			out = append(out, addr)
		}
	}
	return out
}

func detectProxy(addrs []netip.Addr) string {
	for _, r := range proxyRanges {
		for _, p := range r.prefixes {
			prefix, err := netip.ParsePrefix(p)
			if err != nil {
				continue
			}
			for _, a := range addrs {
				if prefix.Contains(a.Unmap()) {
					return r.name
				}
			}
		}
	}
	return ""
}

func (m *Manager) dnsPluginPresent(ctx context.Context, provider string) bool {
	res, err := m.Runner.Run(ctx, system.Cmd{Name: "certbot", Args: []string{"plugins", "--non-interactive"}, OKExit: []int{1}})
	if err != nil || res == nil {
		return false
	}
	return strings.Contains(res.Stdout, "dns-"+provider)
}

// PreflightError turns fatal results into one error naming everything wrong.
func PreflightError(domain string, results []PreflightResult) error {
	var problems []string
	var fixes []string
	for _, r := range results {
		if !r.Fatal {
			continue
		}
		problems = append(problems, r.Check+": "+r.Detail)
		if r.Fix != "" {
			fixes = append(fixes, r.Fix)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	e := rlerr.Preconditionf("%s is not ready for a certificate:\n  - %s", domain, strings.Join(problems, "\n  - "))
	if len(fixes) > 0 {
		e = e.WithHint("%s", strings.Join(fixes, "\n        "))
	}
	return e
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
