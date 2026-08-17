package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// Bun runs a JavaScript or TypeScript server on Bun behind nginx.
//
// Deliberately a sibling of Node rather than a mode of it. They look alike from nginx —
// one process, one socket, the same proxy block — but the differences are the whole
// reason an operator picks one: bun executes TypeScript and JSX without a build step,
// installs with its own resolver, and has no PM2. Folding it into Node would mean every
// node code path growing an "unless it is bun" branch, and the site row's runtime column
// would stop naming the thing that actually executes.
type Bun struct{}

func (Bun) Name() string { return "bun" }

// Provision checks that a usable bun is present. Everything else a bun site needs
// lives in node_modules, which Install handles.
func (b Bun) Provision(ctx context.Context, c *Context) error {
	_, err := b.binary(c)
	return err
}

// binary resolves the bun executable by absolute path.
//
// The same rule as Node's: a managed install under /opt is preferred and shell profiles
// are never consulted, because systemd does not read them. `bun upgrade` is the reason
// this matters more here than it does for node — it rewrites ~/.bun/bin/bun in place, so
// a unit pointing into a tenant's home would change interpreter the day that tenant ran
// it. A managed bun is root-owned and only ratline replaces it.
func (Bun) binary(c *Context) (string, error) {
	version := c.Site.BunVersion
	if version == "" {
		version = c.Cfg.Runtimes.BunDefault
	}
	if version != "" {
		if err := validate.BunVersion(version); err != nil {
			return "", err
		}
		managed := filepath.Join(c.Cfg.Paths.RuntimesDir, "bun", version, "bin", "bun")
		if system.Exists(managed) {
			return managed, nil
		}
		if !c.DryRun {
			return "", rlerr.Preconditionf("Bun %s is not installed", version).
				WithHint("install it with 'ratline runtime install bun %s', or use --bun with a version that is present", version)
		}
		return managed, nil
	}
	for _, dir := range []string{"/usr/local/bin", "/usr/bin"} {
		candidate := filepath.Join(dir, "bun")
		if system.Exists(candidate) {
			return candidate, nil
		}
	}
	if c.DryRun {
		return "/usr/bin/bun", nil
	}
	return "", rlerr.Preconditionf("no Bun installation was found").
		WithHint("install one with 'ratline runtime install bun 1.2'")
}

// Install runs `bun install` as the site user.
//
// A bun site installs with bun. --package-manager is accepted and honoured for the
// projects that keep an npm or pnpm lockfile while running the server on bun, but the
// default is bun's own resolver rather than DetectPackageManager's lockfile sniffing:
// picking npm for a bun site because a stale package-lock.json is still in the tree is a
// guess, and the wrong one.
func (b Bun) Install(ctx context.Context, c *Context) error {
	if c.DryRun {
		c.Log.Info("would install dependencies", "dir", c.AppDir)
		return nil
	}
	if !system.Exists(filepath.Join(c.AppDir, "package.json")) {
		c.Log.Warn("no package.json was found, so no dependencies were installed", "dir", c.AppDir)
		return nil
	}

	pm := c.Site.PackageManager
	if pm == "" {
		pm = "bun"
	}
	argv, err := b.installArgv(c, pm)
	if err != nil {
		return err
	}
	c.Log.Info("installing dependencies", "package_manager", pm, "dir", c.AppDir)

	bunBin, err := b.binary(c)
	if err != nil {
		return err
	}
	// The managed bun first on PATH, so a lifecycle script calling `bun` gets the same
	// binary the service will. NODE_ENV=production only when there is nothing to build:
	// bun reads it exactly as npm does and would otherwise skip the devDependencies that
	// every build tool lives in — the failure node.go's installArgv documents at length,
	// and bun reproduces it faithfully.
	envv := []string{
		"PATH=" + filepath.Dir(bunBin) + ":" + system.DefaultPath,
	}
	if c.Site.BuildCommand == "" {
		envv = append(envv, "NODE_ENV=production")
	}
	env := system.UserEnv(c.Identity, envv...)
	if _, err := runAsOwner(ctx, c, system.Cmd{
		Path:    argv[0],
		Args:    argv[1:],
		Env:     env,
		Timeout: c.Cfg.Runtimes.InstallTimeout.D(),
		Label:   "install dependencies",
	}); err != nil {
		return rlerr.Wrap(err, rlerr.CodeExternal, "installing dependencies failed").
			WithHint("the output above is the package manager's; a lockfile mismatch is the usual cause")
	}
	return nil
}

// installArgv builds the install command, preferring the reproducible form.
func (b Bun) installArgv(c *Context, pm string) ([]string, error) {
	if c.Site.InstallCommand != "" {
		parsed, err := system.ParseCommand(c.Site.InstallCommand)
		if err != nil {
			return nil, err
		}
		return append([]string{resolveProgram(parsed.Argv[0], c)}, parsed.Argv[1:]...), nil
	}
	// Dev dependencies are build dependencies — see node.go's installArgv for the
	// failure this avoids. They are omitted only when there is nothing to build.
	needsDev := c.Site.BuildCommand != ""
	if pm != "bun" {
		// A project that pinned npm, pnpm or yarn keeps them, so its lockfile still
		// means what it says. Node's builder already knows every one of these forms,
		// and a second copy here would be a place for the two to disagree.
		return Node{}.installArgv(c, pm)
	}

	bunBin, err := b.binary(c)
	if err != nil {
		return nil, err
	}
	argv := []string{bunBin, "install"}
	// --frozen-lockfile is the reproducible install: it fails rather than quietly
	// updating the lockfile, which is what a server wants. Only with a lockfile to
	// freeze — bun errors out on the flag when there is none, and a first deploy from a
	// repository that never committed one is an ordinary thing to do.
	if system.Exists(filepath.Join(c.AppDir, "bun.lock")) || system.Exists(filepath.Join(c.AppDir, "bun.lockb")) {
		argv = append(argv, "--frozen-lockfile")
	}
	if !needsDev {
		argv = append(argv, "--production")
	}
	return argv, nil
}

// Build runs the build script.
func (b Bun) Build(ctx context.Context, c *Context) error {
	if c.Site.BuildCommand == "" {
		return nil
	}
	parsed, err := system.ParseCommand(c.Site.BuildCommand)
	if err != nil {
		return err
	}
	for _, w := range parsed.Warnings {
		c.Log.Warn(w)
	}
	bunBin, err := b.binary(c)
	if err != nil {
		return err
	}
	// The site's own variables, then ratline's PATH last so nothing in .env can redirect
	// the build to a different interpreter. NODE_ENV comes first so a project that wants
	// another value can say so in .env.
	buildEnv := append([]string{"NODE_ENV=production"}, c.SiteEnv()...)
	buildEnv = append(buildEnv,
		"PATH="+filepath.Dir(bunBin)+":"+filepath.Join(c.AppDir, "node_modules", ".bin")+":"+system.DefaultPath)
	env := system.UserEnv(c.Identity, buildEnv...)
	c.Log.Info("building", "command", c.Site.BuildCommand)
	_, err = runAsOwner(ctx, c, system.Cmd{
		Path:    resolveProgram(parsed.Argv[0], c),
		Args:    parsed.Argv[1:],
		Env:     env,
		Timeout: c.Cfg.Runtimes.BuildTimeout.D(),
		Label:   "build",
	})
	return err
}

// StartCommand builds the ExecStart line.
//
// Always bun straight under systemd. There is no PM2 equivalent here and none is
// invented: systemd owns the process, sees it crash, and counts the restarts.
func (b Bun) StartCommand(ctx context.Context, c *Context) (string, unit.RenderOptions, error) {
	var opts unit.RenderOptions
	bunBin, err := b.binary(c)
	if err != nil {
		return "", opts, err
	}

	socket := c.Cfg.SocketPath(c.Site.Owner, c.Site.Domain)
	env := []string{"NODE_ENV=production"}
	if c.Site.Listen == "port" {
		// BUN_PORT as well as PORT: it is the variable a default-exported Bun.serve
		// object picks up without the application reading anything itself.
		env = append(env, fmt.Sprintf("PORT=%d", c.Site.Port),
			fmt.Sprintf("BUN_PORT=%d", c.Site.Port), "HOST=127.0.0.1")
	} else {
		// Bun.serve takes a socket as its `unix:` option rather than from the
		// environment, so the application has to read the path itself: the same three
		// spellings a node site gets, and it picks whichever it understands. BUN_PORT
		// is deliberately *not* set here — it names a port, and bun parses it as one,
		// so a socket path in it is a startup failure rather than a fallback.
		env = append(env, "PORT="+socket, "RATLINE_SOCKET="+socket, "SOCKET_PATH="+socket)
		// Same reason as the node path: a socket created under the process umask can
		// land at 0640, and connect(2) needs write permission, so nginx would get
		// EACCES and every request would be a 502. The unit already sets UMask=0007
		// for socket sites; this is the belt for a framework that chmods it itself.
		// Runs as the tenant (no '+' prefix), never root, so a symlink swapped into
		// the tenant's own directory cannot redirect the chmod onto a root-owned
		// file. Always exits 0.
		opts.ExecStartPost = []string{
			"/bin/sh -c 'for i in $(seq 1 100); do if [ -S " + socket + " ]; then chmod 0660 " + socket +
				"; exit 0; fi; sleep 0.1; done; exit 0'",
		}
	}
	opts.Environment = env
	// No ExecReload: bun has no signal that drains connections, and claiming a graceful
	// reload while dropping requests would be worse than saying so. See Reload.

	if c.Site.StartCommand != "" {
		parsed, err := system.ParseCommand(c.Site.StartCommand)
		if err != nil {
			return "", opts, err
		}
		for _, w := range parsed.Warnings {
			c.Log.Warn(w)
		}
		// `bun run start` in a unit means an extra process between systemd and the
		// server, which breaks signal delivery and the main PID. Accepted because the
		// specification asks for it, with a warning.
		if base := filepath.Base(parsed.Argv[0]); base == "bun" || base == "bunx" ||
			base == "npm" || base == "pnpm" || base == "yarn" {
			c.Log.Warn("a package-manager start command puts an extra process between systemd and your server, "+
				"which can break graceful shutdown and restart counting",
				"advice", "prefer --entry with the file that calls Bun.serve()")
		}
		return shellSafeJoin(resolveProgram(parsed.Argv[0], c), parsed.Argv[1:]), opts, nil
	}

	if err := validate.BunEntry(c.Site.Entry); err != nil {
		return "", opts, err
	}
	entry, err := validate.ResolveWithin(c.AppDir, c.Site.Entry)
	if err != nil {
		// The file may not exist yet when a site is created before its code is
		// deployed, so fall back to the lexical check.
		if entry, err = validate.WithinRoot(c.AppDir, c.Site.Entry); err != nil {
			return "", opts, err
		}
	}
	// A missing entry point is only a mistake once there is code to check it against —
	// creating the site before pushing any code is a normal workflow. The same rule the
	// node path applies, and for the same reason.
	if !c.DryRun && !system.Exists(entry) {
		if HasApplicationCode(c.AppDir) {
			return "", opts, rlerr.Preconditionf("the entry point %s does not exist", entry).
				WithHint("check --entry against your project layout; the application " +
					"directory has code in it but not that file")
		}
		c.Log.Warn("the entry point does not exist yet, so the site is configured but not started",
			"entry", c.Site.Entry,
			"next", "deploy your code, then 'ratline site deploy "+c.Site.Domain+" --install --build --restart'")
	}
	// `bun <abspath>`, the exact parallel of node's `node <abspath>`: one process, the
	// application itself, and systemd's main PID is the thing serving. `bun run` would
	// be equivalent for an absolute path but reads as though a package.json script were
	// involved, and the script forms are what the warning above exists to discourage.
	return shellSafeJoin(bunBin, []string{entry}), opts, nil
}

// Reload is not possible on bun, and says so rather than pretending.
//
// PM2 is what gives a node site a zero-downtime reload, and PM2 supervising bun would
// mean a node install, a second supervisor process and a cluster mode bun does not
// implement. Until bun has its own answer, `site reload` on a bun site is a restart and
// the operator is told to ask for one.
func (Bun) Reload(ctx context.Context, c *Context) error {
	return rlerr.Preconditionf("a bun site cannot reload without dropping requests").
		WithHint("bun has no graceful-reload signal, so ratline will not claim one:\n"+
			"        ratline site restart %s\n"+
			"      a node site on PM2 is the option that reloads cleanly: ratline explain bun",
			c.Site.Domain)
}

// Teardown removes node_modules. Bun installs into the same directory npm does, so
// there is nothing bun-specific outside the site directory to clean up.
func (Bun) Teardown(ctx context.Context, c *Context) error {
	modules := filepath.Join(c.AppDir, "node_modules")
	if c.DryRun || !system.Exists(modules) {
		return nil
	}
	if err := os.RemoveAll(modules); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing %s", modules)
	}
	return nil
}

// DetectBunEntry guesses a bun project's entry point, for the wizard's default.
//
// The TypeScript spellings come first, because running them without a build step is the
// reason to choose bun at all — a project with both server.ts and a compiled
// dist/server.js means the source.
func DetectBunEntry(appDir string) string {
	for _, candidate := range []string{
		"server.ts", "index.ts", "app.ts", "main.ts", "src/index.ts", "src/server.ts",
		"server.js", "index.js", "app.js", "main.js",
		"dist/server.js", "dist/index.js", "build/index.js",
	} {
		if system.Exists(filepath.Join(appDir, candidate)) {
			return candidate
		}
	}
	// package.json's own answer, but only when bun could actually execute it.
	if data, err := system.ReadFileLimit(filepath.Join(appDir, "package.json"), 1<<20); err == nil {
		if main := extractJSONString(string(data), "module"); main != "" &&
			validate.BunEntry(main) == nil {
			return main
		}
		if main := extractJSONString(string(data), "main"); main != "" &&
			validate.BunEntry(main) == nil {
			return main
		}
	}
	return ""
}

// UsesBunLockfile reports whether a project carries one of bun's lockfiles, which is
// the strongest signal that bun is what it expects to run under.
func UsesBunLockfile(appDir string) bool {
	return system.Exists(filepath.Join(appDir, "bun.lock")) ||
		system.Exists(filepath.Join(appDir, "bun.lockb"))
}
