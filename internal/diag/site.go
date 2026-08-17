package diag

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/tls"
)

func siteSummary(s *state.Site) string {
	parts := []string{s.Runtime, "owned by " + s.Owner}
	if !s.Enabled {
		parts = append(parts, "disabled")
	}
	return strings.Join(parts, ", ")
}

// SiteChecks walks a site's request path in the order a request travels it.
//
// That order is the whole design: nginx accepts a connection, resolves a vhost,
// proxies to a socket, and a process answers. Checking in that sequence means the
// first failure is the cause and the rest are consequences — which is why every
// check below declares what it depends on rather than being independently reported.
func SiteChecks(env *Env, s *state.Site) []Check {
	siteDir := env.Cfg.SiteDir(s.Owner, s.Domain)
	socket := env.Cfg.SocketPath(s.Owner, s.Domain)

	checks := []Check{
		{
			ID:    "enabled",
			Title: "the site is enabled",
			Run: func(context.Context) Result {
				if !s.Enabled {
					return Fail("the site is disabled, so nginx is not serving it").
						WithFix("ratline site enable %s", s.Domain)
				}
				return Pass("")
			},
		},
		{
			ID:    "owner",
			Title: "the owning tenant exists",
			Run: func(context.Context) Result {
				if !system.UserExists(s.Owner) {
					// Everything below runs as this account, so nothing else can be
					// evaluated until it is back.
					return Fail("the tenant %s has no system account", s.Owner).
						WithFix("ratline reconcile --fix, then ratline troubleshoot %s", s.Owner)
				}
				return Pass("%s", s.Owner)
			},
		},
		{
			ID:    "vhost",
			Title: "nginx has a configuration for this domain",
			Needs: []string{"enabled"},
			Run: func(context.Context) Result {
				vhost := env.Cfg.VhostPath(s.Domain)
				if !system.Exists(vhost) {
					return Fail("there is no vhost at %s", vhost).
						WithFix("ratline reconcile --fix")
				}
				if !system.IsSymlink(env.Cfg.VhostLink(s.Domain)) {
					return Fail("the vhost exists but is not linked into sites-enabled").
						WithFix("ratline reconcile --fix")
				}
				return Pass("%s", vhost)
			},
		},
		{
			ID:    "nginx-config",
			Title: "nginx accepts its configuration",
			Needs: []string{"vhost"},
			Run: func(ctx context.Context) Result {
				if !env.Bins.Available("nginx") {
					return Skip("nginx is not installed")
				}
				if err := env.Nginx.Test(ctx); err != nil {
					// A configuration nginx refuses takes every site down, not just
					// this one, so it outranks anything site-specific below.
					return Fail("%s", firstLine(err.Error())).
						WithFix("nginx -t names the failing file and line; " +
							"ratline troubleshoot nginx checks the rest")
				}
				return Pass("")
			},
		},
		{
			ID:    "directories",
			Title: "the site directory is present",
			Needs: []string{"owner"},
			Run: func(context.Context) Result {
				if !system.Exists(siteDir) {
					return Fail("%s is missing", siteDir).
						WithFix("ratline reconcile --fix, or recreate the site")
				}
				return Pass("%s", siteDir)
			},
		},
	}

	if !s.Dynamic() {
		checks = append(checks, staticChecks(env, s, siteDir)...)
	} else {
		checks = append(checks, dynamicChecks(env, s, socket)...)
	}
	return append(checks, sharedSiteChecks(env, s)...)
}

// staticChecks cover a site with no process: the document root is the whole of it.
func staticChecks(env *Env, s *state.Site, siteDir string) []Check {
	return []Check{
		{
			ID:    "docroot",
			Title: "the document root has something in it",
			Needs: []string{"directories"},
			Run: func(context.Context) Result {
				root := filepath.Join(siteDir, orDefault(s.DocRoot, "public"))
				if !system.Exists(root) {
					return Fail("%s does not exist", root).
						WithFix("deploy your files, or check --root against your project layout")
				}
				index := filepath.Join(root, orDefault(s.IndexFile, "index.html"))
				if !system.Exists(index) && !s.SPA {
					return Warn("there is no %s, so a request for / will 403 or 404", index).
						WithFix("deploy an index document, or pass --spa if a router owns the routes")
				}
				return Pass("%s", root)
			},
		},
		{
			ID:    "docroot-readable",
			Title: "nginx can traverse to the document root",
			Needs: []string{"docroot"},
			Run: func(context.Context) Result {
				// nginx reaches the root through the tenant's group, so every
				// directory on the way needs the group execute bit. A home at 0700 is
				// the usual cause of a 403 on a file that plainly exists.
				home := env.Cfg.HomeDir(s.Owner)
				if fi, err := os.Stat(home); err == nil && fi.Mode().Perm()&0o050 != 0o050 {
					return Fail("%s is mode %04o, so nginx cannot traverse it", home, fi.Mode().Perm()).
						WithFix("chmod 0750 %s", home).WithTopic("layout")
				}
				return Pass("%04o on the way in", 0o750)
			},
		},
	}
}

// dynamicChecks cover a site with a process behind a socket.
func dynamicChecks(env *Env, s *state.Site, socket string) []Check {
	return []Check{
		{
			ID:    "unit",
			Title: "the systemd unit is running",
			Needs: []string{"directories"},
			Run: func(ctx context.Context) Result {
				status, ok := env.unitStatus(ctx, s)
				switch {
				case !ok:
					return Fail("the unit %s could not be queried", env.Site.UnitName(s)).
						WithFix("ratline reconcile --fix")
				case status.Active == "failed":
					return Fail("the service has failed").
						WithFix("ratline site logs %s --journal", s.Domain)
				case status.Active != "active":
					return Fail("the service is %s", status.Active).
						WithFix("ratline site start %s", s.Domain)
				}
				detail := "active"
				if status.MainPID != "" {
					detail += ", pid " + status.MainPID
				}
				return Pass("%s", detail)
			},
		},
		{
			ID:    "workers",
			Title: "the process manager has its workers",
			Needs: []string{"unit"},
			Run: func(ctx context.Context) Result {
				report, err := env.Site.ProcessReport(ctx, s)
				if err != nil || report == nil {
					switch s.Runtime {
					case "node":
						return Skip("this site runs node directly under systemd")
					case "bun":
						return Skip("bun runs directly under systemd; there is no supervisor to ask")
					}
					return Skip("gunicorn workers live inside the unit above")
				}
				switch {
				case report.Instances == 0:
					return Fail("PM2 is running but has no workers for this site").
						WithFix("ratline site restart %s", s.Domain).WithTopic("node")
				case report.Online < report.Instances:
					return Fail("%d of %d workers are online", report.Online, report.Instances).
						WithFix("ratline site logs %s", s.Domain).WithTopic("node")
				case report.Restarts >= 10:
					// Not a failure: it is serving. But systemd's own counter reads
					// zero here, so nothing else on this page would reveal it.
					return Warn("%d workers online, but one has restarted %d times",
						report.Online, report.Restarts).
						WithFix("ratline site logs %s", s.Domain).WithTopic("node")
				}
				return Pass("%d online", report.Online)
			},
		},
		{
			ID:    "listening",
			Title: "the application is listening where nginx expects",
			Needs: []string{"unit"},
			Run: func(ctx context.Context) Result {
				if s.Listen == "port" {
					addr := fmt.Sprintf("127.0.0.1:%d", s.Port)
					if err := system.ProbeTCP(ctx, addr, env.probeTimeout()); err != nil {
						return Fail("nothing answers on %s", addr).
							WithFix("the application may be ignoring PORT and binding elsewhere").
							WithTopic("sockets")
					}
					return Pass("%s", addr)
				}
				fi, err := os.Stat(socket)
				switch {
				case err != nil:
					return Fail("%s does not exist", socket).
						WithFix("ratline site logs %s — the application never bound it", s.Domain).
						WithTopic("sockets")
				case fi.Mode().Perm()&0o060 != 0o060:
					// connect(2) needs *write* permission on the socket inode. At 0640
					// nginx gets EACCES, returns 502, and the application log stays
					// empty because no request ever arrives.
					return Fail("the socket is mode %04o; nginx needs 0660 to connect, "+
						"so every request is a 502", fi.Mode().Perm()).
						WithFix("ratline site restart %s", s.Domain).WithTopic("sockets")
				case system.ProbeUnix(ctx, socket, env.probeTimeout()) != nil:
					return Fail("the socket exists but does not accept connections").
						WithFix("ratline site restart %s — a crashed process leaves the file behind",
							s.Domain).WithTopic("sockets")
				}
				return Pass("%s, mode %04o", socket, fi.Mode().Perm())
			},
		},
		{
			ID:    "app-answers",
			Title: "the application answers a request",
			Needs: []string{"listening"},
			Run: func(ctx context.Context) Result {
				// Straight to the application, bypassing nginx. This is the check that
				// separates "the application is broken" from "nginx cannot reach a
				// working application", and no amount of status output can do it.
				code, elapsed, err := probeApp(ctx, env, s)
				switch {
				case err != nil:
					return Fail("no HTTP response from the application: %s", firstLine(err.Error())).
						WithFix("ratline site logs %s", s.Domain)
				case code >= 500:
					return Warn("HTTP %d in %s — it is listening, but failing", code, elapsed).
						WithFix("ratline site logs %s", s.Domain)
				}
				return Pass("HTTP %d in %s", code, elapsed)
			},
		},
	}
}

// sharedSiteChecks apply to every runtime: the visitor's request, TLS, DNS.
func sharedSiteChecks(env *Env, s *state.Site) []Check {
	return []Check{
		{
			ID:    "served",
			Title: "nginx serves it end to end",
			Needs: []string{"nginx-config"},
			Run: func(ctx context.Context) Result {
				code, _, err := probeThroughNginx(ctx, env, s.Domain)
				switch {
				case err != nil:
					return Fail("nginx did not respond on 127.0.0.1: %s", firstLine(err.Error())).
						WithFix("ratline troubleshoot nginx")
				case code == 502 || code == 503:
					return Fail("HTTP %d — nginx cannot reach the application", code).
						WithFix("the socket check above is the usual cause").
						WithTopic("sockets")
				case code >= 500:
					return Warn("HTTP %d", code).WithFix("ratline site logs %s", s.Domain)
				}
				return Pass("HTTP %d", code)
			},
		},
		{
			ID:    "certificate",
			Title: "a current certificate is attached",
			Run: func(ctx context.Context) Result {
				if env.TLS == nil {
					return Skip("the certificate inventory is unavailable")
				}
				show, err := env.TLS.Show(ctx, s.Domain)
				if err != nil {
					return Warn("no certificate is attached, so this site is HTTP only").
						WithFix("ratline cert issue %s", s.Domain).WithTopic("tls")
				}
				switch show.Status {
				case tls.StatusExpired:
					return Fail("the certificate expired").
						WithFix("ratline cert renew %s --force", s.Domain).WithTopic("tls")
				case tls.StatusExpiring:
					return Warn("%s left", plural(show.Days, "day")).
						WithFix("ratline cert renew %s", s.Domain).WithTopic("tls")
				}
				return Pass("%s, %s left", show.Status, plural(show.Days, "day"))
			},
		},
		{
			ID:    "dns",
			Title: "the domain resolves to this server",
			Run: func(ctx context.Context) Result {
				// Last, because a site can be entirely healthy and simply not have DNS
				// yet — a normal state during setup rather than a fault.
				lookupCtx, cancel := env.probeContext(ctx)
				defer cancel()
				addrs, err := env.resolver().LookupHost(lookupCtx, s.Domain)
				if err != nil {
					return Warn("%s does not resolve", s.Domain).
						WithFix("point an A record here; the site is still reachable " +
							"by IP with a Host header")
				}
				if !resolvesHere(addrs, env.Cfg.Server.PublicIPv4, env.Cfg.Server.PublicIPv6) {
					return Warn("%s resolves to %s, which is not an address of this server",
						s.Domain, strings.Join(addrs, ", ")).
						WithFix("a proxy in front is a normal reason; otherwise the record " +
							"points elsewhere")
				}
				return Pass("%s", strings.Join(addrs, ", "))
			},
		},
	}
}

// probeApp makes one HTTP request straight to the application.
func probeApp(ctx context.Context, env *Env, s *state.Site) (int, time.Duration, error) {
	network, target := "unix", env.Cfg.SocketPath(s.Owner, s.Domain)
	if s.Listen == "port" {
		network, target = "tcp", fmt.Sprintf("127.0.0.1:%d", s.Port)
	}
	timeout := env.probeTimeout()
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: timeout}).DialContext(ctx, network, target)
			},
		},
	}
	return timedGet(ctx, env, client, "http://ratline-troubleshoot/", s.Domain)
}

// probeThroughNginx makes the request a visitor would, over the loopback.
func probeThroughNginx(ctx context.Context, env *Env, domain string) (int, time.Duration, error) {
	timeout := env.probeTimeout()
	client := &http.Client{
		Timeout: timeout,
		// Redirects are not followed: an http→https redirect is the correct answer
		// for a site with a certificate, and following it would turn a healthy 301
		// into a TLS error about a name that may not resolve yet.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", "127.0.0.1:80")
			},
		},
	}
	return timedGet(ctx, env, client, "http://127.0.0.1/", domain)
}

func timedGet(ctx context.Context, env *Env, client *http.Client, url, host string) (int, time.Duration, error) {
	ctx, cancel := env.probeContext(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, err
	}
	// The Host header selects the vhost, so it has to be the site's name even though
	// the connection is to the loopback.
	req.Host = host
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, time.Since(start), err
	}
	resp.Body.Close()
	return resp.StatusCode, time.Since(start).Round(time.Millisecond), nil
}

// resolvesHere reports whether any resolved address belongs to this server.
func resolvesHere(addrs, v4, v6 []string) bool {
	mine := map[string]bool{}
	for _, a := range append(append([]string{}, v4...), v6...) {
		mine[a] = true
	}
	// Nothing to compare against, so claiming a mismatch would be a guess.
	if len(mine) == 0 {
		return true
	}
	for _, a := range addrs {
		if mine[a] {
			return true
		}
	}
	return false
}
