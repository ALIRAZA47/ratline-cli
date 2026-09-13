// Package selfupdate replaces an installed binary in place, on a live server.
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
//   - A binary that runs but cannot work here, which is what a downgrade past a
//     schema migration looks like. Each product supplies its own Verify for that.
//   - A half-written file. Each install is an atomic rename within the same
//     filesystem, so a timer firing mid-update sees the old inode or the new one and
//     never a partial file.
//   - A new binary that turns out to be broken anyway. The previous one is kept
//     beside it, and --rollback puts it back.
//
// It is shared by ratline and ratline-panel. What differs between them lives in the
// three function fields on Updater — what a release installs, how a candidate proves
// itself, and what has to happen afterwards. Everything else is identical, and the
// parts worth not having two copies of are exactly the security-relevant ones:
// checksum verification and the atomic swap.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// Artefact is one file a release installs.
type Artefact struct {
	// Asset is the file name in the release.
	Asset string
	// Target is where it is installed.
	Target string
	// Mode is the mode it is installed with.
	Mode fs.FileMode
	// Required marks an artefact whose absence from a release is a broken release.
	Required bool
}

// Updater performs the update for one product.
type Updater struct {
	// Product is what to call this in messages: "ratline", "ratline-panel".
	Product string
	// Repo is the project the artefacts come from.
	Repo string
	// BaseURL is where release artefacts live. Overridable, because a server without
	// a route to github is a normal thing and mirroring the release is the obvious
	// answer.
	BaseURL string
	// LatestAPI is injectable so the release-lookup failure modes can be tested
	// without reaching github; empty means the real endpoint.
	LatestAPI string
	// Current is the running version.
	Current string

	Log    *log.Logger
	Runner system.Runner

	// AllowUnverified installs without checksums. Deliberately explicit.
	AllowUnverified bool

	// Artefacts is what this release installs. Resolved rather than fixed, so an
	// install under /opt or /usr/bin updates itself rather than something under
	// /usr/local that may not exist.
	Artefacts func() ([]Artefact, error)
	// Verify proves a staged binary works here, before it goes near the install
	// path. Called again on the installed path afterwards, because a rename onto a
	// symlink or a bind mount can land somewhere unexpected.
	Verify func(ctx context.Context, path, version string) error
	// PreInstall runs before anything is downloaded — where a product refuses to
	// update a binary its package manager owns.
	PreInstall func(ctx context.Context, items []Artefact) error
}

// Result describes a completed update.
type Result struct {
	From    string
	To      string
	Items   []Artefact
	Backups map[string]string
}

// DefaultBaseURL is the releases page for a repository.
func DefaultBaseURL(repo string) string {
	return "https://github.com/" + repo + "/releases"
}

// DefaultLatestAPI reports the newest published tag for a repository.
func DefaultLatestAPI(repo string) string {
	return "https://api.github.com/repos/" + repo + "/releases/latest"
}

// Latest resolves a request into a concrete version string.
func (u *Updater) Latest(ctx context.Context, want string) (string, error) {
	if want != "" {
		return strings.TrimPrefix(want, "v"), nil
	}
	endpoint := u.LatestAPI
	if endpoint == "" {
		endpoint = DefaultLatestAPI(u.Repo)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "building the release request")
	}
	req.Header.Set("User-Agent", u.Product)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeExternal, "asking for the latest release").
			WithHint("a server with no route to github can be pointed at a mirror: "+
				"%s update --base-url https://mirror.example.internal/ratline --version X", u.Product)
	}
	defer resp.Body.Close() //nolint:errcheck // a response body on a read path
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// This endpoint 404s when a repository has published no releases at all, which
		// is a different problem from a flaky API — and "pass --version" is the wrong
		// advice for it, because there would be no assets to download either.
		return "", rlerr.Externalf("no release has been published for %s", u.Repo).
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

// AssetURL is where one artefact of one version lives.
func (u *Updater) AssetURL(version, asset string) string {
	base := u.BaseURL
	if base == "" {
		base = DefaultBaseURL(u.Repo)
	}
	return fmt.Sprintf("%s/download/v%s/%s", strings.TrimRight(base, "/"), version, asset)
}

// Checksums reads the release's SHA256SUMS into a name-to-digest map.
//
// A release with no checksum file is not treated as "fine": an unverified binary
// installed as root on a server that holds every tenant's keys is precisely the
// supply-chain hole the runtime installer already refuses to leave open.
func (u *Updater) Checksums(ctx context.Context, version string) (map[string]string, error) {
	url := u.AssetURL(version, "SHA256SUMS")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "building a request for %s", url)
	}
	req.Header.Set("User-Agent", u.Product)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if u.AllowUnverified {
			u.Log.Warn("could not fetch SHA256SUMS, and --allow-unverified was given", "url", url)
			return map[string]string{}, nil
		}
		return nil, rlerr.Wrap(err, rlerr.CodeExternal, "fetching %s", url)
	}
	defer resp.Body.Close() //nolint:errcheck // a response body on a read path
	if resp.StatusCode != http.StatusOK {
		if u.AllowUnverified {
			u.Log.Warn("no SHA256SUMS in this release, and --allow-unverified was given",
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
	if len(sums) == 0 && !u.AllowUnverified {
		return nil, rlerr.Externalf("%s is empty or unparseable", url)
	}
	return sums, nil
}

// Run performs the update. A nil Result with a nil error means the requested
// version is already installed and nothing was done.
func (u *Updater) Run(ctx context.Context, want string) (*Result, error) {
	target, err := u.Latest(ctx, want)
	if err != nil {
		return nil, err
	}
	if SameVersion(u.Current, target) {
		return nil, nil
	}

	items, err := u.Artefacts()
	if err != nil {
		return nil, err
	}
	for _, a := range items {
		// An empty target would fail the rename, and it would fail it after the main
		// binary had already been swapped. Refuse before anything is touched.
		if strings.TrimSpace(a.Target) == "" {
			return nil, rlerr.Preconditionf("there is no configured install path for %s", a.Asset)
		}
		if !filepath.IsAbs(a.Target) {
			return nil, rlerr.Preconditionf("the install path for %s is not absolute: %s", a.Asset, a.Target)
		}
	}
	if u.PreInstall != nil {
		if err := u.PreInstall(ctx, items); err != nil {
			return nil, err
		}
	}

	// Staged beside its own install target, one directory per destination: a rename
	// is only atomic within a filesystem, and /tmp is very often a different one.
	// Artefacts need not share a directory either — the shell wrapper's path is
	// configurable and may well be on another mount — so staging both next to the
	// main binary would install one of them across a device boundary and fail.
	stages := map[string]string{}
	defer func() {
		for _, dir := range stages {
			_ = os.RemoveAll(dir)
		}
	}()
	for _, a := range items {
		parent := filepath.Dir(a.Target)
		if _, ok := stages[parent]; ok {
			continue
		}
		dir, err := os.MkdirTemp(parent, ".ratline-update-*")
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "creating a staging directory in %s", parent)
		}
		stages[parent] = dir
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "securing the staging directory")
		}
	}

	sums, err := u.Checksums(ctx, target)
	if err != nil {
		return nil, err
	}

	staged := map[string]string{}
	for _, a := range items {
		path := filepath.Join(stages[filepath.Dir(a.Target)], a.Asset)
		u.Log.Info("downloading", "asset", a.Asset, "version", target)
		got, err := Download(ctx, u.AssetURL(target, a.Asset), path, u.Product, 10*time.Minute)
		if err != nil {
			return nil, err
		}
		if wantSum, ok := sums[a.Asset]; ok {
			if got != wantSum {
				// Either the download was corrupted or the artefact is not the one the
				// release published. Both are refusals, not warnings.
				return nil, rlerr.Externalf("%s does not match the published checksum", a.Asset).
					WithHint("expected %s, got %s — retry, and if it persists the release "+
						"or the mirror is wrong", wantSum[:16], got[:16])
			}
		} else if !u.AllowUnverified {
			return nil, rlerr.Externalf("the release does not list a checksum for %s", a.Asset).
				WithHint("this is unusual and worth understanding before installing; " +
					"--allow-unverified overrides it")
		}
		if err := os.Chmod(path, a.Mode); err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "setting mode on %s", path)
		}
		staged[a.Target] = path
	}

	// Prove the new binary works before it is anywhere near the install path.
	if u.Verify != nil {
		if err := u.Verify(ctx, staged[items[0].Target], target); err != nil {
			return nil, err
		}
	}

	// Keep the outgoing binaries where a rollback can find them.
	backups := map[string]string{}
	for _, a := range items {
		if !system.Exists(a.Target) {
			continue
		}
		backup := BackupPath(a.Target, u.Current)
		if err := system.CopyFile(a.Target, backup, a.Mode, 0, 0); err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "keeping a copy of %s", a.Target)
		}
		backups[a.Target] = backup
	}

	// The swap. Atomic per file, and the rollback stack puts back anything already
	// replaced if a later one fails.
	rb := system.NewRollback(u.Log)
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
	if swapErr == nil && u.Verify != nil {
		// The installed binary, not the staged one: a rename onto a path that is a
		// symlink or a bind mount can land somewhere unexpected.
		swapErr = u.Verify(ctx, items[0].Target, target)
	}
	if swapErr != nil {
		rb.Unwind(ctx)
		return nil, rlerr.Wrap(swapErr, rlerr.CodeOf(swapErr),
			"the update was reverted and %s is unchanged", u.Current)
	}
	rb.Commit()

	return &Result{From: u.Current, To: target, Items: items, Backups: backups}, nil
}

// Rollback restores the binaries kept by the last update.
func (u *Updater) Rollback(ctx context.Context) (map[string]string, error) {
	_ = ctx
	items, err := u.Artefacts()
	if err != nil {
		return nil, err
	}
	restored := map[string]string{}
	for _, a := range items {
		backup, version := NewestBackup(a.Target)
		if backup == "" {
			continue
		}
		if err := os.Rename(backup, a.Target); err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "restoring %s", a.Target)
		}
		if err := os.Chmod(a.Target, a.Mode); err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "setting mode on %s", a.Target)
		}
		restored[a.Target] = version
	}
	if len(restored) == 0 {
		return nil, rlerr.Preconditionf("there is no kept binary to roll back to").
			WithHint("a copy is only kept by '%s update'; reinstall the version you "+
				"want with the installer", u.Product)
	}
	return restored, nil
}

// Download streams a URL to disk and returns its SHA-256.
func Download(ctx context.Context, url, dest, agent string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "building a request for %s", url)
	}
	req.Header.Set("User-Agent", agent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeExternal, "downloading %s", url)
	}
	defer resp.Body.Close() //nolint:errcheck // a response body on a read path
	if resp.StatusCode != http.StatusOK {
		return "", rlerr.Externalf("%s returned HTTP %d", url, resp.StatusCode)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "creating %s", dest)
	}
	defer f.Close() //nolint:errcheck // the hash below is what proves the write
	h := sha256.New()
	// Hash while writing, so the file is never read twice and a huge artefact does
	// not have to fit in memory.
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 512<<20)); err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeExternal, "downloading %s", url)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// BackupPath is where the outgoing binary is kept.
func BackupPath(target, version string) string {
	return fmt.Sprintf("%s.%s.previous", target, version)
}

// NewestBackup finds the most recently kept copy of a binary, and the version it is.
func NewestBackup(target string) (path, version string) {
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

// SameVersion compares versions tolerantly, since a tag may carry a v and a
// development build may not be a version at all.
func SameVersion(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// ChecksumFile hashes a file on disk.
func ChecksumFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // the caller names the path
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck // a read-only handle
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
