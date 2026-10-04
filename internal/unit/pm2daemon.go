package unit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
	"github.com/ALIRAZA47/ratline-cli/templates"
)

// One PM2 daemon per tenant and Node version, instead of one per site.
//
// A site's unit used to start a PM2 daemon of its own, with PM2_HOME inside the site
// directory: ten node sites were ten daemons, ten God processes and ten RPC sockets.
// Now a tenant's PM2-supervised sites share one daemon, run by this unit as the
// tenant, and each site's own unit is a oneshot that registers its application with
// it (`pm2 start <ecosystem>`) and takes it out again (`pm2 delete <name>`).
//
// Per tenant rather than per server, because a daemon shared between tenants would have
// to run as root to start each one's workers as them — root evaluating a tenant's
// configuration and opening paths in a tenant's tree, which is the class of bug the
// trusted-path helpers exist to close. Per Node version as well, because PM2's cluster
// workers are forked from the daemon's own node binary: a Node 18 site on a Node 22
// daemon would quietly run on 22.
//
// What sharing costs, stated plainly: the kernel-enforced ceiling is now the tenant's,
// the sum of their sites' MemoryMax and CPUQuota, rather than each site's. One site of a
// tenant can starve a sibling of the same tenant, never another tenant. The per-site
// memory ceiling survives as PM2's max_memory_restart, which restarts a worker that
// grows past it rather than letting the kernel kill the whole tree.

// PM2Daemon is one tenant's shared PM2 for one Node version.
type PM2Daemon struct {
	Owner string
	// Key names the Node version the daemon runs on: "node22", or "system" for a
	// node found on the system rather than a managed one.
	Key string
	// Home is the daemon's PM2_HOME, inside the tenant's home.
	Home    string
	NodeBin string
	PM2     string
	// Sites are the PM2-supervised sites it hosts, enabled or not.
	Sites []*state.Site
}

// Unit is the daemon's systemd unit name.
func (d *PM2Daemon) Unit() string { return validate.PM2UnitName(d.Owner, d.Key) }

// pm2DaemonData is the daemon template's input.
type pm2DaemonData struct {
	Owner, Key, Home, Path, PM2 string
	GeneratedAt                 string
	Wants                       []string
	RestartSec, TimeoutStopSec  string
	Limits, Hardening           []string
	Relaxed                     bool
	RelaxedList                 string
}

// PM2UnitPath is where a tenant's daemon unit lives.
func (m *Manager) PM2UnitPath(owner, key string) string {
	return filepath.Join(m.Cfg.Paths.SystemdDir, validate.PM2UnitName(owner, key))
}

// RenderPM2Daemon produces the daemon's unit file.
func (m *Manager) RenderPM2Daemon(d *PM2Daemon) ([]byte, error) {
	for _, v := range []string{d.Owner, d.Key, d.Home, d.NodeBin, d.PM2} {
		if err := validate.NoControlChars("unit field", v); err != nil {
			return nil, err
		}
		// A space would split ExecStart into a different program and arguments.
		if strings.ContainsAny(v, " \t") {
			return nil, rlerr.Genericf("internal error: a PM2 daemon field contains whitespace: %q", v)
		}
	}
	data := &pm2DaemonData{
		Owner:          d.Owner,
		Key:            d.Key,
		Home:           d.Home,
		Path:           filepath.Dir(d.NodeBin) + ":" + system.DefaultPath,
		PM2:            d.PM2,
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		RestartSec:     m.Cfg.Defaults.RestartSec.D().String(),
		TimeoutStopSec: m.Cfg.Defaults.StopTimeout.D().String(),
	}

	// The ceiling is what the running sites add up to. A disabled site's application is
	// not in the daemon, so counting it would hand the others headroom nobody asked for.
	var (
		memTotal int64
		cpuTotal int
		running  int
		relaxed  = map[string]bool{}
	)
	sites := append([]*state.Site(nil), d.Sites...)
	sort.Slice(sites, func(i, j int) bool { return sites[i].Domain < sites[j].Domain })
	for _, s := range sites {
		for _, r := range s.Relaxed {
			relaxed[r] = true
		}
		for _, r := range defaultRelaxed[s.Runtime] {
			relaxed[r] = true
		}
		if !s.Enabled {
			continue
		}
		running++
		data.Wants = append(data.Wants, validate.UnitName(s.Owner, s.Domain))
		mem, err := validate.Size(orDefault(s.MemoryMax, m.Cfg.Defaults.MemoryMax))
		if err != nil {
			return nil, err
		}
		memTotal += mem
		cpu, err := quotaPercent(orDefault(s.CPUQuota, m.Cfg.Defaults.CPUQuota))
		if err != nil {
			return nil, err
		}
		cpuTotal += cpu
	}
	// PM2 itself runs on node whatever it supervises, so node's exemption always holds.
	for _, r := range defaultRelaxed["node"] {
		relaxed[r] = true
	}
	if running == 0 {
		// Nothing enabled: the daemon will not be started by anything, but its unit
		// still has to be a valid one.
		mem, err := validate.Size(m.Cfg.Defaults.MemoryMax)
		if err != nil {
			return nil, err
		}
		memTotal = mem
		cpu, err := quotaPercent(m.Cfg.Defaults.CPUQuota)
		if err != nil {
			return nil, err
		}
		cpuTotal = cpu
		running = 1
	}
	high := int64(float64(memTotal) * m.Cfg.Defaults.MemoryHighRatio)
	data.Limits = limitLines(m.Cfg, validate.FormatSize(memTotal), high,
		fmt.Sprintf("%d%%", cpuTotal), m.Cfg.Defaults.TasksMax*running)

	var relaxedList []string
	for _, h := range HardeningDirectives {
		if relaxed[h.Name] {
			data.Hardening = append(data.Hardening, "# "+h.Directive+" — relaxed for these sites")
			relaxedList = append(relaxedList, h.Name)
			continue
		}
		data.Hardening = append(data.Hardening, h.Directive)
	}
	data.Relaxed = len(relaxedList) > 0
	data.RelaxedList = strings.Join(relaxedList, ", ")
	data.Hardening = append(data.Hardening,
		// The tenant's whole home rather than each site directory, so a site added later
		// is reachable without restarting the daemon — and every site in it is already
		// the same user's. Other tenants' homes stay hidden behind ProtectHome=tmpfs.
		"BindPaths="+m.Cfg.HomeDir(d.Owner),
		// Each site's socket directory is created under here by that site's own unit,
		// after this daemon may already be running. The tenant can write only the
		// directories that are theirs; the leading '-' tolerates a missing parent.
		"ReadWritePaths=-"+m.Cfg.Paths.RunDir,
	)

	tmpl, err := template.New("tenant-pm2.service.tmpl").ParseFS(templates.FS, "systemd/tenant-pm2.service.tmpl")
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "parsing the PM2 daemon template")
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "rendering the PM2 daemon for %s", d.Owner)
	}
	return buf.Bytes(), nil
}

// quotaPercent reads "150%" as 150.
func quotaPercent(s string) (int, error) {
	if err := validate.CPUQuota(s); err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(s), "%"))
}

// InstallPM2Daemon writes a tenant's daemon unit when it has changed.
//
// What it does to a daemon that is already running depends on what changed, because
// restarting it restarts every PM2 site of that tenant:
//
//   - only the list of sites or the ceilings: the unit is rewritten and systemd reloaded,
//     and the new ceilings are applied to the running cgroup with set-property. Adding a
//     site to a tenant does not bounce the tenant's other sites.
//   - anything else — the sandbox, the node binary, PM2 itself: the daemon is restarted,
//     because none of that reaches a running process. That is said out loud first.
func (m *Manager) InstallPM2Daemon(ctx context.Context, d *PM2Daemon, rb *system.Rollback) error {
	body, err := m.RenderPM2Daemon(d)
	if err != nil {
		return err
	}
	path := m.PM2UnitPath(d.Owner, d.Key)
	unitName := d.Unit()

	existed := system.Exists(path)
	var previous []byte
	if existed {
		managed, err := system.HasManagedHeader(path)
		if err != nil {
			return err
		}
		if !managed {
			return rlerr.Preconditionf("%s exists but was not created by ratline", path).
				WithHint("move it aside if you want ratline to manage this tenant's PM2")
		}
		if previous, err = system.ReadFileLimit(path, 1<<20); err != nil {
			return err
		}
		if pm2UnitShape(previous, false) == pm2UnitShape(body, false) {
			return nil
		}
	}
	if err := m.EnsureTarget(ctx); err != nil {
		return err
	}
	needsRestart := existed && pm2UnitShape(previous, true) != pm2UnitShape(body, true)
	if m.DryRun {
		m.Log.Info("would install the tenant's PM2 daemon", "unit", unitName, "sites", len(d.Sites))
		if needsRestart && m.isActive(ctx, unitName) {
			m.Log.Warn("would restart the tenant's PM2 daemon, and with it every PM2 site of "+d.Owner,
				"unit", unitName)
		}
		return nil
	}

	if err := system.WriteFileAtomic(path, body, 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}
	restarted := false
	// rb is nil when the caller is a teardown with nothing to unwind into: a site that
	// is being deleted is not put back because its tenant's daemon could not be updated.
	undo := func(ctx context.Context) error {
		if existed {
			if err := system.WriteFileAtomic(path, previous, 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
				return err
			}
		} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if _, err := m.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{"daemon-reload"}, Mutates: true,
		}); err != nil {
			return err
		}
		if restarted {
			_, err := m.Runner.Run(ctx, system.Cmd{
				Name: "systemctl", Args: []string{"restart", unitName},
				Mutates: true, OKExit: []int{1, 5}, Timeout: 2 * time.Minute,
			})
			return err
		}
		return nil
	}
	if rb != nil {
		rb.Push("wrote the PM2 daemon unit "+path, undo)
	}

	if res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemd-analyze", Args: []string{"verify", path}, OKExit: []int{1},
	}); err == nil && res != nil {
		if out := strings.TrimSpace(res.Stderr); out != "" && strings.Contains(out, "Unknown") {
			m.Log.Warn("systemd reported unknown directives in the PM2 daemon's unit",
				"detail", firstLine(out))
		}
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"daemon-reload"}, Mutates: true, Label: "daemon-reload",
	}); err != nil {
		return err
	}
	if !m.isActive(ctx, unitName) {
		return nil
	}
	if needsRestart {
		m.Log.Warn("restarting the tenant's PM2 daemon; every PM2 site of "+d.Owner+" restarts with it",
			"unit", unitName, "why", "its sandbox, node or PM2 changed, and none of that reaches a running process")
		if _, err := m.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{"restart", unitName},
			Mutates: true, Label: "restart", Timeout: 2 * time.Minute,
		}); err != nil {
			return m.explainFailure(ctx, unitName, err)
		}
		restarted = true
		return nil
	}
	// The ceilings, onto the running cgroup. --runtime, because the unit file already
	// says the same thing and is what holds after a reboot.
	args := []string{"set-property", "--runtime", unitName}
	for _, line := range strings.Split(string(body), "\n") {
		for _, key := range []string{"MemoryMax=", "MemoryHigh=", "CPUQuota=", "TasksMax="} {
			if strings.HasPrefix(line, key) {
				args = append(args, line)
			}
		}
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: args, Mutates: true, OKExit: []int{1},
	}); err != nil {
		m.Log.Warn("could not apply the new ceilings to the running PM2 daemon; they apply on its next restart",
			"unit", unitName, "err", err)
	}
	return nil
}

// pm2UnitShape is a daemon unit with the lines that never need a restart removed: the
// timestamp always, and — when restartOnly — the sites it wants and its ceilings, both
// of which reach a running daemon without one.
func pm2UnitShape(body []byte, restartOnly bool) string {
	var keep []string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "# generated:") {
			continue
		}
		if restartOnly {
			skip := false
			for _, p := range []string{"Wants=ratline-", "MemoryMax=", "MemoryHigh=", "CPUQuota=", "TasksMax="} {
				if strings.HasPrefix(line, p) {
					skip = true
				}
			}
			if skip {
				continue
			}
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}

// isActive reports whether a unit is running. Anything other than a clear yes is no.
func (m *Manager) isActive(ctx context.Context, unitName string) bool {
	res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"is-active", "--quiet", unitName},
		OKExit: []int{1, 3, 4},
	})
	return err == nil && res != nil && res.ExitCode == 0
}

// IsActive is isActive for callers outside the package: whether it is safe to ask a
// tenant's PM2 daemon anything, since a pm2 command that cannot reach one starts one.
func (m *Manager) IsActive(ctx context.Context, unitName string) bool {
	return m.isActive(ctx, unitName)
}

// RemovePM2Daemon stops and deletes a tenant's daemon that no longer hosts any site.
func (m *Manager) RemovePM2Daemon(ctx context.Context, owner, key string) error {
	unitName := validate.PM2UnitName(owner, key)
	path := m.PM2UnitPath(owner, key)
	if m.DryRun {
		m.Log.Info("would remove the tenant's PM2 daemon, which no longer hosts a site", "unit", unitName)
		return nil
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"stop", unitName},
		Mutates: true, OKExit: []int{1, 5}, Label: "stop",
	}); err != nil {
		m.Log.Debug("systemctl reported a problem stopping the PM2 daemon, continuing", "err", err)
	}
	if system.Exists(path) {
		if err := system.RemoveManaged(path); err != nil {
			return err
		}
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"daemon-reload"}, Mutates: true,
	}); err != nil {
		return err
	}
	// systemd keeps a unit that failed in `systemctl --failed` after its file is gone,
	// which is exactly what monitoring watches.
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"reset-failed", unitName},
		Mutates: true, OKExit: []int{1, 5},
	}); err != nil {
		m.Log.Debug("could not reset the PM2 daemon's failed state", "err", err)
	}
	m.Log.Info("removed the tenant's PM2 daemon", "unit", unitName)
	return nil
}

// PM2DaemonKeys lists the daemons installed for a tenant, by key, from the unit files
// on disk — which is what has to be cleaned up, whatever the state database says.
func (m *Manager) PM2DaemonKeys(owner string) ([]string, error) {
	prefix := "ratline-pm2@" + owner + "."
	entries, err := os.ReadDir(m.Cfg.Paths.SystemdDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", m.Cfg.Paths.SystemdDir)
	}
	var keys []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".service") {
			keys = append(keys, strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".service"))
		}
	}
	return keys, nil
}

// IsPM2DaemonUnit reports whether a unit name is a tenant's shared PM2 daemon.
func IsPM2DaemonUnit(name string) bool {
	return strings.HasPrefix(name, "ratline-pm2@") && strings.HasSuffix(name, ".service")
}
