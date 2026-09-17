package cli

import (
	"context"
	"fmt"
	"io"
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

// newLogsCommand is `ratline logs <domain>`, the top-level shortcut for
// `ratline site logs <domain>`.
//
// Reading a site's log is the thing an operator does most often and least
// deliberately — usually right after something broke — so it earns a verb at the top
// level rather than one buried under `site`. It is the same command: built by the
// same function, with the same flags, so the two cannot drift.
func newLogsCommand(g *Globals) *cobra.Command {
	cmd := newSiteLogsCommand(g)
	cmd.GroupID = GroupSites
	cmd.Short = "Tail a site's log (the same as 'site logs')"
	return cmd
}

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
			//
			// Asked of the *configuration*, not of the running daemon. This used to be
			// `mgr.ProcessReport(...) != nil`, which queries PM2 itself — so a site
			// whose PM2 was unreachable (a failed deploy that took node_modules with
			// it, a crashed daemon) was read as "not PM2 supervised" and sent here to
			// the journal, where a PM2 site has nothing but PM2's own messages. The
			// screen came up empty at precisely the moment somebody needed it.
			pm2Supervised := mgr.UsesPM2(site)

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
			// nginx's logs live under root's directory; the application's is the tenant's
			// own file inside the tenant's tree. That one is opened only if the tenant owns
			// it and neither follows a symlink: this runs as root, and through the panel it
			// would otherwise print any file a tenant cared to link a log to.
			owner := system.KeepUnchanged
			if which == "app" {
				id, err := system.LookupIdentity(site.Owner)
				if err != nil {
					return err
				}
				owner = id.UID
			}
			f, err := system.OpenFileNoFollow(path, owner)
			if err != nil {
				return err
			}
			defer f.Close()
			return tailLog(cmd.Context(), g, f, lines, follow)
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

// execInPlace runs a viewer wired to the operator's own streams, so following
// a log behaves like running tail directly: lines appear as they are written,
// not when the child finally exits.
func execInPlace(ctx context.Context, g *Globals, path string, args []string) error {
	_, err := g.Runner.Run(ctx, system.Cmd{
		Path: path, Args: args, Timeout: 24 * time.Hour,
		Stdout: g.Stdout, Stderr: g.Stderr,
		OKExit: []int{1, 130},
	})
	if ctx.Err() != nil {
		//nolint:nilerr // Ctrl+C is how an operator ends a --follow; the cancellation is the intent, not a failure
		return nil
	}
	return err
}

// tailLog prints the last n lines of an open log and, with follow, keeps printing as it
// grows until the context ends.
//
// In-process rather than exec'ing tail: tail opens the path by name, as root, and
// follows a symlink there. The descriptor here was opened with O_NOFOLLOW and checked,
// and nothing a tenant does to the path afterwards changes what it reads.
func tailLog(ctx context.Context, g *Globals, f *os.File, n int, follow bool) error {
	const window = 64 << 20
	if fi, err := f.Stat(); err == nil && fi.Size() > window {
		// Only the tail of a huge log, the same way tail itself behaves.
		if _, err := f.Seek(fi.Size()-window, io.SeekStart); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "seeking in %s", f.Name())
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", f.Name())
	}
	if trimmed := strings.TrimRight(string(data), "\n"); trimmed != "" {
		lines := strings.Split(trimmed, "\n")
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
		for _, l := range lines {
			fmt.Fprintln(g.Stdout, l)
		}
	}
	if !follow {
		return nil
	}
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	buf := make([]byte, 64<<10)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		for {
			k, err := f.Read(buf)
			if k > 0 {
				_, _ = g.Stdout.Write(buf[:k])
			}
			if err != nil {
				break
			}
		}
	}
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
		// After the pull, deliberately. A hook script lives in the repository, so running
		// it before the pull would run the previous deploy's version of it — which is the
		// one thing somebody editing a hook would not expect. Before install and build, so
		// it can do the work those depend on.
		{"pre-deploy hook", site.PreDeployCommand != "", func() error {
			return runtime.RunHook(ctx, rc, "pre-deploy", site.PreDeployCommand)
		}},
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

			// Whether to revert is decided by what is safe, not by whether this
			// deploy did the pulling.
			//
			// It used to require --pull, on the reasoning that reverting code the
			// deploy did not fetch would undo something the operator staged. But that
			// left the far worse outcome: `site deploy --restart` on a broken commit
			// reported failure and walked away with the service *down*, while the
			// documented promise is that the previous version keeps serving.
			//
			// The real constraint is uncommitted work: `git reset --hard` destroys it,
			// and no automatic recovery is worth that. A clean tree loses nothing —
			// the commit being reverted from still exists and is named in the error.
			// Where to go back to. The commit that was HEAD when this deploy started
			// is only the right answer if this deploy moved HEAD — and an operator who
			// commits a break and then runs `site deploy --restart` has already moved
			// it, so HEAD is the broken commit and reverting to it achieves nothing.
			//
			// What was actually serving is recorded: the git SHA of the last deployment
			// that finished healthy. That is the honest target, and it is why the
			// deployment history is kept.
			currentSHA := gitSHA(ctx, g, rc.AppDir, id)
			revertTo := lastHealthySHA(ctx, g, st, site.Domain, deployID)
			if revertTo == "" {
				revertTo = previousSHA
			}
			canRevert := revertTo != "" && currentSHA != revertTo
			if canRevert && !g.gitTreeIsClean(ctx, rc, id) {
				canRevert = false
				g.Log.Warn("not reverting: the working tree has uncommitted changes, and "+
					"reverting would destroy them",
					"fix", "commit or stash them, then 'ratline site deploy "+site.Domain+"'")
			}

			if canRevert {
				g.Log.Warn("the application did not become healthy; reverting to the last "+
					"commit that was serving", "sha", shortSHA(revertTo))
				if rerr := g.gitReset(ctx, rc, id, revertTo); rerr != nil {
					return finish(rlerr.Wrap(err, rlerr.CodeRollbackFailed,
						"the deploy failed and the revert also failed (%v)", rerr))
				}
				if _, ierr := mgr.Control(ctx, site.Domain, "restart"); ierr != nil {
					return finish(rlerr.Wrap(err, rlerr.CodeRollbackFailed,
						"the deploy failed, and the previous version did not restart either (%v)", ierr))
				}
				record.RolledBack = true
				g.Log.Info("the previous version is serving again", "sha", shortSHA(revertTo))
				return finish(rlerr.Wrap(err, rlerr.CodeUnhealthy,
					"the deploy was reverted to %s, which is serving again", shortSHA(revertTo)))
			}
			// Nothing to revert to, or reverting would have destroyed work. The
			// service is down and saying so plainly is the only honest option — a
			// caller that does not know this is left probing a dead site.
			return finish(rlerr.Wrap(err, rlerr.CodeUnhealthy,
				"%s is not serving and there was nothing safe to revert to", site.Domain).
				WithHint("the code on disk is what failed to start; fix it and re-run, "+
					"or 'ratline site troubleshoot %s' for the cause", site.Domain))
		}
		record.Steps = append(record.Steps, "restart")
		record.Health = health
	}

	// The post-deploy hook, once the site is up and answering.
	//
	// Deliberately after the health check, and deliberately not able to revert the deploy.
	// The site is serving the new code at this point; a smoke test or a cache warm that
	// fails is worth reporting loudly, but rolling back a *healthy* site because a
	// notification could not reach Slack would be a worse outcome than the failure it is
	// reacting to. So this reports and exits non-zero, and does not touch what is running.
	if site.PostDeployCommand != "" {
		if herr := runtime.RunHook(ctx, rc, "post-deploy", site.PostDeployCommand); herr != nil {
			record.Steps = append(record.Steps, "post-deploy hook")
			newSHA := gitSHA(ctx, g, rc.AppDir, id)
			if newSHA != "" {
				record.GitSHA = newSHA
			}
			_ = st.TouchDeploy(ctx, site.Domain)
			return finish(rlerr.Wrap(herr, rlerr.CodeOf(herr),
				"the deploy succeeded and %s is serving, but its post-deploy hook failed",
				site.Domain).
				WithHint("the new code is live; the hook is what needs attention"))
		}
		record.Steps = append(record.Steps, "post-deploy hook")
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

// lastHealthySHA is the commit of the most recent deployment that finished healthy.
//
// Skips the deployment currently in flight, which is the one that just failed. An
// empty result means this site has never had a recorded healthy deploy — a first
// deploy, or one that predates the record — and the caller falls back to whatever
// HEAD was when this run started.
func lastHealthySHA(ctx context.Context, g *Globals, st *state.Store, domain string, exclude int64) string {
	history, err := st.ListDeployments(ctx, domain, 20)
	if err != nil {
		g.Log.Debug("could not read the deployment history", "err", err)
		return ""
	}
	for _, d := range history {
		if d.ID == exclude || !d.OK || d.GitSHA == "" {
			continue
		}
		return d.GitSHA
	}
	return ""
}

// gitTreeIsClean reports whether reverting would destroy uncommitted work.
//
// Consulted before any automatic `git reset --hard`: recovering a site is worth a
// lot, and never worth silently deleting work somebody has not committed. An
// unreadable status is treated as dirty, because guessing wrong in that direction is
// merely unhelpful rather than destructive.
//
// Untracked files do not count. `git reset --hard` does not remove them, so they are
// not at risk — and treating them as dirty blocked recovery on any site that had ever
// been deployed, because installing dependencies and importing the application leave
// __pycache__, node_modules and build output sitting untracked in the tree. The
// question is only whether a *tracked* file has been modified or staged.
func (g *Globals) gitTreeIsClean(ctx context.Context, rc *runtime.Context, id *system.Identity) bool {
	if !system.IsDir(filepath.Join(rc.AppDir, ".git")) {
		return false
	}
	res, err := g.Runner.Run(ctx, system.Cmd{
		Name: "git", Args: []string{"status", "--porcelain"}, As: id, Dir: rc.AppDir,
	})
	if err != nil || res == nil {
		return false
	}
	for _, line := range strings.Split(res.Out(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// "?? path" is untracked; anything else is a tracked change.
		if !strings.HasPrefix(line, "??") {
			return false
		}
	}
	return true
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
