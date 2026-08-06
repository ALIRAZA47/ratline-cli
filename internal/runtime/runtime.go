// Package runtime is the seam between a site and whatever actually serves it.
//
// Adding PHP-FPM, Go or Ruby later is a new file implementing this interface, not
// a change to the site lifecycle.
package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
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
	case "python":
		return &Python{}, nil
	default:
		return nil, rlerr.Usagef("unknown runtime %q", name).
			WithHint("choose static, node or python")
	}
}

// Names lists the available runtimes.
func Names() []string { return []string{"static", "node", "python"} }

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
	cmd.Stream = true
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

// RuntimeBinDirs are the bin directories of the managed runtime this site is pinned to.
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
func (c *Context) RuntimeBinDirs() []string {
	var out []string
	switch c.Site.Runtime {
	case "node":
		version := c.Site.NodeVersion
		if version == "" {
			version = c.Cfg.Runtimes.NodeDefault
		}
		if version != "" {
			out = append(out, filepath.Join(c.Cfg.Paths.RuntimesDir, "node", version, "bin"))
		}
	case "python":
		version := c.Site.PythonVersion
		if version == "" {
			version = c.Cfg.Runtimes.PythonDefault
		}
		if version != "" {
			out = append(out, filepath.Join(c.Cfg.Paths.RuntimesDir, "python", version, "bin"))
		}
	}
	return out
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
	body, err := os.ReadFile(filepath.Join(c.SiteDir, ".env"))
	if err != nil {
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
