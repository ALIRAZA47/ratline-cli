package site

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/runtime"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// Info is the report behind `ratline site show`.
type Info struct {
	*state.Site
	Unit       *unit.Status       `json:"unit,omitempty"`
	Socket     string             `json:"socket,omitempty"`
	SocketOK   bool               `json:"socket_ok,omitempty"`
	DocRootAbs string             `json:"doc_root_abs,omitempty"`
	DiskBytes  int64              `json:"disk_bytes"`
	DiskHuman  string             `json:"disk_human"`
	Cert       *state.Certificate `json:"certificate,omitempty"`
	CertDays   int                `json:"certificate_days_remaining,omitempty"`
	LastDeploy *state.Deployment  `json:"last_deploy,omitempty"`
	VhostOK    bool               `json:"vhost_present"`
	Enabled    bool               `json:"vhost_enabled"`
}

// Show gathers everything about a site.
func (m *Manager) Show(ctx context.Context, name string) (*Info, error) {
	site, err := m.State.FindSiteByName(ctx, name)
	if err != nil {
		return nil, err
	}
	info := &Info{
		Site:    site,
		VhostOK: system.Exists(m.Cfg.VhostPath(site.Domain)),
		Enabled: system.IsSymlink(m.Cfg.VhostLink(site.Domain)),
	}
	siteDir := m.Cfg.SiteDir(site.Owner, site.Domain)
	if size, err := system.DirSize(siteDir); err == nil {
		info.DiskBytes = size
	}
	info.DiskHuman = validate.FormatSize(info.DiskBytes)

	if site.Runtime == "static" {
		info.DocRootAbs = filepath.Join(siteDir, orDefault(site.DocRoot, "public"))
	}
	if site.Dynamic() {
		if info.Unit, err = m.Unit.Status(ctx, site); err != nil {
			return nil, err
		}
		if site.Listen == "port" {
			info.Socket = fmt.Sprintf("127.0.0.1:%d", site.Port)
			info.SocketOK = system.ProbeTCP(ctx, info.Socket, 2*time.Second) == nil
		} else {
			info.Socket = m.Cfg.SocketPath(site.Owner, site.Domain)
			// Existence on disk is not enough: a socket left by a crashed
			// process is still there.
			info.SocketOK = system.ProbeUnix(ctx, info.Socket, 2*time.Second) == nil
		}
	}
	if cert, err := m.State.CertificateForSite(ctx, site.Domain); err == nil {
		info.Cert = cert
		info.CertDays = cert.DaysRemaining(time.Now())
	}
	if d, err := m.State.LastDeployment(ctx, site.Domain); err == nil {
		info.LastDeploy = d
	}
	return info, nil
}

// List returns sites matching a filter.
func (m *Manager) List(ctx context.Context, f state.SiteFilter) ([]*state.Site, error) {
	return m.State.ListSites(ctx, f)
}

// Enable turns a site on: the vhost is linked and, for a dynamic site, the
// service is started and health checked.
func (m *Manager) Enable(ctx context.Context, name string) error {
	site, err := m.State.FindSiteByName(ctx, name)
	if err != nil {
		return err
	}
	if err := m.State.SetSiteEnabled(ctx, site.Domain, true); err != nil {
		return err
	}
	site.Enabled = true

	rb := system.NewRollback(m.Log)
	if site.Dynamic() {
		if err := m.Unit.Control(ctx, site, "enable"); err != nil {
			return err
		}
	}
	cert, _ := m.State.CertificateForSite(ctx, site.Domain)
	if err := m.Nginx.Apply(ctx, site, cert, rb); err != nil {
		return err
	}
	if site.Dynamic() {
		if _, err := m.startAndWait(ctx, site); err != nil {
			return err
		}
	}
	rb.Commit()
	m.Log.Info("site enabled", "domain", site.Domain)
	return nil
}

// Disable takes a site offline without deleting anything.
//
// nginx keeps answering with a 503 rather than being removed, because a site that
// disappears from nginx entirely means the ACME challenge location goes with it,
// and a certificate that cannot renew while a site is paused is a trap.
func (m *Manager) Disable(ctx context.Context, name string) error {
	site, err := m.State.FindSiteByName(ctx, name)
	if err != nil {
		return err
	}
	if err := m.State.SetSiteEnabled(ctx, site.Domain, false); err != nil {
		return err
	}
	site.Enabled = false

	if site.Dynamic() {
		if err := m.Unit.Control(ctx, site, "stop"); err != nil {
			m.Log.Warn("the service did not stop cleanly", "domain", site.Domain, "err", err)
		}
		if err := m.Unit.Control(ctx, site, "disable"); err != nil {
			m.Log.Warn("the service could not be disabled", "domain", site.Domain, "err", err)
		}
	}
	rb := system.NewRollback(m.Log)
	cert, _ := m.State.CertificateForSite(ctx, site.Domain)
	// Re-rendered with the disabled branch, which serves 503 for everything
	// except the ACME challenge.
	if err := m.Nginx.Apply(ctx, site, cert, rb); err != nil {
		return err
	}
	rb.Commit()
	m.Log.Info("site disabled; it now returns 503, and certificate renewal still works", "domain", site.Domain)
	return nil
}

// Control runs start, stop, restart or status against a site's service.
func (m *Manager) Control(ctx context.Context, name, verb string) (string, error) {
	site, err := m.State.FindSiteByName(ctx, name)
	if err != nil {
		return "", err
	}
	if !site.Dynamic() {
		return "", rlerr.Preconditionf("%s is a static site, so there is no service to %s", site.Domain, verb).
			WithHint("nginx serves its files directly; use 'ratline site enable' or 'disable' instead")
	}
	switch verb {
	case "start", "restart":
		return m.startAndWait(ctx, site)
	case "stop":
		return "", m.Unit.Control(ctx, site, "stop")
	default:
		return "", rlerr.Genericf("internal error: unknown verb %q", verb)
	}
}

// Reload performs a zero-downtime reload where the runtime supports one.
func (m *Manager) Reload(ctx context.Context, name string) (string, error) {
	site, err := m.State.FindSiteByName(ctx, name)
	if err != nil {
		return "", err
	}
	if !site.Dynamic() {
		return "", rlerr.Preconditionf("%s is a static site; there is nothing to reload", site.Domain)
	}
	rt, err := runtime.For(site.Runtime)
	if err != nil {
		return "", err
	}
	id, err := m.identity(site.Owner)
	if err != nil {
		return "", err
	}
	rc := runtime.NewContext(m.Cfg, m.Log, m.Runner, site, id, m.DryRun)
	if err := rt.Reload(ctx, rc); err != nil {
		return "", err
	}
	if err := m.Unit.Control(ctx, site, "reload"); err != nil {
		return "", err
	}
	// A reload that leaves the application unable to answer is a failed reload.
	return m.Unit.WaitHealthy(ctx, site, m.Cfg.Defaults.HealthTimeout.D())
}

// ScaleOptions is the resolved form of `ratline site scale`.
type ScaleOptions struct {
	Workers   int
	Instances int
	MemoryMax string
	CPUQuota  string
	// ClientMaxBodySize is nginx's upload ceiling. It belongs here rather than only
	// on `site add`, because it is the documented commonest cause of a mystery 413
	// and "delete the site and recreate it" is not an acceptable way to raise an
	// upload limit.
	ClientMaxBodySize string
}

// Scale changes a site's resource envelope and applies it without downtime where
// the runtime allows.
func (m *Manager) Scale(ctx context.Context, name string, opts ScaleOptions) (*state.Site, error) {
	site, err := m.State.FindSiteByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if !site.Dynamic() {
		return nil, rlerr.Preconditionf("%s is a static site, so there is nothing to scale", site.Domain)
	}
	changed := false
	if opts.Workers > 0 {
		if opts.Workers > 64 {
			return nil, rlerr.Usagef("%d workers is more than any single site needs", opts.Workers)
		}
		site.Workers, changed = opts.Workers, true
	}
	if opts.Instances > 0 {
		if opts.Instances > 16 {
			return nil, rlerr.Usagef("%d instances is more than any single site needs", opts.Instances)
		}
		previous := site.Instances
		site.Instances = opts.Instances
		// Checked here as well as at creation, or the rule would only hold for new
		// sites and `scale` would be a way around it.
		if err := validateInstances(site, m.Cfg.Runtimes.NodeProcessManager); err != nil {
			site.Instances = previous
			return nil, err
		}
		changed = true
	}
	if opts.MemoryMax != "" {
		if _, err := validate.Size(opts.MemoryMax); err != nil {
			return nil, err
		}
		site.MemoryMax, changed = opts.MemoryMax, true
	}
	if opts.CPUQuota != "" {
		if err := validate.CPUQuota(opts.CPUQuota); err != nil {
			return nil, err
		}
		site.CPUQuota, changed = opts.CPUQuota, true
	}
	if opts.ClientMaxBodySize != "" {
		if _, err := validate.Size(opts.ClientMaxBodySize); err != nil {
			return nil, err
		}
		site.ClientMaxBodySize, changed = opts.ClientMaxBodySize, true
	}
	if !changed {
		return nil, rlerr.Usagef("nothing to change").
			WithHint("pass at least one of --workers, --instances, --memory-max, " +
				"--cpu-quota or --client-max-body-size")
	}

	if err := m.State.PutSite(ctx, site); err != nil {
		return nil, err
	}
	rb := system.NewRollback(m.Log)
	rt, err := runtime.For(site.Runtime)
	if err != nil {
		return nil, err
	}
	id, err := m.identity(site.Owner)
	if err != nil {
		return nil, err
	}
	rc := runtime.NewContext(m.Cfg, m.Log, m.Runner, site, id, m.DryRun)
	if err := m.applyUnit(ctx, site, rt, rc, rb); err != nil {
		return nil, err
	}
	cert, _ := m.State.CertificateForSite(ctx, site.Domain)
	if err := m.Nginx.Apply(ctx, site, cert, rb); err != nil {
		return nil, err
	}

	// Gunicorn re-forks to the new worker count on SIGHUP, one worker at a time,
	// with the master holding the socket — so a worker-only change costs no
	// requests. Deliberately narrow: whether `pm2 reload` re-reads a changed
	// instance count is not something this code can prove, and a restart that is
	// honest beats a reload that may quietly not have scaled. Anything touching the
	// cgroup restarts too.
	workersOnly := opts.Instances == 0 && opts.MemoryMax == "" && opts.CPUQuota == "" &&
		opts.ClientMaxBodySize == ""
	if workersOnly && site.Runtime == "python" && site.AppServer == "gunicorn" {
		if err := m.Unit.Control(ctx, site, "reload"); err != nil {
			return nil, err
		}
		if _, err := m.Unit.WaitHealthy(ctx, site, m.Cfg.Defaults.HealthTimeout.D()); err != nil {
			return nil, err
		}
	} else {
		if _, err := m.startAndWait(ctx, site); err != nil {
			return nil, err
		}
	}
	rb.Commit()
	if err := m.writeManifest(site, id); err != nil {
		return nil, err
	}
	m.Log.Info("scaled", "domain", site.Domain, "workers", site.Workers,
		"instances", site.Instances, "memory_max", site.MemoryMax, "cpu_quota", site.CPUQuota)
	return site, nil
}

// Inventory is what a delete will destroy, gathered before anything is touched so
// an operator can see it and type the domain to confirm.
type Inventory struct {
	Domain    string   `json:"domain"`
	Owner     string   `json:"user"`
	Paths     []string `json:"paths"`
	DiskHuman string   `json:"disk"`
	Unit      string   `json:"unit,omitempty"`
	Port      int      `json:"port,omitempty"`
	Cert      string   `json:"certificate,omitempty"`
	Aliases   []string `json:"aliases,omitempty"`
	KeyCount  int      `json:"ssh_keys"`
	StateRows []string `json:"state_rows"`
}

// InventoryFor describes what deleting a site would remove.
func (m *Manager) InventoryFor(ctx context.Context, name string) (*Inventory, error) {
	site, err := m.State.FindSiteByName(ctx, name)
	if err != nil {
		return nil, err
	}
	siteDir := m.Cfg.SiteDir(site.Owner, site.Domain)
	inv := &Inventory{
		Domain:  site.Domain,
		Owner:   site.Owner,
		Aliases: site.Aliases,
		Paths: []string{
			siteDir,
			m.Cfg.VhostPath(site.Domain),
			m.Cfg.VhostLink(site.Domain),
			filepath.Join(m.Cfg.Paths.LogrotateDir, "ratline-"+site.Domain),
		},
		StateRows: []string{"sites", "aliases", "cert_attachments", "ports", "deployments"},
	}
	if size, err := system.DirSize(siteDir); err == nil {
		inv.DiskHuman = validate.FormatSize(size)
	}
	if site.Dynamic() {
		inv.Unit = m.Cfg.UnitPath(site.Owner, site.Domain)
		inv.Paths = append(inv.Paths, inv.Unit, m.Cfg.RuntimeDir(site.Owner, site.Domain))
	}
	if site.Port > 0 {
		inv.Port = site.Port
	}
	if cert, err := m.State.CertificateForSite(ctx, site.Domain); err == nil {
		inv.Cert = cert.Name + " (" + cert.Source + ")"
	}
	if keys, err := m.State.ListKeys(ctx, state.KeyFilter{Scope: state.ScopeSite, Site: site.Domain}); err == nil {
		inv.KeyCount = len(keys)
	}
	sort.Strings(inv.Paths)
	return inv, nil
}

// Delete removes a site.
//
// The order matters: the service stops before its files go, and nginx stops
// pointing at it before the files it serves disappear. Reversed, the window
// between them is served as a 502 or a 403.
func (m *Manager) Delete(ctx context.Context, name string, purge bool, backupDir string) error {
	site, err := m.State.FindSiteByName(ctx, name)
	if err != nil {
		return err
	}
	siteDir := m.Cfg.SiteDir(site.Owner, site.Domain)

	if backupDir != "" {
		if err := m.backup(ctx, site, backupDir); err != nil {
			return err
		}
	}

	if site.Dynamic() {
		if err := m.Unit.Remove(ctx, site); err != nil {
			return err
		}
	}
	if err := m.Nginx.Remove(ctx, site.Domain); err != nil {
		return err
	}
	if err := m.Unit.RemoveLogrotate(site); err != nil {
		m.Log.Warn("could not remove the logrotate policy", "err", err)
	}

	// Site-scoped keys grant access to a directory that is about to stop
	// existing, so they go too.
	keys, err := m.State.ListKeys(ctx, state.KeyFilter{Scope: state.ScopeSite, Site: site.Domain})
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := m.State.DeleteKey(ctx, k.ID); err != nil {
			return err
		}
		m.Log.Info("removed a site-scoped key along with the site", "label", k.Label)
	}

	if purge && !m.DryRun {
		if err := os.RemoveAll(siteDir); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "removing %s", siteDir)
		}
	} else if !purge {
		m.Log.Info("the site directory was kept", "path", siteDir, "remove_with", "--purge")
	}

	if site.Port > 0 {
		if err := m.State.ReleasePort(ctx, site.Domain); err != nil {
			return err
		}
	}
	if err := m.State.DeleteSite(ctx, site.Domain); err != nil {
		return err
	}
	m.Log.Info("site deleted", "domain", site.Domain, "purged", purge)
	return nil
}

func (m *Manager) backup(ctx context.Context, site *state.Site, dir string) error {
	if _, err := system.EnsureDir(dir, 0o700, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	archive := filepath.Join(dir, fmt.Sprintf("%s-%s.tar.gz", site.Domain, stamp))
	siteDir := m.Cfg.SiteDir(site.Owner, site.Domain)
	m.Log.Info("backing up the site", "archive", archive)
	_, err := m.Runner.Run(ctx, system.Cmd{
		Name:    "tar",
		Args:    []string{"--create", "--gzip", "--file", archive, "-C", filepath.Dir(siteDir), filepath.Base(siteDir)},
		Mutates: true, Timeout: 30 * time.Minute, Label: "tar",
	})
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeExternal, "the backup failed, so nothing was deleted")
	}
	return nil
}

// AddAlias adds a name to a site and re-renders the vhost.
func (m *Manager) AddAlias(ctx context.Context, name, alias string) (*state.Site, error) {
	site, err := m.State.FindSiteByName(ctx, name)
	if err != nil {
		return nil, err
	}
	normalised, err := validate.Domain(alias)
	if err != nil {
		return nil, err
	}
	if owner, used, err := m.State.NameInUse(ctx, normalised); err != nil {
		return nil, err
	} else if used {
		if owner == site.Domain {
			return site, nil
		}
		return nil, rlerr.Preconditionf("%s is already served by %s", normalised, owner)
	}
	site.Aliases = append(site.Aliases, normalised)
	if err := m.State.PutSite(ctx, site); err != nil {
		return nil, err
	}
	rb := system.NewRollback(m.Log)
	cert, _ := m.State.CertificateForSite(ctx, site.Domain)
	if err := m.Nginx.Apply(ctx, site, cert, rb); err != nil {
		return nil, err
	}
	rb.Commit()
	// A new name is not on the existing certificate's SAN list, so HTTPS for it
	// will fail until the certificate is reissued.
	if cert != nil && !cert.Covers(normalised) {
		m.Log.Warn("the certificate does not cover the new alias, so HTTPS will fail for it",
			"alias", normalised, "fix", "ratline cert issue "+site.Domain+" --force")
	}
	return site, nil
}

// RemoveAlias takes a name off a site.
func (m *Manager) RemoveAlias(ctx context.Context, name, alias string) (*state.Site, error) {
	site, err := m.State.FindSiteByName(ctx, name)
	if err != nil {
		return nil, err
	}
	normalised, err := validate.Domain(alias)
	if err != nil {
		return nil, err
	}
	kept := make([]string, 0, len(site.Aliases))
	found := false
	for _, a := range site.Aliases {
		if a == normalised {
			found = true
			continue
		}
		kept = append(kept, a)
	}
	if !found {
		return nil, rlerr.Preconditionf("%s is not an alias of %s", normalised, site.Domain)
	}
	site.Aliases = kept
	if err := m.State.PutSite(ctx, site); err != nil {
		return nil, err
	}
	rb := system.NewRollback(m.Log)
	cert, _ := m.State.CertificateForSite(ctx, site.Domain)
	if err := m.Nginx.Apply(ctx, site, cert, rb); err != nil {
		return nil, err
	}
	rb.Commit()
	return site, nil
}

// ProcessReport asks a site's process manager what it is actually running.
//
// It exists because PM2 does the restarting on a PM2-supervised site, which leaves
// systemd's own NRestarts at zero even while the application crash-loops. Anything
// that reports on health has to read PM2's counter or it reports a lie.
//
// Returns (nil, nil) when there is nothing to ask — a static or python site, or a
// node site running directly under systemd — so a caller can treat "not
// applicable" and "nothing running" the same way.
func (m *Manager) ProcessReport(ctx context.Context, site *state.Site) (*runtime.PM2Status, error) {
	if site.Runtime != "node" {
		return nil, nil
	}
	rt, err := runtime.For(site.Runtime)
	if err != nil {
		return nil, err
	}
	node, ok := rt.(*runtime.Node)
	if !ok {
		return nil, nil
	}
	id, err := m.identity(site.Owner)
	if err != nil {
		return nil, err
	}
	rc := runtime.NewContext(m.Cfg, m.Log, m.Runner, site, id, m.DryRun)
	if runtime.ProcessManagerFor(rc) != runtime.ProcessManagerPM2 {
		return nil, nil
	}
	return node.PM2Report(ctx, rc)
}

// ReapplyUnit re-renders and reinstalls a dynamic site's unit.
//
// Needed when something that changes the unit's *shape* changes — the process
// manager above all, since PM2 needs Type=forking, a PIDFile and an ExecStop that
// direct supervision does not have. Restarting alone would keep the old unit.
func (m *Manager) ReapplyUnit(ctx context.Context, site *state.Site) (err error) {
	if !site.Dynamic() {
		return nil
	}
	rt, err := runtime.For(site.Runtime)
	if err != nil {
		return err
	}
	id, err := m.identity(site.Owner)
	if err != nil {
		return err
	}
	rc := runtime.NewContext(m.Cfg, m.Log, m.Runner, site, id, m.DryRun)
	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)
	return m.applyUnit(ctx, site, rt, rc, rb)
}

// LogPaths returns a site's log files.
func (m *Manager) LogPaths(site *state.Site) map[string]string {
	logDir := filepath.Join(m.Cfg.SiteDir(site.Owner, site.Domain), "logs")
	return map[string]string{
		"access": filepath.Join(logDir, "access.log"),
		"error":  filepath.Join(logDir, "error.log"),
		"app":    filepath.Join(logDir, "app.log"),
	}
}

// UnitName is the systemd unit for a site.
func (m *Manager) UnitName(site *state.Site) string {
	return validate.UnitName(site.Owner, site.Domain)
}

var _ = strings.TrimSpace
