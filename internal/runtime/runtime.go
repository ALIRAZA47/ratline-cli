// Package runtime is the seam between a site and whatever actually serves it.
//
// Adding PHP-FPM, Go or Ruby later is a new file implementing this interface, not
// a change to the site lifecycle.
package runtime

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/sitepath"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
)

// Runtime is what every site type must be able to do.
type Runtime interface {
	// Name is the value of --runtime.
	Name() string

	// Provision prepares the site directory: a virtualenv, a node_modules, or
	// nothing at all for a static site.
	Provision(context.Context, *Context) error

	// Install runs the dependency installer as the site user.
	Install(context.Context, *Context) error

	// Build runs the build command, if the site has one.
	Build(context.Context, *Context) error

	// StartCommand returns the absolute ExecStart line and the unit options that
	// go with it. A static site returns an empty command: nginx serves it and
	// there is nothing to supervise.
	StartCommand(context.Context, *Context) (string, unit.RenderOptions, error)

	// Reload is the zero-downtime reload, where the runtime supports one.
	Reload(context.Context, *Context) error

	// Teardown removes anything Provision created that is not inside the site
	// directory.
	Teardown(context.Context, *Context) error
}

// Context is everything a runtime needs, resolved once by the site lifecycle.
type Context struct {
	Cfg      *config.Config
	Log      *log.Logger
	Runner   system.Runner
	Site     *state.Site
	Identity *system.Identity
	DryRun   bool

	// Directories, all inside the owner's home.
	SiteDir string
	AppDir  string
	LogDir  string
	TmpDir  string
	VenvDir string
}

// NewContext resolves the paths for a site.
func NewContext(cfg *config.Config, lg *log.Logger, runner system.Runner, site *state.Site, id *system.Identity, dryRun bool) *Context {
	siteDir := cfg.SiteDir(site.Owner, site.Domain)
	return &Context{
		Cfg: cfg, Log: lg, Runner: runner, Site: site, Identity: id, DryRun: dryRun,
		SiteDir: siteDir,
		AppDir:  filepath.Join(siteDir, "app"),
		LogDir:  filepath.Join(siteDir, "logs"),
		TmpDir:  filepath.Join(siteDir, "tmp"),
		VenvDir: filepath.Join(siteDir, "venv"),
	}
}

// For returns the runtime implementation for a site.
func For(name string) (Runtime, error) {
	switch name {
	case "static":
		return &Static{}, nil
	case "node":
		return &Node{}, nil
	case "bun":
		return &Bun{}, nil
	case "python":
		return &Python{}, nil
	default:
		return nil, rlerr.Usagef("unknown runtime %q", name).
			WithHint("choose static, node, bun or python")
	}
}

// Names lists the available runtimes.
func Names() []string { return []string{"static", "node", "bun", "python"} }

// runAsOwner runs a command as the site user, never as root.
//
// A postinstall script in a dependency tree is code the tenant chose to trust;
// running it as root would make every npm install a route to compromising the
// whole server.
func runAsOwner(ctx context.Context, c *Context, cmd system.Cmd) (*system.Result, error) {
	cmd.As = c.Identity
	if cmd.Dir == "" {
		cmd.Dir = c.AppDir
	}
	// Streaming exists so that a slow install does not look like a hang: it relays the
	// child's output through the logger, framed and labelled. A caller already taking
	// the output raw — `site exec`, whose output *is* the product — wants it once, not
	// once plainly and once again with a log prefix in front of every line.
	if cmd.Stdout == nil && cmd.Stderr == nil {
		cmd.Stream = true
	}
	cmd.Mutates = true
	return c.Runner.Run(ctx, cmd)
}

// HasApplicationCode reports whether anything has been deployed into the application
// directory yet.
//
// Dotfiles do not count: a bare git clone that failed, or a stray .DS_Store, is not code.
// The same rule the site layer uses to decide whether to install and build, so the two
// cannot disagree about whether a site is still waiting for its first deploy.
func HasApplicationCode(appDir string) bool {
	entries, err := os.ReadDir(appDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			return true
		}
	}
	return false
}

// RuntimeBinDirs is the bin directory of the managed runtime this site is pinned to.
//
// A site created with --node 24 gets its node from /opt/ratline/runtimes/node/24/bin, and
// so must its npm. resolveProgram did not know that: it searched the venv, node_modules,
// the default PATH, and then gave up on /usr/bin/<program> — so `npm install` on a server
// with no system Node failed with
//
//	could not run /usr/bin/npm install: fork/exec /usr/bin/npm: no such file or directory
//
// which reads as a missing package rather than as ratline looking in the wrong place. The
// managed runtimes exist precisely so a server does not need a system Node.
//
// The answer itself lives in internal/sitepath, because a site's job unit needs the same
// one and internal/unit cannot import this package.
func (c *Context) RuntimeBinDirs() []string {
	return sitepath.RuntimeBinDirs(c.Cfg, c.Site)
}

// SiteEnv reads the site's .env, the same file systemd hands the service.
//
// The build needs it as much as the service does. Next.js evaluates route modules while
// collecting page data, static generation reads whatever the pages read, and NEXT_PUBLIC_*
// values are inlined at build time — so a build without the environment fails on code that
// works perfectly at run time. On this project it failed with
//
//	Error: MONGODB_URI is not set
//
// from a module that the service would have started with quite happily.
//
// Parsed the way systemd's EnvironmentFile parser does, which is not a shell: KEY=VALUE
// lines, no expansion, no command substitution. Anything else is skipped rather than
// guessed at, because a build that receives a half-interpreted value is worse than one
// that receives nothing.
func (c *Context) SiteEnv() []string {
	// Read as root, on the tenant's behalf, from a directory the tenant owns — so only a
	// regular file the tenant owns is read, never a link they pointed at somebody else's
	// .env. Refusing is said out loud: the build is about to run without the values it
	// would otherwise have had, and a silent nil would look like an empty file.
	body, err := system.ReadFileNoFollow(filepath.Join(c.SiteDir, ".env"), 1<<20, c.Identity.UID)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			c.Log.Warn("the site's .env was not loaded", "err", err)
		}
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		// PATH is ratline's to decide: the build must use the managed runtime, and a
		// value from .env would quietly send it to a different interpreter.
		if key == "PATH" {
			continue
		}
		out = append(out, key+"="+value)
	}
	return out
}

// execPath is the PATH anything ratline runs as a tenant gets.
//
// It is programSearchDirs in the same order, with the system path behind them, so that
// the program ratline resolves is the program a child process of it would find. Those
// two disagreeing is a whole class of bug: a python site's hook resolved to the venv's
// `python` while any subprocess it spawned got /usr/bin/python and a different set of
// packages, which fails a long way from the cause.
//
// The composition is sitepath's, not a second one that happens to agree today: the same
// string is written into every job and worker unit by internal/unit.
func execPath(c *Context) string {
	return sitepath.PATH(c.Cfg, c.Site, c.SiteDir)
}

// tenantEnv is the environment for anything run as a site's tenant: the site's own
// variables, whatever the caller adds, and ratline's PATH last.
func tenantEnv(c *Context, extra ...string) []string {
	env := append(c.SiteEnv(), extra...)
	env = append(env, "PATH="+execPath(c))
	return system.UserEnv(c.Identity, env...)
}

// RunHook runs a site's deploy hook as the tenant.
//
// The same conditions as the build command: the tenant's identity, the application
// directory, the site's own environment, and ratline's PATH appended last so a hook cannot
// redirect itself to a different interpreter.
//
// Exported because the hook belongs to the deploy chain in internal/cli rather than to a
// runtime — a static site has no runtime worth speaking of and can still want a hook.
func RunHook(ctx context.Context, c *Context, which, command string) error {
	parsed, err := system.ParseCommand(command)
	if err != nil {
		return err
	}
	c.Log.Info("running the "+which+" hook", "command", command)
	_, err = runAsOwner(ctx, c, system.Cmd{
		Path: resolveProgram(parsed.Argv[0], c),
		Args: parsed.Argv[1:],
		Env: tenantEnv(c,
			"RATLINE_HOOK="+which,
			"RATLINE_DOMAIN="+c.Site.Domain),
		Timeout: c.Cfg.Runtimes.BuildTimeout.D(),
		Label:   which + " hook",
	})
	return err
}
