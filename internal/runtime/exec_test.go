package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
)

// execFixture builds a site on disk: an application directory, a .env, and a managed
// runtime tree the site is pinned to. Everything is owned by whoever runs the test,
// because SiteEnv refuses to read a .env that is not the tenant's.
func execFixture(t *testing.T, site *state.Site) *Context {
	t.Helper()
	root := t.TempDir()
	siteDir := filepath.Join(root, "acme", site.Domain)

	cfg := config.Default()
	cfg.Paths.RuntimesDir = filepath.Join(root, "runtimes")

	c := &Context{
		Cfg: cfg, Log: log.Discard(), Site: site,
		Identity: &system.Identity{
			Name: site.Owner, UID: os.Getuid(), GID: os.Getgid(),
			Home: filepath.Join(root, "acme"), Shell: "/bin/sh",
		},
		SiteDir: siteDir,
		AppDir:  filepath.Join(siteDir, "app"),
		LogDir:  filepath.Join(siteDir, "logs"),
		TmpDir:  filepath.Join(siteDir, "tmp"),
		VenvDir: filepath.Join(siteDir, "venv"),
	}
	if err := os.MkdirAll(c.AppDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A deployed site. Without it every plan would take the "no application code yet"
	// branch, and the tests below would be asserting against a different error.
	if err := os.WriteFile(filepath.Join(c.AppDir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return c
}

// writeExecutable puts a program somewhere and makes it runnable.
func writeExecutable(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func nodeSite() *state.Site {
	return &state.Site{Domain: "app.example.com", Owner: "acme", Slug: "acme-app_example_com",
		Runtime: "node", NodeVersion: "24"}
}

// The whole reason this command exists: `npm` has to be the npm belonging to the
// version this site is pinned to, on a server with no system Node at all. Resolving it
// through the ambient PATH would find nothing, or — worse — something else.
func TestPlanExecResolvesTheSitesOwnRuntime(t *testing.T) {
	c := execFixture(t, nodeSite())
	npm := writeExecutable(t, filepath.Join(c.Cfg.Paths.RuntimesDir, "node", "24", "bin", "npm"), "#!/bin/sh\n")

	plan, err := PlanExec(c, []string{"npm", "run", "bootstrap"}, "")
	if err != nil {
		t.Fatalf("PlanExec = %v", err)
	}
	if plan.Program != npm {
		t.Errorf("program = %q, want the managed runtime's npm at %q", plan.Program, npm)
	}
	if got, want := strings.Join(plan.Args, " "), "run bootstrap"; got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
	if plan.Dir != c.AppDir {
		t.Errorf("dir = %q, want the application directory %q", plan.Dir, c.AppDir)
	}
	if plan.User != "acme" {
		t.Errorf("user = %q, want the tenant", plan.User)
	}
	// PATH has to agree with the resolution, or a child of npm finds a different node.
	if first := strings.Split(plan.Path, ":")[0]; !strings.HasSuffix(plan.Path, system.DefaultPath) ||
		!strings.Contains(plan.Path, filepath.Dir(npm)) {
		t.Errorf("PATH = %q, want the managed runtime ahead of the system path (first %q)", plan.Path, first)
	}
}

// A python site's `python` is the venv's, which is the interpreter that has the
// project's packages. The same word resolving to /usr/bin/python is how a management
// command fails with ModuleNotFoundError on a server that looks perfectly set up.
func TestPlanExecPrefersTheVenvForAPythonSite(t *testing.T) {
	c := execFixture(t, &state.Site{Domain: "api.example.com", Owner: "acme",
		Runtime: "python", PythonVersion: "3.12"})
	py := writeExecutable(t, filepath.Join(c.VenvDir, "bin", "python"), "#!/bin/sh\n")

	plan, err := PlanExec(c, []string{"python", "manage.py", "migrate"}, "")
	if err != nil {
		t.Fatalf("PlanExec = %v", err)
	}
	if plan.Program != py {
		t.Errorf("program = %q, want the venv's interpreter %q", plan.Program, py)
	}
	if dirs := strings.Split(plan.Path, ":"); dirs[0] != filepath.Dir(py) {
		t.Errorf("PATH starts with %q, want the venv's bin first", dirs[0])
	}
}

// Nothing goes through a shell, so an operator who types one gets told, rather than
// silently handing "&&" to npm as an argument and getting something baffling.
func TestPlanExecRefusesAShellLine(t *testing.T) {
	c := execFixture(t, nodeSite())
	writeExecutable(t, filepath.Join(c.Cfg.Paths.RuntimesDir, "node", "24", "bin", "npm"), "#!/bin/sh\n")

	for _, argv := range [][]string{
		{"npm", "run", "build", "&&", "npm", "start"},
		{"npm", "run", "build", "|", "tee", "log"},
		{"npm", "run", "build", ">", "out.txt"},
		{"npm run build && npm start"},
	} {
		if _, err := PlanExec(c, argv, ""); err == nil {
			t.Errorf("PlanExec(%q) = nil, want a refusal", strings.Join(argv, " "))
		}
	}

	// And the negative case, so the check above is not refusing everything: a semicolon
	// of its own is how `find -exec` ends, and it has to survive.
	find := writeExecutable(t, filepath.Join(c.AppDir, "bin", "find"), "#!/bin/sh\n")
	if _, err := PlanExec(c, []string{find, ".", "-name", "*.tmp", "-exec", "rm", "{}", ";"}, ""); err != nil {
		t.Errorf("PlanExec with a find terminator = %v, want it accepted", err)
	}
}

// A newline in an argument is refused at the boundary to execve, and saying so here
// names the argument rather than letting it surface from inside the runner.
func TestPlanExecRefusesControlCharacters(t *testing.T) {
	c := execFixture(t, nodeSite())
	writeExecutable(t, filepath.Join(c.Cfg.Paths.RuntimesDir, "node", "24", "bin", "npm"), "#!/bin/sh\n")

	for _, bad := range []string{"run\nbootstrap", "run\x00bootstrap"} {
		if _, err := PlanExec(c, []string{"npm", bad}, ""); err == nil {
			t.Errorf("PlanExec with %q = nil, want a refusal", bad)
		}
	}
}

// "npm was not found" has to say where it looked, or the operator's next move is to
// install a system Node — which is the one thing managed runtimes exist to avoid.
func TestPlanExecSaysWhereItLooked(t *testing.T) {
	c := execFixture(t, nodeSite())
	_, err := PlanExec(c, []string{"nmp", "run", "bootstrap"}, "")
	if err == nil {
		t.Fatal("PlanExec with a typo = nil, want a refusal")
	}
	hint := rlerr.Hint(err)
	if !strings.Contains(hint, filepath.Join(c.Cfg.Paths.RuntimesDir, "node", "24", "bin")) {
		t.Errorf("hint = %q, want the runtime bin directory it searched", hint)
	}
}

// A site whose code has never been deployed has none of its own tooling either, and
// "npm: no such program" is a true answer to the wrong question.
func TestPlanExecSaysWhenTheSiteHasNoCodeYet(t *testing.T) {
	c := execFixture(t, nodeSite())
	if err := os.Remove(filepath.Join(c.AppDir, "package.json")); err != nil {
		t.Fatal(err)
	}
	_, err := PlanExec(c, []string{"./bin/seed"}, "")
	if err == nil {
		t.Fatal("PlanExec on an empty application directory = nil, want a refusal")
	}
	if hint := rlerr.Hint(err); !strings.Contains(hint, "no application code yet") {
		t.Errorf("hint = %q, want it to name the actual cause", hint)
	}
}

// A script in the repository that nobody chmod'ed is the most common way this fails,
// and execve's EACCES does not suggest the fix.
func TestPlanExecNamesAFileThatIsNotExecutable(t *testing.T) {
	c := execFixture(t, nodeSite())
	script := filepath.Join(c.AppDir, "bin", "seed")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := PlanExec(c, []string{"./bin/seed"}, "")
	if err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("PlanExec on a file without +x = %v, want it named as not executable", err)
	}
	if hint := rlerr.Hint(err); !strings.Contains(hint, "chmod +x") {
		t.Errorf("hint = %q, want the fix", hint)
	}
}

// The plan is printed by --dry-run and emitted under --json. The site's .env is in the
// environment this command runs with, and a DATABASE_URL belongs in neither.
func TestPlanExecCarriesNoSecrets(t *testing.T) {
	c := execFixture(t, nodeSite())
	writeExecutable(t, filepath.Join(c.Cfg.Paths.RuntimesDir, "node", "24", "bin", "npm"), "#!/bin/sh\n")
	if err := os.WriteFile(filepath.Join(c.SiteDir, ".env"),
		[]byte("DATABASE_URL=mongodb://u:hunter2@127.0.0.1/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan, err := PlanExec(c, []string{"npm", "run", "bootstrap"}, "")
	if err != nil {
		t.Fatalf("PlanExec = %v", err)
	}
	rendered := plan.CommandLine() + " " + plan.Path + " " + plan.Dir + " " + plan.User
	if strings.Contains(rendered, "hunter2") {
		t.Errorf("the plan contains a value from the site's .env: %q", rendered)
	}
}

// The environment the command actually runs with is the build's: the tenant, the
// application directory, the site's variables — and ratline's PATH last, so nothing in
// .env can point the interpreter somewhere else.
func TestRunExecRunsAsTheTenantWithTheSiteEnvironment(t *testing.T) {
	c := execFixture(t, nodeSite())
	npm := writeExecutable(t, filepath.Join(c.Cfg.Paths.RuntimesDir, "node", "24", "bin", "npm"), "#!/bin/sh\n")
	if err := os.WriteFile(filepath.Join(c.SiteDir, ".env"),
		[]byte("API_TOKEN=secret-value\nPATH=/tmp/attacker\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var got system.Cmd
	fake := systest.NewFakeRunner()
	fake.Hook = func(cmd system.Cmd) (*system.Result, error) {
		got = cmd
		return &system.Result{Path: cmd.Path, Args: cmd.Args}, nil
	}
	c.Runner = fake

	plan, err := PlanExec(c, []string{"npm", "run", "bootstrap"}, "")
	if err != nil {
		t.Fatalf("PlanExec = %v", err)
	}
	if _, err := RunExec(context.Background(), c, plan, ExecOptions{Stdout: os.Stderr}); err != nil {
		t.Fatalf("RunExec = %v", err)
	}

	if got.As == nil || got.As.Name != "acme" {
		t.Errorf("ran as %v, want the tenant: root running a tenant's code is the escalation", got.As)
	}
	if got.Path != npm || strings.Join(got.Args, " ") != "run bootstrap" {
		t.Errorf("ran %q %v, want %q run bootstrap", got.Path, got.Args, npm)
	}
	if got.Dir != c.AppDir {
		t.Errorf("dir = %q, want %q", got.Dir, c.AppDir)
	}
	if !got.Mutates {
		t.Error("the command is not marked as mutating, so --dry-run would run it")
	}
	if got.Stream {
		t.Error("output is streamed through the logger as well as raw, so every line appears twice")
	}
	env := envMap(got.Env)
	if env["API_TOKEN"] != "secret-value" {
		t.Errorf("API_TOKEN = %q, want the site's own value", env["API_TOKEN"])
	}
	if env["RATLINE_DOMAIN"] != "app.example.com" {
		t.Errorf("RATLINE_DOMAIN = %q", env["RATLINE_DOMAIN"])
	}
	if dirs := strings.Split(env["PATH"], ":"); dirs[0] == "/tmp/attacker" {
		t.Errorf("PATH = %q: a value in .env redirected the interpreter", env["PATH"])
	}
	if !strings.Contains(env["PATH"], filepath.Dir(npm)) {
		t.Errorf("PATH = %q, want the managed runtime on it", env["PATH"])
	}
}

// A hook and an exec are the same thing with a different trigger, so they get the same
// PATH. They did not: a hook's PATH held the managed runtime but neither the venv nor
// node_modules/.bin, so `python` resolved to the venv while any subprocess it spawned
// got /usr/bin/python and a different set of packages.
func TestAHookAndAnExecGetTheSamePath(t *testing.T) {
	c := execFixture(t, nodeSite())
	writeExecutable(t, filepath.Join(c.Cfg.Paths.RuntimesDir, "node", "24", "bin", "npm"), "#!/bin/sh\n")
	writeExecutable(t, filepath.Join(c.AppDir, "bin", "smoke"), "#!/bin/sh\n")

	var hookEnv []string
	fake := systest.NewFakeRunner()
	fake.Hook = func(cmd system.Cmd) (*system.Result, error) {
		hookEnv = cmd.Env
		return &system.Result{}, nil
	}
	c.Runner = fake
	if err := RunHook(context.Background(), c, "pre-deploy", "./bin/smoke"); err != nil {
		t.Fatalf("RunHook = %v", err)
	}

	plan, err := PlanExec(c, []string{"npm", "run", "bootstrap"}, "")
	if err != nil {
		t.Fatalf("PlanExec = %v", err)
	}
	if got, want := envMap(hookEnv)["PATH"], plan.Path; got != want {
		t.Errorf("hook PATH = %q\nexec PATH = %q\nthey have to be the same environment", got, want)
	}
}

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok {
			out[k] = v
		}
	}
	return out
}

// The property this whole arrangement exists for: the same command means the same thing
// however it is triggered.
//
// It did not. `site exec app -- npm run nightly` worked while the identical line as
// `site cron add --command 'npm run nightly'` failed at 3am with "npm was not found on
// PATH", because a unit gets only systemd's minimal default environment and nothing put
// the site's managed interpreter on it. The two PATHs are built from one list now, and
// this is the test that says so — it reaches across internal/unit, which is the layer
// that writes the unit, and internal/runtime, which is the layer that runs an exec.
func TestAJobAndAnExecAgreeOnPath(t *testing.T) {
	c := execFixture(t, nodeSite())
	writeExecutable(t, filepath.Join(c.Cfg.Paths.RuntimesDir, "node", "24", "bin", "npm"), "#!/bin/sh\n")

	// The unit, rendered the way `site cron add` renders it. The manager derives the
	// site directory from the configuration, so the fixture's has to be the one it
	// derives — which is the same rule production runs under.
	cfg := *c.Cfg
	cfg.Paths.HomeBase = filepath.Dir(filepath.Dir(c.SiteDir))
	m := &unit.Manager{Cfg: &cfg, Log: log.Discard()}
	body, _, err := m.RenderSiteUnit(c.Site, &state.SiteUnit{
		Domain: c.Site.Domain, Name: "nightly", Kind: state.UnitJob,
		Command: "npm run nightly", Schedule: "*-*-* 03:00:00", Enabled: true,
	})
	if err != nil {
		t.Fatalf("RenderSiteUnit = %v", err)
	}

	var unitPath string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "Environment=PATH="); ok {
			unitPath = rest
		}
	}
	if unitPath == "" {
		t.Fatal("the job's unit sets no PATH")
	}

	plan, err := PlanExec(c, []string{"npm", "run", "nightly"}, "")
	if err != nil {
		t.Fatalf("PlanExec = %v", err)
	}
	if unitPath != plan.Path {
		t.Errorf("a job and an exec run with different PATHs:\n  job  %s\n  exec %s", unitPath, plan.Path)
	}

	// And the program the exec resolves is on it, which is what makes `npm run nightly`
	// a thing a unit can run at all.
	if !strings.Contains(unitPath, filepath.Dir(plan.Program)) {
		t.Errorf("the job's PATH %q does not contain %q", unitPath, filepath.Dir(plan.Program))
	}
}

// --cwd exists because a build output carries its own project. A Next.js standalone
// directory has its own package.json, its own node_modules and its own server.js, and
// `npm` run in app/ above it fails with "Could not read package.json" — which is exactly
// what a live server reported the day site exec shipped.
func TestExecRunsInTheDirectoryItIsGiven(t *testing.T) {
	c := execFixture(t, nodeSite())
	npm := writeExecutable(t, filepath.Join(c.Cfg.Paths.RuntimesDir, "node", "24", "bin", "npm"), "#!/bin/sh\n")
	standalone := filepath.Join(c.AppDir, ".next", "standalone")
	if err := os.MkdirAll(standalone, 0o755); err != nil {
		t.Fatal(err)
	}
	// ResolveWithin resolves symlinks, and on macOS t.TempDir() sits under /var, which
	// is one. The production path is not, but the expectation has to match what the
	// resolver returns rather than what the test happened to build.
	standalone = realPath(t, standalone)

	plan, err := PlanExec(c, []string{"npm", "ci"}, ".next/standalone")
	if err != nil {
		t.Fatalf("PlanExec = %v", err)
	}
	if plan.Dir != standalone {
		t.Errorf("dir = %q, want %q", plan.Dir, standalone)
	}
	if plan.Program != npm {
		t.Errorf("program = %q, want the site's npm at %q", plan.Program, npm)
	}
	// The PATH has to name that directory's node_modules, or a tool the build output
	// installed for itself is invisible to the command running inside it.
	if first := strings.Split(plan.Path, ":")[0]; first != filepath.Join(standalone, "node_modules", ".bin") {
		t.Errorf("PATH starts with %q, want the working directory's node_modules/.bin", first)
	}

	// And a program named relatively is that directory's, not app/'s.
	script := writeExecutable(t, filepath.Join(standalone, "run.sh"), "#!/bin/sh\n")
	plan, err = PlanExec(c, []string{"./run.sh"}, ".next/standalone")
	if err != nil {
		t.Fatalf("PlanExec(./run.sh) = %v", err)
	}
	if plan.Program != script {
		t.Errorf("program = %q, want %q", plan.Program, script)
	}
}

// An absolute path is taken as given, as long as it is inside the site: logs/ and tmp/
// are legitimate places to run something.
func TestExecAcceptsAnAbsoluteDirectoryInsideTheSite(t *testing.T) {
	c := execFixture(t, nodeSite())
	writeExecutable(t, filepath.Join(c.Cfg.Paths.RuntimesDir, "node", "24", "bin", "npm"), "#!/bin/sh\n")
	if err := os.MkdirAll(c.LogDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanExec(c, []string{"npm", "--version"}, c.LogDir)
	if err != nil {
		t.Fatalf("PlanExec = %v", err)
	}
	if want := realPath(t, c.LogDir); plan.Dir != want {
		t.Errorf("dir = %q, want %q", plan.Dir, want)
	}
}

// The site is the boundary. It holds even though the command runs as the tenant anyway:
// a working directory outside the site is not something ratline offers, and a tenant owns
// this tree and can put a link in it.
func TestExecRefusesADirectoryOutsideTheSite(t *testing.T) {
	c := execFixture(t, nodeSite())
	writeExecutable(t, filepath.Join(c.Cfg.Paths.RuntimesDir, "node", "24", "bin", "npm"), "#!/bin/sh\n")

	outside := t.TempDir()
	// A symlink inside the site pointing out of it: resolved before the check, refused.
	if err := os.Symlink(outside, filepath.Join(c.AppDir, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../../../..", outside, "escape", "/etc"} {
		if _, err := PlanExec(c, []string{"npm", "--version"}, bad); err == nil {
			t.Errorf("--cwd %q was accepted, want it refused as outside the site", bad)
		}
	}
}

func TestExecRefusesADirectoryThatIsNotOne(t *testing.T) {
	c := execFixture(t, nodeSite())
	writeExecutable(t, filepath.Join(c.Cfg.Paths.RuntimesDir, "node", "24", "bin", "npm"), "#!/bin/sh\n")

	_, err := PlanExec(c, []string{"npm", "--version"}, "nope")
	if err == nil || !strings.Contains(err.Error(), "no such directory") {
		t.Errorf("a missing --cwd = %v, want it named as missing", err)
	}
	if hint := rlerr.Hint(err); !strings.Contains(hint, c.AppDir) {
		t.Errorf("hint = %q, want it to say what the path is relative to", hint)
	}
	if _, err := PlanExec(c, []string{"npm", "--version"}, "package.json"); err == nil {
		t.Error("a file was accepted as a working directory")
	}
}

// realPath is the path with symlinks resolved, which is what validate.ResolveWithin
// returns. On macOS every t.TempDir() is under /var, a symlink to /private/var.
func realPath(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s) = %v", p, err)
	}
	return resolved
}
