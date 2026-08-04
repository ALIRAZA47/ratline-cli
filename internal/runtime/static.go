package runtime

import (
	"context"
	"os"
	"path/filepath"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// Static serves files straight from disk. There is no process to supervise, so
// there is no unit, no socket and nothing to health check beyond nginx itself.
type Static struct{}

func (Static) Name() string { return "static" }

func (Static) Provision(ctx context.Context, c *Context) error {
	root := orDefault(c.Site.DocRoot, "public")
	if err := validate.Subdir(root); err != nil {
		return err
	}
	target, err := validate.ResolveWithin(c.SiteDir, root)
	if err != nil {
		return err
	}
	if c.DryRun {
		c.Log.Info("would create the document root", "path", target)
		return nil
	}
	if _, err := system.EnsureDir(target, 0o750, c.Identity.UID, c.Identity.GID); err != nil {
		return err
	}

	// A placeholder means the site answers 200 the moment it is created, rather
	// than 403 from an empty directory, which reads as a misconfiguration.
	index := filepath.Join(target, orDefault(c.Site.IndexFile, "index.html"))
	if system.Exists(index) {
		return nil
	}
	body := "<!doctype html>\n<html lang=\"en\">\n<head>\n" +
		"<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">\n" +
		"<title>" + c.Site.Domain + "</title>\n</head>\n<body>\n" +
		"<h1>" + c.Site.Domain + "</h1>\n" +
		"<p>This site is provisioned and serving. Replace this file with your build output.</p>\n" +
		"<p>Document root: <code>" + target + "</code></p>\n" +
		"</body>\n</html>\n"
	return system.WriteFileAtomic(index, []byte(body), 0o640, c.Identity.UID, c.Identity.GID)
}

// Install does nothing: a static site has no dependencies unless it also has a
// build, which Build handles.
func (Static) Install(ctx context.Context, c *Context) error { return nil }

// Build runs an optional build and publishes its output into the document root.
func (s Static) Build(ctx context.Context, c *Context) error {
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
	c.Log.Info("building", "command", c.Site.BuildCommand, "dir", c.AppDir)
	if _, err := runAsOwner(ctx, c, system.Cmd{
		Path:    resolveProgram(parsed.Argv[0], c),
		Args:    parsed.Argv[1:],
		Timeout: c.Cfg.Runtimes.BuildTimeout.D(),
		Label:   "build",
	}); err != nil {
		return err
	}
	return s.publish(ctx, c)
}

// publish points the document root at the build output.
//
// A symlink rather than a copy: publishing is then atomic, the previous build
// stays on disk for a rollback, and nginx picks it up without a reload.
func (Static) publish(ctx context.Context, c *Context) error {
	if c.Site.BuildOutput == "" {
		return nil
	}
	if err := validate.Subdir(c.Site.BuildOutput); err != nil {
		return err
	}
	output, err := validate.ResolveWithin(c.AppDir, c.Site.BuildOutput)
	if err != nil {
		return err
	}
	if !c.DryRun && !system.IsDir(output) {
		return rlerr.Preconditionf("the build finished but %s does not exist", output).
			WithHint("check --build-output against what your build actually writes")
	}
	docRoot := filepath.Join(c.SiteDir, orDefault(c.Site.DocRoot, "public"))
	if c.DryRun {
		c.Log.Info("would publish the build output", "from", output, "to", docRoot)
		return nil
	}
	// Replacing a real directory with a symlink would destroy whatever an
	// operator put there, so it is only done when the document root is empty or
	// already a symlink.
	if system.IsDir(docRoot) && !system.IsSymlink(docRoot) {
		entries, err := os.ReadDir(docRoot)
		if err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", docRoot)
		}
		if len(entries) > 1 {
			return rlerr.Preconditionf("%s already holds files, so the build output was not published over it", docRoot).
				WithHint("either set --root to a directory ratline owns, or remove the contents of %s", docRoot)
		}
		if err := os.RemoveAll(docRoot); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "clearing %s", docRoot)
		}
	}
	if _, err := system.EnsureSymlink(output, docRoot); err != nil {
		return err
	}
	c.Log.Info("published the build output", "from", output, "to", docRoot)
	return nil
}

// StartCommand is empty: nginx serves the files itself.
func (Static) StartCommand(context.Context, *Context) (string, unit.RenderOptions, error) {
	return "", unit.RenderOptions{}, nil
}

func (Static) Reload(context.Context, *Context) error { return nil }

func (Static) Teardown(context.Context, *Context) error { return nil }

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
