// Package install puts the panel onto a server and takes it off again: its systemd
// unit, its nginx vhost, and the certificate for the domain it answers on.
//
// It follows ratline's own discipline rather than inventing a lighter one, because
// the failure modes are the same ones. Render to a temporary file, validate it with
// the real tool — nginx -t, systemd-analyze verify — move it into place atomically,
// and push an undo step. A `domain set` that fails half way leaves nginx serving
// exactly what it was serving before.
//
// It does not use ratline to do this, and that is deliberate. The panel is not a
// ratline site: it has no tenant, no home directory and no unit running as a site
// owner, so registering one would mean lying to the model to borrow the renderer.
// What it borrows instead is internal/system — the same atomic writes, the same
// rollback stack, the same argv-only execution.
package install

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"text/template"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/panel"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
	"github.com/ALIRAZA47/ratline-cli/templates"
)

// UnitName is the systemd unit the panel runs as.
const UnitName = "ratline-panel.service"

// UnitPath is where it is written.
const UnitPath = "/etc/systemd/system/" + UnitName

// ACMEWebroot is ratline's, shared on purpose.
//
// The panel answering its challenge out of the same directory means one webroot
// exists on the server, one nginx location pattern serves it, and an operator
// debugging a failed challenge has one place to look rather than two.
const ACMEWebroot = "/var/www/ratline-acme"

// Manager performs the installation steps.
type Manager struct {
	Cfg    *panel.Config
	Log    *log.Logger
	Runner system.Runner
	// SelfPath is this binary, baked into the unit's ExecStart. Resolved from
	// /proc/self/exe rather than assumed, so a panel installed somewhere unusual
	// writes a unit that starts the binary that wrote it.
	SelfPath string
	DryRun   bool
}

// EnsureUnit writes the systemd unit and enables it.
func (m *Manager) EnsureUnit(ctx context.Context) (err error) {
	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)

	body, err := templates.FS.ReadFile("panel/ratline-panel.service")
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "reading the embedded unit template")
	}
	if m.SelfPath != "" && m.SelfPath != "/usr/local/bin/ratline-panel" {
		body = bytes.ReplaceAll(body,
			[]byte("ExecStart=/usr/local/bin/ratline-panel serve"),
			[]byte("ExecStart="+m.SelfPath+" serve"))
	}
	if m.DryRun {
		m.Log.Info("would write the systemd unit", "path", UnitPath)
		return nil
	}
	if err := m.writeManaged(UnitPath, body, 0o644, rb); err != nil {
		return err
	}
	// Verified before it is loaded, not after. systemd accepts a unit with an
	// unknown key and logs a warning nobody reads, so the check is what turns a
	// typo into a refusal instead of a service that silently ignores a directive.
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemd-analyze", Args: []string{"verify", UnitPath},
		Label: "systemd-analyze verify",
	}); err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition, "the generated unit did not verify")
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"daemon-reload"}, Mutates: true,
	}); err != nil {
		return err
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"enable", UnitName}, Mutates: true,
	}); err != nil {
		return err
	}
	rb.Commit()
	return nil
}

// Restart restarts the panel's own service.
//
// Called after a change to the unit or the configuration, which means the process
// making the call is the one being replaced. systemctl returns once the new process
// has started, and the old one is killed after — so this returns into a process that
// is about to end, and anything after it must be nothing that matters.
func (m *Manager) Restart(ctx context.Context) error {
	_, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"restart", UnitName}, Mutates: true,
	})
	return err
}

// vhostData is what the template needs.
type vhostData struct {
	Domain      string
	Upstream    string
	TLS         bool
	CertPath    string
	KeyPath     string
	ChainPath   string
	ACMEWebroot string
	GeneratedAt string
}

// DomainOptions is one call to `domain set`.
type DomainOptions struct {
	Domain string
	// Email is the ACME contact. Without one, the vhost is written for plain HTTP
	// and the operator is told what to run when DNS is ready — which is the honest
	// order, because issuing before DNS resolves spends a rate-limit budget on a
	// challenge that cannot succeed.
	Email string
	// Staging uses Let's Encrypt's staging environment: a certificate no browser
	// trusts, and no rate limit spent proving the plumbing works.
	Staging bool
	// NoTLS writes the HTTP vhost and stops, for somebody terminating TLS
	// somewhere else.
	NoTLS bool
}

// SetDomain writes the vhost, obtains a certificate and reloads nginx.
func (m *Manager) SetDomain(ctx context.Context, opts DomainOptions) (err error) {
	domain, err := validate.Domain(opts.Domain)
	if err != nil {
		return err
	}
	if opts.Email != "" {
		if err := validate.Email(opts.Email); err != nil {
			return err
		}
	}
	if err := m.checkUpstreamIsLocal(); err != nil {
		return err
	}

	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)

	upstream := net.JoinHostPort(m.Cfg.Listen.Address, strconv.Itoa(m.Cfg.Listen.Port))
	if m.Cfg.Listen.Address == "0.0.0.0" || m.Cfg.Listen.Address == "::" {
		upstream = net.JoinHostPort("127.0.0.1", strconv.Itoa(m.Cfg.Listen.Port))
	}

	// HTTP first, always. The ACME challenge is served over port 80 out of a vhost
	// that must exist before certbot runs, and writing the TLS vhost first would
	// name a certificate file that is not there yet — which nginx -t rejects, so
	// the reload fails and the challenge never gets answered.
	data := vhostData{
		Domain:      domain,
		Upstream:    upstream,
		ACMEWebroot: ACMEWebroot,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := m.writeVhost(ctx, data, rb); err != nil {
		return err
	}

	if opts.NoTLS || opts.Email == "" {
		m.Log.Info("the panel vhost is serving plain HTTP", "domain", domain)
		if opts.Email == "" && !opts.NoTLS {
			m.Log.Warn("no --email was given, so no certificate was requested",
				"next", "ratline-panel domain set "+domain+" --email you@example.com")
		}
		if err := m.persistDomain(domain); err != nil {
			return err
		}
		rb.Commit()
		return nil
	}

	if err := m.issue(ctx, domain, opts); err != nil {
		return err
	}

	live := filepath.Join("/etc/letsencrypt/live", domain)
	data.TLS = true
	data.CertPath = filepath.Join(live, "fullchain.pem")
	data.KeyPath = filepath.Join(live, "privkey.pem")
	data.ChainPath = filepath.Join(live, "chain.pem")
	if !system.Exists(data.CertPath) {
		return rlerr.Preconditionf("certbot reported success but %s is not there", data.CertPath).
			WithHint("check 'certbot certificates' for where the lineage was written")
	}
	if err := m.writeVhost(ctx, data, rb); err != nil {
		return err
	}
	if err := m.persistDomain(domain); err != nil {
		return err
	}
	rb.Commit()
	m.Log.Info("the panel is on its domain", "url", "https://"+domain)
	return nil
}

// checkUpstreamIsLocal refuses to write a vhost proxying to an address nginx cannot
// reach, which is the mistake somebody makes once after editing listen.address.
func (m *Manager) checkUpstreamIsLocal() error {
	ip := net.ParseIP(m.Cfg.Listen.Address)
	if ip == nil {
		return rlerr.Usagef("listen.address %q is not an IP address", m.Cfg.Listen.Address)
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return nil
	}
	return rlerr.Preconditionf(
		"listen.address is %s, so nginx would proxy to an address that is not this host",
		m.Cfg.Listen.Address).
		WithHint("set listen.address to 127.0.0.1 in %s and restart the panel", m.Cfg.SourcePath)
}

func (m *Manager) writeVhost(ctx context.Context, data vhostData, rb *system.Rollback) error {
	raw, err := templates.FS.ReadFile("panel/panel-vhost.conf.tmpl")
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "reading the embedded vhost template")
	}
	tmpl, err := template.New("panel-vhost").Parse(string(raw))
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "parsing the vhost template")
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "rendering the vhost")
	}
	if m.DryRun {
		m.Log.Info("would write the nginx vhost", "path", m.Cfg.Paths.NginxVhost,
			"tls", data.TLS)
		return nil
	}
	if _, err := system.EnsureDir(ACMEWebroot, 0o755, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}
	if err := m.writeManaged(m.Cfg.Paths.NginxVhost, buf.Bytes(), 0o644, rb); err != nil {
		return err
	}
	link := filepath.Join("/etc/nginx/sites-enabled", filepath.Base(m.Cfg.Paths.NginxVhost))
	created, err := system.EnsureSymlink(m.Cfg.Paths.NginxVhost, link)
	if err != nil {
		return err
	}
	if created {
		rb.Push("remove the vhost symlink", func(context.Context) error { return os.Remove(link) })
	}
	// The real tool, on the real file, before it is live. `nginx -t` parses the
	// whole configuration, so this catches a vhost that is individually valid and
	// collides with something already there.
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "nginx", Args: []string{"-t"}, Label: "nginx -t",
	}); err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition, "the generated vhost did not pass nginx -t")
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"reload", "nginx"}, Mutates: true,
	}); err != nil {
		return err
	}
	return nil
}

// issue asks certbot for a certificate over the webroot.
//
// --deploy-hook is the part that matters afterwards: certbot stores it in the
// lineage's renewal configuration, so when the distribution's own certbot timer
// renews this in sixty days, nginx is reloaded and the new certificate is actually
// served. Without it the renewal succeeds, the file changes and nginx keeps serving
// the old one until somebody notices it has expired.
func (m *Manager) issue(ctx context.Context, domain string, opts DomainOptions) error {
	args := []string{
		"certonly", "--webroot", "-w", ACMEWebroot,
		"-d", domain,
		"--email", opts.Email,
		"--agree-tos", "--non-interactive", "--no-eff-email",
		"--cert-name", domain,
		"--deploy-hook", m.selfPath() + " nginx reload",
	}
	if opts.Staging {
		args = append(args, "--staging")
	}
	if m.DryRun {
		args = append(args, "--dry-run")
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "certbot", Args: args, Label: "certbot", Mutates: !m.DryRun,
		Stream: true, Timeout: 5 * time.Minute,
	}); err != nil {
		return rlerr.Wrap(err, rlerr.CodeACME, "certbot could not issue a certificate for %s", domain).
			WithHint("check that %s resolves to this server and that port 80 reaches nginx", domain)
	}
	return nil
}

func (m *Manager) selfPath() string {
	if m.SelfPath != "" {
		return m.SelfPath
	}
	return "/usr/local/bin/ratline-panel"
}

// persistDomain records the domain in panel.yaml, so a restart keeps serving it and
// the cookie and Origin checks know the panel's own name.
func (m *Manager) persistDomain(domain string) error {
	if m.DryRun {
		return nil
	}
	m.Cfg.Listen.Domain = domain
	return m.Cfg.Write(m.Cfg.SourcePath)
}

// ClearDomain removes the vhost and forgets the name.
//
// The certificate is left alone. Deleting a lineage because somebody took the panel
// off a domain would spend a rate limit to get it back, and certbot's own `delete`
// is the right tool for actually wanting it gone.
func (m *Manager) ClearDomain(ctx context.Context) error {
	link := filepath.Join("/etc/nginx/sites-enabled", filepath.Base(m.Cfg.Paths.NginxVhost))
	if m.DryRun {
		m.Log.Info("would remove the panel vhost", "path", m.Cfg.Paths.NginxVhost)
		return nil
	}
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing %s", link)
	}
	// RemoveManaged refuses a file without the managed-by header, so an operator
	// who replaced the vhost with their own keeps it.
	if err := system.RemoveManaged(m.Cfg.Paths.NginxVhost); err != nil {
		return err
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "nginx", Args: []string{"-t"}, Label: "nginx -t",
	}); err != nil {
		return err
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"reload", "nginx"}, Mutates: true,
	}); err != nil {
		return err
	}
	m.Cfg.Listen.Domain = ""
	return m.Cfg.Write(m.Cfg.SourcePath)
}

// ReloadNginx is what certbot's deploy hook calls after a renewal.
func (m *Manager) ReloadNginx(ctx context.Context) error {
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "nginx", Args: []string{"-t"}, Label: "nginx -t",
	}); err != nil {
		return err
	}
	_, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"reload", "nginx"}, Mutates: true,
	})
	return err
}

// writeManaged writes a file the panel owns, refusing to overwrite one it does not.
//
// The same rule ratline applies to its own paths: a file at a managed location that
// carries neither the managed-by header nor a record is somebody's hand-written
// configuration, and silently replacing it is how a tool earns a reputation.
func (m *Manager) writeManaged(path string, body []byte, mode os.FileMode, rb *system.Rollback) error {
	existed := system.Exists(path)
	if existed {
		managed, err := system.HasManagedHeader(path)
		if err != nil {
			return err
		}
		if !managed {
			return rlerr.Preconditionf("%s exists and was not written by ratline-panel", path).
				WithHint("move it aside if you want the panel to manage it")
		}
		backup, err := system.BackupFile(path, ".ratline-panel-bak")
		if err != nil {
			return err
		}
		rb.Push("restore "+path, func(context.Context) error {
			return os.Rename(backup, path)
		})
	} else {
		rb.Push("remove "+path, func(context.Context) error {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		})
	}
	return system.WriteFileAtomic(path, body, mode, 0, 0)
}
