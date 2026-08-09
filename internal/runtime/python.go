package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// Python runs a WSGI or ASGI application under Gunicorn in a per-site virtualenv.
type Python struct{}

func (Python) Name() string { return "python" }

// Provision creates the virtualenv with the managed interpreter.
//
// The venv is created as the site user, so every file in it is owned by the
// tenant and pip never runs as root.
func (p Python) Provision(ctx context.Context, c *Context) error {
	interpreter, err := p.interpreter(c)
	if err != nil {
		return err
	}
	if c.DryRun {
		c.Log.Info("would create the virtualenv", "path", c.VenvDir, "interpreter", interpreter)
		return nil
	}
	if system.Exists(filepath.Join(c.VenvDir, "bin", "python")) {
		c.Log.Debug("the virtualenv already exists", "path", c.VenvDir)
		return nil
	}
	if _, err := system.EnsureDir(c.VenvDir, 0o750, c.Identity.UID, c.Identity.GID); err != nil {
		return err
	}
	c.Log.Info("creating the virtualenv", "path", c.VenvDir, "python", interpreter)
	res, err := runAsOwner(ctx, c, system.Cmd{
		Path:    interpreter,
		Args:    []string{"-m", "venv", "--upgrade-deps", c.VenvDir},
		Dir:     c.SiteDir,
		Timeout: c.Cfg.Runtimes.InstallTimeout.D(),
		Label:   "create venv",
	})
	if err != nil {
		wrapped := rlerr.Wrap(err, rlerr.CodeExternal, "could not create the virtualenv")
		// --upgrade-deps makes pip reach the index, so the most common failure here is
		// not a missing package at all: it is no route to PyPI. Sending an operator to
		// `apt-get install python3-venv` when the venv was created fine and pip could
		// not resolve a hostname is a wrong answer that reads like a confident one.
		if unreachableIndex(res) {
			return wrapped.WithHint("pip could not reach the package index, so this is " +
				"a network or DNS problem on the server rather than a missing package. " +
				"Check outbound HTTPS and /etc/resolv.conf, then retry")
		}
		return wrapped.WithHint("the python3-venv package may be missing: apt-get install python3-venv")
	}
	return nil
}

// unreachableIndex reports whether a pip failure was a failure to reach the index.
func unreachableIndex(res *system.Result) bool {
	if res == nil {
		return false
	}
	out := strings.ToLower(res.Stdout + res.Stderr)
	for _, sign := range []string{
		"name or service not known",
		"no address associated with hostname",
		"temporary failure in name resolution",
		"max retries exceeded",
		"network is unreachable",
		"no route to host",
		"connection timed out",
	} {
		if strings.Contains(out, sign) {
			return true
		}
	}
	return false
}

// interpreter resolves the Python to build the venv from.
//
// A managed interpreter under /opt is preferred, because the system Python moves
// with distribution upgrades and a venv built against a replaced interpreter
// stops working in a way that is hard to diagnose.
func (Python) interpreter(c *Context) (string, error) {
	version := c.Site.PythonVersion
	if version == "" {
		version = c.Cfg.Runtimes.PythonDefault
	}
	if version != "" {
		if err := validate.PythonVersion(version); err != nil {
			return "", err
		}
		managed := filepath.Join(c.Cfg.Paths.RuntimesDir, "python", version, "bin", "python3")
		if system.Exists(managed) {
			return managed, nil
		}
		// deadsnakes and the distribution both install versioned binaries.
		for _, candidate := range []string{
			"/usr/bin/python" + version,
			"/usr/local/bin/python" + version,
		} {
			if system.Exists(candidate) {
				return candidate, nil
			}
		}
		if !c.DryRun {
			return "", rlerr.Preconditionf("Python %s is not installed", version).
				WithHint("install it with 'ratline runtime install python %s', or pass --python with a version that is present", version)
		}
	}
	for _, candidate := range []string{"/usr/bin/python3", "/usr/local/bin/python3"} {
		if system.Exists(candidate) {
			return candidate, nil
		}
	}
	if c.DryRun {
		return "/usr/bin/python3", nil
	}
	return "", rlerr.Preconditionf("no Python interpreter was found").
		WithHint("apt-get install python3 python3-venv, or 'ratline runtime install python 3.12'")
}

// Install installs the application's dependencies plus the server it runs under.
func (p Python) Install(ctx context.Context, c *Context) error {
	pip := filepath.Join(c.VenvDir, "bin", "pip")
	if c.DryRun {
		c.Log.Info("would install dependencies", "pip", pip)
		return nil
	}
	if !system.Exists(pip) {
		return rlerr.Preconditionf("%s does not exist, so the virtualenv is incomplete", pip).
			WithHint("re-run with --verbose to see what venv creation reported")
	}

	// The server goes in first: without it there is nothing to run, and a
	// requirements file that pins its own gunicorn simply wins on the next step.
	serverPackages := []string{"gunicorn"}
	if c.Site.ASGI {
		serverPackages = append(serverPackages, "uvicorn[standard]")
	}
	c.Log.Info("installing the application server", "packages", strings.Join(serverPackages, ", "))
	if _, err := runAsOwner(ctx, c, system.Cmd{
		Path:    pip,
		Args:    append([]string{"install", "--disable-pip-version-check", "--no-input"}, serverPackages...),
		Timeout: c.Cfg.Runtimes.InstallTimeout.D(),
		Label:   "pip install server",
	}); err != nil {
		return err
	}

	spec, err := p.detectDependencySpec(c)
	if err != nil {
		return err
	}
	if spec.kind == "" {
		c.Log.Warn("no requirements.txt, pyproject.toml or setup.py was found, so only the server was installed",
			"dir", c.AppDir)
		return nil
	}
	c.Log.Info("installing dependencies", "from", spec.detail)
	if _, err := runAsOwner(ctx, c, system.Cmd{
		Path:    pip,
		Args:    spec.args,
		Timeout: c.Cfg.Runtimes.InstallTimeout.D(),
		Label:   "pip install",
	}); err != nil {
		return rlerr.Wrap(err, rlerr.CodeExternal, "installing dependencies failed").
			WithHint("the output above is pip's; a missing system library usually needs a -dev package")
	}
	return nil
}

type dependencySpec struct {
	kind   string
	detail string
	args   []string
}

// detectDependencySpec works out how this project declares its dependencies.
func (Python) detectDependencySpec(c *Context) (dependencySpec, error) {
	if c.Site.Requirements != "" {
		if err := validate.Subdir(c.Site.Requirements); err != nil {
			return dependencySpec{}, err
		}
		path, err := validate.ResolveWithin(c.AppDir, c.Site.Requirements)
		if err != nil {
			// The application directory does not exist yet under --dry-run. The
			// lexical check still refuses traversal.
			if path, err = validate.WithinRoot(c.AppDir, c.Site.Requirements); err != nil {
				return dependencySpec{}, err
			}
		}
		if !system.Exists(path) && !c.DryRun {
			return dependencySpec{}, rlerr.Preconditionf("%s does not exist", path).
				WithHint("check --requirements against your project layout")
		}
		return dependencySpec{
			kind: "requirements", detail: path,
			args: []string{"install", "--disable-pip-version-check", "--no-input", "-r", path},
		}, nil
	}
	for _, name := range []string{"requirements.txt", "requirements/production.txt", "requirements/prod.txt"} {
		path := filepath.Join(c.AppDir, name)
		if system.Exists(path) {
			return dependencySpec{
				kind: "requirements", detail: path,
				args: []string{"install", "--disable-pip-version-check", "--no-input", "-r", path},
			}, nil
		}
	}
	// A pyproject or setup.py means the project installs itself, which also
	// covers uv and poetry projects when they export PEP 621 metadata.
	for _, name := range []string{"pyproject.toml", "setup.py"} {
		if system.Exists(filepath.Join(c.AppDir, name)) {
			return dependencySpec{
				kind: "project", detail: filepath.Join(c.AppDir, name),
				args: []string{"install", "--disable-pip-version-check", "--no-input", "."},
			}, nil
		}
	}
	return dependencySpec{}, nil
}

// Build runs an optional build step — Django's collectstatic, a Tailwind pass.
func (Python) Build(ctx context.Context, c *Context) error {
	if c.Site.BuildCommand == "" {
		return nil
	}
	parsed, err := system.ParseCommand(c.Site.BuildCommand)
	if err != nil {
		return err
	}
	c.Log.Info("building", "command", c.Site.BuildCommand)
	// The site's own variables, the same ones the service is started with. A Django
	// collectstatic reads DJANGO_SETTINGS_MODULE and usually a database URL; a build
	// without them fails on configuration the running site has.
	buildEnv := c.SiteEnv()
	buildEnv = append(buildEnv,
		"PATH="+filepath.Join(c.VenvDir, "bin")+":"+system.DefaultPath)
	_, err = runAsOwner(ctx, c, system.Cmd{
		Path:    resolveProgram(parsed.Argv[0], c),
		Args:    parsed.Argv[1:],
		Env:     system.UserEnv(c.Identity, buildEnv...),
		Timeout: c.Cfg.Runtimes.BuildTimeout.D(),
		Label:   "build",
	})
	return err
}

// StartCommand builds the Gunicorn invocation.
//
// Always an absolute path into the venv: relying on PATH would mean the unit
// depends on shell configuration that systemd does not read.
func (p Python) StartCommand(ctx context.Context, c *Context) (string, unit.RenderOptions, error) {
	var opts unit.RenderOptions
	if err := validate.AppModule(c.Site.AppModule); err != nil {
		return "", opts, err
	}

	server := c.Site.AppServer
	if server == "" {
		server = "gunicorn"
	}
	bind := "unix:" + c.Cfg.SocketPath(c.Site.Owner, c.Site.Domain)
	if c.Site.Listen == "port" {
		bind = fmt.Sprintf("127.0.0.1:%d", c.Site.Port)
	}
	workers := c.Site.Workers
	if workers <= 0 {
		workers = DefaultWorkers(c.Cfg.Defaults.WorkerCap)
	}

	accessLog := filepath.Join(c.LogDir, "access.log")
	errorLog := filepath.Join(c.LogDir, "app.log")

	switch server {
	case "gunicorn":
		bin := filepath.Join(c.VenvDir, "bin", "gunicorn")
		args := []string{
			"--workers", fmt.Sprint(workers),
			"--bind", bind,
			"--access-logfile", accessLog,
			"--error-logfile", errorLog,
			"--capture-output",
			// Recycle workers to bound the effect of a slow leak, with jitter so
			// they do not all restart at once.
			"--max-requests", "2000",
			"--max-requests-jitter", "200",
			"--graceful-timeout", "30",
			"--timeout", "60",
		}
		if c.Site.ASGI {
			args = append(args, "--worker-class", "uvicorn.workers.UvicornWorker")
		}
		if strings.HasPrefix(bind, "unix:") {
			// connect(2) on a Unix socket needs write permission on the inode.
			// Gunicorn applies this umask when it creates the socket, giving 0660
			// so that nginx — a member of the tenant's group — can connect. At the
			// 0027 used elsewhere the socket is 0640 and nginx gets EACCES, which
			// surfaces as a 502 with nothing useful in the log.
			args = append(args, "--umask", "0117")
		}
		args = append(args, c.Site.AppModule)
		opts.ExecReload = "/bin/kill -HUP $MAINPID"
		return shellSafeJoin(bin, args), opts, nil

	case "uvicorn":
		bin := filepath.Join(c.VenvDir, "bin", "uvicorn")
		args := []string{"--workers", fmt.Sprint(workers)}
		if strings.HasPrefix(bind, "unix:") {
			args = append(args, "--uds", strings.TrimPrefix(bind, "unix:"))
			// uvicorn has no umask option, so the socket it creates is 0640
			// under any sane umask and nginx cannot connect. A chmod after the
			// socket appears is the fix; it is idempotent and bounded. It runs as
			// the tenant (no '+'), never root: the socket is the tenant's own inode,
			// so a symlink they swap in cannot redirect the chmod onto a root file.
			opts.ExecStartPost = []string{
				"/bin/sh -c 'for i in $(seq 1 50); do [ -S " + strings.TrimPrefix(bind, "unix:") +
					" ] && chmod 0660 " + strings.TrimPrefix(bind, "unix:") + " && exit 0; sleep 0.1; done; exit 0'",
			}
		} else {
			host, port, _ := strings.Cut(bind, ":")
			args = append(args, "--host", host, "--port", port)
		}
		args = append(args, c.Site.AppModule)
		return shellSafeJoin(bin, args), opts, nil

	default:
		return "", opts, rlerr.Usagef("unknown application server %q", server).
			WithHint("choose gunicorn or uvicorn")
	}
}

// Reload asks Gunicorn for a graceful worker restart.
//
// SIGHUP replaces the workers one at a time while the master keeps the listening
// socket open, so no connection is refused and no request in flight is dropped.
func (Python) Reload(ctx context.Context, c *Context) error {
	if c.Site.AppServer == "uvicorn" {
		return rlerr.Preconditionf("uvicorn cannot reload without dropping connections").
			WithHint("use --server gunicorn for zero-downtime reloads, or accept a restart with 'ratline site restart'")
	}
	return nil // the unit's ExecReload does the work
}

// Teardown removes the virtualenv, which is the only thing Provision created
// that a plain directory removal would leave behind if the site directory were
// kept.
func (Python) Teardown(ctx context.Context, c *Context) error {
	if c.DryRun || !system.Exists(c.VenvDir) {
		return nil
	}
	if err := os.RemoveAll(c.VenvDir); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing %s", c.VenvDir)
	}
	return nil
}

// DefaultWorkers is Gunicorn's own recommendation, capped so that a large host
// does not give one small site sixty-five processes.
func DefaultWorkers(cap int) int {
	n := 2*runtime.NumCPU() + 1
	if cap > 0 && n > cap {
		return cap
	}
	return n
}

// DetectASGI guesses whether a project is ASGI, so the wizard and `site add` can
// pre-select the right worker class.
func DetectASGI(appDir, appModule string) bool {
	module, _, _ := strings.Cut(appModule, ":")
	path := filepath.Join(appDir, strings.ReplaceAll(module, ".", string(filepath.Separator))+".py")
	data, err := system.ReadFileLimit(path, 1<<20)
	if err != nil {
		// A Django project names its WSGI callable in a wsgi module, which is a
		// strong signal in the other direction.
		return strings.Contains(appModule, "asgi")
	}
	body := string(data)
	for _, marker := range []string{"FastAPI(", "Starlette(", "from fastapi", "from starlette", "import fastapi", "ASGIApp"} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

// shellSafeJoin renders an ExecStart line.
//
// systemd parses ExecStart itself and splits on whitespace, so an argument
// containing a space must be quoted. Nothing here is passed to a shell.
func shellSafeJoin(bin string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, bin)
	for _, a := range args {
		if strings.ContainsAny(a, " \t\"'") {
			parts = append(parts, `"`+strings.ReplaceAll(a, `"`, `\"`)+`"`)
			continue
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// resolveProgram turns a build command's first word into an absolute path.
func resolveProgram(program string, c *Context) string {
	if filepath.IsAbs(program) {
		return program
	}
	// A venv or node_modules binary is what a project almost always means, then the
	// managed runtime the site is pinned to — that is where its npm and its python live —
	// and only then the system PATH.
	dirs := []string{
		filepath.Join(c.VenvDir, "bin"),
		filepath.Join(c.AppDir, "node_modules", ".bin"),
	}
	dirs = append(dirs, c.RuntimeBinDirs()...)
	for _, dir := range dirs {
		candidate := filepath.Join(dir, program)
		if system.Exists(candidate) {
			return candidate
		}
	}
	if strings.HasPrefix(program, "./") || strings.Contains(program, "/") {
		return filepath.Join(c.AppDir, program)
	}
	for _, dir := range strings.Split(system.DefaultPath, ":") {
		candidate := filepath.Join(dir, program)
		if system.Exists(candidate) {
			return candidate
		}
	}
	return filepath.Join("/usr/bin", program)
}
