package sitepath

import (
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

func cfgFor(t *testing.T) *config.Config {
	t.Helper()
	c := config.Default()
	c.Paths.RuntimesDir = "/opt/ratline/runtimes"
	return c
}

// The order is the whole contract: a project's own tooling, then the interpreter the site
// is pinned to, then the system. Reversed, a server-wide `vite` would shadow the one the
// project installed; without the managed directory at all, `npm` is not found on a server
// that has no system Node — which is every server ratline sets up.
func TestDirsPutTheProjectFirstAndTheManagedRuntimeNext(t *testing.T) {
	site := &state.Site{Domain: "app.example.com", Owner: "acme", Runtime: "node", NodeVersion: "24"}
	got := Dirs(cfgFor(t), site, "/home/acme/app.example.com")
	want := []string{
		"/home/acme/app.example.com/venv/bin",
		"/home/acme/app.example.com/app/node_modules/.bin",
		"/opt/ratline/runtimes/node/24/bin",
	}
	if strings.Join(got, ":") != strings.Join(want, ":") {
		t.Errorf("Dirs =\n  %v\nwant\n  %v", got, want)
	}
}

// A site that pinned no version takes the server's default. It is the same fallback the
// unit's ExecStart resolves through, so a job and the service it belongs to cannot end up
// on different interpreters.
func TestAnUnpinnedSiteTakesTheServerDefault(t *testing.T) {
	cfg := cfgFor(t)
	cfg.Runtimes.NodeDefault = "22"
	site := &state.Site{Domain: "app.example.com", Owner: "acme", Runtime: "node"}
	if got := RuntimeBinDirs(cfg, site); len(got) != 1 || got[0] != "/opt/ratline/runtimes/node/22/bin" {
		t.Errorf("RuntimeBinDirs = %v, want the server's default version", got)
	}

	// And with no default either, there is no managed directory to name. Inventing
	// /opt/ratline/runtimes/node//bin would put a path that cannot exist on every PATH.
	cfg.Runtimes.NodeDefault = ""
	if got := RuntimeBinDirs(cfg, site); len(got) != 0 {
		t.Errorf("RuntimeBinDirs = %v, want nothing when no version is known", got)
	}
}

func TestEachRuntimeGetsItsOwnTree(t *testing.T) {
	cfg := cfgFor(t)
	for _, tc := range []struct {
		site *state.Site
		want string
	}{
		{&state.Site{Runtime: "node", NodeVersion: "24"}, "/opt/ratline/runtimes/node/24/bin"},
		{&state.Site{Runtime: "bun", BunVersion: "1.2"}, "/opt/ratline/runtimes/bun/1.2/bin"},
		{&state.Site{Runtime: "python", PythonVersion: "3.12"}, "/opt/ratline/runtimes/python/3.12/bin"},
	} {
		got := RuntimeBinDirs(cfg, tc.site)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: RuntimeBinDirs = %v, want [%s]", tc.site.Runtime, got, tc.want)
		}
	}

	// A static site has no interpreter. Nothing is the right answer, not a guess.
	if got := RuntimeBinDirs(cfg, &state.Site{Runtime: "static"}); len(got) != 0 {
		t.Errorf("a static site got %v", got)
	}
}

// The system path goes last and is always there: a job that runs /bin/sh or `curl` must
// still find them, and a site's own directories must never be able to shadow one.
func TestPATHEndsWithTheSystemPath(t *testing.T) {
	site := &state.Site{Domain: "app.example.com", Owner: "acme", Runtime: "static"}
	got := PATH(cfgFor(t), site, "/home/acme/app.example.com")
	if !strings.HasSuffix(got, system.DefaultPath) {
		t.Errorf("PATH = %q, want it to end with the system path", got)
	}
	if strings.HasPrefix(got, system.DefaultPath) {
		t.Errorf("PATH = %q, want the site's own directories first", got)
	}
	if n := strings.Count(got, system.DefaultPath); n != 1 {
		t.Errorf("PATH names the system path %d times: %q", n, got)
	}
}
