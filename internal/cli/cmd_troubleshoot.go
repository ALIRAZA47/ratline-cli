package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/site"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/tls"
)

// `site troubleshoot` walks one site's request path in the order a request travels
// it, and stops at the first thing that is broken.
//
// It exists because `doctor` answers the wrong question when a specific site is
// down. doctor sweeps the whole server and reports findings in whatever order the
// checks run, which leaves an operator with a list rather than a cause. A request
// arrives at nginx, is proxied to a socket, is answered by a process — so checking
// in that order means the first failure *is* the cause, and everything after it is
// a consequence not worth printing as a separate problem.

// checkVerdict is the outcome of one step.
type checkVerdict string

const (
	verdictOK      checkVerdict = "ok"
	verdictFailed  checkVerdict = "failed"
	verdictWarning checkVerdict = "warning"
	verdictSkipped checkVerdict = "skipped"
)

// CheckStep is one step of the walk.
type CheckStep struct {
	Step    string       `json:"step"`
	Verdict checkVerdict `json:"verdict"`
	Detail  string       `json:"detail,omitempty"`
	Fix     string       `json:"fix,omitempty"`
}

// TroubleshootReport is the whole walk.
type TroubleshootReport struct {
	Domain string      `json:"domain"`
	Steps  []CheckStep `json:"steps"`
	Cause  string      `json:"likely_cause,omitempty"`
	Fix    string      `json:"fix,omitempty"`
	OK     bool        `json:"ok"`
}

func newSiteTroubleshootCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "troubleshoot <domain>",
		Short: "Walk one site's request path and find where it breaks",
		Args:  cobra.ExactArgs(1),
		Long: "Follows a request from nginx to the application and reports the first step that\n" +
			"fails, which is the cause — everything after it is a consequence.\n\n" +
			"Checks, in order: the site is in state, nginx has a configuration for it and\n" +
			"accepts it, the directories exist, the unit is installed and running, the socket\n" +
			"exists and has the permissions nginx needs, the application answers a real HTTP\n" +
			"request, nginx serves it end to end, and TLS is present and current.\n\n" +
			"Changes nothing.",
		Example: "  ratline site troubleshoot app.example.com\n" +
			"  ratline site troubleshoot app.example.com --json",
		ValidArgsFunction: g.completeDomains,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.siteManager(cmd.Context())
			if err != nil {
				return err
			}
			report, err := g.troubleshoot(cmd.Context(), mgr, args[0])
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(report)
			}
			return g.printTroubleshoot(report)
		},
	}
	// Read-only, so it never takes the lock and never needs --dry-run. It does need
	// root, because the socket and the unit are not readable otherwise — a check
	// that silently could not look would be worse than one that refuses.
	return cmd
}

// troubleshoot runs the walk.
func (g *Globals) troubleshoot(ctx context.Context, mgr *site.Manager, name string) (*TroubleshootReport, error) {
	store, err := g.Store(ctx)
	if err != nil {
		return nil, err
	}
	s, err := store.FindSiteByName(ctx, name)
	if err != nil {
		return nil, err
	}

	r := &TroubleshootReport{Domain: s.Domain}
	// broken records the first failure. Everything after it is skipped with a
	// reason, rather than reported as an independent problem.
	broken := false
	add := func(step string, verdict checkVerdict, detail, fix string) {
		if broken && verdict == verdictFailed {
			verdict, detail = verdictSkipped, "not checked: "+detail
		}
		if verdict == verdictFailed {
			broken = true
			if r.Cause == "" {
				r.Cause, r.Fix = detail, fix
			}
		}
		r.Steps = append(r.Steps, CheckStep{Step: step, Verdict: verdict, Detail: detail, Fix: fix})
	}
	skip := func(step, why string) {
		r.Steps = append(r.Steps, CheckStep{Step: step, Verdict: verdictSkipped, Detail: why})
	}

	// 1. State and nginx configuration.
	if !s.Enabled {
		add("enabled", verdictFailed, "the site is disabled, so nginx is not serving it",
			"ratline site enable "+s.Domain)
	} else {
		add("enabled", verdictOK, "", "")
	}

	vhost := g.Cfg.VhostPath(s.Domain)
	switch {
	case !system.Exists(vhost):
		add("nginx configuration", verdictFailed, "there is no vhost at "+vhost, "ratline reconcile --fix")
	case s.Enabled && !system.IsSymlink(g.Cfg.VhostLink(s.Domain)):
		add("nginx configuration", verdictFailed,
			"the vhost exists but is not linked into sites-enabled", "ratline reconcile --fix")
	default:
		add("nginx configuration", verdictOK, vhost, "")
	}

	if g.Bins.Available("nginx") {
		if err := mgr.Nginx.Test(ctx); err != nil {
			// nginx refusing its whole configuration takes every site down, not
			// just this one, so it outranks anything site-specific.
			add("nginx accepts the configuration", verdictFailed, firstLine(err.Error()),
				"nginx -t names the failing file and line")
		} else {
			add("nginx accepts the configuration", verdictOK, "", "")
		}
	} else {
		skip("nginx accepts the configuration", "nginx is not installed")
	}

	// 2. The filesystem.
	siteDir := g.Cfg.SiteDir(s.Owner, s.Domain)
	if !system.Exists(siteDir) {
		add("site directory", verdictFailed, siteDir+" is missing",
			"ratline reconcile --fix, or recreate the site")
	} else {
		add("site directory", verdictOK, siteDir, "")
	}

	if !s.Dynamic() {
		root := g.staticRoot(s)
		index := root + "/" + orDefault2(s.IndexFile, "index.html")
		switch {
		case !system.Exists(root):
			add("document root", verdictFailed, root+" does not exist",
				"deploy your files, or check --root against your project layout")
		case !system.Exists(index) && !s.SPA:
			add("document root", verdictWarning, "there is no "+index,
				"a request for / will 403 or 404 until the index document exists")
		default:
			add("document root", verdictOK, root, "")
		}
		g.finishTroubleshoot(ctx, mgr, s, r, add, skip)
		return r, nil
	}

	// 3. The unit.
	status, err := mgr.Unit.Status(ctx, s)
	switch {
	case err != nil || status == nil:
		add("systemd unit", verdictFailed, "the unit could not be queried: "+mgr.UnitName(s),
			"ratline reconcile --fix")
	case status.Active == "failed":
		add("systemd unit", verdictFailed, "the service has failed",
			"ratline site logs "+s.Domain+" --journal")
	case status.Active != "active":
		add("systemd unit", verdictFailed, "the service is "+status.Active,
			"ratline site start "+s.Domain)
	default:
		detail := "active"
		if status.MainPID != "" {
			detail += ", pid " + status.MainPID
		}
		add("systemd unit", verdictOK, detail, "")
	}

	// 4. The process manager's own view, which is the only place a crash loop shows
	// up on a PM2 site — systemd's restart counter stays at zero there.
	if report, perr := mgr.ProcessReport(ctx, s); perr == nil && report != nil {
		switch {
		case report.Instances == 0:
			add("pm2 workers", verdictFailed, "PM2 is running but has no workers for this site",
				"ratline site restart "+s.Domain)
		case report.Online < report.Instances:
			add("pm2 workers", verdictFailed,
				fmt.Sprintf("%d of %d workers are online", report.Online, report.Instances),
				"ratline site logs "+s.Domain)
		case report.Restarts >= 10:
			add("pm2 workers", verdictWarning,
				fmt.Sprintf("%d workers online, but one has restarted %d times", report.Online, report.Restarts),
				"ratline site logs "+s.Domain)
		default:
			add("pm2 workers", verdictOK, fmt.Sprintf("%d online", report.Online), "")
		}
	} else if s.Runtime == "node" {
		skip("pm2 workers", "this site runs node directly under systemd")
	}

	// 5. The socket. This is where the silent 502 lives.
	if s.Listen == "port" {
		addr := fmt.Sprintf("127.0.0.1:%d", s.Port)
		if err := system.ProbeTCP(ctx, addr, 2*time.Second); err != nil {
			add("listening", verdictFailed, "nothing answers on "+addr,
				"the application may be ignoring PORT and binding somewhere else")
		} else {
			add("listening", verdictOK, addr, "")
		}
	} else {
		sock := g.Cfg.SocketPath(s.Owner, s.Domain)
		fi, serr := os.Stat(sock)
		switch {
		case serr != nil:
			add("socket", verdictFailed, sock+" does not exist",
				"ratline site logs "+s.Domain+" — the application never bound it")
		case fi.Mode().Perm()&0o060 != 0o060:
			// connect(2) needs *write* permission on the socket inode. At 0640 nginx
			// gets EACCES, returns 502, and the application log stays empty because
			// no request ever arrives.
			add("socket permissions", verdictFailed,
				fmt.Sprintf("the socket is mode %04o; nginx needs 0660 to connect, so every request is a 502",
					fi.Mode().Perm()),
				"ratline site restart "+s.Domain+"; the full story is in 'ratline explain sockets'")
		case system.ProbeUnix(ctx, sock, 2*time.Second) != nil:
			add("socket", verdictFailed, "the socket exists but does not accept connections",
				"ratline site restart "+s.Domain+" — a crashed process leaves the file behind")
		default:
			add("socket permissions", verdictOK, fmt.Sprintf("%s, mode %04o", sock, fi.Mode().Perm()), "")
		}
	}

	// 6. A real request to the application, bypassing nginx. This distinguishes
	// "the application is broken" from "nginx cannot reach a working application",
	// which no amount of status output can.
	if broken {
		skip("the application answers", "an earlier step has to pass first")
	} else {
		code, elapsed, err := probeApp(ctx, g.Cfg, s)
		switch {
		case err != nil:
			add("the application answers", verdictFailed,
				"no HTTP response from the application: "+firstLine(err.Error()),
				"ratline site logs "+s.Domain)
		case code >= 500:
			add("the application answers", verdictWarning,
				fmt.Sprintf("HTTP %d in %s — it is listening, but failing", code, elapsed),
				"ratline site logs "+s.Domain)
		default:
			add("the application answers", verdictOK,
				fmt.Sprintf("HTTP %d in %s", code, elapsed), "")
		}
	}

	g.finishTroubleshoot(ctx, mgr, s, r, add, skip)
	return r, nil
}

// finishTroubleshoot runs the checks that apply to every runtime: the request as a
// visitor makes it, TLS, and DNS.
func (g *Globals) finishTroubleshoot(ctx context.Context, mgr *site.Manager, s *state.Site,
	r *TroubleshootReport, add func(string, checkVerdict, string, string), skip func(string, string)) {

	// 7. Through nginx over the loopback with the right Host header, which is the
	// same path a visitor's request takes minus the network.
	code, _, err := probeThroughNginx(ctx, s.Domain)
	switch {
	case err != nil:
		add("nginx serves it", verdictFailed, "nginx did not respond on 127.0.0.1: "+firstLine(err.Error()),
			"systemctl status nginx")
	case code == 502 || code == 503:
		add("nginx serves it", verdictFailed,
			fmt.Sprintf("HTTP %d — nginx cannot reach the application", code),
			"the socket check above is the usual cause; 'ratline explain sockets' has the detail")
	case code >= 500:
		add("nginx serves it", verdictWarning, fmt.Sprintf("HTTP %d", code), "ratline site logs "+s.Domain)
	default:
		add("nginx serves it", verdictOK, fmt.Sprintf("HTTP %d", code), "")
	}

	// 8. TLS.
	if certMgr, err := g.certManager(ctx); err != nil {
		skip("certificate", "the certificate inventory could not be read")
	} else if show, err := certMgr.Show(ctx, s.Domain); err != nil {
		add("certificate", verdictWarning, "no certificate is attached, so this site is HTTP only",
			"ratline cert issue "+s.Domain)
	} else {
		switch {
		case show.Status == tls.StatusExpired:
			add("certificate", verdictFailed, "the certificate has expired",
				"ratline cert renew "+s.Domain+" --force")
		case show.Status == tls.StatusExpiring:
			add("certificate", verdictWarning,
				fmt.Sprintf("%s left", plural(show.Days, "day")),
				"ratline cert renew "+s.Domain)
		default:
			add("certificate", verdictOK,
				fmt.Sprintf("%s, %s left", show.Status, plural(show.Days, "day")), "")
		}
	}

	// 9. DNS, last: a site can be perfectly healthy and simply not have DNS yet,
	// which is a normal state during setup rather than a fault.
	if addrs, err := net.DefaultResolver.LookupHost(ctx, s.Domain); err != nil {
		add("dns", verdictWarning, s.Domain+" does not resolve",
			"point an A record at this server; the site is still reachable by IP with a Host header")
	} else if !resolvesHere(addrs, g.Cfg.Server.PublicIPv4, g.Cfg.Server.PublicIPv6) {
		add("dns", verdictWarning,
			s.Domain+" resolves to "+strings.Join(addrs, ", ")+", which is not an address of this server",
			"a proxy in front is a normal reason; otherwise the record points elsewhere")
	} else {
		add("dns", verdictOK, strings.Join(addrs, ", "), "")
	}

	for _, step := range r.Steps {
		if step.Verdict == verdictFailed {
			return
		}
	}
	r.OK = true
}

// resolvesHere reports whether any resolved address belongs to this server.
func resolvesHere(addrs, v4, v6 []string) bool {
	mine := map[string]bool{}
	for _, a := range append(append([]string{}, v4...), v6...) {
		mine[a] = true
	}
	if len(mine) == 0 {
		// Nothing to compare against, so claiming a mismatch would be a guess.
		return true
	}
	for _, a := range addrs {
		if mine[a] {
			return true
		}
	}
	return false
}

// probeApp makes one HTTP request straight to the application.
func probeApp(ctx context.Context, cfg interface {
	SocketPath(owner, domain string) string
}, s *state.Site) (int, time.Duration, error) {
	network, target := "unix", cfg.SocketPath(s.Owner, s.Domain)
	if s.Listen == "port" {
		network, target = "tcp", fmt.Sprintf("127.0.0.1:%d", s.Port)
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, target)
			},
		},
	}
	return timedGet(ctx, client, "http://ratline-troubleshoot/", s.Domain)
}

// probeThroughNginx makes the request a visitor would, over the loopback.
func probeThroughNginx(ctx context.Context, domain string) (int, time.Duration, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
		// Redirects are not followed: an http→https redirect is the correct answer
		// for a site with a certificate, and following it would turn a healthy 301
		// into a TLS error about a name that does not resolve yet.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", "127.0.0.1:80")
			},
		},
	}
	return timedGet(ctx, client, "http://127.0.0.1/", domain)
}

func timedGet(ctx context.Context, client *http.Client, url, host string) (int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, err
	}
	// The Host header is what selects the vhost, so it has to be the site's name
	// even though the connection is to the loopback.
	req.Host = host
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, time.Since(start), err
	}
	resp.Body.Close()
	return resp.StatusCode, time.Since(start).Round(time.Millisecond), nil
}

// printTroubleshoot renders the walk.
func (g *Globals) printTroubleshoot(r *TroubleshootReport) error {
	g.Printf("%s\n\n", r.Domain)
	for _, s := range r.Steps {
		mark := map[checkVerdict]string{
			verdictOK: "ok  ", verdictFailed: "FAIL", verdictWarning: "warn", verdictSkipped: "--  ",
		}[s.Verdict]
		line := fmt.Sprintf("  %s  %s", mark, s.Step)
		if s.Detail != "" {
			line += "  —  " + s.Detail
		}
		g.Printf("%s\n", line)
	}
	g.Printf("\n")
	switch {
	case r.OK:
		g.Printf("Nothing is wrong with this site's request path.\n")
	case r.Cause != "":
		g.Printf("Likely cause: %s\n", r.Cause)
		if r.Fix != "" {
			g.Printf("Try:          %s\n", r.Fix)
		}
	default:
		// Warnings only. Worth saying plainly, so a warning is not mistaken for a
		// diagnosis of the problem being investigated.
		g.Printf("No failures, but there are warnings above.\n")
	}
	return nil
}

// staticRoot resolves a static site's document root.
func (g *Globals) staticRoot(s *state.Site) string {
	return g.Cfg.SiteDir(s.Owner, s.Domain) + "/" + orDefault2(s.DocRoot, "public")
}
