package unit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
	"github.com/ALIRAZA47/ratline-cli/templates"
)

// Scheduled jobs and workers, as units belonging to a site.
//
// The reason these are not lines in a tenant's crontab is that a crontab line runs outside
// every limit the site is held to. No MemoryMax, so a runaway import takes the host down
// instead of one service. No ProtectSystem, so a job can write anywhere the tenant can. No
// cgroup, so nothing accounts for what it used. And nothing in `status`, `doctor` or
// `reconcile` knows it exists, which means the one thing on the server most likely to be
// silently broken is the one thing nothing watches.
//
// A unit gets the site's tenant, working directory, .env, sandbox and ceiling, because it
// is the same application doing the same work on a different trigger.

// SiteUnitData is the template input for a job or worker.
type SiteUnitData struct {
	Domain          string
	Owner           string
	Group           string
	Slug            string
	Kind            string
	UnitName        string
	GeneratedAt     string
	WorkingDir      string
	EnvironmentFile string
	Environment     []string
	ExecStart       string
	LogFile         string
	TimeoutSec      string
	RestartSec      string
	Limits          []string
	Hardening       []string
	Relaxed         bool
	RelaxedList     string
	IsWorker        bool
	SiteUnitName    string

	// timer only
	Schedule        string
	Persistent      bool
	JitterSec       int
	ServiceUnitName string
}

// SiteUnitLogPath is where a job or worker's output goes.
//
// One place decides this, because the template writes it and `site cron logs` reads it —
// and when those two disagreed, a job that had just run reported "Nothing logged yet".
func SiteUnitLogPath(cfg *config.Config, site *state.Site, u *state.SiteUnit) string {
	return siteUnitLogPath(cfg.SiteDir(site.Owner, site.Domain), u)
}

func siteUnitLogPath(siteDir string, u *state.SiteUnit) string {
	return filepath.Join(siteDir, "logs", u.Kind+"-"+u.Name+".log")
}

// SiteUnitName is the systemd unit filename for a job or worker.
//
// The site's slug is already collision-proof, and the kind is in the name so a job and a
// worker may share one — `nightly` as both a job and a worker is odd but not wrong, and
// having them silently collide would be.
func SiteUnitName(slug, kind, name string) string {
	return fmt.Sprintf("ratline-%s-%s-%s.service", slug, kind, name)
}

// SiteTimerName is the timer that drives a job.
func SiteTimerName(slug, name string) string {
	return fmt.Sprintf("ratline-%s-job-%s.timer", slug, name)
}

// RenderSiteUnit produces the service, and for a job the timer as well.
func (m *Manager) RenderSiteUnit(site *state.Site, u *state.SiteUnit) (service, timer []byte, err error) {
	// The command and the timeout are written verbatim into the unit as ExecStart and
	// TimeoutStartSec. A newline in either adds a directive to a root-installed unit that
	// `systemd-analyze verify` still accepts, because a second ExecStart is valid syntax.
	// This is the boundary the invariant has to hold at: the CLI checks the same things
	// for a friendlier message, but import and clone reach here too, and a crafted export
	// is exactly the untrusted input that would carry a newline.
	if err := validate.NoControlChars("command", u.Command); err != nil {
		return nil, nil, err
	}
	if u.Timeout != "" {
		if err := validate.NoControlChars("timeout", u.Timeout); err != nil {
			return nil, nil, err
		}
		if _, err := validate.Duration(u.Timeout); err != nil {
			return nil, nil, rlerr.Wrap(err, rlerr.CodeUsage, "the timeout %q is not a duration", u.Timeout)
		}
	}
	// The unit's own memory ceiling reaches a Limits line (MemoryMax=...); validate.Size
	// below accepts a trailing newline, so refuse control characters here for the same
	// reason the command and timeout are refused.
	if u.MemoryMax != "" {
		if err := validate.NoControlChars("memory-max", u.MemoryMax); err != nil {
			return nil, nil, err
		}
	}

	siteDir := m.Cfg.SiteDir(site.Owner, site.Domain)
	relaxed := append([]string(nil), site.Relaxed...)
	relaxed = append(relaxed, defaultRelaxed[site.Runtime]...)

	d := &SiteUnitData{
		Domain:          site.Domain,
		Owner:           site.Owner,
		Group:           site.Owner,
		Slug:            site.Slug,
		Kind:            u.Kind,
		UnitName:        u.Name,
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		WorkingDir:      filepath.Join(siteDir, "app"),
		EnvironmentFile: filepath.Join(siteDir, ".env"),
		ExecStart:       u.Command,
		LogFile:         siteUnitLogPath(siteDir, u),
		RestartSec:      m.Cfg.Defaults.RestartSec.D().String(),
		Relaxed:         len(relaxed) > 0,
		RelaxedList:     strings.Join(relaxed, ", "),
		IsWorker:        u.Kind == state.UnitWorker,
		SiteUnitName:    validate.UnitName(site.Owner, site.Domain),
		Schedule:        u.Schedule,
		Persistent:      u.Persistent,
		ServiceUnitName: SiteUnitName(site.Slug, u.Kind, u.Name),
	}
	if u.Timeout != "" {
		d.TimeoutSec = u.Timeout
	}
	// Every site's nightly job firing on the same second is a thundering herd against
	// whatever they all talk to. A minute of jitter costs nothing and spreads it.
	d.JitterSec = 60

	// The ceiling is the unit's own if it has one, otherwise the site's. A job that
	// imports a large file legitimately needs more than the web process, and making it
	// take the site's limit would mean raising the site's.
	memMax := orDefault(u.MemoryMax, orDefault(site.MemoryMax, m.Cfg.Defaults.MemoryMax))
	memBytes, err := validate.Size(memMax)
	if err != nil {
		return nil, nil, err
	}
	high := int64(float64(memBytes) * m.Cfg.Defaults.MemoryHighRatio)
	d.Limits = []string{
		"MemoryMax=" + memMax,
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
	d.Hardening = append(d.Hardening,
		"BindPaths="+siteDir,
		"ReadWritePaths="+filepath.Join(siteDir, "logs")+" "+filepath.Join(siteDir, "tmp"),
	)
	d.Environment = append(d.Environment, "TMPDIR="+filepath.Join(siteDir, "tmp"))

	if service, err = renderTemplate("site-unit.service.tmpl", d); err != nil {
		return nil, nil, err
	}
	if u.Kind == state.UnitJob {
		if timer, err = renderTemplate("site-unit.timer.tmpl", d); err != nil {
			return nil, nil, err
		}
	}
	return service, timer, nil
}

func renderTemplate(name string, d *SiteUnitData) ([]byte, error) {
	tmpl, err := template.New(name).ParseFS(templates.FS, "systemd/"+name)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "parsing %s", name)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, d); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "rendering %s", name)
	}
	return buf.Bytes(), nil
}

// VerifySchedule hands the calendar expression to systemd and reports what it makes of it.
//
// The same stage-verify-commit shape as everything else: the real tool judges the artefact
// before it is written. It also returns the next few firings, because an operator who is
// shown "Sun 2026-08-09 03:00:00" can tell at a glance whether a translated cron
// expression means what they intended, and one who is shown nothing cannot.
func (m *Manager) VerifySchedule(ctx context.Context, calendar string) (next []string, err error) {
	res, err := m.Runner.Run(ctx, system.Cmd{
		Name:  "systemd-analyze",
		Args:  []string{"calendar", "--iterations=3", calendar},
		Label: "check the schedule",
	})
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeUsage, "systemd does not understand %q", calendar).
			WithHint("see 'man systemd.time' for the calendar syntax, or write it as cron: " +
				"'0 3 * * *'")
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimSpace(line)
		// Both "Next elapse:" and the numbered "Iter. #n:" lines carry a time.
		if _, after, found := strings.Cut(line, ":"); found {
			if strings.HasPrefix(line, "Next elapse") || strings.HasPrefix(line, "Iter.") {
				next = append(next, strings.TrimSpace(after))
			}
		}
	}
	return next, nil
}

// InstallSiteUnit writes the unit, verifies it and reloads systemd.
func (m *Manager) InstallSiteUnit(ctx context.Context, site *state.Site, u *state.SiteUnit,
	service, timer []byte, rb *system.Rollback) error {

	units := []struct {
		name string
		body []byte
	}{{SiteUnitName(site.Slug, u.Kind, u.Name), service}}
	if timer != nil {
		units = append(units, struct {
			name string
			body []byte
		}{SiteTimerName(site.Slug, u.Name), timer})
	}

	for _, un := range units {
		path := filepath.Join("/etc/systemd/system", un.name)
		// The same refusal every generated file here carries: a file at one of ratline's
		// paths without its header was put there by somebody else, and overwriting it is
		// not ratline's call to make.
		if system.Exists(path) {
			managed, err := system.HasManagedHeader(path)
			if err != nil {
				return err
			}
			if !managed {
				return rlerr.Preconditionf("%s exists but was not created by ratline", path).
					WithHint("move it aside if you want ratline to manage this unit")
			}
		}
		if m.DryRun {
			m.Log.Info("would write", "unit", un.name)
			continue
		}
		existed := system.Exists(path)
		var previous []byte
		if existed {
			var err error
			if previous, err = system.ReadFileLimit(path, 1<<20); err != nil {
				return err
			}
		}
		if err := system.WriteFileAtomic(path, un.body, 0o644,
			system.KeepUnchanged, system.KeepUnchanged); err != nil {
			return err
		}
		rb.Push("wrote the unit "+path, func(context.Context) error {
			if existed {
				return system.WriteFileAtomic(path, previous, 0o644,
					system.KeepUnchanged, system.KeepUnchanged)
			}
			return os.Remove(path)
		})
	}

	if m.DryRun {
		return nil
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"daemon-reload"},
		Mutates: true, Label: "reload systemd",
	}); err != nil {
		return err
	}
	// systemd-analyze verify is the real check on what was written. Run after the reload
	// so it sees the unit in place, and treated as advisory for a job whose ExecStart
	// points at something that does not exist yet — a deploy has not necessarily happened.
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name:  "systemd-analyze",
		Args:  []string{"verify", SiteUnitName(site.Slug, u.Kind, u.Name)},
		Label: "verify the unit",
	}); err != nil {
		m.Log.Warn("systemd-analyze had something to say about the unit",
			"unit", SiteUnitName(site.Slug, u.Kind, u.Name), "err", err)
	}
	return nil
}

// EnableSiteUnit starts a worker, or arms a job's timer.
func (m *Manager) EnableSiteUnit(ctx context.Context, site *state.Site, u *state.SiteUnit) error {
	if m.DryRun {
		return nil
	}
	target := SiteUnitName(site.Slug, u.Kind, u.Name)
	if u.Kind == state.UnitJob {
		// The timer is what gets enabled. Enabling the service would run the job now and
		// again at every boot, which is not what a schedule means.
		target = SiteTimerName(site.Slug, u.Name)
	}
	verb := "enable"
	args := []string{verb, "--now", target}
	if !u.Enabled {
		args = []string{"disable", "--now", target}
	}
	_, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: args, Mutates: true,
		Label: verb + " " + target,
	})
	return err
}

// RemoveSiteUnit stops and deletes a job or worker.
func (m *Manager) RemoveSiteUnit(ctx context.Context, site *state.Site, u *state.SiteUnit) error {
	names := []string{SiteUnitName(site.Slug, u.Kind, u.Name)}
	if u.Kind == state.UnitJob {
		// The timer first: disabling the service while its timer is still armed leaves
		// systemd trying to start a unit that is on its way out.
		names = append([]string{SiteTimerName(site.Slug, u.Name)}, names...)
	}
	for _, n := range names {
		if !m.DryRun {
			// Best effort. A unit that is already gone is the state we want, and failing
			// the whole removal because systemd had nothing to stop would leave the row
			// behind for something that does not exist.
			if _, err := m.Runner.Run(ctx, system.Cmd{
				Name: "systemctl", Args: []string{"disable", "--now", n},
				Mutates: true, Label: "stop " + n, OKExit: []int{1, 5},
			}); err != nil {
				m.Log.Debug("nothing to stop", "unit", n, "err", err)
			}
		}
		path := filepath.Join("/etc/systemd/system", n)
		if m.DryRun {
			m.Log.Info("would remove", "unit", n)
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "removing %s", path)
		}
	}
	if m.DryRun {
		return nil
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"daemon-reload"},
		Mutates: true, Label: "reload systemd",
	}); err != nil {
		return err
	}

	// Forget the failed state, or the removal leaves residue nothing on disk explains.
	//
	// systemd remembers that a unit failed after its file is gone: the entry becomes
	// "not-found failed" and stays in `systemctl list-units --all` and, worse, in
	// `systemctl --failed` — which is exactly what an operator and a monitoring check look
	// at. A job that failed once and was then deleted would alarm about itself for ever,
	// for a unit that no longer exists and no file mentions.
	//
	// Found by removing a deliberately-failing job on a real server and then comparing the
	// server against a snapshot taken before the run. Nothing on disk had changed; this had.
	for _, n := range names {
		if _, err := m.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{"reset-failed", n},
			Mutates: true, Label: "forget " + n, OKExit: []int{1, 4, 5},
		}); err != nil {
			m.Log.Debug("nothing to reset", "unit", n, "err", err)
		}
	}
	return nil
}

// SiteUnitStatus is what systemd currently thinks of one.
type SiteUnitStatus struct {
	Active    string
	Sub       string
	Enabled   string
	NextRun   string
	LastRun   string
	LastState string
}

// SiteUnitStatusOf reads it back.
func (m *Manager) SiteUnitStatusOf(ctx context.Context, site *state.Site, u *state.SiteUnit) *SiteUnitStatus {
	s := &SiteUnitStatus{Active: "unknown"}
	name := SiteUnitName(site.Slug, u.Kind, u.Name)

	show := func(unit string, props ...string) map[string]string {
		out := map[string]string{}
		res, err := m.Runner.Run(ctx, system.Cmd{
			Name: "systemctl",
			Args: []string{"show", "--no-pager", unit,
				"--property=" + strings.Join(props, ",")},
			Label: "read unit state",
		})
		if err != nil {
			return out
		}
		for _, line := range strings.Split(res.Stdout, "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
				out[k] = v
			}
		}
		return out
	}

	p := show(name, "ActiveState", "SubState", "UnitFileState", "ExecMainStatus", "Result")
	s.Active = p["ActiveState"]
	s.Sub = p["SubState"]
	s.Enabled = p["UnitFileState"]
	s.LastState = p["Result"]

	if u.Kind == state.UnitJob {
		t := show(SiteTimerName(site.Slug, u.Name),
			"NextElapseUSecRealtime", "LastTriggerUSec", "UnitFileState")
		s.Enabled = t["UnitFileState"]
		s.NextRun = t["NextElapseUSecRealtime"]
		s.LastRun = t["LastTriggerUSec"]
	}
	return s
}

// Probe makes one HTTP request through the site's socket or port.
//
// The single-shot form of WaitHealthy, which retries until a deadline because it is used
// right after a start. A periodic check wants the opposite: ask once, record what came
// back, and let the caller decide what a streak of failures means.
//
// Deliberately reports a status code rather than a verdict. Whether a 404 counts as broken
// depends on the site — one behind authentication answers 401 to an unauthenticated
// request and is perfectly healthy — so that judgement belongs to the caller and not here.
func (m *Manager) Probe(ctx context.Context, site *state.Site) (int, error) {
	if m.DryRun {
		return 0, nil
	}
	target := m.Cfg.SocketPath(site.Owner, site.Domain)
	network := "unix"
	if site.Listen == "port" {
		network = "tcp"
		target = fmt.Sprintf("127.0.0.1:%d", site.Port)
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, target)
			},
		},
		// A redirect is the application answering, which is all this asks. Following one
		// would mean leaving the socket for whatever Location says — quite possibly the
		// public internet — from a probe that is supposed to stay on this machine.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://ratline-health/", nil)
	if err != nil {
		return 0, err
	}
	req.Host = site.Domain
	req.Header.Set("User-Agent", "ratline-health/1")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	// The body is drained and discarded: not reading it leaks the connection, and keeping
	// it would mean holding a tenant's response in ratline's memory for no reason.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, nil
}
