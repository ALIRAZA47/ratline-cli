package runtime

import (
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

// Dev dependencies are build dependencies. Every install was production-only, so a
// Next.js site failed its build with "Cannot find module '@tailwindcss/postcss'" after an
// install that reported success — Tailwind, TypeScript, PostCSS and Vite all live in
// devDependencies, which meant practically no modern Node project could be built at all.

func ctxFor(t *testing.T, site *state.Site) *Context {
	t.Helper()
	cfg := config.Default()
	cfg.Paths.RuntimesDir = "/opt/ratline/runtimes"
	return &Context{Cfg: cfg, Log: log.Discard(), Site: site, AppDir: t.TempDir(), DryRun: true}
}

func TestABuildGetsItsDevDependencies(t *testing.T) {
	for _, pm := range []string{"npm", "pnpm", "yarn", "bun"} {
		c := ctxFor(t, &state.Site{Runtime: "node", NodeVersion: "24", BuildCommand: "npm run build"})
		argv, err := Node{}.installArgv(c, pm)
		if err != nil {
			t.Fatalf("%s: %v", pm, err)
		}
		line := strings.Join(argv, " ")
		for _, forbidden := range []string{"--omit=dev", "--prod", "--production"} {
			if strings.Contains(line, forbidden) {
				t.Errorf("%s: install is %q — %s leaves the build without its tooling",
					pm, line, forbidden)
			}
		}
	}
}

// With nothing to build, the dev tooling is dead weight on a server, so it stays omitted.
func TestWithoutABuildTheInstallStaysProductionOnly(t *testing.T) {
	for pm, want := range map[string]string{
		"npm": "--omit=dev", "pnpm": "--prod", "yarn": "--production", "bun": "--production",
	} {
		c := ctxFor(t, &state.Site{Runtime: "node", NodeVersion: "24"})
		argv, err := Node{}.installArgv(c, pm)
		if err != nil {
			t.Fatalf("%s: %v", pm, err)
		}
		if line := strings.Join(argv, " "); !strings.Contains(line, want) {
			t.Errorf("%s: install is %q, want it to contain %s", pm, line, want)
		}
	}
}

// A site pinned to a managed runtime must use that runtime's npm. It resolved /usr/bin/npm
// instead and failed with "fork/exec /usr/bin/npm: no such file or directory" on a server
// with no system Node — which is the server managed runtimes exist for.
func TestBuildToolsComeFromTheManagedRuntime(t *testing.T) {
	c := ctxFor(t, &state.Site{Runtime: "node", NodeVersion: "24", BuildCommand: "npm run build"})
	dirs := c.RuntimeBinDirs()
	if len(dirs) != 1 || !strings.HasSuffix(dirs[0], "/node/24/bin") {
		t.Fatalf("RuntimeBinDirs = %v, want the pinned runtime's bin directory", dirs)
	}

	c2 := ctxFor(t, &state.Site{Runtime: "python", PythonVersion: "3.12"})
	if dirs := c2.RuntimeBinDirs(); len(dirs) != 1 || !strings.HasSuffix(dirs[0], "/python/3.12/bin") {
		t.Errorf("python RuntimeBinDirs = %v", dirs)
	}
}
