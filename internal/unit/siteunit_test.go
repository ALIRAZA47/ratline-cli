package unit

import (
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
)

func renderJob(t *testing.T, site *state.Site, u *state.SiteUnit) (service, timer string) {
	t.Helper()
	s, tm, err := testManager().RenderSiteUnit(site, u)
	if err != nil {
		t.Fatalf("RenderSiteUnit = %v", err)
	}
	return string(s), string(tm)
}

func aJob() *state.SiteUnit {
	return &state.SiteUnit{
		Domain: "api.example.com", Name: "nightly", Kind: state.UnitJob,
		Command:  "/home/alice/api.example.com/app/bin/nightly",
		Schedule: "*-*-* 03:00:00", Enabled: true,
	}
}

func aWorker() *state.SiteUnit {
	return &state.SiteUnit{
		Domain: "api.example.com", Name: "queue", Kind: state.UnitWorker,
		Command: "/home/alice/api.example.com/app/bin/worker", Enabled: true,
	}
}

// The entire argument for these being units rather than crontab lines is that they carry
// the site's isolation. If they do not, a crontab line would have been simpler and this
// is worse than what it replaced.
func TestAJobCarriesTheSitesIsolation(t *testing.T) {
	service, _ := renderJob(t, pythonSite(), aJob())

	for _, want := range []string{
		"User=alice",
		"Group=alice",
		"WorkingDirectory=/home/alice/api.example.com/app",
		"EnvironmentFile=-/home/alice/api.example.com/.env",
		"MemoryMax=",
		"MemoryAccounting=true",
		"CPUQuota=",
		"TasksMax=",
		"ProtectSystem=strict",
		"ProtectHome=tmpfs",
		"NoNewPrivileges=true",
		"PrivateTmp=true",
		"SystemCallFilter=@system-service",
		"BindPaths=/home/alice/api.example.com",
		"# managed-by: ratline",
	} {
		if !strings.Contains(service, want) {
			t.Errorf("a job's unit is missing %q — it would run outside the site's limits:\n%s",
				want, service)
		}
	}
}

// A job must not overlap itself. Type=oneshot is what stops a run that takes longer than
// the interval from being started again on top of itself, which for an import job means
// two copies writing the same rows.
func TestAJobIsOneshotAndAWorkerRestarts(t *testing.T) {
	job, _ := renderJob(t, pythonSite(), aJob())
	if !strings.Contains(job, "Type=oneshot") {
		t.Errorf("a job is not oneshot, so a slow run can overlap the next:\n%s", job)
	}
	if strings.Contains(job, "Restart=always") {
		t.Errorf("a job restarts for ever, so a failing one becomes a hot loop:\n%s", job)
	}

	worker, timer := renderJob(t, pythonSite(), aWorker())
	if !strings.Contains(worker, "Restart=always") {
		t.Errorf("a worker does not restart, so one crash ends it silently:\n%s", worker)
	}
	if strings.Contains(worker, "Type=oneshot") {
		t.Errorf("a worker is oneshot:\n%s", worker)
	}
	if timer != "" {
		t.Errorf("a worker was given a timer; nothing schedules it:\n%s", timer)
	}
}

// A worker belongs to its site. One left running against a stopped or half-removed site
// keeps consuming a queue as a tenant nobody is thinking about any more.
func TestAWorkerIsBoundToItsSite(t *testing.T) {
	worker, _ := renderJob(t, pythonSite(), aWorker())
	if !strings.Contains(worker, "PartOf=ratline-alice-api_example_com.service") {
		t.Errorf("a worker is not bound to the site, so stopping the site leaves it running:\n%s",
			worker)
	}
	if !strings.Contains(worker, "After=ratline-alice-api_example_com.service") {
		t.Errorf("a worker does not wait for the site:\n%s", worker)
	}
	if !strings.Contains(worker, "[Install]") {
		t.Errorf("a worker has no [Install], so it cannot be enabled:\n%s", worker)
	}
}

// The timer is the half that decides when anything happens.
func TestTheTimerCarriesTheScheduleAndItsSafeguards(t *testing.T) {
	u := aJob()
	u.Persistent = true
	_, timer := renderJob(t, pythonSite(), u)

	if !strings.Contains(timer, "OnCalendar=*-*-* 03:00:00") {
		t.Errorf("the schedule did not reach the timer:\n%s", timer)
	}
	if !strings.Contains(timer, "Persistent=true") {
		t.Errorf("--persistent did not reach the timer:\n%s", timer)
	}
	// Without jitter every site's nightly job fires on the same second, which for a fleet
	// is a stampede against whatever they all talk to.
	if !strings.Contains(timer, "RandomizedDelaySec=") {
		t.Errorf("the timer has no jitter, so a fleet of sites all fire at once:\n%s", timer)
	}
	if !strings.Contains(timer, "Unit=ratline-alice-api_example_com-job-nightly.service") {
		t.Errorf("the timer does not point at its service:\n%s", timer)
	}
	if !strings.Contains(timer, "WantedBy=timers.target") {
		t.Errorf("the timer cannot be enabled:\n%s", timer)
	}

	// Not persistent unless asked: a job that catches up on every missed firing after a
	// week of downtime is rarely what somebody wants by default.
	_, plain := renderJob(t, pythonSite(), aJob())
	if strings.Contains(plain, "Persistent=true") {
		t.Errorf("Persistent is on by default:\n%s", plain)
	}
}

// A job may need more memory than the web process — an import legitimately does — and
// making it take the site's ceiling would mean raising the site's.
func TestAJobsOwnMemoryCeilingWinsOverTheSites(t *testing.T) {
	site := pythonSite()
	site.MemoryMax = "256M"

	u := aJob()
	u.MemoryMax = "2G"
	withOwn, _ := renderJob(t, site, u)
	if !strings.Contains(withOwn, "MemoryMax=2G") {
		t.Errorf("the job's own ceiling was ignored:\n%s", withOwn)
	}

	inherited, _ := renderJob(t, site, aJob())
	if !strings.Contains(inherited, "MemoryMax=256M") {
		t.Errorf("a job with no ceiling of its own did not inherit the site's:\n%s", inherited)
	}
}

// Relaxing a directive for the site has to relax it for the site's jobs too, or a node
// job dies on the JIT that the site's own service was allowed to use.
func TestASitesRelaxationsReachItsJobs(t *testing.T) {
	site := pythonSite()
	site.Runtime = "node"
	service, _ := renderJob(t, site, aJob())

	if !strings.Contains(service, "# MemoryDenyWriteExecute=true — relaxed for this site") {
		t.Errorf("a node job did not inherit the runtime's relaxation, so V8 cannot JIT:\n%s",
			service)
	}
	// Commented rather than dropped, so the next person to read the unit can see the
	// decision was made rather than forgotten.
	if strings.Contains(service, "\nMemoryDenyWriteExecute=true") {
		t.Errorf("the relaxed directive is still active:\n%s", service)
	}
}

// The unit filename is what everything else addresses. A job and a worker sharing a name
// must not collide, and the log path has to match what the template writes.
func TestTheGeneratedNamesAreDistinctAndStable(t *testing.T) {
	job := SiteUnitName("alice-api_example_com", state.UnitJob, "nightly")
	worker := SiteUnitName("alice-api_example_com", state.UnitWorker, "nightly")
	if job == worker {
		t.Errorf("a job and a worker with the same name collide: both are %q", job)
	}
	for _, n := range []string{job, worker} {
		if !strings.HasPrefix(n, "ratline-") || !strings.HasSuffix(n, ".service") {
			t.Errorf("%q is not a ratline service unit name", n)
		}
	}
	if timer := SiteTimerName("alice-api_example_com", "nightly"); !strings.HasSuffix(timer, ".timer") {
		t.Errorf("%q is not a timer name", timer)
	}

	// The template writes StandardOutput to this path and `site cron logs` reads it. When
	// those disagreed, a job that had just run reported "Nothing logged yet".
	service, _ := renderJob(t, pythonSite(), aJob())
	want := SiteUnitLogPath(testManager().Cfg, pythonSite(), aJob())
	if !strings.Contains(service, "StandardOutput=append:"+want) {
		t.Errorf("the unit writes somewhere other than %s:\n%s", want, service)
	}
}

// systemd judges the calendar expression, not ratline. A schedule it rejects has to be a
// refusal before anything is written, rather than a timer that never fires.
func TestAScheduleSystemdRejectsIsARefusal(t *testing.T) {
	m := testManager()
	fake := systest.NewFakeRunner()
	m.Runner = fake

	fake.ExpectFailure("systemd-analyze calendar --iterations=3 nonsense", 1,
		"Failed to parse calendar specification")
	if _, err := m.VerifySchedule(t.Context(), "nonsense"); err == nil {
		t.Fatal("a schedule systemd rejected was accepted")
	}

	fake.ExpectOutput("systemd-analyze calendar --iterations=3 daily",
		"  Original form: daily\nNormalized form: *-*-* 00:00:00\n"+
			"    Next elapse: Sat 2026-08-08 00:00:00 UTC\n"+
			"       From now: 5h left\n")
	next, err := m.VerifySchedule(t.Context(), "daily")
	if err != nil {
		t.Fatalf("a schedule systemd accepted was refused: %v", err)
	}
	// The operator is shown when it will run, because that is how they check a translated
	// cron expression means what they intended.
	if len(next) == 0 {
		t.Error("no run times came back, so the operator is shown nothing to check")
	}
}

// Removing a job takes its timer as well, and the timer goes first: disabling the service
// while the timer is still armed leaves systemd starting a unit on its way out.
func TestRemovingAJobStopsItsTimerFirst(t *testing.T) {
	m := testManager()
	fake := systest.NewFakeRunner()
	m.Runner = fake

	if err := m.RemoveSiteUnit(t.Context(), pythonSite(), aJob()); err != nil {
		t.Fatalf("RemoveSiteUnit = %v", err)
	}
	keys := strings.Join(fake.Keys(), "\n")
	timerAt := strings.Index(keys, "job-nightly.timer")
	serviceAt := strings.Index(keys, "job-nightly.service")
	if timerAt < 0 {
		t.Fatalf("the timer was never stopped:\n%s", keys)
	}
	if serviceAt >= 0 && timerAt > serviceAt {
		t.Errorf("the service was stopped before its timer:\n%s", keys)
	}
	if !strings.Contains(keys, "daemon-reload") {
		t.Errorf("systemd was not reloaded after the units were removed:\n%s", keys)
	}
}

// A dry run must not touch systemd at all.
func TestADryRunDoesNotStopAnything(t *testing.T) {
	m := testManager()
	m.DryRun = true
	fake := systest.NewFakeRunner()
	m.Runner = fake

	if err := m.RemoveSiteUnit(t.Context(), pythonSite(), aJob()); err != nil {
		t.Fatalf("RemoveSiteUnit = %v", err)
	}
	if err := m.EnableSiteUnit(t.Context(), pythonSite(), aJob()); err != nil {
		t.Fatalf("EnableSiteUnit = %v", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("a dry run ran %d command(s) against systemd: %v", len(calls), fake.Keys())
	}
}

// A job created --disabled must not be armed.
func TestADisabledJobIsNotArmed(t *testing.T) {
	m := testManager()
	fake := systest.NewFakeRunner()
	m.Runner = fake

	u := aJob()
	u.Enabled = false
	if err := m.EnableSiteUnit(t.Context(), pythonSite(), u); err != nil {
		t.Fatalf("EnableSiteUnit = %v", err)
	}
	keys := strings.Join(fake.Keys(), "\n")
	if !strings.Contains(keys, "disable") {
		t.Errorf("a --disabled job was not disabled:\n%s", keys)
	}

	// And an enabled one arms the timer rather than the service: enabling the service
	// would run the job now and at every boot, which is not what a schedule means.
	fake.Reset()
	if err := m.EnableSiteUnit(t.Context(), pythonSite(), aJob()); err != nil {
		t.Fatalf("EnableSiteUnit = %v", err)
	}
	keys = strings.Join(fake.Keys(), "\n")
	if !strings.Contains(keys, ".timer") {
		t.Errorf("the timer was not what got enabled:\n%s", keys)
	}
	if strings.Contains(keys, "job-nightly.service") {
		t.Errorf("the service was enabled, so the job runs at every boot:\n%s", keys)
	}
}

// Removing a unit has to make systemd forget that it failed.
//
// systemd keeps the failed state after the unit file is gone: the entry becomes "not-found
// failed" and stays in `systemctl --failed`, which is what an operator and a monitoring
// check look at. A job that failed once and was then deleted would alarm about itself for
// ever, for a unit no file mentions. Found by comparing a real server against a snapshot
// after removing a deliberately-failing job — nothing on disk had changed, and this had.
func TestRemovingAUnitMakesSystemdForgetItFailed(t *testing.T) {
	m := testManager()
	fake := systest.NewFakeRunner()
	m.Runner = fake

	if err := m.RemoveSiteUnit(t.Context(), pythonSite(), aJob()); err != nil {
		t.Fatalf("RemoveSiteUnit = %v", err)
	}
	keys := strings.Join(fake.Keys(), "\n")
	if !strings.Contains(keys, "reset-failed") {
		t.Errorf("nothing reset the failed state, so `systemctl --failed` keeps reporting a "+
			"unit that no longer exists:\n%s", keys)
	}
	// Both halves of a job, or the timer keeps its own failed entry.
	for _, want := range []string{"job-nightly.service", "job-nightly.timer"} {
		found := false
		for _, k := range fake.Keys() {
			if strings.Contains(k, "reset-failed") && strings.Contains(k, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("the failed state of %s was not reset:\n%s", want, keys)
		}
	}
	// And after the reload, not before: resetting a unit systemd still has loaded from a
	// file that is already gone is the wrong order.
	reload := strings.Index(keys, "daemon-reload")
	reset := strings.Index(keys, "reset-failed")
	if reload < 0 || reset < reload {
		t.Errorf("reset-failed ran before daemon-reload:\n%s", keys)
	}
}

// A job command or timeout containing a newline would add a directive to a root-installed
// systemd unit. `systemd-analyze verify` accepts a second ExecStart, so the render is where
// this has to be caught — and it has to be caught here rather than only at the CLI, because
// `import` and `clone` reach RenderSiteUnit without passing through the CLI's checks.
func TestAJobCannotInjectSystemdDirectives(t *testing.T) {
	m := testManager()
	bad := aJob()
	bad.Command = "/srv/bin/x\nExecStartPre=/bin/rm -rf /"
	if _, _, err := m.RenderSiteUnit(pythonSite(), bad); err == nil {
		t.Error("a command with a newline rendered; it injects a systemd directive as root")
	}

	badTimeout := aJob()
	badTimeout.Timeout = "30m\nExecStartPost=/bin/sh -c evil"
	if _, _, err := m.RenderSiteUnit(pythonSite(), badTimeout); err == nil {
		t.Error("a timeout with a newline rendered; it injects a systemd directive")
	}

	// A timeout that is not a duration at all is refused too — a garbage value would make
	// the unit fail to start, which is a worse way to find out.
	notDuration := aJob()
	notDuration.Timeout = "banana"
	if _, _, err := m.RenderSiteUnit(pythonSite(), notDuration); err == nil {
		t.Error("a non-duration timeout was accepted")
	}

	// And a legitimate job with a real timeout still renders.
	good := aJob()
	good.Timeout = "30m"
	if _, _, err := m.RenderSiteUnit(pythonSite(), good); err != nil {
		t.Errorf("a legitimate job was rejected: %v", err)
	}
}

// A job's own MemoryMax reaches a Limits line in the unit. validate.Size accepts a trailing
// newline, so a control character has to be refused at the render boundary — the same guard
// the command and timeout already carry, made uniform across every render-bound unit field.
func TestAJobMemoryMaxRejectsControlChars(t *testing.T) {
	m := testManager()

	bad := aJob()
	// A value validate.Size accepts (trailing newline) but that must not reach a unit.
	bad.MemoryMax = "512M\n"
	if _, _, err := m.RenderSiteUnit(pythonSite(), bad); err == nil {
		t.Error("a memory-max carrying a newline rendered")
	}

	good := aJob()
	good.MemoryMax = "512M"
	if _, _, err := m.RenderSiteUnit(pythonSite(), good); err != nil {
		t.Errorf("a legitimate memory-max was rejected: %v", err)
	}
}
