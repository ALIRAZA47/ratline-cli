package site

import (
	"context"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/runtime"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"path/filepath"
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

func bunOptions() AddOptions {
	return AddOptions{
		Domain: "edge.example.com", Owner: "alice", Runtime: "bun",
		Entry: "server.ts", Listen: "socket",
	}
}

// A bun site has no supervisor to choose and no cluster to fan out into. Both flags
// have to be refused rather than accepted and dropped: an operator who passed
// --instances 4 and saw the site created would believe four workers were serving.
func TestBunRefusesTheNodeOnlySupervisionFlags(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*AddOptions)
		wantHas string
	}{
		{"a supervisor", func(o *AddOptions) { o.ProcessManager = "pm2" }, "--daemon"},
		{"cluster workers", func(o *AddOptions) { o.Instances = 4 }, "single process"},
		{"a node version", func(o *AddOptions) { o.NodeVersion = "22" }, "--node"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := bunOptions()
			tc.mutate(&opts)
			_, err := testManager().buildSite(context.Background(), &opts)
			if err == nil {
				t.Fatal("the flag should have been refused on a bun site")
			}
			combined := err.Error() + " " + rlerr.Hint(err)
			if !strings.Contains(combined, tc.wantHas) {
				t.Errorf("the refusal should mention %q, got: %s", tc.wantHas, combined)
			}
		})
	}
}

// Bun's whole selling point is running TypeScript and JSX unbuilt, so buildSite has to
// accept an entry point the node branch would refuse — and still refuse a bad one.
func TestBunEntryPointsAreJudgedAgainstBun(t *testing.T) {
	for _, entry := range []string{"server.ts", "src/index.tsx", "app.jsx", "dist/server.js"} {
		opts := bunOptions()
		opts.Entry = entry
		if _, err := testManager().buildSite(context.Background(), &opts); err != nil {
			t.Errorf("entry %q was refused on a bun site: %v", entry, err)
		}
	}
	for _, entry := range []string{"server.py", "../server.ts", "server.ts;reboot"} {
		opts := bunOptions()
		opts.Entry = entry
		if _, err := testManager().buildSite(context.Background(), &opts); err == nil {
			t.Errorf("entry %q should have been refused", entry)
		}
	}
	// And the wider set stays on bun's side of the fence: a node site given a .tsx
	// entry point would write a unit that dies on first start.
	opts := nodeOptions()
	opts.Entry = "src/index.tsx"
	if _, err := testManager().buildSite(context.Background(), &opts); err == nil {
		t.Error("a .tsx entry point should be refused on a node site")
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

func TestDryRunAddWritesNoStateRow(t *testing.T) {
	// `site add --dry-run` used to write a real row and reserve a real port, so the
	// next *real* `site add` refused with "already exists with a different
	// configuration" for a site that had never been created — and every preview
	// leaked a port.
	//
	// Exercised through the manager rather than the CLI, because the CLI refuses
	// before it reaches these writes on a host without root, which would make the
	// assertion vacuous on a developer machine.
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	if err := st.PutUser(ctx, &state.User{Name: "alice", Home: "/home/alice", Shell: "/bin/sh"}); err != nil {
		t.Fatal(err)
	}

	mgr := testManager()
	mgr.State = st
	mgr.DryRun = true

	for _, opts := range []AddOptions{
		{Domain: "static.example.com", Owner: "alice", Runtime: "static"},
		{Domain: "node.example.com", Owner: "alice", Runtime: "node",
			Entry: "server.js", Listen: "port"},
	} {
		site, err := mgr.buildSite(ctx, &opts)
		if err != nil {
			t.Fatalf("buildSite(%s) = %v", opts.Domain, err)
		}
		// buildSite itself must not write; the guards are in Add, so this asserts the
		// state before Add is reached as well as the shape of the row it would write.
		if _, err := st.GetSite(ctx, site.Domain); err == nil {
			t.Errorf("buildSite recorded %s in state", site.Domain)
		}
	}

	// Nothing at all in either table.
	sites, err := st.ListSites(ctx, state.SiteFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 0 {
		t.Errorf("%d site row(s) written under --dry-run", len(sites))
	}
	ports, err := st.ListPorts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 0 {
		t.Errorf("%d port(s) reserved under --dry-run", len(ports))
	}
}

func TestDryRunLifecycleOperationsWriteNothing(t *testing.T) {
	// site add was not the only preview that wrote. scale, alias and delete each had
	// their own unguarded PutSite/DeleteSite, so `--dry-run` on any of them changed
	// the database it was supposed to be previewing against.
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	if err := st.PutUser(ctx, &state.User{Name: "alice", Home: "/home/alice", Shell: "/bin/sh"}); err != nil {
		t.Fatal(err)
	}
	original := &state.Site{
		Domain: "app.example.com", Owner: "alice", Runtime: "static", Slug: "alice-app_example_com",
		Enabled: true, DocRoot: "public", IndexFile: "index.html", Workers: 2, Instances: 1,
	}
	if err := st.PutSite(ctx, original); err != nil {
		t.Fatal(err)
	}

	mgr := testManager()
	mgr.State = st
	mgr.DryRun = true

	// A copy, so a mutation of the in-memory struct is not mistaken for a write.
	changed := *original
	changed.Workers = 8
	changed.Aliases = []string{"www.app.example.com"}
	if err := mgr.putSite(ctx, &changed, "record the new limits"); err != nil {
		t.Fatalf("putSite under --dry-run = %v", err)
	}

	back, err := st.GetSite(ctx, original.Domain)
	if err != nil {
		t.Fatal(err)
	}
	if back.Workers != original.Workers {
		t.Errorf("workers = %d, want the original %d — the preview wrote", back.Workers, original.Workers)
	}
	if len(back.Aliases) != 0 {
		t.Errorf("aliases = %v, want none — the preview wrote", back.Aliases)
	}

	// And the real path still writes, or the guard would have broken the command.
	mgr.DryRun = false
	if err := mgr.putSite(ctx, &changed, "record the new limits"); err != nil {
		t.Fatal(err)
	}
	if back, _ = st.GetSite(ctx, original.Domain); back.Workers != 8 {
		t.Errorf("workers = %d after a real write, want 8", back.Workers)
	}
}
