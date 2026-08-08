package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/buildinfo"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
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
// the two URLs below cannot drift apart from each other or from the error messages.
const updateRepo = "ALIRAZA47/ratline-cli"

// updateBaseURL is where release artefacts live. Overridable, because a server
// without a route to github is a normal thing and mirroring the release is the
// obvious answer.
const updateBaseURL = "https://github.com/" + updateRepo + "/releases"

// latestAPI reports the newest published tag.
const latestAPI = "https://api.github.com/repos/" + updateRepo + "/releases/latest"

// updateArtefacts are the files an install consists of, keyed by the install path
// they land at relative to the configured prefix.
type artefact struct {
	// Asset is the file name in the release.
	Asset string
	// Target is where it is installed.
	Target string
	// Mode is the mode it is installed with.
	Mode os.FileMode
	// Required — ratline-shell is only present once keys have been used, but it is
	// part of every release and its absence from a release is a broken release.
	Required bool
}

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
			"not exec this binary, so replacing it cannot drop a request.",
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
	// Mutating: it replaces files under /usr/local and takes the lock for the swap,
	// so it cannot interleave with a deploy that is halfway through rendering a unit.
	return Mutating(cmd)
}

type updater struct {
	// latestAPI is injectable so the release-lookup failure modes can be tested
	// without reaching github; empty means the real endpoint.
	latestAPI       string
	g               *Globals
	baseURL         string
	allowUnverified bool
}

// artefacts is what this release installs, resolved against the running binary's
// own location so an install under /opt or /usr/bin updates itself rather than
// something under /usr/local that may not exist.
func (u *updater) artefacts() ([]artefact, error) {
	self, err := system.SelfPath()
	if err != nil {
		return nil, err
	}
	arch := goruntime.GOARCH
	return []artefact{
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

// check reports whether an update is available.
func (u *updater) check(ctx context.Context, want string) error {
	target, err := u.resolveVersion(ctx, want)
	if err != nil {
		return err
	}
	current := buildinfo.Version
	same := sameVersion(current, target)

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
	target, err := u.resolveVersion(ctx, want)
	if err != nil {
		return err
	}
	current := buildinfo.Version
	if sameVersion(current, target) {
		u.g.Printf("ratline %s is already installed; nothing to do.\n", current)
		return nil
	}

	items, err := u.artefacts()
	if err != nil {
		return err
	}
	for _, a := range items {
		// An empty target would fail the rename, and it would fail it after the main
		// binary had already been swapped. Refuse before anything is touched.
		if strings.TrimSpace(a.Target) == "" {
			return rlerr.Preconditionf("there is no configured install path for %s", a.Asset).
				WithHint("set paths.shell_wrapper in /etc/ratline/config.yaml, " +
					"or run 'ratline doctor' to see what else is unset")
		}
		if !filepath.IsAbs(a.Target) {
			return rlerr.Preconditionf("the install path for %s is not absolute: %s", a.Asset, a.Target)
		}
	}
	if err := u.refuseIfPackaged(ctx, items); err != nil {
		return err
	}

	// Staged beside its own install target, one directory per destination: a rename
	// is only atomic within a filesystem, and /tmp is very often a different one.
	// The two artefacts need not share a directory either — the shell wrapper's path
	// is configurable and may well be on another mount — so staging both next to the
	// main binary would install one of them across a device boundary and fail.
	stages := map[string]string{}
	defer func() {
		for _, dir := range stages {
			os.RemoveAll(dir)
		}
	}()
	for _, a := range items {
		parent := filepath.Dir(a.Target)
		if _, ok := stages[parent]; ok {
			continue
		}
		dir, err := os.MkdirTemp(parent, ".ratline-update-*")
		if err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "creating a staging directory in %s", parent)
		}
		stages[parent] = dir
		if err := os.Chmod(dir, 0o700); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "securing the staging directory")
		}
	}

	sums, err := u.fetchChecksums(ctx, target)
	if err != nil {
		return err
	}

	staged := map[string]string{}
	for _, a := range items {
		path := filepath.Join(stages[filepath.Dir(a.Target)], a.Asset)
		u.g.Log.Info("downloading", "asset", a.Asset, "version", target)
		got, err := download(ctx, u.assetURL(target, a.Asset), path, 10*time.Minute)
		if err != nil {
			return err
		}
		if want, ok := sums[a.Asset]; ok {
			if got != want {
				// Either the download was corrupted or the artefact is not the one the
				// release published. Both are refusals, not warnings.
				return rlerr.Externalf("%s does not match the published checksum", a.Asset).
					WithHint("expected %s, got %s — retry, and if it persists the release "+
						"or the mirror is wrong", want[:16], got[:16])
			}
		} else if !u.allowUnverified {
			return rlerr.Externalf("the release does not list a checksum for %s", a.Asset).
				WithHint("this is unusual and worth understanding before installing; " +
					"--allow-unverified overrides it")
		}
		if err := os.Chmod(path, a.Mode); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "setting mode on %s", path)
		}
		staged[a.Target] = path
	}

	// Prove the new binary works before it is anywhere near the install path.
	newMain := staged[items[0].Target]
	if err := u.verifyBinary(ctx, newMain, target); err != nil {
		return err
	}

	// Keep the outgoing binaries where a rollback can find them.
	backups := map[string]string{}
	for _, a := range items {
		if !system.Exists(a.Target) {
			continue
		}
		backup := backupPath(a.Target, current)
		if err := system.CopyFile(a.Target, backup, a.Mode, 0, 0); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "keeping a copy of %s", a.Target)
		}
		backups[a.Target] = backup
	}

	// The swap. Atomic per file, and the rollback stack puts back anything already
	// replaced if a later one fails.
	rb := system.NewRollback(u.g.Log)
	var swapErr error
	for _, a := range items {
		src := staged[a.Target]
		if err := os.MkdirAll(filepath.Dir(a.Target), 0o755); err != nil {
			swapErr = rlerr.Wrap(err, rlerr.CodeGeneric, "creating %s", filepath.Dir(a.Target))
			break
		}
		if err := os.Rename(src, a.Target); err != nil {
			swapErr = rlerr.Wrap(err, rlerr.CodeGeneric, "installing %s", a.Target)
			break
		}
		// Not named `target`: that is the version being installed, in scope here, and
		// shadowing it in a loop that renames files is a trap worth not setting.
		installed, backup := a.Target, backups[a.Target]
		rb.Push("installed "+installed, func(context.Context) error {
			if backup == "" {
				return os.Remove(installed)
			}
			return os.Rename(backup, installed)
		})
	}
	if swapErr == nil {
		// The installed binary, not the staged one: a rename onto a path that is a
		// symlink or a bind mount can land somewhere unexpected.
		swapErr = u.verifyBinary(ctx, items[0].Target, target)
	}
	if swapErr != nil {
		rb.Unwind(ctx)
		return rlerr.Wrap(swapErr, rlerr.CodeOf(swapErr),
			"the update was reverted and %s is unchanged", current)
	}
	rb.Commit()

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
			"updated": true, "from": current, "to": target,
			"timers": installedUnits, "rollback": "ratline update --rollback",
		})
	}
	u.g.Printf("Updated ratline %s → %s\n", current, target)
	if err := u.g.Fields(
		[2]string{"binary", items[0].Target},
		[2]string{"shell wrapper", items[1].Target},
		[2]string{"kept", backups[items[0].Target]},
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
	restored := map[string]string{}
	for _, a := range items {
		backup, version := newestBackup(a.Target)
		if backup == "" {
			continue
		}
		if err := os.Rename(backup, a.Target); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "restoring %s", a.Target)
		}
		if err := os.Chmod(a.Target, a.Mode); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "setting mode on %s", a.Target)
		}
		restored[a.Target] = version
	}
	if len(restored) == 0 {
		return rlerr.Preconditionf("there is no kept binary to roll back to").
			WithHint("a copy is only kept by 'ratline update'; reinstall the version you " +
				"want with install.sh")
	}
	if u.g.JSON {
		return u.g.EmitJSON(map[string]any{"rolled_back": true, "restored": restored})
	}
	u.g.Printf("Restored ratline %s\n", restored[items[0].Target])
	u.g.Printf("\nConfirm it:\n  ratline version\n  ratline doctor\n")
	return nil
}

// resolveVersion turns a request into a concrete version string.
func (u *updater) resolveVersion(ctx context.Context, want string) (string, error) {
	if want != "" {
		return strings.TrimPrefix(want, "v"), nil
	}
	endpoint := u.latestAPI
	if endpoint == "" {
		endpoint = latestAPI
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "building the release request")
	}
	req.Header.Set("User-Agent", "ratline")
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeExternal, "asking for the latest release").
			WithHint("a server with no route to github can be pointed at a mirror: " +
				"ratline update --base-url https://mirror.example.internal/ratline --version X")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// This endpoint 404s when a repository has published no releases at all, which
		// is a different problem from a flaky API — and "pass --version" is the wrong
		// advice for it, because there would be no assets to download either.
		return "", rlerr.Externalf("no release has been published for %s", updateRepo).
			WithHint("there is nothing to update to yet. If you build from source, " +
				"install over the running binary yourself; if you are pointing at a " +
				"fork or a mirror, pass --base-url and --version")
	case http.StatusForbidden, http.StatusTooManyRequests:
		// Unauthenticated GitHub API calls are rate limited per address, and a server
		// behind shared NAT hits it without having done anything wrong.
		return "", rlerr.Externalf("the release API rate limited this server (HTTP %d)", resp.StatusCode).
			WithHint("this resets within the hour; pass --version to skip the lookup entirely")
	default:
		return "", rlerr.Externalf("the release API returned HTTP %d", resp.StatusCode).
			WithHint("pass --version to skip the lookup")
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeExternal, "reading the release list")
	}
	if payload.TagName == "" {
		return "", rlerr.Externalf("the latest release has no tag").
			WithHint("pass --version explicitly")
	}
	return strings.TrimPrefix(payload.TagName, "v"), nil
}

// assetURL is where one artefact of one version lives.
func (u *updater) assetURL(version, asset string) string {
	return fmt.Sprintf("%s/download/v%s/%s", u.baseURL, version, asset)
}

// fetchChecksums reads the release's SHA256SUMS into a name-to-digest map.
//
// A release with no checksum file is not treated as "fine": an unverified binary
// installed as root on a server that holds every tenant's keys is precisely the
// supply-chain hole the runtime installer already refuses to leave open.
func (u *updater) fetchChecksums(ctx context.Context, version string) (map[string]string, error) {
	url := u.assetURL(version, "SHA256SUMS")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "building a request for %s", url)
	}
	req.Header.Set("User-Agent", "ratline")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if u.allowUnverified {
			u.g.Log.Warn("could not fetch SHA256SUMS, and --allow-unverified was given", "url", url)
			return map[string]string{}, nil
		}
		return nil, rlerr.Wrap(err, rlerr.CodeExternal, "fetching %s", url)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if u.allowUnverified {
			u.g.Log.Warn("no SHA256SUMS in this release, and --allow-unverified was given",
				"status", resp.StatusCode)
			return map[string]string{}, nil
		}
		return nil, rlerr.Externalf("%s returned HTTP %d", url, resp.StatusCode).
			WithHint("a release without checksums cannot be verified; " +
				"--allow-unverified overrides this, deliberately")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeExternal, "reading %s", url)
	}
	sums := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// The sha256sum format prefixes binary entries with '*'.
		sums[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	if len(sums) == 0 && !u.allowUnverified {
		return nil, rlerr.Externalf("%s is empty or unparseable", url)
	}
	return sums, nil
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
	if got := payload.Data.Version; !sameVersion(got, wantVersion) {
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
func (u *updater) refuseIfPackaged(ctx context.Context, items []artefact) error {
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

// backupPath is where the outgoing binary is kept.
func backupPath(target, version string) string {
	return fmt.Sprintf("%s.%s.previous", target, version)
}

// newestBackup finds the most recently kept copy of a binary, and the version it is.
func newestBackup(target string) (path, version string) {
	dir, base := filepath.Split(target)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ""
	}
	var newest os.DirEntry
	var newestMod time.Time
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, base+".") || !strings.HasSuffix(name, ".previous") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if newest == nil || info.ModTime().After(newestMod) {
			newest, newestMod = e, info.ModTime()
		}
	}
	if newest == nil {
		return "", ""
	}
	name := newest.Name()
	return filepath.Join(dir, name),
		strings.TrimSuffix(strings.TrimPrefix(name, base+"."), ".previous")
}

// sameVersion compares versions tolerantly, since a tag may carry a v and a
// development build may not be a version at all.
func sameVersion(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// checksumFile is used by the tests to build a fixture release.
func checksumFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
