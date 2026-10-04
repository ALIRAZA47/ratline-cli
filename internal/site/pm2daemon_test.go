package site

import (
	"context"
	"os"
	osuser "os/user"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
)

// syncFixture is a manager whose tenant is the account running the test — the one owner
// identity() can resolve for real without root — with managed Node 18 and 22 and PM2
// laid down as empty files, and a systemd directory of its own.
func syncFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	me, err := osuser.Current()
	if err != nil {
		t.Skip("no current user")
	}
	cfg := config.Default()
	cfg.Paths.SystemdDir = t.TempDir()
	cfg.Paths.RuntimesDir = t.TempDir()
	cfg.Runtimes.NodeDefault = "22"
	for _, v := range []string{"18", "22"} {
		bin := filepath.Join(cfg.Paths.RuntimesDir, "node", v, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"node", "pm2"} {
			if err := os.WriteFile(filepath.Join(bin, name), nil, 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.PutUser(ctx, &state.User{Name: me.Username, Home: cfg.HomeDir(me.Username), Shell: "/bin/sh"}); err != nil {
		t.Fatal(err)
	}
	runner := systest.NewFakeRunner()
	m := &Manager{
		Cfg: cfg, Log: log.Discard(), State: st, Runner: runner,
		Unit: &unit.Manager{Cfg: cfg, Log: log.Discard(), Runner: runner},
	}
	return m, me.Username
}

func putSite(t *testing.T, m *Manager, s *state.Site) {
	t.Helper()
	if err := m.State.PutSite(context.Background(), s); err != nil {
		t.Fatalf("PutSite(%s) = %v", s.Domain, err)
	}
}

func syncSite(owner, domain, runtime, pm, node string, enabled bool) *state.Site {
	return &state.Site{
		Domain: domain, Owner: owner, Runtime: runtime, ProcessManager: pm, NodeVersion: node,
		Slug: owner + "-" + strings.ReplaceAll(domain, ".", "_"), Enabled: enabled,
		Entry: "server.js", AppModule: "app:app", Listen: "socket", Instances: 1,
	}
}

func daemonFiles(t *testing.T, m *Manager) []string {
	t.Helper()
	entries, err := os.ReadDir(m.Cfg.Paths.SystemdDir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if unit.IsPM2DaemonUnit(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func TestATenantsPM2SitesShareOneDaemonPerNodeVersion(t *testing.T) {
	m, owner := syncFixture(t)
	ctx := context.Background()
	putSite(t, m, syncSite(owner, "a.example.com", "node", "", "", true))
	putSite(t, m, syncSite(owner, "b.example.com", "node", "pm2", "22", true))
	putSite(t, m, syncSite(owner, "old.example.com", "node", "", "18", true))
	putSite(t, m, syncSite(owner, "direct.example.com", "node", "direct", "", true))
	putSite(t, m, syncSite(owner, "api.example.com", "python", "", "", true))

	rb := system.NewRollback(log.Discard())
	defer rb.Unwind(ctx)
	if err := m.syncPM2Daemons(ctx, owner, nil, "", rb); err != nil {
		t.Fatalf("syncPM2Daemons = %v", err)
	}
	rb.Commit()

	// Three PM2 sites, two Node versions: two daemons, not three, and nothing for the
	// direct node site or the python one.
	want := []string{
		"ratline-pm2@" + owner + ".node18.service",
		"ratline-pm2@" + owner + ".node22.service",
	}
	if got := daemonFiles(t, m); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("daemon units = %v, want %v", got, want)
	}
	body, _ := os.ReadFile(filepath.Join(m.Cfg.Paths.SystemdDir, want[1]))
	for _, w := range []string{"a_example_com", "b_example_com"} {
		if !strings.Contains(string(body), "Wants=ratline-"+owner+"-"+w+".service") {
			t.Errorf("the Node 22 daemon does not start %s:\n%s", w, body)
		}
	}
	for _, not := range []string{"old_example_com", "direct_example_com", "api_example_com"} {
		if strings.Contains(string(body), not) {
			t.Errorf("the Node 22 daemon names %s, which is not one of its sites:\n%s", not, body)
		}
	}
}

func TestMovingTheLastSiteOffAVersionRemovesThatDaemon(t *testing.T) {
	m, owner := syncFixture(t)
	ctx := context.Background()
	putSite(t, m, syncSite(owner, "a.example.com", "node", "", "22", true))
	old := syncSite(owner, "old.example.com", "node", "", "18", true)
	putSite(t, m, old)
	rb := system.NewRollback(log.Discard())
	defer rb.Unwind(ctx)
	if err := m.syncPM2Daemons(ctx, owner, nil, "", rb); err != nil {
		t.Fatalf("syncPM2Daemons = %v", err)
	}
	if n := len(daemonFiles(t, m)); n != 2 {
		t.Fatalf("%d daemons before the move, want 2", n)
	}

	// `site runtime --node 22`: the site arrives as it is about to be.
	moved := *old
	moved.NodeVersion = "22"
	if err := m.syncPM2Daemons(ctx, owner, &moved, "", rb); err != nil {
		t.Fatalf("syncPM2Daemons = %v", err)
	}
	if got := daemonFiles(t, m); len(got) != 1 || !strings.HasSuffix(got[0], ".node22.service") {
		t.Fatalf("daemon units = %v, want only Node 22's", got)
	}

	// Disabling a site takes it out of the daemon's Wants= without removing the daemon.
	off := moved
	off.Enabled = false
	if err := m.syncPM2Daemons(ctx, owner, &off, "", rb); err != nil {
		t.Fatalf("syncPM2Daemons = %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(m.Cfg.Paths.SystemdDir, daemonFiles(t, m)[0]))
	if strings.Contains(string(body), "old_example_com") {
		t.Errorf("a disabled site is still started by the daemon:\n%s", body)
	}

	// Deleting the last site removes the daemon, and stops it first.
	for _, d := range []string{"a.example.com", "old.example.com"} {
		if err := m.State.DeleteSite(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	runner := systest.NewFakeRunner()
	m.Runner, m.Unit.Runner = runner, runner
	if err := m.syncPM2Daemons(ctx, owner, nil, "old.example.com", nil); err != nil {
		t.Fatalf("syncPM2Daemons = %v", err)
	}
	rb.Commit()
	if got := daemonFiles(t, m); len(got) != 0 {
		t.Errorf("daemon units = %v after the tenant's last PM2 site went, want none", got)
	}
	stopped := false
	for _, k := range runner.Keys() {
		if strings.HasPrefix(k, "systemctl stop ratline-pm2@"+owner+".node22.service") {
			stopped = true
		}
	}
	if !stopped {
		t.Errorf("the daemon was removed without being stopped: %v", runner.Keys())
	}
}

// Under --dry-run nothing is written: the daemon unit is not there afterwards.
func TestSyncingDaemonsUnderDryRunWritesNothing(t *testing.T) {
	m, owner := syncFixture(t)
	m.DryRun, m.Unit.DryRun = true, true
	ctx := context.Background()
	putSite(t, m, syncSite(owner, "a.example.com", "node", "", "22", true))
	rb := system.NewRollback(log.Discard())
	defer rb.UnwindOn(ctx, new(error))
	if err := m.syncPM2Daemons(ctx, owner, nil, "", rb); err != nil {
		t.Fatalf("syncPM2Daemons = %v", err)
	}
	if got := daemonFiles(t, m); len(got) != 0 {
		t.Errorf("--dry-run wrote %v", got)
	}
}
