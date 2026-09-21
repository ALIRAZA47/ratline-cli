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
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
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

// PM2 may supervise a bun site, and what that does and does not buy is enforced here
// rather than left to be discovered.
//
// The shape of the rule: PM2's cluster mode is node's own cluster module, so bun runs
// in fork mode and every instance is an independent process. Independent processes
// cannot share a Unix socket — the first binds it and the rest crash-loop behind a
// site that answers perfectly — so more than one instance needs PM2 *and* a port.
// Each of those is refused for its own reason and names it, because an operator who
// passed --instances 4 and saw the site created would believe four processes were
// serving.
func TestBunInstancesNeedPM2AndAPort(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*AddOptions)
		wantErr bool
		wantHas string
	}{
		{"a supervisor is now a choice bun has",
			func(o *AddOptions) { o.ProcessManager = "pm2" }, false, ""},
		{"and so is running without one",
			func(o *AddOptions) { o.ProcessManager = "direct" }, false, ""},
		{"but not a supervisor that does not exist",
			func(o *AddOptions) { o.ProcessManager = "runit" }, true, "--daemon"},

		{"instances without PM2 have nothing to fan out",
			func(o *AddOptions) { o.Instances = 4 }, true, "single process"},
		{"instances on a socket would fight over it",
			func(o *AddOptions) { o.ProcessManager = "pm2"; o.Instances = 4 }, true, "Unix socket"},
		{"instances on a port are what PM2 buys here",
			func(o *AddOptions) { o.ProcessManager = "pm2"; o.Instances = 4; o.Listen = "port" }, false, ""},

		{"a node version is still node's", func(o *AddOptions) { o.NodeVersion = "22" }, true, "--node"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := bunOptions()
			tc.mutate(&opts)
			_, err := testManager().buildSite(context.Background(), &opts)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("this should be accepted on a bun site, got: %v", err)
				}
				return
			}
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

// A bun site that says nothing is supervised directly, whatever the server's node
// default is.
//
// runtimes.node_process_manager answers a question about node sites. A server that
// set it to pm2 did not thereby ask every bun site on the box to grow a dependency on
// a Node install and a second supervisor process — and a bun site silently acquiring
// one at its next reconcile is the kind of drift that is only noticed when the node
// runtime is removed.
func TestABunSiteDoesNotInheritNodesProcessManager(t *testing.T) {
	cfg := config.Default()
	cfg.Runtimes.NodeProcessManager = "pm2"
	site := &state.Site{Domain: "edge.example.com", Owner: "alice", Runtime: "bun", Entry: "server.ts"}
	rc := runtime.NewContext(cfg, log.Discard(), nil, site, nil, true)
	if got := runtime.ProcessManagerFor(rc); got != runtime.ProcessManagerDirect {
		t.Errorf("a bun site defaulted to %q, want %q", got, runtime.ProcessManagerDirect)
	}

	// The negative: a node site on the same server does follow the setting, or the
	// check above would pass on a resolver that always answered "direct".
	site.Runtime = "node"
	site.Entry = "server.js"
	if got := runtime.ProcessManagerFor(rc); got != runtime.ProcessManagerPM2 {
		t.Errorf("a node site resolved to %q, want %q", got, runtime.ProcessManagerPM2)
	}

	// And an explicit choice still wins for bun.
	site.Runtime = "bun"
	site.ProcessManager = runtime.ProcessManagerPM2
	if got := runtime.ProcessManagerFor(rc); got != runtime.ProcessManagerPM2 {
		t.Errorf("an explicit --daemon pm2 resolved to %q, want %q", got, runtime.ProcessManagerPM2)
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

// The third round of the same bug, found by driving the panel's "Dry run" button
// against a real server.
//
// `site enable/disable --dry-run` wrote the enabled flag, and `site delete --dry-run`
// deleted the site's SSH keys — while both correctly left nginx alone. So a rehearsal
// left ratline believing a site was disabled that nginx was still serving, and a
// rehearsed deletion revoked access to a site that still existed.
func TestDryRunEnableAndDisableDoNotWriteTheFlag(t *testing.T) {
	st, mgr, site := lifecycleFixture(t)
	ctx := context.Background()

	mgr.DryRun = true
	// Exercised through setEnabled rather than Disable, for the same reason the test
	// above uses putSite: the surrounding command reaches nginx and systemd, which a
	// developer machine does not have, and a test that cannot run proves nothing.
	if err := mgr.setEnabled(ctx, site, false); err != nil {
		t.Fatal(err)
	}
	back, err := st.GetSite(ctx, site.Domain)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Enabled {
		t.Error("a dry-run disable marked the site disabled in state, while nginx kept serving it")
	}

	if err := st.SetSiteEnabled(ctx, site.Domain, false); err != nil {
		t.Fatal(err)
	}
	if err := mgr.setEnabled(ctx, site, true); err != nil {
		t.Fatal(err)
	}
	if back, err = st.GetSite(ctx, site.Domain); err != nil {
		t.Fatal(err)
	}
	if back.Enabled {
		t.Error("a dry-run enable marked the site enabled in state")
	}

	// And the real path still writes, or the guard has broken the command.
	mgr.DryRun = false
	if err := mgr.setEnabled(ctx, site, true); err != nil {
		t.Fatal(err)
	}
	if back, err = st.GetSite(ctx, site.Domain); err != nil {
		t.Fatal(err)
	}
	if !back.Enabled {
		t.Error("a real enable did not write the flag")
	}
}

func TestDryRunDeleteDoesNotRevokeSiteKeys(t *testing.T) {
	st, mgr, site := lifecycleFixture(t)
	ctx := context.Background()

	key := &state.Key{
		ID: "k1", Label: "ci", Fingerprint: "SHA256:probe", Algorithm: "ssh-ed25519",
		Blob: "AAAA", Scope: state.ScopeSite, Site: site.Domain, Source: "manual",
	}
	if err := st.PutKey(ctx, key); err != nil {
		t.Fatal(err)
	}

	mgr.DryRun = true
	if err := mgr.removeSiteKeys(ctx, site); err != nil {
		t.Fatalf("removeSiteKeys under --dry-run = %v", err)
	}
	keys, err := st.ListKeys(ctx, state.KeyFilter{Scope: state.ScopeSite, Site: site.Domain})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("%d site-scoped keys after a rehearsed deletion, want 1 — the preview "+
			"revoked access to a site it did not delete", len(keys))
	}

	// The negative case: a real delete does revoke them, or the test above would
	// pass for a function that had stopped working entirely.
	mgr.DryRun = false
	if err := mgr.removeSiteKeys(ctx, site); err != nil {
		t.Fatal(err)
	}
	if keys, err = st.ListKeys(ctx, state.KeyFilter{Scope: state.ScopeSite, Site: site.Domain}); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Errorf("%d site-scoped keys survived a real deletion", len(keys))
	}
}

// lifecycleFixture is a store with one tenant and one enabled static site, plus a
// manager pointed at it.
func lifecycleFixture(t *testing.T) (*state.Store, *Manager, *state.Site) {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	if err := st.PutUser(ctx, &state.User{Name: "alice", Home: "/home/alice", Shell: "/bin/sh"}); err != nil {
		t.Fatal(err)
	}
	site := &state.Site{
		Domain: "app.example.com", Owner: "alice", Runtime: "static",
		Slug: "alice-app_example_com", Enabled: true, DocRoot: "public",
		IndexFile: "index.html", Instances: 1,
	}
	if err := st.PutSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	mgr := testManager()
	mgr.State = st
	return st, mgr, site
}

// Where a site's application log lives is a property of how the site is configured, and
// `site logs --app` has to decide from that rather than from whether PM2 answers.
//
// It used to decide from ProcessReport, which runs `pm2 jlist` as the tenant: a site
// whose PM2 could not be reached — a deploy that took node_modules with it, a daemon
// that died — was read as "not PM2 supervised" and the reader was sent to the journal,
// which on a PM2 site carries PM2's own messages and never the application's. The screen
// came up empty at the one moment somebody was certain to be looking at it.
//
// The Manager here has no Runner at all, which is the negative case made structural:
// anything that tried to ask a daemon would have to dereference a nil one.
func TestUsesPM2AsksTheConfigurationRatherThanTheDaemon(t *testing.T) {
	for _, tc := range []struct {
		name  string
		site  *state.Site
		cfgPM string
		want  bool
	}{
		{"node, nothing chosen, so the default", &state.Site{Domain: "a.example.com", Owner: "alice", Runtime: "node"}, "", true},
		{"node, pm2 on the site", &state.Site{Domain: "a.example.com", Owner: "alice", Runtime: "node", ProcessManager: "pm2"}, "", true},
		{"node, direct on the site", &state.Site{Domain: "a.example.com", Owner: "alice", Runtime: "node", ProcessManager: "direct"}, "", false},
		{"node, direct by configuration", &state.Site{Domain: "a.example.com", Owner: "alice", Runtime: "node"}, "direct", false},
		{"node, the site overrides the configuration", &state.Site{Domain: "a.example.com", Owner: "alice", Runtime: "node", ProcessManager: "pm2"}, "direct", true},
		{"python has no PM2", &state.Site{Domain: "a.example.com", Owner: "alice", Runtime: "python"}, "", false},
		// bun's *default* is direct supervision, which is why this reads false — not
		// because bun cannot be supervised by PM2. The case below is the difference,
		// and conflating the two sent `site logs --app` to the journal for a bun site
		// whose output PM2 was writing to logs/app.log.
		{"bun, nothing chosen, so direct", &state.Site{Domain: "a.example.com", Owner: "alice", Runtime: "bun"}, "", false},
		{"bun, pm2 on the site", &state.Site{Domain: "a.example.com", Owner: "alice", Runtime: "bun", ProcessManager: "pm2"}, "", true},
		// The node default must not leak into bun: bun stays direct unless asked.
		{"bun, direct on the site", &state.Site{Domain: "a.example.com", Owner: "alice", Runtime: "bun", ProcessManager: "direct"}, "", false},
		{"static has no PM2", &state.Site{Domain: "a.example.com", Owner: "alice", Runtime: "static"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testManager()
			m.Cfg.Runtimes.NodeProcessManager = tc.cfgPM
			if got := m.UsesPM2(tc.site); got != tc.want {
				t.Errorf("UsesPM2 = %v, want %v", got, tc.want)
			}
		})
	}
}

// The two proxy settings are refused where they would do nothing, and capped where
// they would do harm.
func TestProxyOptionsAreRefusedWhereTheyWouldBeIgnored(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		buffering, readTimout string
		runtime               string
		wantErr               bool
	}{
		// nginx serves a static site from disk; the generated vhost has no
		// proxy_pass in it, so neither directive has anything to attach to.
		{"buffering on a static site", "off", "", "static", true},
		{"a read timeout on a static site", "", "1h", "static", true},
		{"nothing at all on a static site", "", "", "static", false},

		{"off on a bun site", "off", "", "bun", false},
		{"on spelled out", "on", "", "node", false},
		{"a value that is neither", "maybe", "", "bun", true},
		{"a value carrying a directive", "off; add_header X 1", "", "bun", true},

		{"an hour", "", "1h", "python", false},
		{"a day, the ceiling exactly", "", "24h", "bun", false},
		{"more than a day", "", "25h", "bun", true},
		{"a week", "", "7d", "bun", true},
		{"not a duration", "", "forever", "bun", true},
		{"zero", "", "0s", "bun", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProxyOptions(tc.buffering, tc.readTimout, tc.runtime)
			if tc.wantErr && err == nil {
				t.Errorf("validateProxyOptions(%q, %q, %q) = nil, want an error",
					tc.buffering, tc.readTimout, tc.runtime)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateProxyOptions(%q, %q, %q) = %v, want nil",
					tc.buffering, tc.readTimout, tc.runtime, err)
			}
		})
	}
}

// A bun site supervised by PM2 must have PM2's counters read, exactly as a node one does.
//
// The bug this pins: ProcessReport gated on `site.Runtime == "node"`, so a bun site
// created with `--daemon pm2 --listen port` — a supported topology, documented on
// `site scale --instances` — answered (nil, nil), meaning "there is no supervisor to
// ask". Every caller then fell back to systemd's NRestarts, which PM2 holds at zero
// because PM2 is the one doing the restarting. A bun application crash-looping under
// PM2 therefore read as healthy in `doctor`, `status`, `site status` and
// `troubleshoot` at once, and the skipped check printed "bun runs directly under
// systemd" about a site that was not running directly under systemd.
//
// jlist reports one worker that has restarted 12 times, which is the crash loop the
// whole mechanism exists to surface.
func TestProcessReportReadsPM2CountersForABunSiteUnderPM2(t *testing.T) {
	const jlist = `[
  {"name":"alice-mcp_example_com","pm2_env":{"status":"online","restart_time":12,"exec_mode":"fork_mode"},
   "monit":{"memory":40,"cpu":1}}
]`
	bun := func(pm string) *state.Site {
		return &state.Site{
			Domain: "mcp.example.com", Owner: "alice", Runtime: "bun",
			Slug: "alice-mcp_example_com", Entry: "server.ts",
			Listen: "port", Port: 20000, Instances: 1, ProcessManager: pm,
		}
	}

	t.Run("pm2 is asked and its restart counter comes back", func(t *testing.T) {
		m := testManager()
		fake := systest.NewFakeRunner()
		fake.Default = systest.Response{Stdout: jlist}
		m.Runner = fake

		report, err := m.ProcessReport(context.Background(), bun("pm2"))
		if err != nil {
			t.Fatalf("ProcessReport = %v", err)
		}
		if report == nil {
			t.Fatal("report is nil: a bun site under PM2 has a supervisor to ask, " +
				"and nil sends every caller back to systemd's NRestarts, which reads zero")
		}
		if report.Restarts != 12 {
			t.Errorf("restarts = %d, want 12 — the counter systemd cannot supply", report.Restarts)
		}
		if report.Online != 1 {
			t.Errorf("online = %d, want 1", report.Online)
		}
	})

	// The negative case, so the fix is a change of *condition* and not a change of
	// answer: bun's default really is direct supervision, and asking PM2 about a site
	// that has no PM2 would be a different wrong answer.
	t.Run("a bun site left on its default is still not asked", func(t *testing.T) {
		m := testManager()
		fake := systest.NewFakeRunner()
		fake.Default = systest.Response{Stdout: jlist}
		m.Runner = fake

		report, err := m.ProcessReport(context.Background(), bun(""))
		if err != nil {
			t.Fatalf("ProcessReport = %v", err)
		}
		if report != nil {
			t.Errorf("report = %+v, want nil: bun defaults to direct supervision", report)
		}
		if n := len(fake.Calls()); n != 0 {
			t.Errorf("ran %d command(s), want none — nothing should be asked of a "+
				"daemon that does not supervise this site", n)
		}
	})
}
