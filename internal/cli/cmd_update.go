package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/buildinfo"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/selfupdate"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// `update` replaces the installed binaries in place, on a server that is serving.
//
// The whole design is about the failure cases, because the success case is a file
// copy. What could go wrong, and what stops it:
//
//   - A truncated or tampered download. Every artefact is checksummed against the
//     release's own SHA256SUMS before anything is installed, and a missing checksum
//     file is a refusal rather than a shrug.
//   - A binary that does not run — wrong architecture, corrupt, built for a
//     different libc. It is executed from the staging directory and asked its
//     version before it is allowed near the install path.
//   - A binary that runs but cannot read this server's state, which is what a
//     downgrade past a schema migration looks like. It is asked to list the sites
//     first, using the real database.
//   - A half-written file. Each install is an atomic rename within the same
//     filesystem, so a timer firing mid-update sees the old inode or the new one and
//     never a partial file.
//   - A new binary that turns out to be broken anyway. The previous one is kept
//     beside it, and `ratline update --rollback` puts it back.
//
// What it deliberately does not do: touch any running site. Sites are systemd units
// that exec an interpreter, not this binary — so replacing it cannot interrupt a
// request. `ratline-shell` is the one exception, because forced commands in
// authorized_keys point at it, which is why it is verified and swapped the same way.

// updateRepo is the project the artefacts come from, named in its own constant so
// the release URLs cannot drift apart from each other or from the error messages.
const updateRepo = "ALIRAZA47/ratline-cli"

// updateBaseURL is where release artefacts live. Overridable, because a server
// without a route to github is a normal thing and mirroring the release is the
// obvious answer.
const updateBaseURL = "https://github.com/" + updateRepo + "/releases"

func newUpdateCommand(g *Globals) *cobra.Command {
	var (
		version    string
		baseURL    string
		check      bool
		rollback   bool
		unverified bool
		noPanel    bool
	)
	cmd := &cobra.Command{
		Use:     "update",
		Short:   "Update ratline itself, in place, on a live server",
		GroupID: GroupOps,
		Args:    cobra.NoArgs,
		Long: "Replaces the installed binaries with a newer release. One command, and it is\n" +
			"safe to run on a server that is serving traffic.\n\n" +
			"Nothing is installed until the download has been checksummed against the\n" +
			"release's own SHA256SUMS, the new binary has been run and asked its version,\n" +
			"and it has proved it can read this server's state — which is what catches a\n" +
			"downgrade past a schema migration. The install itself is an atomic rename, and\n" +
			"the previous binary is kept beside it for --rollback.\n\n" +
			"No site is interrupted. Sites are systemd units running an interpreter; they do\n" +
			"not exec this binary, so replacing it cannot drop a request.\n\n" +
			"The web panel, if it is installed here, is taken to the same release in the\n" +
			"same run — it updates itself, so it refuses while one of its jobs is running\n" +
			"and restarts its own service afterwards. --no-panel leaves it alone, and\n" +
			"'ratline-panel update' still works on its own.",
		Example: "  ratline update                       # to the latest release\n" +
			"  ratline update --check               # is there one? change nothing\n" +
			"  ratline update --version 1.2.0\n" +
			"  ratline update --rollback            # back to the previous binary\n" +
			"  ratline update --base-url https://mirror.example.internal/ratline",
		RunE: func(cmd *cobra.Command, _ []string) error {
			u := &updater{g: g, baseURL: strings.TrimRight(orDefault2(baseURL, updateBaseURL), "/"),
				allowUnverified: unverified, noPanel: noPanel}
			// --dry-run has to be honoured here, by hand. The updater downloads,
			// verifies and renames with plain file operations rather than through the
			// Runner, so the Runner's dry-run mode does not reach it — and a rehearsal
			// that replaced the root binary, unlocked, was what the panel's Preview
			// button did. A dry run reports, exactly as --check does.
			switch {
			case rollback && g.DryRun:
				g.Log.Info("would restore the previous binaries; nothing was changed")
				return nil
			case rollback:
				return u.rollback(cmd.Context())
			case check || g.DryRun:
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
	f.BoolVar(&noPanel, "no-panel", false,
		"Leave ratline-panel alone; update only ratline itself")
	return Mutating(cmd)
}

// artefacts resolves what a release installs, against the running binary's own
// location so an install under /opt or /usr/bin updates itself rather than something
// under /usr/local that may not exist.
func (u *updater) artefacts() ([]selfupdate.Artefact, error) {
	self, err := system.SelfPath()
	if err != nil {
		return nil, err
	}
	arch := goruntime.GOARCH
	return []selfupdate.Artefact{
		{
			Asset: fmt.Sprintf("ratline-linux-%s", arch),
			// The running binary, whatever path it was installed at.
			Target: self, Mode: 0o755, Required: true,
		},
		{
			Asset: fmt.Sprintf("ratline-shell-linux-%s", arch),
			// Forced commands in authorized_keys point here by absolute path, so it
			// has to keep the path configuration records rather than following the
			// main binary.
			Target: u.g.Cfg.Paths.ShellWrapper, Mode: 0o755, Required: true,
		},
	}, nil
}

// updater binds the shared self-updater to ratline's globals.
type updater struct {
	// latestAPI is injectable so the release-lookup failure modes can be tested
	// without reaching github; empty means the real endpoint.
	latestAPI string
	// noPanel leaves the web panel alone. For somebody who upgrades the two
	// deliberately, or whose panel is managed by something else.
	noPanel bool
	// panelBinaryOverride names the panel binary instead of searching for it.
	// Tests only: a search that walks /usr/local/bin would find the developer's own
	// install and try to update it.
	panelBinaryOverride string
	g                   *Globals
	baseURL             string
	allowUnverified     bool
}

// engine builds the shared updater with ratline's own artefacts, verification and
// refusals hung off it.
func (u *updater) engine() *selfupdate.Updater {
	return &selfupdate.Updater{
		Product: "ratline", Repo: updateRepo,
		BaseURL: u.baseURL, LatestAPI: u.latestAPI,
		Current: buildinfo.Version,
		Log:     u.g.Log, Runner: u.g.Runner,
		AllowUnverified: u.allowUnverified,
		Artefacts:       u.artefacts,
		Verify:          u.verifyBinary,
		PreInstall:      u.refuseIfPackaged,
	}
}

// check reports whether an update is available.
func (u *updater) check(ctx context.Context, want string) error {
	target, err := u.engine().Latest(ctx, want)
	if err != nil {
		return err
	}
	current := buildinfo.Version
	same := selfupdate.SameVersion(current, target)

	// Reported, not run: a check that changed the panel would not be a check.
	panelInstalled := !u.noPanel && u.panelBinary() != ""

	if u.g.JSON {
		return u.g.EmitJSON(map[string]any{
			"current": current, "latest": target, "update_available": !same,
			"panel_installed": panelInstalled,
		})
	}
	if same {
		u.g.Printf("ratline %s is current.\n", current)
		if panelInstalled {
			u.g.Printf("ratline-panel is installed here; 'ratline update' checks it too.\n")
		}
		return nil
	}
	u.g.Printf("ratline %s is installed; %s is available.\n", current, target)
	if panelInstalled {
		u.g.Printf("ratline-panel is installed here and would be taken to %s as well.\n", target)
	}
	u.g.Printf("\nInstall it:\n  ratline update\n")
	return nil
}

// run performs the update.
// panelUpdate is what happened to the web panel during a `ratline update`.
type panelUpdate struct {
	Path    string `json:"path,omitempty"`
	Updated bool   `json:"updated"`
	Skipped string `json:"skipped,omitempty"`
	Output  string `json:"output,omitempty"`
}

// panelBinary finds ratline-panel on this server, or returns "".
//
// Beside this binary first, because the installer puts them in the same prefix and
// an operator with ratline under /opt almost certainly has the panel there too.
func (u *updater) panelBinary() string {
	if u.panelBinaryOverride != "" {
		if system.Exists(u.panelBinaryOverride) {
			return u.panelBinaryOverride
		}
		return ""
	}
	candidates := []string{}
	if self, err := system.SelfPath(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(self), "ratline-panel"))
	}
	candidates = append(candidates, "/usr/local/bin/ratline-panel", "/usr/bin/ratline-panel")
	for _, path := range candidates {
		if system.Exists(path) {
			return path
		}
	}
	return ""
}

// updatePanel brings the web panel up to the same release, by running its own update.
//
// Delegated rather than reimplemented. The panel is a daemon with a job queue and a
// service to restart, and it already knows how to replace itself safely: it refuses
// while a job is running, verifies that what it downloaded identifies itself as
// ratline-panel, swaps atomically and keeps the old binary for --rollback. Doing any
// of that a second time here would be a second implementation of the careful part,
// and the careful part is the whole feature.
//
// The version is passed through so the two stay in lockstep. They are built and
// released together, and a box running yesterday's panel against today's ratline is
// tolerable but not what somebody asked for by typing `ratline update`.
func (u *updater) updatePanel(ctx context.Context, version string) *panelUpdate {
	path := u.panelBinary()
	if path == "" {
		u.g.Log.Debug("no web panel on this server; nothing else to update")
		return &panelUpdate{Skipped: "not installed"}
	}
	out := &panelUpdate{Path: path}

	args := []string{"update", "--version=" + version}
	if u.baseURL != updateBaseURL {
		args = append(args, "--base-url="+u.baseURL)
	}
	if u.allowUnverified {
		args = append(args, "--allow-unverified")
	}

	res, err := u.g.Runner.Run(ctx, system.Cmd{
		Path: path, Args: args, Label: "ratline-panel update", Mutates: true,
		Timeout: 15 * time.Minute,
	})
	stderr := ""
	if res != nil {
		out.Output = strings.TrimSpace(res.Out())
		stderr = res.Stderr
	}
	if err != nil {
		combined := out.Output + " " + stderr
		// A panel from before the update command existed cannot update itself, and
		// saying "unknown command" to somebody who just upgraded ratline is not an
		// answer. The installer is the way onto the first version that can.
		if strings.Contains(combined, "unknown command") {
			out.Skipped = "too old to update itself"
			u.g.Log.Warn("the installed web panel predates 'ratline-panel update'",
				"fix", "curl -fsSL https://ratline.alirazakhan.me/panel.sh | sudo NO_INSTALL=1 sh")
			return out
		}
		// Not fatal to ratline's own update, which has already succeeded. The panel
		// unwinds its own failure and keeps serving the binary it had.
		out.Skipped = "failed"
		u.g.Log.Warn("ratline was updated but the web panel was not", "err", err,
			"fix", "run 'ratline-panel update' and read what it says")
		return out
	}
	out.Updated = true
	return out
}

func (u *updater) run(ctx context.Context, want string) error {
	current := buildinfo.Version
	// Resolved once, and the concrete version handed to both halves: asking twice
	// could straddle a release and put a panel on the server that this ratline was
	// never tested against.
	target, err := u.engine().Latest(ctx, want)
	if err != nil {
		return err
	}
	res, err := u.engine().Run(ctx, target)
	if err != nil {
		return err
	}
	if res == nil {
		// ratline is current, which says nothing about the panel — it is a separate
		// binary and can be a release behind on its own.
		panel := u.panel(ctx, target)
		if u.g.JSON {
			return u.g.EmitJSON(map[string]any{
				"updated": false, "from": current, "to": current, "panel": panel,
			})
		}
		u.g.Printf("ratline %s is already installed; nothing to do.\n", current)
		u.reportPanel(panel, target)
		return nil
	}

	// A release that adds one of ratline's own timers has to install it here, not only in
	// `init`. v0.11.0 shipped continuous health checks and, on every server that upgraded
	// rather than installed fresh, nothing was continuous: the commands were there and the
	// timer was not. `init` is run once in a server's life, and a feature that depends on a
	// unit cannot depend on somebody thinking to run it again.
	//
	// Safe to repeat: EnsureTimers writes only what is missing or still carries ratline's
	// header, and leaves a hand-edited unit alone.
	installedUnits := ""
	if mgr, merr := u.g.siteManager(ctx); merr == nil {
		// The same rule for nginx's catch-all server as for the timers below: a server
		// that upgraded rather than installed fresh has no default_server until
		// something writes one, and a `site add` that never happens again would never
		// write it. Reloaded only when the file actually changed, and gracefully.
		if changed, derr := mgr.Nginx.EnsureDefaultServer(ctx); derr != nil {
			u.g.Log.Warn("could not install nginx's catch-all server block", "err", derr,
				"fix", "ratline reconcile --fix, then ratline doctor")
		} else if changed && u.g.Bins.Available("nginx") {
			if terr := mgr.Nginx.Test(ctx); terr != nil {
				u.g.Log.Warn("nginx refused the configuration with the catch-all in it; leaving nginx as it was",
					"err", terr, "fix", "nginx -t, then ratline reconcile --fix")
			} else if rerr := mgr.Nginx.Reload(ctx); rerr != nil {
				u.g.Log.Warn("could not reload nginx for the catch-all server block", "err", rerr)
			} else {
				u.g.Log.Info("installed nginx's catch-all server block: requests for unknown hosts now get nothing")
			}
		}
		if terr := mgr.Unit.EnsureTimers(ctx); terr != nil {
			// Not fatal: the binary is already replaced and working, and a timer that
			// could not be installed is a warning rather than a reason to roll back a
			// good update.
			u.g.Log.Warn("could not install ratline's own timers", "err", terr,
				"fix", "ratline init --write-config-only, then ratline doctor")
		} else {
			installedUnits = "checked"
		}
	}

	panel := u.panel(ctx, res.To)

	if u.g.JSON {
		return u.g.EmitJSON(map[string]any{
			"updated": true, "from": res.From, "to": res.To,
			"timers": installedUnits, "panel": panel,
			"rollback": "ratline update --rollback",
		})
	}
	u.g.Printf("Updated ratline %s → %s\n", res.From, res.To)
	if err := u.g.Fields(
		[2]string{"binary", res.Items[0].Target},
		[2]string{"shell wrapper", res.Items[1].Target},
		[2]string{"kept", res.Backups[res.Items[0].Target]},
	); err != nil {
		return err
	}
	u.reportPanel(panel, res.To)
	u.g.Printf("\nNo site was interrupted. Worth running once:\n" +
		"  ratline doctor\n" +
		"  ratline reconcile --dry-run   # a new release may generate better units\n" +
		"\nIf anything is wrong:\n  ratline update --rollback\n")
	return nil
}

// panel runs the web panel's update unless told not to.
func (u *updater) panel(ctx context.Context, version string) *panelUpdate {
	if u.noPanel {
		return &panelUpdate{Skipped: "not asked for"}
	}
	return u.updatePanel(ctx, version)
}

// reportPanel says what happened to the panel, in the operator's terms.
func (u *updater) reportPanel(p *panelUpdate, version string) {
	switch {
	case p == nil, p.Skipped == "not installed", p.Skipped == "not asked for":
		return
	case p.Updated:
		u.g.Printf("\nratline-panel is installed here, so it was updated too:\n")
		for _, line := range strings.Split(p.Output, "\n") {
			u.g.Printf("  %s\n", line)
		}
	case p.Skipped == "too old to update itself":
		u.g.Printf("\nratline-panel is installed here but predates its own update command,\n"+
			"so it could not be taken to %s. Once, with the installer — after that it\n"+
			"updates with ratline:\n"+
			"  curl -fsSL https://ratline.alirazakhan.me/panel.sh | sudo NO_INSTALL=1 sh\n"+
			"  sudo systemctl restart ratline-panel\n", version)
	default:
		u.g.Printf("\nratline is updated, but ratline-panel was not: it is still serving the\n" +
			"binary it had. Run it yourself and read what it says:\n" +
			"  ratline-panel update\n")
	}
}

// rollback restores the binaries kept by the last update.
func (u *updater) rollback(ctx context.Context) error {
	items, err := u.artefacts()
	if err != nil {
		return err
	}
	restored, err := u.engine().Rollback(ctx)
	if err != nil {
		return err
	}
	if u.g.JSON {
		return u.g.EmitJSON(map[string]any{"rolled_back": true, "restored": restored})
	}
	u.g.Printf("Restored ratline %s\n", restored[items[0].Target])
	u.g.Printf("\nConfirm it:\n  ratline version\n  ratline doctor\n")
	return nil
}

// verifyBinary runs a candidate and satisfies itself that it works here.
func (u *updater) verifyBinary(ctx context.Context, path, wantVersion string) error {
	// It runs at all, and reports the version the release claimed. A mismatch means
	// the asset naming and the tag have drifted, which is worth stopping for.
	res, err := u.g.Runner.Run(ctx, system.Cmd{
		Path: path, Args: []string{"version", "--json"}, Timeout: 30 * time.Second,
	})
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeExternal, "the downloaded binary does not run").
			WithHint("this is usually the wrong architecture: this host is %s", goruntime.GOARCH)
	}
	var payload struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.Out()), &payload); err != nil {
		return rlerr.Wrap(err, rlerr.CodeExternal, "the downloaded binary printed no version")
	}
	if got := payload.Data.Version; !selfupdate.SameVersion(got, wantVersion) {
		return rlerr.Externalf("the downloaded binary reports version %q, but %q was requested",
			got, wantVersion)
	}

	// And it can read this server's state. This is the check that catches a
	// downgrade past a schema migration, which would otherwise install cleanly and
	// fail on the next command an operator ran.
	if !system.Exists(u.g.Cfg.Paths.StateDB) {
		return nil
	}
	if _, err := u.g.Runner.Run(ctx, system.Cmd{
		Path: path, Args: []string{"site", "list", "--json"}, Timeout: 60 * time.Second,
	}); err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition,
			"the downloaded binary cannot read this server's state").
			WithHint("this is what a downgrade past a schema migration looks like; " +
				"nothing was installed")
	}
	return nil
}

// refuseIfPackaged declines to overwrite a file a package manager owns.
//
// Replacing a dpkg-managed file behind dpkg's back leaves the package database
// lying, and the next `apt upgrade` silently reverts the update. Saying so is more
// useful than winning the race.
func (u *updater) refuseIfPackaged(ctx context.Context, items []selfupdate.Artefact) error {
	if !u.g.Bins.Available("dpkg") {
		return nil
	}
	for _, a := range items {
		res, err := u.g.Runner.Run(ctx, system.Cmd{
			Name: "dpkg", Args: []string{"-S", a.Target}, OKExit: []int{1},
		})
		if err != nil || res == nil || res.ExitCode != 0 {
			continue
		}
		pkg, _, ok := strings.Cut(res.Out(), ":")
		if !ok || strings.TrimSpace(pkg) == "" {
			continue
		}
		return rlerr.Preconditionf("%s belongs to the %s package", a.Target, strings.TrimSpace(pkg)).
			WithHint("update it the way it was installed, so the package database stays true:\n" +
				"        apt-get update && apt-get install --only-upgrade ratline")
	}
	return nil
}
