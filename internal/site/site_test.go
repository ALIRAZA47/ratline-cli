package site

import (
	"context"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/runtime"
)

// buildSite is the part of `site add` that turns flags into a row, before
// anything touches the filesystem — so it is where flag combinations can be
// checked without a server.
func testManager() *Manager {
	return &Manager{Cfg: config.Default(), Log: log.Discard(), DryRun: true}
}

func nodeOptions() AddOptions {
	return AddOptions{
		Domain: "app.example.com", Owner: "alice", Runtime: "node",
		Entry: "server.js", Listen: "socket",
	}
}

func TestDaemonFlagAcceptsBothSupervisorsAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		value  string
		wantOK bool
	}{
		{"", true},
		{runtime.ProcessManagerPM2, true},
		{runtime.ProcessManagerDirect, true},
		{"systemd", false},
		{"PM2", false}, // no case folding: the stored value is compared literally
		{"supervisord", false},
	} {
		opts := nodeOptions()
		opts.ProcessManager = tc.value
		site, err := testManager().buildSite(context.Background(), &opts)
		switch {
		case tc.wantOK && err != nil:
			t.Errorf("--daemon %q was refused: %v", tc.value, err)
		case !tc.wantOK && err == nil:
			t.Errorf("--daemon %q was accepted, which would be silently ignored later", tc.value)
		case !tc.wantOK:
			if rlerr.CodeOf(err) != rlerr.CodeUsage {
				t.Errorf("--daemon %q should be a usage error, got code %v", tc.value, rlerr.CodeOf(err))
			}
		case tc.wantOK && site.ProcessManager != tc.value:
			// A value that is accepted has to be stored, or the flag does nothing.
			t.Errorf("stored process manager = %q, want %q", site.ProcessManager, tc.value)
		}
	}
}

func TestDaemonRefusalNamesTheTwoChoices(t *testing.T) {
	opts := nodeOptions()
	opts.ProcessManager = "supervisord"
	_, err := testManager().buildSite(context.Background(), &opts)
	if err == nil {
		t.Fatal("an unknown process manager must be refused")
	}
	if !strings.Contains(err.Error(), "pm2 or direct") {
		t.Errorf("the refusal should name what is allowed, got: %v", err)
	}
}

func TestAStaticSiteIgnoresTheDaemonFlagRatherThanFailing(t *testing.T) {
	// --daemon is meaningless for a static site, but a stray value in a script
	// should not be what stops a deploy: the runtime branch never reads it.
	opts := AddOptions{Domain: "s.example.com", Owner: "alice", Runtime: "static", ProcessManager: "pm2"}
	if _, err := testManager().buildSite(context.Background(), &opts); err != nil {
		t.Errorf("buildSite for a static site = %v", err)
	}
}

func TestInstancesAboveOneIsRefusedWhereNothingCanFanOut(t *testing.T) {
	// --instances means PM2 cluster workers. Accepting it where nothing can act on
	// it is how an operator comes to believe a site runs four workers when it runs
	// one, so every case that cannot honour it is refused and names the flag that
	// does work.
	for _, tc := range []struct {
		name    string
		mutate  func(*AddOptions)
		wantOK  bool
		wantHas string
	}{
		{"pm2 node fans out", func(o *AddOptions) { o.ProcessManager = "pm2" }, true, ""},
		{"the default is pm2", func(*AddOptions) {}, true, ""},
		{
			"direct node is one process",
			func(o *AddOptions) { o.ProcessManager = "direct" },
			false, "--daemon pm2",
		},
		{
			"python scales with workers",
			func(o *AddOptions) {
				o.Runtime, o.Entry, o.AppModule = "python", "", "app.main:app"
			},
			false, "--workers",
		},
		{
			"a static site has no process at all",
			func(o *AddOptions) { o.Runtime, o.Entry, o.Listen = "static", "", "" },
			false, "node and python",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := nodeOptions()
			opts.Instances = 4
			tc.mutate(&opts)
			site, err := testManager().buildSite(context.Background(), &opts)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("instances=4 was refused: %v", err)
				}
				if site.Instances != 4 {
					t.Errorf("instances = %d, want 4", site.Instances)
				}
				return
			}
			if err == nil {
				t.Fatal("instances=4 should have been refused")
			}
			combined := err.Error() + " " + rlerr.Hint(err)
			if !strings.Contains(combined, tc.wantHas) {
				t.Errorf("the refusal should mention %q, got: %s", tc.wantHas, combined)
			}
		})
	}
}

func TestInstancesFollowsTheServerDefaultWhenTheSiteDidNotChoose(t *testing.T) {
	// A site with no --daemon follows the configured default. Checking only the
	// site's own field would accept --instances on a server configured for direct
	// supervision — the exact case the refusal exists for.
	mgr := testManager()
	mgr.Cfg.Runtimes.NodeProcessManager = "direct"

	opts := nodeOptions()
	opts.Instances = 4
	if _, err := mgr.buildSite(context.Background(), &opts); err == nil {
		t.Error("a server defaulting to direct supervision cannot honour four instances")
	}

	// And an explicit --daemon pm2 still overrides that default.
	opts.ProcessManager = "pm2"
	if _, err := mgr.buildSite(context.Background(), &opts); err != nil {
		t.Errorf("an explicit --daemon pm2 should win over the default: %v", err)
	}
}

func TestOneInstanceIsAlwaysFine(t *testing.T) {
	// The check must not fire on the default, or a plain python site would refuse
	// to be created at all.
	for _, rt := range []string{"static", "node", "python"} {
		opts := AddOptions{Domain: "a.example.com", Owner: "alice", Runtime: rt, Instances: 1}
		switch rt {
		case "node":
			opts.Entry = "server.js"
		case "python":
			opts.AppModule = "app.main:app"
		}
		if _, err := testManager().buildSite(context.Background(), &opts); err != nil {
			t.Errorf("a %s site with one instance = %v", rt, err)
		}
	}
}
