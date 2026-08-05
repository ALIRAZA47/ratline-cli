// Package unit renders and drives one systemd service per dynamic site.
//
// This is what replaces a PHP-FPM pool. The important properties: the service
// runs as the site's own user, its resource ceiling is enforced by the kernel
// rather than by convention, and nothing reports success until a real HTTP
// request has come back through the socket.
package unit

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
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

// Manager renders units and controls services.
type Manager struct {
	Cfg    *config.Config
	Log    *log.Logger
	Runner system.Runner
	DryRun bool
}

// Data is the unit template's input.
type Data struct {
	Domain          string
	Owner           string
	Group           string
	Runtime         string
	Slug            string
	GeneratedAt     string
	WorkingDir      string
	EnvironmentFile string
	Environment     []string
	RuntimeDirName  string
	SocketPath      string
	UMask           string
	ExecStart       string
	ExecStartPost   []string
	ExecReload      string
	ExecStop        string
	Type            string
	PIDFile         string
	RestartSec      string
	TimeoutStopSec  string
	StandardOutput  string
	StandardError   string
	Limits          []string
	Hardening       []string
	Relaxed         bool
	RelaxedList     string
}

// HardeningDirectives is the full sandbox ratline applies.
//
// Each one is verified at install time by starting the service and health
// checking it. When a directive breaks an application, the failure names it so
// the operator can relax that one deliberately instead of abandoning the sandbox
// wholesale.
//
// The keys are the names accepted by --relax.
var HardeningDirectives = []struct {
	Name      string
	Directive string
	Why       string
}{
	{"NoNewPrivileges", "NoNewPrivileges=true", "stops the service gaining privileges through setuid binaries"},
	{"PrivateTmp", "PrivateTmp=true", "gives the service its own /tmp, so tenants cannot see each other's temporary files"},
	{"PrivateDevices", "PrivateDevices=true", "hides raw devices"},
	{"ProtectSystem", "ProtectSystem=strict", "makes the whole filesystem read-only apart from the paths listed below"},
	{"ProtectHome", "ProtectHome=tmpfs", "hides every other tenant's home directory"},
	{"ProtectKernelTunables", "ProtectKernelTunables=true", "makes /proc/sys read-only"},
	{"ProtectKernelModules", "ProtectKernelModules=true", "refuses module loading"},
	{"ProtectKernelLogs", "ProtectKernelLogs=true", "hides the kernel ring buffer"},
	{"ProtectControlGroups", "ProtectControlGroups=true", "makes the cgroup hierarchy read-only"},
	{"ProtectClock", "ProtectClock=true", "refuses clock changes"},
	{"ProtectHostname", "ProtectHostname=true", "refuses hostname changes"},
	{"RestrictNamespaces", "RestrictNamespaces=true", "refuses namespace creation, which blocks container tricks"},
	{"RestrictSUIDSGID", "RestrictSUIDSGID=true", "refuses creating setuid files"},
	{"RestrictRealtime", "RestrictRealtime=true", "refuses realtime scheduling"},
	{"LockPersonality", "LockPersonality=true", "refuses changing the execution domain"},
	{"MemoryDenyWriteExecute", "MemoryDenyWriteExecute=true", "refuses writable-executable memory — breaks most JITs, including Node"},
	{"SystemCallFilter", "SystemCallFilter=@system-service", "allows only the syscalls a service needs"},
	{"SystemCallArchitectures", "SystemCallArchitectures=native", "refuses foreign-architecture syscalls"},
	{"RestrictAddressFamilies", "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6", "allows only the socket families a web application needs"},
}

// defaultRelaxed is applied per runtime, for directives that are known to break
// it. Discovering these at install time works, but shipping a unit that fails on
// first start would be a poor first impression.
var defaultRelaxed = map[string][]string{
	// V8 needs writable-executable memory for its JIT.
	"node": {"MemoryDenyWriteExecute"},
	// CPython does not JIT, so the default sandbox holds. Native wheels that
	// mmap executable pages are the exception, and the install-time check
	// catches them.
	"python": {},
}

// Render produces the unit file.
func (m *Manager) Render(site *state.Site, execStart string, opts RenderOptions) ([]byte, error) {
	siteDir := m.Cfg.SiteDir(site.Owner, site.Domain)
	relaxed := append([]string(nil), site.Relaxed...)
	relaxed = append(relaxed, defaultRelaxed[site.Runtime]...)

	d := &Data{
		Domain:          site.Domain,
		Owner:           site.Owner,
		Group:           site.Owner,
		Runtime:         site.Runtime,
		Slug:            site.Slug,
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		WorkingDir:      opts.WorkingDir,
		EnvironmentFile: filepath.Join(siteDir, ".env"),
		Environment:     opts.Environment,
		RuntimeDirName:  filepath.Join("ratline", site.Slug),
		UMask:           m.Cfg.Defaults.Umask,
		ExecStart:       execStart,
		ExecStartPost:   opts.ExecStartPost,
		ExecReload:      opts.ExecReload,
		ExecStop:        opts.ExecStop,
		Type:            orDefault(opts.Type, "exec"),
		PIDFile:         opts.PIDFile,
		RestartSec:      m.Cfg.Defaults.RestartSec.D().String(),
		TimeoutStopSec:  m.Cfg.Defaults.StopTimeout.D().String(),
		Relaxed:         len(relaxed) > 0,
		RelaxedList:     strings.Join(relaxed, ", "),
	}
	if d.WorkingDir == "" {
		d.WorkingDir = filepath.Join(siteDir, "app")
	}
	if site.Listen != "port" {
		d.SocketPath = m.Cfg.SocketPath(site.Owner, site.Domain)
	}

	memMax := orDefault(site.MemoryMax, m.Cfg.Defaults.MemoryMax)
	memBytes, err := validate.Size(memMax)
	if err != nil {
		return nil, err
	}
	high := int64(float64(memBytes) * m.Cfg.Defaults.MemoryHighRatio)
	d.Limits = []string{
		"MemoryMax=" + memMax,
		// MemoryHigh throttles and reclaims before MemoryMax kills, which turns
		// a hard OOM into back pressure the application may survive.
		"MemoryHigh=" + validate.FormatSize(high),
		"MemoryAccounting=true",
		"CPUQuota=" + orDefault(site.CPUQuota, m.Cfg.Defaults.CPUQuota),
		"CPUAccounting=true",
		fmt.Sprintf("TasksMax=%d", m.Cfg.Defaults.TasksMax),
		fmt.Sprintf("LimitNOFILE=%d", m.Cfg.Defaults.LimitNOFILE),
		"OOMPolicy=continue",
	}

	skip := map[string]bool{}
	for _, r := range relaxed {
		skip[r] = true
	}
	for _, h := range HardeningDirectives {
		if skip[h.Name] {
			d.Hardening = append(d.Hardening, "# "+h.Directive+" — relaxed for this site")
			continue
		}
		d.Hardening = append(d.Hardening, h.Directive)
	}
	// ProtectSystem=strict makes everything read-only, so the paths the
	// application legitimately writes have to be named. BindPaths rather than
	// ReadWritePaths, because ProtectHome=tmpfs has already replaced /home in the
	// namespace and the site directory has to be mounted back in.
	d.Hardening = append(d.Hardening,
		"BindPaths="+siteDir,
		"ReadWritePaths="+filepath.Join(siteDir, "logs")+" "+filepath.Join(siteDir, "tmp"),
	)
	if opts.ExtraReadWritePaths != "" {
		d.Hardening = append(d.Hardening, "ReadWritePaths="+opts.ExtraReadWritePaths)
	}
	// A private /tmp is useless if the application still writes to the shared
	// one, so point TMPDIR at the site's own directory.
	d.Environment = append(d.Environment, "TMPDIR="+filepath.Join(siteDir, "tmp"))

	tmpl, err := template.New("site.service.tmpl").ParseFS(templates.FS, "systemd/site.service.tmpl")
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "parsing the unit template")
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, d); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "rendering the unit for %s", site.Domain)
	}
	return buf.Bytes(), nil
}

// RenderOptions carries the runtime-specific parts of a unit.
type RenderOptions struct {
	WorkingDir          string
	Environment         []string
	ExecStartPost       []string
	ExecReload          string
	ExecStop            string
	ExtraReadWritePaths string

	// Type overrides the service type. PM2 daemonises, so a PM2-supervised site is
	// Type=forking with a PIDFile; everything else stays Type=exec.
	Type    string
	PIDFile string
}

// Install writes the unit, verifies it and enables it.
func (m *Manager) Install(ctx context.Context, site *state.Site, body []byte, rb *system.Rollback) error {
	path := m.Cfg.UnitPath(site.Owner, site.Domain)
	if system.Exists(path) {
		managed, err := system.HasManagedHeader(path)
		if err != nil {
			return err
		}
		if !managed {
			return rlerr.Preconditionf("%s exists but was not created by ratline", path).
				WithHint("move it aside if you want ratline to manage this service")
		}
	}
	if err := m.EnsureTarget(ctx); err != nil {
		return err
	}
	if m.DryRun {
		m.Log.Info("would install the unit", "path", path)
		return nil
	}

	existed := system.Exists(path)
	var previous []byte
	if existed {
		var err error
		if previous, err = system.ReadFileLimit(path, 1<<20); err != nil {
			return err
		}
	}
	if err := system.WriteFileAtomic(path, body, 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}
	rb.Push("wrote the unit "+path, func(context.Context) error {
		if existed {
			return system.WriteFileAtomic(path, previous, 0o644, system.KeepUnchanged, system.KeepUnchanged)
		}
		return os.Remove(path)
	})

	// Catch a malformed unit before daemon-reload rather than after.
	if res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemd-analyze", Args: []string{"verify", path}, OKExit: []int{1},
	}); err == nil && res != nil {
		if out := strings.TrimSpace(res.Stderr); out != "" && strings.Contains(out, "Unknown") {
			// Unknown directives are usually an old systemd, not a broken unit.
			m.Log.Warn("systemd reported unknown directives in the unit; it will still start, but some hardening may be ignored",
				"detail", firstLine(out))
		}
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"daemon-reload"}, Mutates: true, Label: "daemon-reload",
	}); err != nil {
		return err
	}

	unitName := validate.UnitName(site.Owner, site.Domain)
	if site.Enabled {
		if _, err := m.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{"enable", unitName}, Mutates: true, Label: "enable",
		}); err != nil {
			return err
		}
		rb.Push("enabled "+unitName, func(ctx context.Context) error {
			// Stop before disable, and before anything further up the stack removes
			// the site's directories.
			//
			// `systemctl disable` only unlinks the wants symlink; it does not stop a
			// running unit. With Restart=always, a unit whose start failed keeps being
			// restarted — so the rollback would delete the logs and tmp directories out
			// from under a service systemd was still trying to launch, turning one
			// clear failure into a stream of 226/NAMESPACE errors in the journal.
			if _, err := m.Runner.Run(ctx, system.Cmd{
				Name: "systemctl", Args: []string{"stop", unitName},
				Mutates: true, OKExit: []int{1, 5},
			}); err != nil {
				return err
			}
			_, err := m.Runner.Run(ctx, system.Cmd{
				Name: "systemctl", Args: []string{"disable", unitName}, Mutates: true, OKExit: []int{1, 5},
			})
			return err
		})
	}
	return nil
}

// EnsureTarget installs ratline.target, so every managed site can be stopped at
// once.
func (m *Manager) EnsureTarget(ctx context.Context) error {
	path := filepath.Join(m.Cfg.Paths.SystemdDir, "ratline.target")
	if system.Exists(path) {
		return nil
	}
	body, err := templates.FS.ReadFile("systemd/ratline.target")
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "reading the embedded target")
	}
	if m.DryRun {
		m.Log.Info("would install ratline.target")
		return nil
	}
	if err := system.WriteFileAtomic(path, body, 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"daemon-reload"}, Mutates: true,
	}); err != nil {
		return err
	}
	_, err = m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"enable", "ratline.target"}, Mutates: true, OKExit: []int{1},
	})
	return err
}

// managedTimers are the units ratline installs for itself, as opposed to the one it
// generates per site. They live in the embedded templates so that a binary is
// self-sufficient: `ratline init` can install them on a server that only ever received
// the binary, which is what makes a one-command install possible and what stops the
// installer from depending on files sitting next to it in a checkout.
var managedTimers = []string{
	"ratline-cert-renew.service",
	"ratline-cert-renew.timer",
	"ratline-key-prune.service",
	"ratline-key-prune.timer",
}

// IsOwnUnit reports whether a unit name is one ratline runs for itself, as opposed to
// one it generated for a site.
//
// Kept beside the list that installs them so the two cannot disagree: a unit added to
// managedTimers and not to this check would be reported as an orphan by `doctor`, whose
// suggested fix is to delete it.
func IsOwnUnit(name string) bool {
	if name == "ratline.target" {
		return true
	}
	for _, own := range managedTimers {
		if name == own {
			return true
		}
	}
	return false
}

// EnsureTimers installs ratline's own renewal and key-pruning units and starts their
// timers.
//
// Refreshed rather than skipped when present, because a new release may ship a corrected
// unit and this is what an operator running `ratline init` after an update expects. The
// managed-by header means a hand-edited copy is left alone: system.WriteManaged refuses a
// file that does not carry it.
func (m *Manager) EnsureTimers(ctx context.Context) error {
	if err := m.EnsureTarget(ctx); err != nil {
		return err
	}
	var installed []string
	for _, name := range managedTimers {
		body, err := templates.FS.ReadFile("systemd/" + name)
		if err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "reading the embedded unit %s", name)
		}
		path := filepath.Join(m.Cfg.Paths.SystemdDir, name)
		if m.DryRun {
			m.Log.Info("would install a unit", "unit", name)
			continue
		}
		// A unit the operator has edited is theirs. The promise is that ratline never
		// overwrites a file lacking its header, and that has to hold for its own units
		// as much as for a vhost.
		if system.Exists(path) {
			managed, err := system.HasManagedHeader(path)
			if err != nil {
				return err
			}
			if !managed {
				m.Log.Warn("leaving a hand-edited unit alone", "unit", name, "path", path,
					"note", "it does not carry ratline's header, so it was not replaced")
				continue
			}
		}
		if err := system.WriteFileAtomic(path, body, 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
			return err
		}
		installed = append(installed, name)
	}
	if m.DryRun || len(installed) == 0 {
		return nil
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"daemon-reload"}, Mutates: true,
	}); err != nil {
		return err
	}
	// Only the timers are enabled; the services they trigger must not run at boot.
	for _, name := range managedTimers {
		if !strings.HasSuffix(name, ".timer") {
			continue
		}
		if _, err := m.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{"enable", "--now", name},
			Mutates: true, OKExit: []int{1},
		}); err != nil {
			// A timer that will not start is worth reporting, but it must not stop
			// setup: everything else on the server is already configured, and
			// 'ratline doctor' names a missing timer.
			m.Log.Warn("could not start a timer", "unit", name, "err", err,
				"note", "certificates will not renew automatically until this starts")
		}
	}
	m.Log.Info("installed ratline's own timers", "units", len(installed))
	return nil
}

// Control runs a systemctl verb against a site's unit.
func (m *Manager) Control(ctx context.Context, site *state.Site, verb string) error {
	unitName := validate.UnitName(site.Owner, site.Domain)

	// Clear a stale failure before trying to bring the unit up.
	//
	// StartLimitBurst puts a unit that has crash-looped into a state where systemd
	// refuses to start it at all until the counter is reset. That is right for a
	// service nobody is attending to, and wrong for the recovery path: a deploy that
	// fails, reverts the code and restarts is refused by the very limit the failed
	// deploy just consumed — so the revert reports "the previous version did not
	// restart either" for code that is perfectly good. Resetting is what an operator
	// would do by hand, and it changes nothing for a unit that is not in that state.
	if verb == "start" || verb == "restart" {
		if _, err := m.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{"reset-failed", unitName},
			Mutates: true, OKExit: []int{1, 5}, Timeout: 30 * time.Second,
		}); err != nil {
			m.Log.Debug("could not reset the unit's failure counter", "unit", unitName, "err", err)
		}
	}

	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{verb, unitName},
		Mutates: true, Label: verb, Timeout: 2 * time.Minute,
	}); err != nil {
		return m.explainFailure(ctx, unitName, err)
	}
	return nil
}

// Status describes a service.
type Status struct {
	Unit        string `json:"unit"`
	Active      string `json:"active"`
	Sub         string `json:"sub,omitempty"`
	Enabled     string `json:"enabled,omitempty"`
	MainPID     string `json:"main_pid,omitempty"`
	MemoryHuman string `json:"memory,omitempty"`
	Since       string `json:"since,omitempty"`
	NRestarts   string `json:"restarts,omitempty"`
}

// Status reads a service's state.
func (m *Manager) Status(ctx context.Context, site *state.Site) (*Status, error) {
	unitName := validate.UnitName(site.Owner, site.Domain)
	st := &Status{Unit: unitName, Active: "unknown"}
	res, err := m.Runner.Run(ctx, system.Cmd{
		Name:   "systemctl",
		Args:   []string{"show", unitName, "--property=ActiveState,SubState,UnitFileState,MainPID,MemoryCurrent,ActiveEnterTimestamp,NRestarts"},
		OKExit: []int{1, 3, 4},
	})
	if err != nil || res == nil {
		// The zero value already says Active: "unknown", which is displayed as such.
		// A site whose unit cannot be queried is not a reason to fail `site list`.
		//nolint:nilerr // "unknown" is the honest answer and is shown to the operator
		return st, nil
	}
	for _, line := range res.Lines() {
		key, value, ok := strings.Cut(line, "=")
		if !ok || value == "" {
			continue
		}
		switch key {
		case "ActiveState":
			st.Active = value
		case "SubState":
			st.Sub = value
		case "UnitFileState":
			st.Enabled = value
		case "MainPID":
			if value != "0" {
				st.MainPID = value
			}
		case "MemoryCurrent":
			if n := parseInt64(value); n > 0 {
				st.MemoryHuman = validate.FormatSize(n)
			}
		case "ActiveEnterTimestamp":
			st.Since = value
		case "NRestarts":
			st.NRestarts = value
		}
	}
	return st, nil
}

// WaitHealthy blocks until the application answers a real request.
//
// This is the difference between "systemctl start returned 0" and "the site
// works". A unit can be active while the application is still importing modules,
// or crash-looping fast enough that systemd still calls it active. Making an HTTP
// request through the socket nginx will use is the only check that means anything.
func (m *Manager) WaitHealthy(ctx context.Context, site *state.Site, timeout time.Duration) (string, error) {
	if m.DryRun {
		return "skipped under --dry-run", nil
	}
	if timeout <= 0 {
		timeout = m.Cfg.Defaults.HealthTimeout.D()
	}
	target := m.Cfg.SocketPath(site.Owner, site.Domain)
	network := "unix"
	if site.Listen == "port" {
		network = "tcp"
		target = fmt.Sprintf("127.0.0.1:%d", site.Port)
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, target)
			},
		},
	}

	deadline := time.Now().Add(timeout)
	unitName := validate.UnitName(site.Owner, site.Domain)
	attempt := 0
	var lastErr error
	for time.Now().Before(deadline) {
		attempt++
		// A service that has already given up will never answer, so stop early
		// and report why rather than waiting out the full timeout.
		if st, err := m.Status(ctx, site); err == nil && st.Active == "failed" {
			return "", m.unhealthyError(ctx, site, unitName, "the service failed to start")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://ratline-health/", nil)
		if err != nil {
			return "", rlerr.Wrap(err, rlerr.CodeGeneric, "building the health request")
		}
		req.Host = site.Domain
		start := time.Now()
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			// Any HTTP response proves the application is listening and serving.
			// A 404 from a router with no "/" route is a healthy application.
			return fmt.Sprintf("HTTP %d in %s", resp.StatusCode, time.Since(start).Round(time.Millisecond)), nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return "", rlerr.Unhealthyf("the health check was interrupted")
		}
		time.Sleep(backoff(attempt))
	}
	_ = lastErr
	return "", m.unhealthyError(ctx, site, unitName,
		fmt.Sprintf("the application did not answer on %s within %s", target, timeout))
}

// unhealthyError builds the failure message, with the log lines already attached.
//
// An operator should never have to go and find the journal themselves: the last
// twenty lines are almost always the answer, so they come with the error.
func (m *Manager) unhealthyError(ctx context.Context, site *state.Site, unitName, what string) error {
	detail := what
	if st, err := m.Status(ctx, site); err == nil {
		detail += fmt.Sprintf("; systemd reports %s (%s)", st.Active, st.Sub)
		if st.NRestarts != "" && st.NRestarts != "0" {
			detail += ", restarted " + st.NRestarts + " time(s)"
		}
	}
	logs := m.Logs(ctx, unitName, 20)
	if last := lastMeaningful(logs); last != "" {
		detail += fmt.Sprintf("; the last log line was %q", last)
	}
	e := rlerr.Unhealthyf("%s", detail).
		WithField("unit", unitName).
		WithHint("full output: journalctl -u %s -n 50 --no-pager", unitName)
	if logs != "" {
		e = e.WithField("recent_logs", logs)
	}
	return e
}

// Logs returns the last n journal lines for a unit.
func (m *Manager) Logs(ctx context.Context, unitName string, n int) string {
	res, err := m.Runner.Run(ctx, system.Cmd{
		Name:   "journalctl",
		Args:   []string{"-u", unitName, "-n", fmt.Sprint(n), "--no-pager", "--output=short-iso"},
		OKExit: []int{1},
	})
	if err != nil || res == nil {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// explainFailure enriches a systemctl error with the journal.
func (m *Manager) explainFailure(ctx context.Context, unitName string, err error) error {
	logs := m.Logs(ctx, unitName, 20)
	if logs == "" {
		return err
	}
	m.Log.Error("the service failed; recent log lines follow", "unit", unitName)
	for _, line := range strings.Split(logs, "\n") {
		m.Log.Error("  " + line)
	}
	return rlerr.Wrap(err, rlerr.CodeExternal, "%s failed", unitName).
		WithField("recent_logs", logs).
		WithHint("journalctl -u %s -n 50 --no-pager", unitName)
}

// Remove stops, disables and deletes a site's unit.
func (m *Manager) Remove(ctx context.Context, site *state.Site) error {
	unitName := validate.UnitName(site.Owner, site.Domain)
	if m.DryRun {
		m.Log.Info("would remove the unit", "unit", unitName)
		return nil
	}
	for _, verb := range []string{"stop", "disable"} {
		if _, err := m.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{verb, unitName},
			Mutates: true, OKExit: []int{1, 5}, Label: verb,
		}); err != nil {
			m.Log.Debug("systemctl reported a problem, continuing with the teardown", "verb", verb, "err", err)
		}
	}
	path := m.Cfg.UnitPath(site.Owner, site.Domain)
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
	// systemd leaves the runtime directory behind when the unit file is gone
	// before the reload, which would leave a stale socket for the next site with
	// the same slug.
	runtimeDir := m.Cfg.RuntimeDir(site.Owner, site.Domain)
	if system.Exists(runtimeDir) {
		if err := os.RemoveAll(runtimeDir); err != nil {
			m.Log.Warn("could not remove the runtime directory", "path", runtimeDir, "err", err)
		}
	}
	return nil
}

// InstallLogrotate writes the log rotation policy for a site.
func (m *Manager) InstallLogrotate(ctx context.Context, site *state.Site) error {
	siteDir := m.Cfg.SiteDir(site.Owner, site.Domain)
	data := map[string]any{
		"Domain":   site.Domain,
		"Owner":    site.Owner,
		"LogGroup": m.Cfg.Users.LogGroup,
		"LogGlob":  filepath.Join(siteDir, "logs", "*.log"),
		"Dynamic":  site.Dynamic(),
		"Unit":     validate.UnitName(site.Owner, site.Domain),
	}
	tmpl, err := template.New("site.tmpl").ParseFS(templates.FS, "logrotate/site.tmpl")
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "parsing the logrotate template")
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "rendering the logrotate policy")
	}
	path := filepath.Join(m.Cfg.Paths.LogrotateDir, "ratline-"+site.Domain)
	if m.DryRun {
		m.Log.Info("would write the logrotate policy", "path", path)
		return nil
	}
	return system.WriteFileAtomic(path, buf.Bytes(), 0o644, system.KeepUnchanged, system.KeepUnchanged)
}

// RemoveLogrotate deletes a site's rotation policy.
func (m *Manager) RemoveLogrotate(site *state.Site) error {
	path := filepath.Join(m.Cfg.Paths.LogrotateDir, "ratline-"+site.Domain)
	if !system.Exists(path) || m.DryRun {
		return nil
	}
	return system.RemoveManaged(path)
}

func backoff(attempt int) time.Duration {
	// Fast at first, because most applications are up in well under a second,
	// then slower so a long import does not mean hundreds of probes.
	switch {
	case attempt < 10:
		return 100 * time.Millisecond
	case attempt < 30:
		return 300 * time.Millisecond
	default:
		return time.Second
	}
}

func lastMeaningful(logs string) string {
	lines := strings.Split(logs, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		// Drop journald's own framing so the application's message is what shows.
		if idx := strings.Index(l, "]: "); idx > 0 {
			l = l[idx+3:]
		}
		if l != "" {
			return l
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func parseInt64(s string) int64 {
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int64(r-'0')
	}
	return n
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
