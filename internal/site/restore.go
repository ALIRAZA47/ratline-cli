package site

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// RestoreOptions is the resolved form of `ratline restore`.
type RestoreOptions struct {
	// Archive is the .tar.gz written by `ratline backup`.
	Archive string
	// Force allows replacing a directory that already exists.
	Force bool
	// SkipStart leaves the service stopped, for restoring onto a server that is not
	// meant to start serving yet.
	SkipStart bool
}

// RestoreResult reports what was put back.
type RestoreResult struct {
	Kind     string      `json:"kind"` // site or home
	Name     string      `json:"name"`
	Target   string      `json:"target"`
	Replaced bool        `json:"replaced"`
	Site     *state.Site `json:"site,omitempty"`
	Health   string      `json:"health,omitempty"`
	Bytes    int64       `json:"bytes"`
}

// Restore puts a backup archive back.
//
// The difficult part is not the extraction, it is everything the archive does not
// contain. `backup` archives a directory: the code, the logs, the .env and — for a site
// — the manifest. It does not archive the state database, the nginx vhost, the systemd
// unit, or the tenant's uid. So a restore has to rebuild all of those from the one
// record that travelled with the files, and it has to cope with the uid having changed,
// which it will have if the account was recreated on a new server.
//
// Ordering matters and is deliberate:
//
//  1. Read the archive's manifest *before* touching anything, so a bad archive is
//     refused while the existing site is still intact and serving.
//  2. Require the owning account to exist. Recreating it means a uid, a group, a shell
//     and authorized_keys, which is `user add`'s job and not something to infer.
//  3. Extract to a staging directory beside the target, on the same filesystem, so the
//     swap is a rename rather than a copy that can fail halfway.
//  4. chown to the account's *current* uid and gid, because the numbers in the archive
//     belong to whatever server wrote it.
//  5. Swap, keeping the previous directory until everything after it has succeeded.
//  6. Rebuild the state row, the vhost and the unit, then prove it serves.
func (m *Manager) Restore(ctx context.Context, opts RestoreOptions) (res *RestoreResult, err error) {
	archive, err := filepath.Abs(opts.Archive)
	if err != nil {
		return nil, rlerr.Usagef("cannot resolve %s", opts.Archive)
	}
	fi, err := os.Stat(archive)
	if err != nil {
		return nil, rlerr.Preconditionf("cannot read the archive %s", archive).
			WithHint("list what is available with: ls -la %s", m.Cfg.Paths.BackupDir)
	}
	if fi.IsDir() {
		return nil, rlerr.Usagef("%s is a directory, not an archive", archive)
	}

	// 1. Look inside first. Everything that can be judged from the archive alone is
	// judged now, while the site that is about to be replaced is still running.
	top, err := m.archiveRoot(ctx, archive)
	if err != nil {
		return nil, err
	}

	stage, err := os.MkdirTemp(m.Cfg.Paths.HomeBase, ".ratline-restore-*")
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "creating a staging directory")
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o700); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "securing the staging directory")
	}
	if err := m.untar(ctx, archive, stage); err != nil {
		return nil, err
	}
	staged := filepath.Join(stage, top)
	if !system.IsDir(staged) {
		return nil, rlerr.Preconditionf("the archive did not contain the directory %q it advertised", top)
	}

	res = &RestoreResult{Bytes: fi.Size()}
	if _, ok := ManifestExistsIn(staged); ok {
		return m.restoreSite(ctx, opts, staged, res)
	}
	return m.restoreHome(ctx, opts, top, staged, res)
}

// restoreSite puts a site directory back and rebuilds everything around it.
func (m *Manager) restoreSite(ctx context.Context, opts RestoreOptions, staged string, res *RestoreResult) (*RestoreResult, error) {
	manifest, _ := ManifestExistsIn(staged)
	site, err := ReadManifest(manifest)
	if err != nil {
		return nil, err
	}
	res.Kind, res.Name, res.Site = "site", site.Domain, site

	id, err := m.requireOwner(site.Owner)
	if err != nil {
		return nil, err
	}
	target := m.Cfg.SiteDir(site.Owner, site.Domain)
	res.Target = target

	// A site that already exists in state but belongs to somebody else is not a
	// restore, it is a takeover. Refuse before anything is moved.
	if existing, err := m.State.FindSiteByName(ctx, site.Domain); err == nil && existing != nil {
		if existing.Owner != site.Owner {
			return nil, rlerr.Preconditionf(
				"%s already exists and belongs to %s, not %s", site.Domain, existing.Owner, site.Owner).
				WithHint("delete it first, or restore onto a server that does not have it")
		}
	}
	// And a name another tenant is serving through nginx would collide the same way.
	if conflict, err := m.Nginx.ConflictingServerName(site.Domain, site.Domain); err != nil {
		return nil, err
	} else if conflict != "" {
		return nil, rlerr.Preconditionf("%s is already claimed by the nginx configuration %s",
			site.Domain, conflict).
			WithHint("remove the duplicate server_name; nginx resolves a collision unpredictably")
	}

	if m.DryRun {
		m.Log.Info("would restore a site", "domain", site.Domain, "owner", site.Owner,
			"runtime", site.Runtime, "to", target)
		return res, nil
	}

	replaced, restoreDir, err := m.swapIn(ctx, staged, target, id, opts.Force)
	if err != nil {
		return nil, err
	}
	res.Replaced = replaced

	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)
	if restoreDir != "" {
		rb.Push("replaced "+target, func(context.Context) error {
			_ = os.RemoveAll(target)
			return os.Rename(restoreDir, target)
		})
	}

	// A port-listening site needs an allocation on *this* server before the vhost is
	// rendered, or it would proxy to a port belonging to something else.
	if site.Listen == "port" {
		port, err := m.reservePort(ctx, site)
		if err != nil {
			return nil, err
		}
		site.Port = port
	}

	if err := m.State.PutSite(ctx, site); err != nil {
		return nil, err
	}
	rb.Push("recorded "+site.Domain, func(ctx context.Context) error {
		return m.State.DeleteSite(ctx, site.Domain)
	})

	// The vhost and the unit are rendered from the row rather than restored from the
	// archive, deliberately: they contain absolute paths and a uid, and this ratline
	// may generate better ones than the one that took the backup.
	if err := m.Nginx.Apply(ctx, site, nil, rb); err != nil {
		return nil, err
	}
	if err := m.ReapplyUnit(ctx, site); err != nil {
		return nil, err
	}
	if err := m.writeManifest(site, id); err != nil {
		return nil, err
	}

	if site.Dynamic() && !opts.SkipStart {
		health, err := m.startAndWait(ctx, site)
		if err != nil {
			// Everything is in place and the failure is the application's, so unwinding
			// would throw away a correct restore. Report it and leave it to be fixed.
			m.Log.Warn("the site was restored but did not become healthy",
				"domain", site.Domain, "err", err)
			res.Health = "unhealthy: " + firstLine(err.Error())
		} else {
			res.Health = health
		}
	}
	rb.Commit()
	if restoreDir != "" {
		_ = os.RemoveAll(restoreDir)
	}
	return res, nil
}

// restoreHome puts a tenant's home back, sites and all.
func (m *Manager) restoreHome(ctx context.Context, opts RestoreOptions, top, staged string, res *RestoreResult) (*RestoreResult, error) {
	owner := filepath.Base(top)
	res.Kind, res.Name = "home", owner

	id, err := m.requireOwner(owner)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeOf(err),
			"this archive is %s's home, and that account is not on this server", owner)
	}
	res.Target = id.Home

	if m.DryRun {
		m.Log.Info("would restore a home", "user", owner, "to", id.Home)
		return res, nil
	}

	replaced, restoreDir, err := m.swapIn(ctx, staged, id.Home, id, opts.Force)
	if err != nil {
		return nil, err
	}
	res.Replaced = replaced

	// A home holds site directories, each with its own manifest. Restoring the home
	// alone would leave every one of them with files but no vhost, no unit and no state
	// row — which looks exactly like a working restore until someone visits the site.
	var restored []string
	entries, _ := os.ReadDir(id.Home)
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(id.Home, e.Name())
		manifest, ok := ManifestExistsIn(dir)
		if !ok {
			continue
		}
		site, err := ReadManifest(manifest)
		if err != nil {
			m.Log.Warn("a site in this home has an unusable manifest and was left alone",
				"dir", dir, "err", err)
			continue
		}
		if site.Owner != owner {
			m.Log.Warn("a manifest in this home names a different owner and was left alone",
				"dir", dir, "manifest_owner", site.Owner)
			continue
		}
		if err := m.rebuildFromManifest(ctx, site, id, opts); err != nil {
			m.Log.Warn("a site in this home could not be rebuilt", "domain", site.Domain, "err", err)
			continue
		}
		restored = append(restored, site.Domain)
	}
	if len(restored) > 0 {
		m.Log.Info("rebuilt the sites in this home", "sites", strings.Join(restored, " "))
		res.Health = fmt.Sprintf("%s rebuilt", plural(len(restored), "site"))
	}
	if restoreDir != "" {
		_ = os.RemoveAll(restoreDir)
	}
	return res, nil
}

// rebuildFromManifest records a site and renders what serves it.
func (m *Manager) rebuildFromManifest(ctx context.Context, site *state.Site, id *system.Identity, opts RestoreOptions) (err error) {
	if site.Listen == "port" {
		port, perr := m.reservePort(ctx, site)
		if perr != nil {
			return perr
		}
		site.Port = port
	}
	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)
	if err := m.State.PutSite(ctx, site); err != nil {
		return err
	}
	if err := m.Nginx.Apply(ctx, site, nil, rb); err != nil {
		return err
	}
	if err := m.ReapplyUnit(ctx, site); err != nil {
		return err
	}
	if err := m.writeManifest(site, id); err != nil {
		return err
	}
	if site.Dynamic() && !opts.SkipStart {
		if _, err := m.startAndWait(ctx, site); err != nil {
			m.Log.Warn("restored but not healthy", "domain", site.Domain, "err", err)
		}
	}
	rb.Commit()
	return nil
}

// requireOwner resolves the account a restore belongs to, and refuses if it is absent.
//
// Not created here on purpose. An account is a uid, a gid, a group, a shell, a home and
// a set of SSH keys, and inventing those from an archive that contains none of them
// would produce a tenant nobody can log in as, owning files whose uid does not match
// anything. `user add` is the command that knows how.
func (m *Manager) requireOwner(name string) (*system.Identity, error) {
	if !system.UserExists(name) {
		return nil, rlerr.Preconditionf("the account %q does not exist on this server", name).
			WithHint("create it first, then restore: ratline user add %s", name)
	}
	return system.LookupIdentity(name)
}

// swapIn moves a staged tree into place, keeping whatever was there.
//
// The staging directory is under home_base so this is a rename within one filesystem:
// atomic, and it cannot leave a half-copied tree if the process dies. The previous
// directory is moved aside rather than deleted, so a failure in any of the steps after
// this one can put it back.
func (m *Manager) swapIn(ctx context.Context, staged, target string, id *system.Identity, force bool) (replaced bool, kept string, err error) {
	if system.Exists(target) {
		if !force {
			return false, "", rlerr.Preconditionf("%s already exists", target).
				WithHint("--force replaces it; the current contents are moved aside " +
					"and removed only once the restore has succeeded")
		}
		replaced = true
	}

	// Ownership comes from the account as it is *now*. The uids inside the archive
	// belong to whichever server wrote it, and on a rebuilt server they will belong to
	// a different account or to nobody.
	if err := system.ChownTree(staged, id.UID, id.GID); err != nil {
		return false, "", err
	}
	// The site directory and the home are both 0750: nginx reaches a site's files by
	// being in the tenant's group, and 0755 would expose every tenant to every other.
	if err := system.Chmod(staged, 0o750); err != nil {
		return false, "", err
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return false, "", rlerr.Wrap(err, rlerr.CodeGeneric, "creating %s", filepath.Dir(target))
	}
	if replaced {
		kept = target + ".restore-" + time.Now().UTC().Format("20060102T150405Z")
		if err := os.Rename(target, kept); err != nil {
			return false, "", rlerr.Wrap(err, rlerr.CodeGeneric,
				"moving the existing %s aside", target)
		}
	}
	if err := os.Rename(staged, target); err != nil {
		if kept != "" {
			_ = os.Rename(kept, target)
		}
		return false, "", rlerr.Wrap(err, rlerr.CodeGeneric, "moving the restored tree into %s", target)
	}
	m.Log.Info("restored the directory", "target", target, "replaced", replaced)
	return replaced, kept, nil
}

// archiveRoot reports the single top-level directory the archive contains.
//
// `backup` writes `tar -C <parent> <basename>`, so a well-formed archive has exactly
// one. More than one, or an entry with an absolute or traversing path, means it was not
// written by ratline — and extracting it would scatter files outside the staging
// directory.
func (m *Manager) archiveRoot(ctx context.Context, archive string) (string, error) {
	res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "tar", Args: []string{"--list", "--file", archive}, Timeout: 10 * time.Minute,
		Label: "tar --list",
	})
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeExternal, "%s is not a readable tar archive", archive)
	}
	roots := map[string]bool{}
	for _, line := range strings.Split(res.Out(), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "/") {
			return "", rlerr.Preconditionf("%s contains the absolute path %q", archive, name).
				WithHint("ratline's own archives hold relative paths only; this was written by something else")
		}
		// Rejected rather than sanitised: ".." anywhere in a member is how an archive
		// writes outside the directory it is extracted into, and there is no legitimate
		// reason for one of ratline's own backups to contain it.
		for _, part := range strings.Split(name, "/") {
			if part == ".." {
				return "", rlerr.Preconditionf("%s contains a traversing path %q", archive, name).
					WithHint("this archive would write outside the directory it is extracted into")
			}
		}
		roots[strings.SplitN(strings.TrimPrefix(name, "./"), "/", 2)[0]] = true
	}
	delete(roots, "")
	delete(roots, ".")
	if len(roots) != 1 {
		names := make([]string, 0, len(roots))
		for r := range roots {
			names = append(names, r)
		}
		return "", rlerr.Preconditionf("%s has %d top-level entries, want exactly one",
			archive, len(roots)).
			WithHint("ratline's archives contain one directory; this one has: %s",
				strings.Join(names, " "))
	}
	for r := range roots {
		return r, nil
	}
	return "", rlerr.Preconditionf("%s is empty", archive)
}

// untar extracts into a directory that already exists.
func (m *Manager) untar(ctx context.Context, archive, into string) error {
	_, err := m.Runner.Run(ctx, system.Cmd{
		Name: "tar",
		// --no-same-owner: the uids in the archive are the previous server's, and
		// swapIn sets the right ones. Letting tar apply them as root would briefly
		// leave files owned by whatever uid happened to be in the archive.
		Args:    []string{"--extract", "--gzip", "--no-same-owner", "--file", archive, "-C", into},
		Mutates: true, Timeout: 2 * time.Hour, Label: "tar --extract",
	})
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeExternal, "extracting %s", archive)
	}
	return nil
}

// reservePort allocates a port for a restored site.
//
// The port recorded in the manifest is advisory and usually not honoured: it was free on
// whichever server took the backup, and on this one it may belong to another site or to
// something that is not ratline at all. Since the vhost is re-rendered from the row, the
// only requirement is that the row and the unit agree — so the allocator decides, and it
// checks the port is genuinely free rather than only unclaimed in the database.
func (m *Manager) reservePort(ctx context.Context, site *state.Site) (int, error) {
	port, err := m.State.AllocatePort(ctx, site.Domain,
		m.Cfg.Ports.RangeStart, m.Cfg.Ports.RangeEnd, system.PortFree)
	if err != nil {
		return 0, err
	}
	if site.Port > 0 && port != site.Port {
		m.Log.Info("the archived port was not available, so a new one was allocated",
			"domain", site.Domain, "archived_port", site.Port, "port", port)
	}
	return port, nil
}

// firstLine is the first line of a multi-line message.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// plural renders "1 site" and "2 sites".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
