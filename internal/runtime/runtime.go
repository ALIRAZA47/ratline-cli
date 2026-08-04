// Package runtime is the seam between a site and whatever actually serves it.
//
// Adding PHP-FPM, Go or Ruby later is a new file implementing this interface, not
// a change to the site lifecycle.
package runtime

import (
	"context"
	"path/filepath"

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
