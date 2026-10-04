package unit

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
)

func pm2Site(domain string, enabled bool, mem, cpu string) *state.Site {
	return &state.Site{
		Domain: domain, Owner: "alice", Runtime: "node",
		Slug: "alice-" + strings.ReplaceAll(domain, ".", "_"), Enabled: enabled,
		Entry: "server.js", Listen: "socket", Instances: 1, MemoryMax: mem, CPUQuota: cpu,
	}
}

func testDaemon(sites ...*state.Site) *PM2Daemon {
	return &PM2Daemon{
		Owner: "alice", Key: "node22",
		Home:    "/home/alice/.ratline/pm2/node22",
		NodeBin: "/opt/ratline/runtimes/node/22/bin/node",
		PM2:     "/opt/ratline/runtimes/node/22/bin/pm2",
		Sites:   sites,
	}
}

// directives drops comment lines, which is what systemd reads: the template's comments
// name directives to explain them, and an assertion that matched a comment would pass
// against a unit that does not carry the directive at all.
func directives(body string) string {
	var keep []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n")
}

func TestPM2DaemonUnitIsTheTenantsAndSumsItsRunningSites(t *testing.T) {
	relaxedSite := pm2Site("b.example.com", true, "512M", "100%")
	relaxedSite.Relaxed = []string{"PrivateTmp"}
	body, err := testManager().RenderPM2Daemon(testDaemon(
		pm2Site("a.example.com", true, "256M", "50%"),
		relaxedSite,
		pm2Site("c.example.com", false, "4G", "400%"),
	))
	if err != nil {
		t.Fatalf("RenderPM2Daemon = %v", err)
	}
	out := directives(string(body))

	for _, want := range []string{
		"User=alice",
		"Group=alice",
		"Type=forking",
		"PIDFile=/home/alice/.ratline/pm2/node22/pm2.pid",
		"Environment=PM2_HOME=/home/alice/.ratline/pm2/node22",
		"ExecStartPre=-/opt/ratline/runtimes/node/22/bin/pm2 kill",
		"ExecStart=/opt/ratline/runtimes/node/22/bin/pm2 ping",
		"ExecStop=/opt/ratline/runtimes/node/22/bin/pm2 kill",
		// The enabled sites, so a restarted daemon brings them back.
		"Wants=ratline-alice-a_example_com.service",
		"Wants=ratline-alice-b_example_com.service",
		// 256M + 512M; the disabled site's 4G is not running and does not count.
		"MemoryMax=768M",
		"CPUQuota=150%",
		"PartOf=ratline.target",
		"BindPaths=/home/alice",
		"ProtectHome=tmpfs",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the daemon unit is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "c_example_com") {
		t.Errorf("a disabled site must not be started by the daemon:\n%s", out)
	}
	// One site's relaxation is the daemon's: they are one process tree.
	if strings.Contains(out, "PrivateTmp=true") {
		t.Errorf("PrivateTmp was relaxed by one of the daemon's sites but is still applied:\n%s", out)
	}
	// Node's JIT exemption holds for PM2 itself whatever it supervises.
	if strings.Contains(out, "MemoryDenyWriteExecute=true") {
		t.Errorf("PM2 runs on node, which needs writable-executable memory:\n%s", out)
	}
	// Never root, never another tenant.
	if strings.Contains(out, "User=root") || strings.Contains(out, "/home/bob") {
		t.Errorf("the daemon must be the tenant's alone:\n%s", out)
	}
}

// A space in a path would split ExecStart into a different program and arguments in a
// root-owned unit file.
func TestPM2DaemonRefusesAFieldThatWouldSplitTheCommand(t *testing.T) {
	d := testDaemon(pm2Site("a.example.com", true, "", ""))
	d.PM2 = "/opt/ratline/runtimes/node/22/bin/pm2 --evil"
	if _, err := testManager().RenderPM2Daemon(d); err == nil {
		t.Fatal("RenderPM2Daemon accepted a PM2 path containing a space")
	}
	d = testDaemon(pm2Site("a.example.com", true, "", ""))
	d.Home = "/home/alice/x\nUser=root"
	if _, err := testManager().RenderPM2Daemon(d); err == nil {
		t.Fatal("RenderPM2Daemon accepted a newline")
	}
}

func TestASiteUnitBoundToTheDaemonIsAOneshotWithoutItsOwnCeiling(t *testing.T) {
	site := pm2Site("app.example.com", true, "512M", "")
	out := directives(render(t, site, "/opt/ratline/runtimes/node/22/bin/pm2 start /x/ecosystem.config.json --update-env",
		RenderOptions{
			Type: "oneshot", RemainAfterExit: true,
			BindsTo:        "ratline-pm2@alice.node22.service",
			ExecStop:       "-/opt/ratline/runtimes/node/22/bin/pm2 delete alice-app_example_com",
			ExtraBindPaths: []string{"/home/alice/.ratline/pm2/node22"},
		}))
	for _, want := range []string{
		"BindsTo=ratline-pm2@alice.node22.service",
		"After=ratline-pm2@alice.node22.service",
		"Type=oneshot",
		"RemainAfterExit=yes",
		"BindPaths=/home/alice/.ratline/pm2/node22",
		"ExecStop=-/opt/ratline/runtimes/node/22/bin/pm2 delete alice-app_example_com",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the site unit is missing %q:\n%s", want, out)
		}
	}
	// systemd refuses Restart=always on a oneshot and will not load the unit at all.
	if strings.Contains(out, "Restart=always") {
		t.Errorf("a oneshot must not carry Restart=always:\n%s", out)
	}
	// Its processes are in the daemon's cgroup; a ceiling here would hold nothing.
	if strings.Contains(out, "MemoryMax=") {
		t.Errorf("the ceiling belongs to the daemon, not the oneshot:\n%s", out)
	}

	// The negative: a site not bound to a daemon keeps its own ceiling and restarts.
	plain := directives(render(t, pythonSite(), "/venv/bin/gunicorn app:app", RenderOptions{}))
	if !strings.Contains(plain, "Restart=always") || !strings.Contains(plain, "MemoryMax=") {
		t.Errorf("an ordinary site lost its restart policy or ceiling:\n%s", plain)
	}
}

func installTestDaemon(t *testing.T, d *PM2Daemon, active bool) (*Manager, *systest.FakeRunner) {
	t.Helper()
	cfg := config.Default()
	cfg.Paths.SystemdDir = t.TempDir()
	runner := systest.NewFakeRunner()
	if !active {
		runner.ExpectFailure("systemctl is-active --quiet "+d.Unit(), 3, "")
	}
	m := &Manager{Cfg: cfg, Log: log.Discard(), Runner: runner}
	if err := m.InstallPM2Daemon(context.Background(), d, system.NewRollback(log.Discard())); err != nil {
		t.Fatalf("InstallPM2Daemon = %v", err)
	}
	return m, runner
}

func ranVerb(r *systest.FakeRunner, verb, unitName string) bool {
	for _, k := range r.Keys() {
		if strings.HasPrefix(k, "systemctl "+verb+" ") && strings.Contains(k, " "+unitName) {
			return true
		}
	}
	return false
}

// Adding a site to a tenant must not bounce the tenant's other sites. Only a change
// that cannot reach a running daemon — the sandbox, node, PM2 — restarts it.
func TestAddingASiteDoesNotRestartTheTenantsDaemon(t *testing.T) {
	one := testDaemon(pm2Site("a.example.com", true, "256M", ""))
	m, _ := installTestDaemon(t, one, true)

	runner := systest.NewFakeRunner()
	m.Runner = runner
	two := testDaemon(pm2Site("a.example.com", true, "256M", ""), pm2Site("b.example.com", true, "256M", ""))
	if err := m.InstallPM2Daemon(context.Background(), two, system.NewRollback(log.Discard())); err != nil {
		t.Fatalf("InstallPM2Daemon = %v", err)
	}
	if ranVerb(runner, "restart", two.Unit()) {
		t.Errorf("adding a site restarted the daemon and every site in it: %v", runner.Keys())
	}
	if !ranVerb(runner, "set-property", two.Unit()) {
		t.Errorf("the new ceiling was not applied to the running daemon: %v", runner.Keys())
	}
	got, _ := os.ReadFile(m.PM2UnitPath("alice", "node22"))
	if !strings.Contains(string(got), "Wants=ratline-alice-b_example_com.service") {
		t.Errorf("the unit on disk does not want the new site:\n%s", got)
	}

	// The positive: relaxing the sandbox does restart it, because nothing else would
	// apply the change to a running process.
	runner = systest.NewFakeRunner()
	m.Runner = runner
	relaxed := pm2Site("b.example.com", true, "256M", "")
	relaxed.Relaxed = []string{"PrivateTmp"}
	three := testDaemon(pm2Site("a.example.com", true, "256M", ""), relaxed)
	if err := m.InstallPM2Daemon(context.Background(), three, system.NewRollback(log.Discard())); err != nil {
		t.Fatalf("InstallPM2Daemon = %v", err)
	}
	if !ranVerb(runner, "restart", three.Unit()) {
		t.Errorf("a sandbox change did not restart the daemon: %v", runner.Keys())
	}

	// And an identical render touches nothing at all.
	runner = systest.NewFakeRunner()
	m.Runner = runner
	if err := m.InstallPM2Daemon(context.Background(), three, system.NewRollback(log.Discard())); err != nil {
		t.Fatalf("InstallPM2Daemon = %v", err)
	}
	if len(runner.Keys()) != 0 {
		t.Errorf("an unchanged daemon ran %v", runner.Keys())
	}
}

func TestADaemonThatIsNotRunningIsNotStarted(t *testing.T) {
	d := testDaemon(pm2Site("a.example.com", true, "", ""))
	_, runner := installTestDaemon(t, d, false)
	for _, verb := range []string{"start", "restart", "set-property"} {
		if ranVerb(runner, verb, d.Unit()) {
			t.Errorf("installing the unit ran systemctl %s on a daemon nobody started: %v", verb, runner.Keys())
		}
	}
}

func TestPM2DaemonUnitNameCannotBeASiteUnit(t *testing.T) {
	if !IsPM2DaemonUnit("ratline-pm2@alice.node22.service") {
		t.Error("the daemon's own name was not recognised")
	}
	if IsPM2DaemonUnit("ratline-pm2-alice-node22.service") {
		t.Error("a site unit was taken for a PM2 daemon")
	}
}
