package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/runtime"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

func newSiteLogsCommand(g *Globals) *cobra.Command {
	var (
		app, access, errorLog bool
		journal               bool
		follow                bool
		lines                 int
	)
	cmd := &cobra.Command{
		Use:   "logs <domain>",
		Short: "Show a site's application, access or error log",
		Long: "Where the application log comes from depends on how the site is supervised.\n\n" +
			"Under PM2 — the default for node — the application's stdout is captured by\n" +
			"PM2 into logs/app.log, and the journal holds only PM2's own messages. So\n" +
			"--app reads the file, and --journal is there for when the question is about\n" +
			"the unit itself: a failed start, or an OOM kill.\n\n" +
			"Without PM2 the application writes straight to the journal, and --app reads\n" +
			"that.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.siteManager(cmd.Context())
			if err != nil {
				return err
			}
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			site, err := st.FindSiteByName(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			paths := mgr.LogPaths(site)

			// PM2 captures its workers' stdout into out_file, so on a PM2 site the
			// journal has PM2's messages and not the application's. Reading the
			// journal there would show an operator an empty screen while the app was
			// logging happily to a file two directories away.
			report, _ := mgr.ProcessReport(cmd.Context(), site)
			pm2Supervised := report != nil

			which := "app"
			switch {
			case access:
				which = "access"
			case errorLog:
				which = "error"
			case app:
				which = "app"
			case site.Runtime == "static":
				// A static site has no application log, so the access log is
				// what an operator actually wants.
				which = "access"
			}

			// A dynamic site's stdout goes to the journal as well as its own log,
			// and journalctl is the only way to follow a crash loop.
			if site.Dynamic() && (journal || (which == "app" && !pm2Supervised)) {
				unitName := mgr.UnitName(site)
				jargs := []string{"-u", unitName, "-n", fmt.Sprint(lines), "--no-pager", "--output=short-iso"}
				if follow {
					jargs = append(jargs, "--follow")
				}
				path, err := g.Bins.Path("journalctl")
				if err != nil {
					return err
				}
				return execInPlace(cmd.Context(), g, path, jargs)
			}

			path := paths[which]
			if !system.Exists(path) {
				err := rlerr.Preconditionf("no %s log at %s yet", which, path)
				if which == "app" && pm2Supervised {
					err = err.WithHint("PM2 creates it on the first line the application writes; "+
						"for the unit's own messages: ratline site logs %s --journal", site.Domain)
				}
				return err
			}
			if follow {
				tail, err := g.Bins.Path("tail")
				if err != nil {
					// tail is not in the registry by default; read the file once
					// rather than failing outright.
					g.Log.Warn("tail is unavailable, so the log cannot be followed")
					return printTail(g, path, lines)
				}
				return execInPlace(cmd.Context(), g, tail, []string{"-n", fmt.Sprint(lines), "-f", path})
			}
			return printTail(g, path, lines)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&app, "app", false, "The application log (the default for a dynamic site)")
	f.BoolVar(&access, "access", false, "The nginx access log")
	f.BoolVar(&errorLog, "error", false, "The nginx error log")
	f.BoolVar(&journal, "journal", false, "The systemd journal for the unit rather than the application's own log")
	f.BoolVar(&follow, "follow", false, "Keep printing as lines arrive")
	f.IntVar(&lines, "lines", 100, "How many lines to show")
	return cmd
}

// execInPlace runs a viewer with the process's own streams, so following a log
// behaves like running tail directly.
func execInPlace(ctx context.Context, g *Globals, path string, args []string) error {
	res, err := g.Runner.Run(ctx, system.Cmd{
		Path: path, Args: args, Stream: false, Timeout: 24 * time.Hour,
		OKExit: []int{1, 130},
	})
	if res != nil && res.Stdout != "" {
		fmt.Fprint(g.Stdout, res.Stdout)
	}
	return err
}

func printTail(g *Globals, path string, n int) error {
	data, err := system.ReadFileLimit(path, 64<<20)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for _, l := range lines {
		fmt.Fprintln(g.Stdout, l)
	}
	return nil
}

func newSiteDeployCommand(g *Globals) *cobra.Command {
	var (
		pull, install, build, migrate, collectstatic, restart bool
	)
	cmd := &cobra.Command{
		Use:   "deploy <domain>",
		Short: "Pull, install, build, migrate and restart, rolling back if it fails",
		Args:  cobra.ExactArgs(1),
		Long: "Runs the chain you ask for, health checks the result, and reverts to the previous\n" +
			"commit if the application does not come back healthy.\n\n" +
			"With no step flags, a sensible default chain runs: pull, install, build, restart.",
		Example: "  ratline site deploy api.example.com\n" +
			"  ratline site deploy api.example.com --pull --install --migrate --collectstatic --restart",
		RunE: func(cmd *cobra.Command, args []string) error {
			// No flags at all means "do the usual thing", which is what an
			// operator types when they just want their change live.
			if !pull && !install && !build && !migrate && !collectstatic && !restart {
				pull, install, build, restart = true, true, true, true
			}
			return g.deploy(cmd.Context(), args[0], deployOptions{
				Pull: pull, Install: install, Build: build,
				Migrate: migrate, CollectStatic: collectstatic, Restart: restart,
			})
		},
	}
	f := cmd.Flags()
	f.BoolVar(&pull, "pull", false, "git pull in the application directory")
	f.BoolVar(&install, "install", false, "Install dependencies")
	f.BoolVar(&build, "build", false, "Run the build command")
	f.BoolVar(&migrate, "migrate", false, "Run Django migrations (needs --manage-py)")
	f.BoolVar(&collectstatic, "collectstatic", false, "Run Django collectstatic (needs --manage-py)")
	f.BoolVar(&restart, "restart", false, "Restart the service and wait for health")
	return Mutating(cmd)
}

type deployOptions struct {
	Pull          bool
	Install       bool
	Build         bool
	Migrate       bool
	CollectStatic bool
	Restart       bool
}

// deploy runs the deployment chain, keeping the previous commit addressable so a
// failure can be reverted.
func (g *Globals) deploy(ctx context.Context, name string, opts deployOptions) error {
	mgr, err := g.siteManager(ctx)
	if err != nil {
		return err
	}
	st, err := g.Store(ctx)
	if err != nil {
		return err
	}
	site, err := st.FindSiteByName(ctx, name)
	if err != nil {
		return err
	}
	id, err := system.LookupIdentity(site.Owner)
	if err != nil {
		return err
	}
	rt, err := runtime.For(site.Runtime)
	if err != nil {
		return err
	}
	rc := runtime.NewContext(g.Cfg, g.Log, g.Runner, site, id, g.DryRun)

	deployID, err := st.StartDeployment(ctx, site.Domain)
	if err != nil {
		return err
	}
	record := &state.Deployment{ID: deployID, Domain: site.Domain}

	// The commit that is currently serving. Reverting to it is the rollback, and
	// it is captured before anything changes.
	previousSHA := gitSHA(ctx, g, rc.AppDir, id)
	record.GitSHA = previousSHA

	finish := func(err error) error {
		record.OK = err == nil
		if err != nil {
			record.Error = err.Error()
		}
		if ferr := st.FinishDeployment(ctx, record); ferr != nil {
			g.Log.Debug("could not close the deployment record", "err", ferr)
		}
		return err
	}

	steps := []struct {
		name string
		run  bool
		fn   func() error
	}{
		{"pull", opts.Pull, func() error { return g.gitPull(ctx, rc, id) }},
		{"install", opts.Install, func() error { return rt.Install(ctx, rc) }},
		{"build", opts.Build, func() error { return rt.Build(ctx, rc) }},
		{"migrate", opts.Migrate, func() error { return g.manage(ctx, rc, id, "migrate", "--no-input") }},
		{"collectstatic", opts.CollectStatic, func() error { return g.manage(ctx, rc, id, "collectstatic", "--no-input", "--clear") }},
	}
	for _, s := range steps {
		if !s.run {
			continue
		}
		g.Log.Info("deploy step", "step", s.name, "domain", site.Domain)
		if err := s.fn(); err != nil {
			// Nothing has been restarted yet, so the previous version is still
			// serving and there is nothing to revert.
			g.Log.Error("the deploy step failed; the previous version is still serving", "step", s.name)
			return finish(rlerr.Wrap(err, rlerr.CodeOf(err), "the %s step failed", s.name))
		}
		record.Steps = append(record.Steps, s.name)
	}

	if opts.Restart && site.Dynamic() {
		health, err := mgr.Control(ctx, site.Domain, "restart")
		if err != nil {
			// This is the case that matters: the new code is on disk and the
			// service will not come up. Revert and restart, so the site is
			// serving again before the error is reported.
			record.Steps = append(record.Steps, "restart")
			if previousSHA != "" && opts.Pull {
				g.Log.Warn("the application did not become healthy; reverting to the previous commit",
					"sha", shortSHA(previousSHA))
				if rerr := g.gitReset(ctx, rc, id, previousSHA); rerr != nil {
					return finish(rlerr.Wrap(err, rlerr.CodeRollbackFailed,
						"the deploy failed and the revert also failed (%v)", rerr))
				}
				if _, ierr := mgr.Control(ctx, site.Domain, "restart"); ierr != nil {
					return finish(rlerr.Wrap(err, rlerr.CodeRollbackFailed,
						"the deploy failed, and the previous version did not restart either (%v)", ierr))
				}
				record.RolledBack = true
				g.Log.Info("the previous version is serving again", "sha", shortSHA(previousSHA))
				return finish(rlerr.Wrap(err, rlerr.CodeUnhealthy,
					"the deploy was reverted to %s, which is serving again", shortSHA(previousSHA)))
			}
			return finish(err)
		}
		record.Steps = append(record.Steps, "restart")
		record.Health = health
	}

	newSHA := gitSHA(ctx, g, rc.AppDir, id)
	if newSHA != "" {
		record.GitSHA = newSHA
	}
	if err := st.TouchDeploy(ctx, site.Domain); err != nil {
		return finish(err)
	}
	if err := finish(nil); err != nil {
		return err
	}

	if g.JSON {
		return g.EmitJSON(map[string]any{"domain": site.Domain, "steps": record.Steps,
			"git_sha": record.GitSHA, "health": record.Health, "ok": true})
	}
	g.Printf("Deployed %s\n", site.Domain)
	pairs := [][2]string{{"steps", strings.Join(record.Steps, ", ")}}
	if record.GitSHA != "" {
		pairs = append(pairs, [2]string{"commit", shortSHA(record.GitSHA)})
	}
	if record.Health != "" {
		pairs = append(pairs, [2]string{"health", record.Health})
	}
	return g.Fields(pairs...)
}

func (g *Globals) gitPull(ctx context.Context, rc *runtime.Context, id *system.Identity) error {
	if !system.IsDir(filepath.Join(rc.AppDir, ".git")) {
		return rlerr.Preconditionf("%s is not a git repository, so there is nothing to pull", rc.AppDir).
			WithHint("create the site with --repo, or clone into that directory yourself")
	}
	branch := rc.Site.Branch
	if branch == "" {
		branch = "main"
	}
	for _, args := range [][]string{
		{"fetch", "--depth", "1", "origin", branch},
		// A hard reset rather than a merge: a server working copy has no local
		// commits worth preserving, and a merge conflict mid-deploy is the worst
		// possible state to be in.
		{"reset", "--hard", "origin/" + branch},
		{"clean", "-fd"},
	} {
		if _, err := g.Runner.Run(ctx, system.Cmd{
			Name: "git", Args: args, As: id, Dir: rc.AppDir,
			Mutates: true, Stream: true, Timeout: 10 * time.Minute, Label: "git " + args[0],
		}); err != nil {
			return err
		}
	}
	return nil
}

func (g *Globals) gitReset(ctx context.Context, rc *runtime.Context, id *system.Identity, sha string) error {
	_, err := g.Runner.Run(ctx, system.Cmd{
		Name: "git", Args: []string{"reset", "--hard", sha}, As: id, Dir: rc.AppDir,
		Mutates: true, Timeout: 5 * time.Minute, Label: "git reset",
	})
	return err
}

func gitSHA(ctx context.Context, g *Globals, dir string, id *system.Identity) string {
	if !system.IsDir(filepath.Join(dir, ".git")) {
		return ""
	}
	res, err := g.Runner.Run(ctx, system.Cmd{
		Name: "git", Args: []string{"rev-parse", "HEAD"}, As: id, Dir: dir, OKExit: []int{128},
	})
	if err != nil || res == nil {
		return ""
	}
	return strings.TrimSpace(res.Out())
}

// manage runs a Django management command in the site's virtualenv.
func (g *Globals) manage(ctx context.Context, rc *runtime.Context, id *system.Identity, args ...string) error {
	if rc.Site.Runtime != "python" {
		return rlerr.Usagef("--%s only applies to a python site", args[0])
	}
	if rc.Site.ManagePy == "" {
		return rlerr.Usagef("--%s needs the site to have been created with --manage-py", args[0]).
			WithHint("re-create the site with --manage-py manage.py, or run the command yourself as %s", rc.Site.Owner)
	}
	python := filepath.Join(rc.VenvDir, "bin", "python")
	if !system.Exists(python) && !g.DryRun {
		return rlerr.Preconditionf("%s does not exist", python)
	}
	managePy := filepath.Join(rc.AppDir, rc.Site.ManagePy)
	_, err := g.Runner.Run(ctx, system.Cmd{
		Path: python, Args: append([]string{managePy}, args...),
		As: id, Dir: rc.AppDir, Mutates: true, Stream: true,
		Timeout: 15 * time.Minute, Label: "manage.py " + args[0],
	})
	return err
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

var _ = os.Remove
