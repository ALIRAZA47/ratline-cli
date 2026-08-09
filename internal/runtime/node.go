package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// Node runs a JavaScript server behind nginx.
type Node struct{}

func (Node) Name() string { return "node" }

// Provision checks that a usable Node is present. There is no per-site
// installation step beyond node_modules, which Install handles.
func (n Node) Provision(ctx context.Context, c *Context) error {
	_, err := n.binary(c, "node")
	return err
}

// binary resolves a managed Node binary by absolute path.
//
// nvm, shell profiles and login shells are all deliberately avoided: systemd does
// not read them, so a unit that depended on them would work when tested by hand
// and fail on boot.
func (Node) binary(c *Context, name string) (string, error) {
	version := c.Site.NodeVersion
	if version == "" {
		version = c.Cfg.Runtimes.NodeDefault
	}
	if version != "" {
		if err := validate.NodeVersion(version); err != nil {
			return "", err
		}
		managed := filepath.Join(c.Cfg.Paths.RuntimesDir, "node", version, "bin", name)
		if system.Exists(managed) {
			return managed, nil
		}
		if !c.DryRun {
			return "", rlerr.Preconditionf("Node %s is not installed", version).
				WithHint("install it with 'ratline runtime install node %s', or use --node with a version that is present", version)
		}
		return managed, nil
	}
	for _, dir := range []string{"/usr/local/bin", "/usr/bin"} {
		candidate := filepath.Join(dir, name)
		if system.Exists(candidate) {
			return candidate, nil
		}
	}
	if c.DryRun {
		return "/usr/bin/" + name, nil
	}
	return "", rlerr.Preconditionf("no Node installation was found").
		WithHint("install one with 'ratline runtime install node 22'")
}

// Install runs the package manager as the site user.
func (n Node) Install(ctx context.Context, c *Context) error {
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
		pm = DetectPackageManager(c.AppDir)
	}
	argv, err := n.installArgv(c, pm)
	if err != nil {
		return err
	}
	c.Log.Info("installing dependencies", "package_manager", pm, "dir", c.AppDir)

	nodeBin, err := n.binary(c, "node")
	if err != nil {
		return err
	}
	// The managed Node's bin directory has to be first on PATH so that a
	// lifecycle script calling `node` gets the same interpreter the service will.
	// NODE_ENV=production makes npm skip devDependencies whatever the flags say, so it
	// is set only when they are genuinely unwanted. The build step still gets it, which
	// is where frameworks actually key their production output off it.
	envv := []string{
		"PATH=" + filepath.Dir(nodeBin) + ":" + system.DefaultPath,
		"npm_config_fund=false",
		"npm_config_audit=false",
		"npm_config_update_notifier=false",
	}
	if c.Site.BuildCommand == "" {
		envv = append(envv, "NODE_ENV=production")
	}
	env := system.UserEnv(c.Identity, envv...)
	_, err = runAsOwner(ctx, c, system.Cmd{
		Path:    argv[0],
		Args:    argv[1:],
		Env:     env,
		Timeout: c.Cfg.Runtimes.InstallTimeout.D(),
		Label:   "install dependencies",
	})
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeExternal, "installing dependencies failed").
			WithHint("the output above is the package manager's; a lockfile mismatch is the usual cause")
	}
	return nil
}

// installArgv builds the install command, preferring the reproducible form.
func (n Node) installArgv(c *Context, pm string) ([]string, error) {
	if c.Site.InstallCommand != "" {
		parsed, err := system.ParseCommand(c.Site.InstallCommand)
		if err != nil {
			return nil, err
		}
		return append([]string{resolveProgram(parsed.Argv[0], c)}, parsed.Argv[1:]...), nil
	}
	hasLock := func(name string) bool { return system.Exists(filepath.Join(c.AppDir, name)) }

	// Dev dependencies are build dependencies.
	//
	// Every install was production-only — npm --omit=dev, pnpm --prod, yarn and bun
	// --production — which is right for a site that ships what it committed, and wrong
	// for one that builds. Tailwind, TypeScript, PostCSS, Vite and webpack all live in
	// devDependencies, so a Next.js site failed its build with
	//
	//	Cannot find module '@tailwindcss/postcss'
	//
	// on a server where the install had just reported success. Practically no modern
	// Node project could be built at all.
	//
	// So: omit them only when there is nothing to build. They stay on disk afterwards —
	// pruning risks removing something the built output turns out to need at runtime,
	// and a wrong prune fails at request time rather than at deploy time.
	needsDev := c.Site.BuildCommand != ""

	switch pm {
	case "pnpm":
		argv := []string{resolveProgram("pnpm", c), "install"}
		if hasLock("pnpm-lock.yaml") {
			argv = append(argv, "--frozen-lockfile")
		}
		if !needsDev {
			argv = append(argv, "--prod")
		}
		return argv, nil
	case "yarn":
		argv := []string{resolveProgram("yarn", c), "install"}
		if hasLock("yarn.lock") {
			argv = append(argv, "--frozen-lockfile")
		}
		if !needsDev {
			argv = append(argv, "--production")
		}
		return argv, nil
	case "bun":
		argv := []string{resolveProgram("bun", c), "install", "--frozen-lockfile"}
		if !needsDev {
			argv = append(argv, "--production")
		}
		return argv, nil
	default:
		npm, err := n.binary(c, "npm")
		if err != nil {
			return nil, err
		}
		// npm ci is the reproducible install: it fails rather than silently
		// updating the lockfile, which is what you want on a server.
		argv := []string{npm, "install", "--no-audit", "--no-fund"}
		if hasLock("package-lock.json") {
			argv = []string{npm, "ci", "--no-audit", "--no-fund"}
		}
		if !needsDev {
			argv = append(argv, "--omit=dev")
		}
		return argv, nil
	}
}

// DetectPackageManager infers the package manager from the lockfile.
func DetectPackageManager(appDir string) string {
	for _, c := range []struct{ file, pm string }{
		{"pnpm-lock.yaml", "pnpm"},
		{"yarn.lock", "yarn"},
		{"bun.lockb", "bun"},
		{"bun.lock", "bun"},
		{"package-lock.json", "npm"},
	} {
		if system.Exists(filepath.Join(appDir, c.file)) {
			return c.pm
		}
	}
	return "npm"
}

// Build runs the build script.
func (n Node) Build(ctx context.Context, c *Context) error {
	if c.Site.BuildCommand == "" {
		return nil
	}
	parsed, err := system.ParseCommand(c.Site.BuildCommand)
	if err != nil {
		return err
	}
	nodeBin, err := n.binary(c, "node")
	if err != nil {
		return err
	}
	// The site's own variables, then ratline's PATH last so nothing can redirect the
	// build to a different interpreter. NODE_ENV comes first so a project that wants a
	// different value can say so in .env.
	buildEnv := append([]string{"NODE_ENV=production"}, c.SiteEnv()...)
	buildEnv = append(buildEnv,
		"PATH="+filepath.Dir(nodeBin)+":"+filepath.Join(c.AppDir, "node_modules", ".bin")+":"+system.DefaultPath)
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
// PM2 is the default, because it is the only way a Node site gets a graceful
// reload. --daemon direct runs node straight under systemd, which is one fewer
// moving part and the better choice for an app that never needs a reload.
func (n Node) StartCommand(ctx context.Context, c *Context) (string, unit.RenderOptions, error) {
	switch ProcessManagerFor(c) {
	case ProcessManagerPM2:
		return n.pm2StartCommand(ctx, c)
	case ProcessManagerDirect:
		return n.directStartCommand(ctx, c)
	default:
		return "", unit.RenderOptions{}, rlerr.Usagef(
			"unknown process manager %q", ProcessManagerFor(c)).
			WithHint("choose pm2 or direct")
	}
}

// ProcessManagerFor resolves which supervisor a site uses: its own setting, then
// the configured default, then PM2.
func ProcessManagerFor(c *Context) string {
	if c.Site.ProcessManager != "" {
		return c.Site.ProcessManager
	}
	if c.Cfg.Runtimes.NodeProcessManager != "" {
		return c.Cfg.Runtimes.NodeProcessManager
	}
	return ProcessManagerPM2
}

// directStartCommand runs node under systemd with nothing in between.
func (n Node) directStartCommand(ctx context.Context, c *Context) (string, unit.RenderOptions, error) {
	var opts unit.RenderOptions
	nodeBin, err := n.binary(c, "node")
	if err != nil {
		return "", opts, err
	}

	socket := c.Cfg.SocketPath(c.Site.Owner, c.Site.Domain)
	env := []string{"NODE_ENV=production"}
	if c.Site.Listen == "port" {
		env = append(env, fmt.Sprintf("PORT=%d", c.Site.Port), "HOST=127.0.0.1")
	} else {
		// A Node server has no standard way to be told about a socket, so both
		// spellings are provided: PORT is what most frameworks read, and
		// RATLINE_SOCKET is documented for anything that can listen on a path.
		env = append(env, "PORT="+socket, "RATLINE_SOCKET="+socket, "SOCKET_PATH="+socket)
		// Node creates the socket with the process umask, which at 0027 gives
		// 0640 — and connect(2) needs write permission, so nginx would get
		// EACCES and every request would be a 502. The unit already sets
		// UMask=0007 for socket sites; this waits for the socket and fixes the
		// mode as a belt-and-braces measure for a framework that chmods it
		// itself. It runs as the tenant (no '+' prefix), never root: the socket is
		// the tenant's own inode in a directory they own, so a symlink they swap in
		// cannot redirect the chmod onto a root-owned file — a non-root chmod can
		// only touch what the tenant already owns. Always exits 0.
		opts.ExecStartPost = []string{
			"/bin/sh -c 'for i in $(seq 1 100); do if [ -S " + socket + " ]; then chmod 0660 " + socket +
				"; exit 0; fi; sleep 0.1; done; exit 0'",
		}
	}
	opts.Environment = env
	// Most Node servers exit on SIGTERM without draining. SIGHUP is not a reload
	// either, so there is no ExecReload: `site reload` on a node site restarts it,
	// and says so.

	if c.Site.StartCommand != "" {
		parsed, err := system.ParseCommand(c.Site.StartCommand)
		if err != nil {
			return "", opts, err
		}
		for _, w := range parsed.Warnings {
			c.Log.Warn(w)
		}
		// `npm start` in a unit means an extra process between systemd and the
		// server, which breaks signal delivery and the main PID. It is accepted
		// because the specification asks for it, with a warning.
		if base := filepath.Base(parsed.Argv[0]); base == "npm" || base == "pnpm" || base == "yarn" || base == "bun" {
			c.Log.Warn("a package-manager start command puts an extra process between systemd and your server, "+
				"which can break graceful shutdown and restart counting",
				"advice", "prefer --entry with the file that calls listen()")
		}
		return shellSafeJoin(resolveProgram(parsed.Argv[0], c), parsed.Argv[1:]), opts, nil
	}

	if err := validate.NodeEntry(c.Site.Entry); err != nil {
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
	// A missing entry point is only a mistake once there is code to check it against.
	//
	// Creating the site before pushing any code is a normal workflow — a private repo, a
	// build produced by CI, an rsync from a laptop — and it was impossible: `site add`
	// warned "the application directory is empty … deploy your code, then run site
	// deploy", and then failed on the entry point and rolled the directory it had just
	// made back out of existence. The advice and the behaviour contradicted each other,
	// and --repo was the only way through.
	//
	// With code present the check still fires, because then a missing entry means --entry
	// does not match the project and that is worth catching before the unit is written.
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
	return shellSafeJoin(nodeBin, []string{entry}), opts, nil
}

// Reload is graceful under PM2 and impossible without it.
//
// PM2 cluster mode brings up a replacement worker, waits for it, and only then
// retires the old one — so no request is dropped. Running node directly has no
// equivalent signal, and claiming otherwise while dropping requests would be
// worse than refusing.
func (n Node) Reload(ctx context.Context, c *Context) error {
	if ProcessManagerFor(c) == ProcessManagerPM2 {
		return n.pm2Reload(ctx, c)
	}
	return rlerr.Preconditionf("a Node site running without PM2 cannot reload gracefully").
		WithHint("switch it to PM2, which reloads with no dropped requests:\n"+
			"        ratline site runtime %s --daemon pm2\n"+
			"      or accept a restart: ratline site restart %s\n"+
			"      the trade-off is in: ratline explain node", c.Site.Domain, c.Site.Domain)
}

// Teardown removes node_modules, and stops the site's PM2 daemon so it does not
// outlive the site it was supervising.
func (n Node) Teardown(ctx context.Context, c *Context) error {
	if ProcessManagerFor(c) == ProcessManagerPM2 && !c.DryRun {
		pm2, perr := n.pm2Binary(c)
		env, eerr := n.pm2Env(c)
		if perr == nil && eerr == nil {
			if _, kerr := c.Runner.Run(ctx, system.Cmd{
				Path: pm2, Args: []string{"kill"}, As: c.Identity,
				Env:     env,
				Mutates: true, OKExit: []int{1, 2},
			}); kerr != nil {
				c.Log.Debug("the PM2 daemon did not stop cleanly", "err", kerr)
			}
		}
	}
	modules := filepath.Join(c.AppDir, "node_modules")
	if c.DryRun || !system.Exists(modules) {
		return nil
	}
	if err := os.RemoveAll(modules); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing %s", modules)
	}
	return nil
}

// DetectEntry guesses a project's entry point, for the wizard's default.
func DetectEntry(appDir string) string {
	for _, candidate := range []string{
		"server.js", "index.js", "app.js", "main.js",
		"dist/server.js", "dist/index.js", "build/index.js",
		".next/standalone/server.js",
	} {
		if system.Exists(filepath.Join(appDir, candidate)) {
			return candidate
		}
	}
	// package.json's main field is the project's own answer.
	if data, err := system.ReadFileLimit(filepath.Join(appDir, "package.json"), 1<<20); err == nil {
		if main := extractJSONString(string(data), "main"); main != "" {
			return main
		}
	}
	return ""
}

// extractJSONString pulls one top-level string field out of package.json without
// unmarshalling the whole thing, which would fail on the many package.json files
// that contain comments or trailing commas.
func extractJSONString(body, field string) string {
	needle := `"` + field + `"`
	i := strings.Index(body, needle)
	if i < 0 {
		return ""
	}
	rest := body[i+len(needle):]
	colon := strings.IndexByte(rest, ':')
	if colon < 0 {
		return ""
	}
	rest = strings.TrimSpace(rest[colon+1:])
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	value := rest[:end]
	if strings.ContainsAny(value, "\\\n\r") {
		return ""
	}
	return value
}

// validateNodeEntry and resolveEntry are shared with the PM2 path, so both
// supervision modes resolve an entry point identically.
func validateNodeEntry(entry string) error { return validate.NodeEntry(entry) }

func resolveEntry(c *Context) (string, error) {
	entry, err := validate.ResolveWithin(c.AppDir, c.Site.Entry)
	if err != nil {
		// The file may not exist yet when a site is created before its code is
		// deployed, so fall back to the lexical check.
		if entry, err = validate.WithinRoot(c.AppDir, c.Site.Entry); err != nil {
			return "", err
		}
	}
	// Same rule as buildStartCommand: only a mistake once there is code to check against.
	if !c.DryRun && !system.Exists(entry) && HasApplicationCode(c.AppDir) {
		return "", rlerr.Preconditionf("the entry point %s does not exist", entry).
			WithHint("check --entry against your project layout; the application " +
				"directory has code in it but not that file")
	}
	return entry, nil
}
