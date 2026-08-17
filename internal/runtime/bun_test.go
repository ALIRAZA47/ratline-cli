package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

func bunContext(t *testing.T, mutate func(*state.Site)) *Context {
	t.Helper()
	cfg := config.Default()
	cfg.Runtimes.BunDefault = "1.2"
	site := &state.Site{
		Domain: "edge.example.com", Owner: "alice", Runtime: "bun",
		Slug: "alice-edge_example_com", Enabled: true,
		Entry: "server.ts", Listen: "socket", Instances: 1,
	}
	if mutate != nil {
		mutate(site)
	}
	id := &system.Identity{Name: "alice", UID: 1001, GID: 1001, Home: cfg.HomeDir("alice")}
	// DryRun so the managed interpreter resolves by path without having to exist on
	// the machine running the tests.
	return NewContext(cfg, log.Discard(), &stubRunner{}, site, id, true)
}

// installFakeBun lays down a bun binary where a live resolution will find it, so a
// refusal in a test is the one being tested rather than "bun is missing".
func installFakeBun(t *testing.T, c *Context, version string) string {
	t.Helper()
	c.Cfg.Paths.RuntimesDir = t.TempDir()
	bin := filepath.Join(c.Cfg.Paths.RuntimesDir, "bun", version, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, "bun")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// The ExecStart line is the whole contract with systemd: an absolute interpreter, an
// absolute entry point, and nothing between the two that would take the main PID.
func TestBunStartCommandRunsTheEntryPointDirectly(t *testing.T) {
	c := bunContext(t, nil)
	execStart, opts, err := (Bun{}).StartCommand(context.Background(), c)
	if err != nil {
		t.Fatalf("StartCommand = %v", err)
	}
	wantBin := filepath.Join(c.Cfg.Paths.RuntimesDir, "bun", "1.2", "bin", "bun")
	wantEntry := filepath.Join(c.AppDir, "server.ts")
	if execStart != wantBin+" "+wantEntry {
		t.Errorf("ExecStart = %q, want %q", execStart, wantBin+" "+wantEntry)
	}
	// No supervisor, so nothing that would make the unit Type=forking.
	if opts.Type != "" || opts.PIDFile != "" {
		t.Errorf("a bun unit should stay Type=exec, got type=%q pidfile=%q", opts.Type, opts.PIDFile)
	}
	if opts.ExecReload != "" {
		t.Errorf("bun has no graceful reload, so ExecReload must be empty, got %q", opts.ExecReload)
	}
}

// A socket site must not be handed BUN_PORT: bun parses it as a port number, so a
// socket path in it is a startup failure rather than an ignored value.
func TestBunSocketEnvironmentOmitsBunPort(t *testing.T) {
	c := bunContext(t, nil)
	_, opts, err := (Bun{}).StartCommand(context.Background(), c)
	if err != nil {
		t.Fatalf("StartCommand = %v", err)
	}
	socket := c.Cfg.SocketPath("alice", "edge.example.com")
	var sawSocketPath bool
	for _, e := range opts.Environment {
		if strings.HasPrefix(e, "BUN_PORT=") {
			t.Errorf("a socket site was given %q", e)
		}
		if e == "RATLINE_SOCKET="+socket {
			sawSocketPath = true
		}
	}
	if !sawSocketPath {
		t.Errorf("the socket path was never passed to the application: %v", opts.Environment)
	}
	if len(opts.ExecStartPost) == 0 {
		t.Error("a socket site needs the ExecStartPost that fixes the socket mode for nginx")
	}
}

func TestBunPortSiteGetsBothPortSpellings(t *testing.T) {
	c := bunContext(t, func(s *state.Site) { s.Listen, s.Port = "port", 9310 })
	_, opts, err := (Bun{}).StartCommand(context.Background(), c)
	if err != nil {
		t.Fatalf("StartCommand = %v", err)
	}
	env := strings.Join(opts.Environment, " ")
	for _, want := range []string{"PORT=9310", "BUN_PORT=9310", "HOST=127.0.0.1"} {
		if !strings.Contains(env, want) {
			t.Errorf("the environment is missing %s: %v", want, opts.Environment)
		}
	}
	if len(opts.ExecStartPost) != 0 {
		t.Errorf("a port site has no socket to chmod, got %v", opts.ExecStartPost)
	}
}

// Bun executes TypeScript and JSX without a build step, which is most of the reason to
// choose it. An entry point it can run must not be refused.
func TestBunAcceptsTypeScriptAndJSXEntryPoints(t *testing.T) {
	for _, entry := range []string{"server.ts", "src/index.tsx", "app.jsx", "dist/server.js"} {
		c := bunContext(t, func(s *state.Site) { s.Entry = entry })
		if _, _, err := (Bun{}).StartCommand(context.Background(), c); err != nil {
			t.Errorf("entry %q was refused: %v", entry, err)
		}
	}
}

// Reload has to refuse rather than restart quietly: a caller told the reload succeeded
// would believe no requests were dropped.
func TestBunReloadRefusesRatherThanPretending(t *testing.T) {
	c := bunContext(t, nil)
	err := (Bun{}).Reload(context.Background(), c)
	if err == nil {
		t.Fatal("Reload should refuse on a bun site")
	}
	if !strings.Contains(err.Error(), "drop") {
		t.Errorf("the refusal should say why, got: %v", err)
	}
}

// The reproducible install is the point of --frozen-lockfile, but bun errors out on the
// flag when there is no lockfile to freeze — which a first deploy legitimately has.
func TestBunInstallFreezesOnlyWhenThereIsALockfile(t *testing.T) {
	for _, tc := range []struct {
		name       string
		lockfile   string
		wantFrozen bool
	}{
		{"no lockfile", "", false},
		{"the text lockfile", "bun.lock", true},
		{"the binary lockfile", "bun.lockb", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := bunContext(t, nil)
			installFakeBun(t, c, "1.2")
			// The real AppDir is under /home, which the test process cannot create.
			c.AppDir = t.TempDir()
			if tc.lockfile != "" {
				if err := os.WriteFile(filepath.Join(c.AppDir, tc.lockfile), []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			argv, err := (Bun{}).installArgv(c, "bun")
			if err != nil {
				t.Fatalf("installArgv = %v", err)
			}
			got := strings.Contains(strings.Join(argv, " "), "--frozen-lockfile")
			if got != tc.wantFrozen {
				t.Errorf("--frozen-lockfile = %v, want %v (argv: %v)", got, tc.wantFrozen, argv)
			}
		})
	}
}

// Dev dependencies are build dependencies. --production with a build command to run is
// the failure node.go documents at length, and bun reproduces it exactly.
func TestBunKeepsDevDependenciesWhenThereIsSomethingToBuild(t *testing.T) {
	for _, tc := range []struct {
		build    string
		wantProd bool
	}{
		{"", true},
		{"bun run build", false},
	} {
		c := bunContext(t, func(s *state.Site) { s.BuildCommand = tc.build })
		installFakeBun(t, c, "1.2")
		argv, err := (Bun{}).installArgv(c, "bun")
		if err != nil {
			t.Fatalf("installArgv = %v", err)
		}
		got := strings.Contains(strings.Join(argv, " "), "--production")
		if got != tc.wantProd {
			t.Errorf("build=%q: --production = %v, want %v (argv: %v)", tc.build, got, tc.wantProd, argv)
		}
	}
}

// A version that is pinned but not installed has to be a precondition failure naming the
// command that fixes it, not a fall through to whatever bun happens to be on the host.
func TestBunRefusesAPinnedVersionThatIsNotInstalled(t *testing.T) {
	c := bunContext(t, func(s *state.Site) { s.BunVersion = "1.1" })
	c.DryRun = false
	c.Cfg.Paths.RuntimesDir = t.TempDir()
	_, err := (Bun{}).binary(c)
	if err == nil {
		t.Fatal("a missing pinned version should be refused")
	}
	if !strings.Contains(err.Error(), "1.1") {
		t.Errorf("the refusal should name the version, got: %v", err)
	}
}

// The site's pin wins over the server-wide default, or `site runtime --bun` would move a
// site's version number without moving the interpreter its unit executes.
func TestBunSitePinOverridesTheServerDefault(t *testing.T) {
	c := bunContext(t, func(s *state.Site) { s.BunVersion = "1.1" })
	c.DryRun = false
	installFakeBun(t, c, "1.1")
	got, err := (Bun{}).binary(c)
	if err != nil {
		t.Fatalf("binary = %v", err)
	}
	want := filepath.Join(c.Cfg.Paths.RuntimesDir, "bun", "1.1", "bin", "bun")
	if got != want {
		t.Errorf("binary = %q, want %q", got, want)
	}
}

// RuntimeBinDirs is what puts a bun site's own bin directory ahead of the system PATH
// for hooks and build commands. A bun site that resolved `bun` from /usr/bin would run a
// different engine from the one in its unit.
func TestBunRuntimeBinDirsPointAtTheManagedTree(t *testing.T) {
	c := bunContext(t, func(s *state.Site) { s.BunVersion = "1.2" })
	dirs := c.RuntimeBinDirs()
	want := filepath.Join(c.Cfg.Paths.RuntimesDir, "bun", "1.2", "bin")
	if len(dirs) != 1 || dirs[0] != want {
		t.Errorf("RuntimeBinDirs = %v, want [%s]", dirs, want)
	}
}
