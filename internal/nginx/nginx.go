// Package nginx renders vhosts and drives the web server.
//
// Nothing here reloads nginx without first running `nginx -t` against the staged
// configuration, and nothing replaces a working vhost without keeping the old one
// to restore. A configuration test that passes and a reload that fails is
// survivable; a reload with a broken config takes every site on the box down.
package nginx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
	"github.com/ALIRAZA47/ratline-cli/templates"
)

// Manager renders and applies nginx configuration.
type Manager struct {
	Cfg    *config.Config
	Log    *log.Logger
	Runner system.Runner
	DryRun bool

	// The installed nginx's version, read once. It decides how HTTP/2 is spelled —
	// see http2Support — and shelling out per vhost would be wasteful when a
	// reconcile renders dozens.
	versionOnce sync.Once
	version     [3]int
	versionOK   bool
}

// StaticLocation is one directory served from disk, bypassing the application.
type StaticLocation struct {
	Path string
	Dir  string
}

// VhostData is everything the vhost template needs. It is built once, from
// state, so the rendered file is a pure function of the site row plus config.
type VhostData struct {
	Domain      string
	Owner       string
	Runtime     string
	Slug        string
	ServerNames string
	GeneratedAt string

	Disabled bool

	TLS        bool
	CertPath   string
	KeyPath    string
	ChainPath  string
	HSTS       bool
	HSTSMaxAge int

	DocRoot   string
	IndexFile string
	SPA       bool
	PublicDir string

	// HTTP2Directive and HTTP2OnListen are mutually exclusive spellings of the same
	// thing, chosen by the installed nginx version. See http2Support.
	HTTP2Directive bool
	HTTP2OnListen  bool

	ProxyPass        string
	ProxyReadTimeout string
	ProxyBuffering   bool
	StaticLocations  []StaticLocation

	ClientMaxBodySize string
	AssetMaxAge       int
	AccessLog         string
	ErrorLog          string

	SnippetDir    string
	CustomInclude string

	WWWRedirectFrom string
	WWWRedirectTo   string
}

// BuildVhostData assembles the template input for a site.
func (m *Manager) BuildVhostData(site *state.Site, cert *state.Certificate) (*VhostData, error) {
	siteDir := m.Cfg.SiteDir(site.Owner, site.Domain)
	d := &VhostData{
		Domain:            site.Domain,
		Owner:             site.Owner,
		Runtime:           site.Runtime,
		Slug:              site.Slug,
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		Disabled:          !site.Enabled,
		IndexFile:         orDefault(site.IndexFile, "index.html"),
		SPA:               site.SPA,
		ClientMaxBodySize: orDefault(site.ClientMaxBodySize, m.Cfg.Defaults.ClientMaxBodySize),
		AssetMaxAge:       m.Cfg.Nginx.AssetMaxAge,
		AccessLog:         filepath.Join(siteDir, "logs", "access.log"),
		ErrorLog:          filepath.Join(siteDir, "logs", "error.log"),
		SnippetDir:        m.Cfg.Paths.NginxSnippets,
		CustomInclude:     filepath.Join(m.Cfg.Paths.NginxCustom, site.Domain+".conf"),
		ProxyReadTimeout:  m.Cfg.Defaults.ProxyReadTimeout.D().String(),
		ProxyBuffering:    true,
		HSTSMaxAge:        m.Cfg.Defaults.HSTSMaxAge,
	}

	names := site.ServerNames()
	// The canonical-host redirect takes one name out of the main block and gives
	// it its own, so that only one host serves content.
	switch site.WWWRedirect {
	case "apex":
		if www := "www." + site.Domain; containsName(names, www) {
			d.WWWRedirectFrom = www
			d.WWWRedirectTo = site.Domain
			names = removeName(names, www)
		}
	case "www":
		if www := "www." + site.Domain; containsName(names, www) {
			d.WWWRedirectFrom = site.Domain
			d.WWWRedirectTo = www
			names = removeName(names, site.Domain)
		}
	}
	d.ServerNames = strings.Join(names, " ")

	if cert != nil && cert.CertPath != "" {
		d.TLS = true
		d.CertPath = cert.CertPath
		d.KeyPath = cert.KeyPath
		d.ChainPath = cert.ChainPath
		d.HTTP2Directive, d.HTTP2OnListen = m.http2Support()
		// HSTS on an untrusted certificate would pin a browser to a host it
		// cannot verify, so the combination is refused rather than rendered.
		d.HSTS = site.HSTS && cert.Trusted()
		if site.HSTS && !cert.Trusted() {
			m.Log.Warn("HSTS was requested but the certificate is not trusted, so it was not enabled",
				"domain", site.Domain, "certificate_source", cert.Source)
		}
	}

	switch site.Runtime {
	case "static":
		root, err := validate.ResolveWithin(siteDir, orDefault(site.DocRoot, "public"))
		if err != nil {
			// The directory may not exist yet at render time, so fall back to
			// the lexical check, which still refuses traversal.
			if root, err = validate.WithinRoot(siteDir, orDefault(site.DocRoot, "public")); err != nil {
				return nil, err
			}
		}
		d.DocRoot = root
	case "node", "python":
		if err := m.buildProxyTarget(d, site); err != nil {
			return nil, err
		}
		if site.PublicDir != "" {
			public, err := validate.WithinRoot(siteDir, site.PublicDir)
			if err != nil {
				return nil, err
			}
			d.PublicDir = public
		}
		if site.StaticURL != "" && site.StaticDir != "" {
			dir, err := validate.WithinRoot(siteDir, site.StaticDir)
			if err != nil {
				return nil, err
			}
			// alias needs a trailing slash on both sides or nginx concatenates
			// the path onto the directory name.
			d.StaticLocations = append(d.StaticLocations, StaticLocation{
				Path: ensureSuffix(site.StaticURL, "/"),
				Dir:  ensureSuffix(dir, "/"),
			})
		}
		if site.Runtime == "node" {
			// Next.js and Vite both emit a hashed asset directory that nginx can
			// serve straight from disk.
			for _, p := range []string{"/_next/static/", "/assets/"} {
				dir := filepath.Join(siteDir, "app", strings.Trim(p, "/"))
				if system.IsDir(dir) {
					d.StaticLocations = append(d.StaticLocations, StaticLocation{Path: p, Dir: ensureSuffix(dir, "/")})
				}
			}
		}
	default:
		return nil, rlerr.Genericf("internal error: unknown runtime %q", site.Runtime)
	}
	return d, nil
}

// buildProxyTarget points the vhost at the site's one listening endpoint.
//
// There is exactly one, always: a dynamic site is one systemd unit binding one
// socket or one port. Concurrency lives *inside* the unit — PM2 cluster workers or
// gunicorn workers, which share that one listening handle — so there is nothing for
// an nginx upstream pool to balance across.
//
// An earlier version rendered a pool over app-1.sock … app-N.sock when
// --instances was above one. Nothing ever created those sockets, so the pool
// pointed at paths that did not exist and every request to such a site was a 502.
// `--instances` is now validated where it is set rather than papered over here.
func (m *Manager) buildProxyTarget(d *VhostData, site *state.Site) error {
	if site.Listen == "port" {
		if site.Port == 0 {
			return rlerr.Preconditionf("%s listens on a port but none is allocated", site.Domain).
				WithHint("run 'ratline reconcile --fix', or switch it to a socket")
		}
		d.ProxyPass = fmt.Sprintf("http://127.0.0.1:%d", site.Port)
		return nil
	}
	d.ProxyPass = "http://unix:" + m.Cfg.SocketPath(site.Owner, site.Domain) + ":"
	return nil
}

// RenderVhost produces the vhost file contents.
func (m *Manager) RenderVhost(site *state.Site, cert *state.Certificate) ([]byte, error) {
	d, err := m.BuildVhostData(site, cert)
	if err != nil {
		return nil, err
	}
	return renderTemplate("nginx/vhost.conf.tmpl", d)
}

// Apply writes a site's vhost, tests the configuration and reloads.
//
// The previous file is kept and restored if the test fails, so a bad render never
// takes the other sites down with it.
func (m *Manager) Apply(ctx context.Context, site *state.Site, cert *state.Certificate, rb *system.Rollback) error {
	rendered, err := m.RenderVhost(site, cert)
	if err != nil {
		return err
	}
	path := m.Cfg.VhostPath(site.Domain)

	if system.Exists(path) {
		managed, err := system.HasManagedHeader(path)
		if err != nil {
			return err
		}
		if !managed {
			return rlerr.Preconditionf("%s exists but was not created by ratline", path).
				WithHint("move it aside if you want ratline to manage this site")
		}
	}
	if err := m.EnsureSnippets(ctx); err != nil {
		return err
	}
	if err := m.ensureCustomInclude(site.Domain); err != nil {
		return err
	}

	if m.DryRun {
		m.Log.Info("would write the vhost", "path", path, "bytes", len(rendered))
		return nil
	}

	var previous []byte
	existed := system.Exists(path)
	if existed {
		if previous, err = system.ReadFileLimit(path, 4<<20); err != nil {
			return err
		}
	}
	if err := system.WriteFileAtomic(path, rendered, 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}
	rb.Push("wrote the vhost "+path, func(context.Context) error {
		if existed {
			return system.WriteFileAtomic(path, previous, 0o644, system.KeepUnchanged, system.KeepUnchanged)
		}
		return os.Remove(path)
	})

	if site.Enabled {
		changed, err := system.EnsureSymlink(path, m.Cfg.VhostLink(site.Domain))
		if err != nil {
			return err
		}
		if changed {
			link := m.Cfg.VhostLink(site.Domain)
			rb.Push("enabled the vhost "+link, func(context.Context) error { return os.Remove(link) })
		}
	} else if system.Exists(m.Cfg.VhostLink(site.Domain)) {
		if err := os.Remove(m.Cfg.VhostLink(site.Domain)); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "disabling the vhost for %s", site.Domain)
		}
	}

	// Test before reload, always. This is the step that turns a typo into a
	// clear error instead of an outage.
	if err := m.Test(ctx); err != nil {
		if existed {
			_ = system.WriteFileAtomic(path, previous, 0o644, system.KeepUnchanged, system.KeepUnchanged)
		} else {
			_ = os.Remove(path)
			_ = os.Remove(m.Cfg.VhostLink(site.Domain))
		}
		// Restore before returning, so the operator's other sites keep serving.
		if terr := m.Test(ctx); terr == nil {
			m.Log.Info("the previous configuration was restored and is valid")
		}
		return err
	}
	return m.Reload(ctx)
}

// Test runs `nginx -t`.
func (m *Manager) Test(ctx context.Context) error {
	res, err := m.Runner.Run(ctx, system.Cmd{Name: "nginx", Args: []string{"-t"}, Label: "nginx -t"})
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(res.Stderr)
	if detail == "" && res != nil {
		detail = strings.TrimSpace(res.Stdout)
	}
	return rlerr.Wrap(err, rlerr.CodePrecondition, "the nginx configuration is not valid, so nothing was reloaded").
		WithField("nginx_output", detail).
		WithHint("%s", "nginx reported:\n  "+strings.ReplaceAll(detail, "\n", "\n  "))
}

// Reload reloads nginx. A reload rather than a restart, so no connection is
// dropped and no request in flight is lost.
func (m *Manager) Reload(ctx context.Context) error {
	if m.DryRun {
		m.Log.Info("would reload nginx")
		return nil
	}
	timeout := m.Cfg.Nginx.ReloadTimeout.D()
	before := m.nginxWorkers(ctx)
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"reload", "nginx"},
		Mutates: true, Timeout: timeout, Label: "reload nginx",
	}); err != nil {
		// Fall back to signalling nginx directly, for hosts where it is not
		// under systemd.
		if _, ferr := m.Runner.Run(ctx, system.Cmd{
			Name: "nginx", Args: []string{"-s", "reload"}, Mutates: true, Timeout: timeout,
		}); ferr != nil {
			return rlerr.Wrap(err, rlerr.CodeExternal, "could not reload nginx").
				WithHint("check 'systemctl status nginx'; the configuration itself tested clean")
		}
	}
	// A reload is asynchronous: `systemctl reload nginx` runs `nginx -s reload`,
	// which returns as soon as the signal is delivered. The master then re-reads its
	// configuration and starts new workers while the old ones drain — so for a short
	// window nginx is still serving the *previous* configuration.
	//
	// That window is long enough to matter. `site add` and `cert issue` report
	// success and return; a script that immediately requests the site can be answered
	// by an old worker and see the pre-change behaviour — a missing redirect, a stale
	// document root. Waiting until new workers exist proves the reload landed.
	m.waitForReload(ctx, before, timeout)
	m.Log.Debug("reloaded nginx")
	return nil
}

// nginxWorkers returns the current worker pids, as a set.
//
// Read from the master's children rather than from a pid file, because the pid file
// only names the master and it is the workers that carry the configuration.
// nginxWorkers maps each nginx worker PID to whether it is still accepting.
//
// A worker that has been told to shut down renames itself to "nginx: worker process
// is shutting down". That distinction is the whole point: a draining worker no longer
// takes new connections, so it does not matter how long it lingers, whereas one still
// titled "worker process" is answering requests with whatever configuration it was
// started with.
func (m *Manager) nginxWorkers(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	res, err := m.Runner.Run(ctx, system.Cmd{
		// -a prints the command line beside the PID, which is what makes a draining
		// worker distinguishable from a live one.
		Name: "pgrep", Args: []string{"-a", "-f", "nginx: worker process"},
	})
	if err != nil || res == nil {
		return out
	}
	for _, line := range strings.Split(res.Out(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, rest, ok := strings.Cut(line, " ")
		if !ok {
			// No command line to judge by: assume it is live, which is the
			// conservative reading — it makes the wait longer, never shorter.
			pid, rest = line, ""
		}
		out[pid] = !strings.Contains(rest, "shutting down")
	}
	return out
}

// waitForReload blocks until no worker from before the reload is still accepting.
//
// Not "until a new worker exists", which is what this did and which returns too early:
// nginx's master starts the new workers *first* and only then tells the old ones to
// stop accepting, so in that window both are taking connections and a request can be
// answered with the previous configuration. `site add` and `cert issue` returned inside
// it, and a script that immediately requested the site saw the pre-change behaviour — a
// missing redirect, a stale document root, the old certificate.
//
// Waiting for the old workers to *exit* would be wrong in the other direction: a
// draining worker can hold a long-lived connection for as long as the client keeps it,
// and blocking a provisioning command on someone's websocket is not acceptable. Once a
// worker is titled "shutting down" it takes no new connections, which is all that
// matters here.
//
// Best effort by design: if pgrep is unavailable, or nginx is not running as a
// conventional master-plus-workers, this returns promptly rather than failing a
// reload that in all likelihood worked. The alternative — treating an unobservable
// reload as an error — would break provisioning on any host that runs nginx
// differently.
func (m *Manager) waitForReload(ctx context.Context, before map[string]bool, timeout time.Duration) {
	if len(before) == 0 {
		return
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		now := m.nginxWorkers(ctx)
		if len(now) == 0 {
			// Unobservable, or nginx is not running. Either way there is nothing to
			// learn by waiting.
			return
		}
		if !anyStaleWorkerAccepting(before, now) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	m.Log.Debug("nginx was still serving the previous configuration when the reload timeout "+
		"elapsed, so a request made immediately may see it", "timeout", timeout)
}

// anyStaleWorkerAccepting reports whether a worker from before the reload is still
// taking new connections.
func anyStaleWorkerAccepting(before, now map[string]bool) bool {
	for pid, accepting := range now {
		if accepting && before[pid] {
			return true
		}
	}
	return false
}

// Remove deletes a site's vhost and its symlink.
func (m *Manager) Remove(ctx context.Context, domain string) error {
	if m.DryRun {
		m.Log.Info("would remove the vhost", "domain", domain)
		return nil
	}
	link := m.Cfg.VhostLink(domain)
	if system.Exists(link) || system.IsSymlink(link) {
		if err := os.Remove(link); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "removing %s", link)
		}
	}
	if err := system.RemoveManaged(m.Cfg.VhostPath(domain)); err != nil {
		return err
	}
	if err := m.Test(ctx); err != nil {
		return err
	}
	return m.Reload(ctx)
}

// EnsureSnippets writes the shared includes, creating the ACME webroot too.
func (m *Manager) EnsureSnippets(ctx context.Context) error {
	dirs := []struct {
		path string
		mode os.FileMode
	}{
		{m.Cfg.Paths.NginxSnippets, 0o755},
		{m.Cfg.Paths.NginxCustom, 0o755},
		{m.Cfg.Paths.ACMEWebroot, 0o755},
		{filepath.Join(m.Cfg.Paths.ACMEWebroot, ".well-known", "acme-challenge"), 0o755},
	}
	for _, d := range dirs {
		if err := ensureDirAll(d.path, d.mode); err != nil {
			return err
		}
	}

	// acme-challenge.conf is a template because it carries the webroot path.
	acme, err := renderTemplate("nginx/snippets/acme-challenge.conf",
		map[string]string{"ACMEWebroot": m.Cfg.Paths.ACMEWebroot})
	if err != nil {
		return err
	}
	files := map[string][]byte{"acme-challenge.conf": acme}

	for _, name := range []string{"ssl-params.conf", "proxy-params.conf", "security-headers.conf", "deny-hidden.conf"} {
		b, err := templates.FS.ReadFile("nginx/snippets/" + name)
		if err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "reading the embedded snippet %s", name)
		}
		files[name] = b
	}

	for name, body := range files {
		path := filepath.Join(m.Cfg.Paths.NginxSnippets, name)
		if m.DryRun {
			m.Log.Debug("would write a snippet", "path", path)
			continue
		}
		if err := system.WriteFileAtomic(path, body, 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
			return err
		}
	}

	// The http-level include has to be reachable from nginx.conf. ratline writes
	// it, but wiring it in is the operator's call, because editing nginx.conf is
	// not something a provisioning tool should do behind their back.
	// Only the directives nginx.conf has not already set. Debian and Ubuntu ship
	// `gzip on;` inside the http block and include conf.d/*.conf into that same
	// block — which is where this snippet is linked — so emitting it unconditionally
	// makes `nginx -t` fail with "gzip directive is duplicate" and every single
	// `site add` fail with it, on the exact platform ratline targets.
	taken := httpDirectivesAlreadySet("/etc/nginx/nginx.conf")
	httpConf, err := renderTemplate("nginx/ratline-http.conf.tmpl", map[string]any{
		"Compression": compressionLines(m.Cfg.Nginx.Gzip, m.Cfg.Nginx.Brotli, taken),
		"Skipped":     skippedNames(m.Cfg.Nginx.Gzip, m.Cfg.Nginx.Brotli, taken),
	})
	if err != nil {
		return err
	}
	httpPath := filepath.Join(m.Cfg.Paths.NginxSnippets, "ratline-http.conf")
	if !m.DryRun {
		if err := system.WriteFileAtomic(httpPath, httpConf, 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
			return err
		}
	}
	if err := m.checkHTTPInclude(httpPath); err != nil {
		return err
	}
	return nil
}

// http2Support picks the spelling of HTTP/2 this nginx understands.
//
// `http2 on;` is a standalone directive only from nginx 1.25.1. Before that the only
// spelling is the `http2` parameter on the listen line, and the standalone directive
// is an "unknown directive" that makes nginx refuse the whole configuration — so
// emitting it unconditionally broke every TLS vhost on Ubuntu 24.04 LTS, which ships
// nginx 1.24. Conversely 1.25.1+ deprecates the listen parameter and warns about it.
//
// Returns (standalone, onListen); exactly one is true.
func (m *Manager) http2Support() (bool, bool) {
	major, minor, patch, ok := m.nginxVersion()
	if !ok {
		// Unknown version: prefer the listen parameter, which every nginx that has
		// ever supported HTTP/2 accepts. A deprecation warning is survivable; an
		// unknown directive is not.
		return false, true
	}
	if major > 1 || (major == 1 && (minor > 25 || (minor == 25 && patch >= 1))) {
		return true, false
	}
	return false, true
}

var nginxVersionRe = regexp.MustCompile(`nginx/(\d+)\.(\d+)\.(\d+)`)

// nginxVersion reads the installed nginx's version, once.
func (m *Manager) nginxVersion() (major, minor, patch int, ok bool) {
	m.versionOnce.Do(func() {
		// Rendering must not require a Runner: a caller that only wants the text of a
		// vhost — a golden test, a --dry-run preview — has no reason to supply one,
		// and panicking on it would make rendering depend on being able to execute.
		if m.Runner == nil {
			return
		}
		// `nginx -v` writes to stderr, which the runner captures alongside stdout.
		res, err := m.Runner.Run(context.Background(), system.Cmd{Name: "nginx", Args: []string{"-v"}})
		if err != nil || res == nil {
			return
		}
		fields := nginxVersionRe.FindStringSubmatch(res.Out() + res.Stderr)
		if len(fields) != 4 {
			return
		}
		a, _ := strconv.Atoi(fields[1])
		b, _ := strconv.Atoi(fields[2])
		c, _ := strconv.Atoi(fields[3])
		m.version = [3]int{a, b, c}
		m.versionOK = true
	})
	return m.version[0], m.version[1], m.version[2], m.versionOK
}

// gzipDirectives and brotliDirectives are what ratline would like to set at http
// level, in order, as directive name to full line.
var gzipDirectives = [][2]string{
	{"gzip", "gzip on;"},
	{"gzip_vary", "gzip_vary on;"},
	{"gzip_proxied", "gzip_proxied any;"},
	{"gzip_comp_level", "gzip_comp_level 5;"},
	{"gzip_min_length", "gzip_min_length 256;"},
	{"gzip_types", `gzip_types
    application/atom+xml application/geo+json application/javascript
    application/json application/ld+json application/manifest+json
    application/rdf+xml application/rss+xml application/vnd.ms-fontobject
    application/wasm application/x-web-app-manifest+json application/xhtml+xml
    application/xml font/eot font/otf font/ttf image/bmp image/svg+xml
    text/cache-manifest text/calendar text/css text/javascript text/markdown
    text/plain text/vcard text/vnd.rim.location.xloc text/vtt
    text/x-component text/xml;`},
}

var brotliDirectives = [][2]string{
	{"brotli", "brotli on;"},
	{"brotli_comp_level", "brotli_comp_level 5;"},
	{"brotli_types", `brotli_types
    application/javascript application/json application/xml
    image/svg+xml text/css text/javascript text/plain text/xml;`},
}

// compressionLines returns the directives to emit, skipping any the http block
// already sets.
//
// nginx treats a repeated flag directive in one context as a hard error rather than
// an override, and this snippet is included into the distro's http block — so a
// directive already set there cannot be set again at all, and there is nothing to
// gain by trying.
func compressionLines(gzip, brotli bool, taken map[string]bool) []string {
	var out []string
	add := func(set [][2]string) {
		for _, d := range set {
			if taken[d[0]] {
				continue
			}
			out = append(out, d[1])
		}
	}
	if gzip {
		add(gzipDirectives)
	}
	if brotli {
		add(brotliDirectives)
	}
	return out
}

// skippedNames lists what was left out, so the snippet says so in a comment rather
// than silently differing from the configuration an operator asked for.
func skippedNames(gzip, brotli bool, taken map[string]bool) string {
	var out []string
	check := func(set [][2]string) {
		for _, d := range set {
			if taken[d[0]] {
				out = append(out, d[0])
			}
		}
	}
	if gzip {
		check(gzipDirectives)
	}
	if brotli {
		check(brotliDirectives)
	}
	return strings.Join(out, ", ")
}

// httpDirectivesAlreadySet reports the directives nginx.conf sets inside its http
// block.
//
// Deliberately shallow: nginx.conf itself, not the files it includes. The distro sets
// these there, and following includes would read this very snippet back and conclude
// every directive was taken. Comments are stripped first, because Debian ships most
// of these commented out and a commented directive is not set.
func httpDirectivesAlreadySet(confPath string) map[string]bool {
	out := map[string]bool{}
	data, err := system.ReadFileLimit(confPath, 4<<20)
	if err != nil {
		return out
	}
	depth, inHTTP := 0, false
	for _, raw := range strings.Split(string(data), "\n") {
		line := raw
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		opens := strings.Count(line, "{")
		closes := strings.Count(line, "}")

		// A directive counts only at the top level of the http block: one deeper is
		// a server or location, where a duplicate is a legal override.
		if inHTTP && depth == 1 && opens == 0 {
			if name, _, ok := strings.Cut(line, " "); ok {
				out[strings.TrimSuffix(strings.TrimSpace(name), ";")] = true
			}
		}
		if strings.HasPrefix(line, "http") && opens > 0 {
			inHTTP, depth = true, depth+opens-closes
			continue
		}
		depth += opens - closes
		if inHTTP && depth <= 0 {
			inHTTP = false
		}
	}
	return out
}

// checkHTTPInclude warns when the http-level snippet is not included, because a
// WebSocket upgrade silently misbehaves without the map it defines.
func (m *Manager) checkHTTPInclude(httpPath string) error {
	confPath := "/etc/nginx/nginx.conf"
	data, err := system.ReadFileLimit(confPath, 4<<20)
	if err != nil {
		return nil
	}
	body := string(data)
	if strings.Contains(body, httpPath) || strings.Contains(body, filepath.Dir(httpPath)+"/*.conf") {
		return nil
	}
	// conf.d is included by the default nginx.conf on Debian and Ubuntu, so a
	// symlink there is the least invasive way to get the snippet loaded.
	linkPath := "/etc/nginx/conf.d/ratline-http.conf"
	if system.IsDir("/etc/nginx/conf.d") && !m.DryRun {
		if _, err := system.EnsureSymlink(httpPath, linkPath); err == nil {
			m.Log.Debug("linked the http-level snippet", "link", linkPath)
			return nil
		}
	}
	m.Log.Warn("the http-level snippet is not included by nginx.conf, so WebSocket upgrades may misbehave",
		"snippet", httpPath, "fix", "add 'include "+httpPath+";' inside the http block of "+confPath)
	return nil
}

func (m *Manager) ensureCustomInclude(domain string) error {
	path := filepath.Join(m.Cfg.Paths.NginxCustom, domain+".conf")
	if system.Exists(path) {
		return nil
	}
	if m.DryRun {
		return nil
	}
	body := "# Your additions for " + domain + ".\n" +
		"#\n" +
		"# ratline includes this file at the end of the generated vhost and never\n" +
		"# regenerates it, so anything here survives 'ratline reconcile' and every\n" +
		"# future change to the site.\n"
	return system.WriteFileAtomic(path, []byte(body), 0o644, system.KeepUnchanged, system.KeepUnchanged)
}

// ConflictingServerName reports another vhost claiming the same name, which
// nginx accepts and then resolves unpredictably.
func (m *Manager) ConflictingServerName(name, exceptDomain string) (string, error) {
	entries, err := os.ReadDir(m.Cfg.Paths.NginxSitesEnabled)
	if err != nil {
		return "", nil
	}
	for _, e := range entries {
		if e.IsDir() || strings.TrimSuffix(e.Name(), ".conf") == exceptDomain {
			continue
		}
		path := filepath.Join(m.Cfg.Paths.NginxSitesEnabled, e.Name())
		data, err := system.ReadFileLimit(path, 4<<20)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "server_name") {
				continue
			}
			for _, field := range strings.Fields(strings.TrimSuffix(line, ";"))[1:] {
				if field == name {
					return e.Name(), nil
				}
			}
		}
	}
	return "", nil
}

func renderTemplate(name string, data any) ([]byte, error) {
	tmpl, err := template.New(filepath.Base(name)).
		Option("missingkey=error").
		ParseFS(templates.FS, name)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "parsing the template %s", name)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "rendering the template %s", name)
	}
	return buf.Bytes(), nil
}

func ensureDirAll(path string, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "creating %s", path)
	}
	return system.Chmod(path, mode)
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func ensureSuffix(s, suffix string) string {
	if strings.HasSuffix(s, suffix) {
		return s
	}
	return s + suffix
}

func containsName(names []string, n string) bool {
	for _, v := range names {
		if v == n {
			return true
		}
	}
	return false
}

func removeName(names []string, n string) []string {
	out := make([]string, 0, len(names))
	for _, v := range names {
		if v != n {
			out = append(out, v)
		}
	}
	return out
}

// templatesRead exposes the embedded tree for tests in this package.
func templatesRead(name string) ([]byte, error) { return templates.FS.ReadFile(name) }
