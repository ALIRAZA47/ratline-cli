package cli

import (
	"context"
	"fmt"
	goruntime "runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/buildinfo"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/install"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/selfupdate"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// updateRepo is the project the panel's artefacts come from — the same repository
// ratline releases from, because they are versioned and published together.
const updateRepo = "ALIRAZA47/ratline-cli"

// The panel updates itself, rather than being updated by re-running the installer.
//
// The installer works and always did — it downloads, checksums and re-runs install,
// which restarts the service. What it also does is pipe a script from the network
// into a root shell, which is a lot of ceremony for "take the next patch release" and
// exactly the thing an operator should not be in the habit of. So the panel does what
// ratline does: one command, checksummed, verified before it is installed, atomic,
// and reversible.
//
// Two things differ from ratline's, and both matter:
//
//   - The panel is a daemon. Replacing the file on disk changes nothing until the
//     service restarts, so this restarts it — and says so, because a restart ends
//     every signed-in session's in-flight request.
//   - A running job is a child of the process being replaced. The restart kills it,
//     and the panel marks such jobs failed on the way back up rather than leaving a
//     spinner. This refuses to restart while one is running unless told otherwise,
//     because a deploy interrupted half way is a worse outcome than waiting.

func newUpdateCommand(app *App) *cobra.Command {
	var (
		version    string
		baseURL    string
		check      bool
		rollback   bool
		unverified bool
		force      bool
		noRestart  bool
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update ratline-panel itself, in place",
		Args:  cobra.NoArgs,
		Long: "Replaces the installed binary with a newer release and restarts the service.\n\n" +
			"Nothing is installed until the download has been checksummed against the\n" +
			"release's own SHA256SUMS and the new binary has been run and asked its\n" +
			"version. The install is an atomic rename, and the previous binary is kept\n" +
			"beside it for --rollback.\n\n" +
			"The restart ends open requests, and a job running at that moment is a child\n" +
			"of the process being replaced — so an update waits for the queue to be idle\n" +
			"unless --force says otherwise. Signed-in sessions survive: they live in the\n" +
			"panel's database, not in memory.\n\n" +
			"This updates the panel only. ratline is updated by 'ratline update'.",
		Example: "  ratline-panel update                 # to the latest release\n" +
			"  ratline-panel update --check         # is there one? change nothing\n" +
			"  ratline-panel update --version 0.16.0\n" +
			"  ratline-panel update --rollback      # back to the previous binary",
		RunE: func(cmd *cobra.Command, _ []string) error {
			u := &panelUpdater{app: app, baseURL: strings.TrimSpace(baseURL),
				allowUnverified: unverified, force: force, noRestart: noRestart}
			switch {
			case rollback:
				return u.rollback(cmd.Context())
			case check:
				return u.check(cmd.Context(), version)
			default:
				return u.run(cmd.Context(), version)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&version, "version", "", "Install this version rather than the latest")
	f.StringVar(&baseURL, "base-url", "", "Where releases live (default: the project's GitHub releases)")
	f.BoolVar(&check, "check", false, "Report whether an update is available and change nothing")
	f.BoolVar(&rollback, "rollback", false, "Restore the binary this command last replaced")
	f.BoolVar(&unverified, "allow-unverified", false,
		"Install even when the release publishes no SHA256SUMS (refused by default)")
	f.BoolVar(&force, "force", false, "Restart even though a job is running, killing it")
	f.BoolVar(&noRestart, "no-restart", false,
		"Install without restarting; the running panel keeps serving the old binary")
	return cmd
}

type panelUpdater struct {
	app             *App
	baseURL         string
	latestAPI       string
	allowUnverified bool
	force           bool
	noRestart       bool
}

// artefacts is one file: the panel binary, at whatever path it was installed to.
//
// The interface is embedded in it, so there is nothing else to fetch — which is the
// reason it is embedded rather than installed beside it.
func (u *panelUpdater) artefacts() ([]selfupdate.Artefact, error) {
	self, err := system.SelfPath()
	if err != nil {
		return nil, err
	}
	return []selfupdate.Artefact{{
		Asset:  fmt.Sprintf("ratline-panel-linux-%s", goruntime.GOARCH),
		Target: self, Mode: 0o755, Required: true,
	}}, nil
}

func (u *panelUpdater) engine() *selfupdate.Updater {
	return &selfupdate.Updater{
		Product: "ratline-panel", Repo: updateRepo,
		BaseURL: u.baseURL, LatestAPI: u.latestAPI,
		Current: buildinfo.Version,
		Log:     u.app.Log, Runner: u.app.Runner,
		AllowUnverified: u.allowUnverified,
		Artefacts:       u.artefacts,
		Verify:          u.verify,
	}
}

// verify runs a candidate and satisfies itself that it is the panel, and the version
// the release claimed.
//
// A mismatch means the asset naming and the tag have drifted, which is worth stopping
// for: the alternative is installing something the release does not describe.
func (u *panelUpdater) verify(ctx context.Context, path, wantVersion string) error {
	res, err := u.app.Runner.Run(ctx, system.Cmd{
		Path: path, Args: []string{"version"}, Label: "the new binary",
	})
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition, "the downloaded binary does not run").
			WithHint("wrong architecture, or a corrupt download; nothing was installed")
	}
	out := res.Out()
	if !strings.Contains(out, "ratline-panel") {
		return rlerr.Preconditionf("the downloaded binary does not identify itself as ratline-panel").
			WithHint("it reported: %s", firstLine(out))
	}
	if !strings.Contains(out, wantVersion) && !strings.Contains(out, "v"+wantVersion) {
		return rlerr.Preconditionf("the downloaded binary reports a different version than the release").
			WithHint("the release says %s and the binary says: %s", wantVersion, firstLine(out))
	}
	return nil
}

func (u *panelUpdater) check(ctx context.Context, want string) error {
	target, err := u.engine().Latest(ctx, want)
	if err != nil {
		return err
	}
	current := buildinfo.Version
	same := selfupdate.SameVersion(current, target)
	if u.app.JSON {
		return u.app.emitJSON("ratline-panel update", map[string]any{
			"current": current, "latest": target, "update_available": !same,
		})
	}
	if same {
		u.app.printf("ratline-panel %s is current.\n", current)
		return nil
	}
	u.app.printf("ratline-panel %s is installed; %s is available.\n", current, target)
	u.app.printf("\nInstall it:\n  ratline-panel update\n")
	return nil
}

func (u *panelUpdater) run(ctx context.Context, want string) error {
	current := buildinfo.Version

	// Checked before anything is downloaded, so a refusal costs nothing.
	if !u.noRestart {
		if err := u.refuseIfBusy(ctx); err != nil {
			return err
		}
	}

	res, err := u.engine().Run(ctx, want)
	if err != nil {
		return err
	}
	if res == nil {
		u.app.printf("ratline-panel %s is already installed; nothing to do.\n", current)
		return nil
	}

	restarted := false
	if !u.noRestart {
		mgr := &install.Manager{Cfg: u.app.Cfg, Log: u.app.Log, Runner: u.app.Runner}
		if rerr := mgr.Restart(ctx); rerr != nil {
			// The binary is installed and good; a restart that failed is a warning
			// with an obvious manual step, not a reason to undo a working update.
			u.app.Log.Warn("the new binary is installed but the service did not restart",
				"err", rerr, "fix", "systemctl restart ratline-panel")
		} else {
			restarted = true
		}
	}

	if u.app.JSON {
		return u.app.emitJSON("ratline-panel update", map[string]any{
			"updated": true, "from": res.From, "to": res.To,
			"restarted": restarted, "rollback": "ratline-panel update --rollback",
		})
	}
	u.app.printf("Updated ratline-panel %s → %s\n", res.From, res.To)
	u.app.printf("  binary  %s\n", res.Items[0].Target)
	u.app.printf("  kept    %s\n", res.Backups[res.Items[0].Target])
	switch {
	case restarted:
		u.app.printf("\nThe service was restarted, so it is serving the new binary.\n")
	case u.noRestart:
		u.app.printf("\nNot restarted, as asked — the running panel is still serving the old\n" +
			"binary until you run:\n  systemctl restart ratline-panel\n")
	default:
		u.app.printf("\nThe service did not restart. Run:\n  systemctl restart ratline-panel\n")
	}
	u.app.printf("\nWorth running once:\n  ratline-panel doctor\n" +
		"\nIf anything is wrong:\n  ratline-panel update --rollback\n")
	return nil
}

func (u *panelUpdater) rollback(ctx context.Context) error {
	items, err := u.artefacts()
	if err != nil {
		return err
	}
	restored, err := u.engine().Rollback(ctx)
	if err != nil {
		return err
	}
	restarted := false
	if !u.noRestart {
		mgr := &install.Manager{Cfg: u.app.Cfg, Log: u.app.Log, Runner: u.app.Runner}
		if rerr := mgr.Restart(ctx); rerr == nil {
			restarted = true
		} else {
			u.app.Log.Warn("the previous binary is back but the service did not restart", "err", rerr)
		}
	}
	if u.app.JSON {
		return u.app.emitJSON("ratline-panel update", map[string]any{
			"rolled_back": true, "restored": restored, "restarted": restarted,
		})
	}
	u.app.printf("Restored ratline-panel %s\n", restored[items[0].Target])
	if !restarted {
		u.app.printf("\nRun: systemctl restart ratline-panel\n")
	}
	u.app.printf("\nConfirm it:\n  ratline-panel version\n  ratline-panel doctor\n")
	return nil
}

// refuseIfBusy stops an update that would kill work in flight.
//
// A job is a child of the panel process, so the restart takes it with it — and the
// panel marks such jobs failed when it comes back, which is honest but is not what
// somebody wants to discover about a deploy they started two minutes ago.
func (u *panelUpdater) refuseIfBusy(ctx context.Context) error {
	if u.force {
		return nil
	}
	st, err := u.app.openStore()
	if err != nil {
		// No database yet is not a reason to refuse an update; it means the panel has
		// never been set up, and there is certainly nothing running.
		u.app.Log.Debug("could not read the job queue before updating", "err", err)
		return nil
	}
	defer st.Close() //nolint:errcheck // a read on a command that is about to exit
	jobs, err := st.ListJobs(ctx, 50)
	if err != nil {
		return nil
	}
	if j := firstUnfinishedJob(jobs); j != nil {
		return busyError(j)
	}
	return nil
}

// firstUnfinishedJob returns the job that makes a restart destructive, or nil.
func firstUnfinishedJob(jobs []*store.Job) *store.Job {
	for _, j := range jobs {
		if j != nil && !j.Terminal() {
			return j
		}
	}
	return nil
}

func busyError(j *store.Job) error {
	what := strings.TrimSpace(j.Action + " " + j.Target)
	return rlerr.Preconditionf("a job is %s: %s", j.State, what).
		WithHint("restarting now would kill it. Wait for it to finish, or pass --force " +
			"to update anyway, or --no-restart to install without restarting")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
