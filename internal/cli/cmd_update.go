package cli

import (
	"context"
	"encoding/json"
	"fmt"
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
			"The web panel updates itself the same way, separately: 'ratline-panel update'.",
		Example: "  ratline update                       # to the latest release\n" +
			"  ratline update --check               # is there one? change nothing\n" +
			"  ratline update --version 1.2.0\n" +
			"  ratline update --rollback            # back to the previous binary\n" +
			"  ratline update --base-url https://mirror.example.internal/ratline",
		RunE: func(cmd *cobra.Command, _ []string) error {
			u := &updater{g: g, baseURL: strings.TrimRight(orDefault2(baseURL, updateBaseURL), "/"),
				allowUnverified: unverified}
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
	latestAPI       string
	g               *Globals
	baseURL         string
	allowUnverified bool
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

	if u.g.JSON {
		return u.g.EmitJSON(map[string]any{
			"current": current, "latest": target, "update_available": !same,
		})
	}
	if same {
		u.g.Printf("ratline %s is current.\n", current)
		return nil
	}
	u.g.Printf("ratline %s is installed; %s is available.\n", current, target)
	u.g.Printf("\nInstall it:\n  ratline update\n")
	return nil
}

// run performs the update.
func (u *updater) run(ctx context.Context, want string) error {
	current := buildinfo.Version
	res, err := u.engine().Run(ctx, want)
	if err != nil {
		return err
	}
	if res == nil {
		u.g.Printf("ratline %s is already installed; nothing to do.\n", current)
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

	if u.g.JSON {
		return u.g.EmitJSON(map[string]any{
			"updated": true, "from": res.From, "to": res.To,
			"timers": installedUnits, "rollback": "ratline update --rollback",
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
	u.g.Printf("\nNo site was interrupted. Worth running once:\n" +
		"  ratline doctor\n" +
		"  ratline reconcile --dry-run   # a new release may generate better units\n" +
		"\nIf anything is wrong:\n  ratline update --rollback\n")
	return nil
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
