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
	"strings"
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

	ProxyPass        string
	ProxyReadTimeout string
	ProxyBuffering   bool
	Upstream         bool
	UpstreamServers  []string
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

func (m *Manager) buildProxyTarget(d *VhostData, site *state.Site) error {
	if site.Instances > 1 {
		d.Upstream = true
		for i := 1; i <= site.Instances; i++ {
			if site.Listen == "port" {
				d.UpstreamServers = append(d.UpstreamServers,
					fmt.Sprintf("127.0.0.1:%d", site.Port+i-1))
				continue
			}
			d.UpstreamServers = append(d.UpstreamServers,
				fmt.Sprintf("unix:%s/app-%d.sock", m.Cfg.RuntimeDir(site.Owner, site.Domain), i))
		}
		d.ProxyPass = "http://" + site.Slug
		return nil
	}
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
	m.Log.Debug("reloaded nginx")
	return nil
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
	httpConf, err := renderTemplate("nginx/ratline-http.conf.tmpl", map[string]bool{
		"Gzip": m.Cfg.Nginx.Gzip, "Brotli": m.Cfg.Nginx.Brotli,
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
