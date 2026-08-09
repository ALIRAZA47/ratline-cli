package system

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// ManagedHeader marks a file ratline generated. Combined with a state row it is
// how the tool decides whether it may overwrite something: a config file with
// neither is assumed to belong to the operator and is never clobbered.
const ManagedHeader = "# managed-by: ratline"

// KeepUnchanged is passed as uid or gid to leave ownership alone.
const KeepUnchanged = -1

// WriteFileAtomic writes data to path via a temporary file in the same
// directory followed by a rename, so a reader never observes a half-written
// config and a crash never truncates the previous one.
//
// The parent directory must already exist: creating it here would mean guessing
// its mode and owner, and every caller knows better than this function does.
func WriteFileAtomic(path string, data []byte, mode fs.FileMode, uid, gid int) error {
	dir := filepath.Dir(path)
	// Lstat, not Stat: the parent must be a real directory, not a symlink. ratline writes
	// as root into directories a tenant owns — a site's .env, its logs, its manifest — and
	// a tenant with shell access could replace one of those directories with a symlink to,
	// say, /etc/cron.d between operations. Stat would follow it and this write would land
	// wherever the tenant pointed, as root. A ratline directory is never legitimately a
	// symlink, so refusing one costs nothing and closes that redirection.
	fi, err := os.Lstat(dir)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition, "cannot write %s", path).
			WithHint("its parent directory %s does not exist yet", dir)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return rlerr.Preconditionf("refusing to write %s: its parent %s is a symlink", path, dir).
			WithHint("ratline never writes through a symlinked directory; something replaced " +
				"a real directory with a link to somewhere else")
	}
	if !fi.IsDir() {
		return rlerr.Preconditionf("cannot write %s: %s is not a directory", path, dir)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".ratline-*")
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "creating a temporary file next to %s", path)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return rlerr.Wrap(err, rlerr.CodeGeneric, "writing %s", tmpName)
	}
	// Set the mode and owner before the rename so the file is never briefly
	// visible at the wrong permissions under its real name.
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return rlerr.Wrap(err, rlerr.CodeGeneric, "setting mode %04o on %s", mode.Perm(), tmpName)
	}
	if uid != KeepUnchanged || gid != KeepUnchanged {
		if err := tmp.Chown(uid, gid); err != nil {
			tmp.Close()
			return rlerr.Wrap(err, rlerr.CodeGeneric, "setting ownership %d:%d on %s", uid, gid, tmpName)
		}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return rlerr.Wrap(err, rlerr.CodeGeneric, "flushing %s", tmpName)
	}
	if err := tmp.Close(); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "closing %s", tmpName)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "renaming %s into place as %s", tmpName, path)
	}
	cleanup = false
	return fsyncDir(dir)
}

// WriteFileAtomicPreserve writes data while keeping the existing file's mode and
// ownership. Used for files ratline shares with other tools —
// authorized_keys above all — where changing the mode would be a surprise.
func WriteFileAtomicPreserve(path string, data []byte, fallbackMode fs.FileMode) error {
	mode, uid, gid := fallbackMode, KeepUnchanged, KeepUnchanged
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(st.Uid), int(st.Gid)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "inspecting %s", path)
	}
	return WriteFileAtomic(path, data, mode, uid, gid)
}

// EnsureDir creates a directory with an exact mode and owner if it is missing.
// It reports whether it created it, which the rollback stack uses to decide
// whether removing it on failure is safe.
func EnsureDir(path string, mode fs.FileMode, uid, gid int) (bool, error) {
	// Lstat so a symlink is seen as a symlink, not as whatever it points at. A tenant who
	// replaced a site subdirectory with a link to /etc would otherwise have ratline accept
	// the link as "the directory already exists" and then write through it as root.
	fi, err := os.Lstat(path)
	switch {
	case err == nil:
		if fi.Mode()&os.ModeSymlink != 0 {
			return false, rlerr.Preconditionf("%s is a symlink, not a directory", path).
				WithHint("ratline will not treat a symlink as one of its directories; " +
					"remove it if it was put there by mistake")
		}
		if !fi.IsDir() {
			return false, rlerr.Preconditionf("%s exists but is not a directory", path)
		}
		return false, nil
	case !errors.Is(err, fs.ErrNotExist):
		return false, rlerr.Wrap(err, rlerr.CodeGeneric, "inspecting %s", path)
	}
	if err := os.Mkdir(path, mode); err != nil {
		return false, rlerr.Wrap(err, rlerr.CodeGeneric, "creating %s", path)
	}
	// Mkdir's mode is filtered by the process umask, so set it explicitly.
	if err := os.Chmod(path, mode); err != nil {
		return true, rlerr.Wrap(err, rlerr.CodeGeneric, "setting mode %04o on %s", mode.Perm(), path)
	}
	if uid != KeepUnchanged || gid != KeepUnchanged {
		if err := os.Chown(path, uid, gid); err != nil {
			return true, rlerr.Wrap(err, rlerr.CodeGeneric, "setting ownership %d:%d on %s", uid, gid, path)
		}
	}
	return true, nil
}

// Chown sets ownership, tolerating KeepUnchanged for either field.
func Chown(path string, uid, gid int) error {
	if uid == KeepUnchanged && gid == KeepUnchanged {
		return nil
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "setting ownership %d:%d on %s", uid, gid, path)
	}
	return nil
}

// Chmod sets a file's permission bits.
func Chmod(path string, mode fs.FileMode) error {
	if err := os.Chmod(path, mode); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "setting mode %04o on %s", mode.Perm(), path)
	}
	return nil
}

// ChmodNoFollow sets a file's mode without following a final symlink.
//
// os.Chmod resolves symlinks, so chmod'ing a path inside a directory a tenant owns can be
// redirected: the tenant swaps the file for a link to one of root's between the file's
// creation and the chmod, and root then changes the mode of the link's target — a
// world-readable private key one race away. This opens the path with O_NOFOLLOW so a
// symlinked final component fails outright, confirms the descriptor is a regular file, and
// fchmod's the descriptor, never re-resolving the path between check and change. Pair it with
// CheckNoSymlinks when an intermediate directory could also be swapped: O_NOFOLLOW guards
// only the last component.
func ChmodNoFollow(path string, mode fs.FileMode) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition, "cannot set the mode of %s", path).
			WithHint("it may be a symlink, which ratline will not chmod through")
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "inspecting %s", path)
	}
	if !fi.Mode().IsRegular() {
		return rlerr.Preconditionf("refusing to chmod %s: it is not a regular file", path)
	}
	if err := f.Chmod(mode); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "setting mode %04o on %s", mode.Perm(), path)
	}
	return nil
}

// Owner reports a path's uid and gid.
func Owner(path string) (uid, gid int, err error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0, rlerr.Wrap(err, rlerr.CodeGeneric, "inspecting %s", path)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, rlerr.Genericf("cannot read ownership of %s on this platform", path)
	}
	return int(st.Uid), int(st.Gid), nil
}

// ReadFileLimit reads at most max bytes, refusing larger files rather than
// loading them. Applied to authorized_keys, certificates and fetched key lists,
// all of which are attacker-influenced in size.
func ReadFileLimit(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > max {
		return nil, rlerr.Preconditionf("%s is %d bytes, which exceeds the %d-byte limit", path, fi.Size(), max)
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", path)
	}
	if int64(len(data)) > max {
		return nil, rlerr.Preconditionf("%s exceeds the %d-byte limit", path, max)
	}
	return data, nil
}

// Exists reports whether a path exists, following symlinks.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsDir reports whether a path is a directory.
func IsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// IsSymlink reports whether a path is a symbolic link.
func IsSymlink(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&fs.ModeSymlink != 0
}

// EnsureSymlink makes link point at target, reporting whether it changed
// anything. It refuses to replace something that is not already a symlink, so
// an operator's real file at that path is never destroyed.
func EnsureSymlink(target, link string) (bool, error) {
	fi, err := os.Lstat(link)
	switch {
	case err == nil && fi.Mode()&fs.ModeSymlink != 0:
		if cur, err := os.Readlink(link); err == nil && cur == target {
			return false, nil
		}
	case err == nil:
		return false, rlerr.Preconditionf("%s already exists and is not a symlink", link).
			WithHint("move it aside if you want ratline to manage this path")
	case !errors.Is(err, fs.ErrNotExist):
		return false, rlerr.Wrap(err, rlerr.CodeGeneric, "inspecting %s", link)
	}

	dir := filepath.Dir(link)
	tmp := filepath.Join(dir, "."+filepath.Base(link)+".ratline-link")
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return false, rlerr.Wrap(err, rlerr.CodeGeneric, "creating symlink %s", tmp)
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return false, rlerr.Wrap(err, rlerr.CodeGeneric, "moving symlink into place at %s", link)
	}
	return true, fsyncDir(dir)
}

// HasManagedHeader reports whether a file carries ratline's ownership marker in
// its first few lines.
func HasManagedHeader(path string) (bool, error) {
	data, err := ReadFileLimit(path, 1<<20)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	lines := strings.SplitN(string(data), "\n", 6)
	for _, l := range lines {
		if strings.Contains(l, ManagedHeader) {
			return true, nil
		}
	}
	return false, nil
}

// RemoveManaged deletes a file only if ratline generated it.
func RemoveManaged(path string) error {
	managed, err := HasManagedHeader(path)
	if err != nil {
		return err
	}
	if !Exists(path) {
		return nil
	}
	if !managed {
		return rlerr.Preconditionf("refusing to delete %s: it does not carry the %q header", path, ManagedHeader).
			WithHint("ratline only removes files it created; delete it by hand if you are sure")
	}
	if err := os.Remove(path); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing %s", path)
	}
	return nil
}

// CopyFile copies src to dst atomically with an explicit mode and owner.
func CopyFile(src, dst string, mode fs.FileMode, uid, gid int) error {
	data, err := ReadFileLimit(src, 64<<20)
	if err != nil {
		return err
	}
	return WriteFileAtomic(dst, data, mode, uid, gid)
}

// BackupFile copies path to path.ratline-backup-<suffix>, returning the backup
// path. Used before touching sshd_config and nginx vhosts.
func BackupFile(path, suffix string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodePrecondition, "cannot back up %s", path)
	}
	uid, gid, err := Owner(path)
	if err != nil {
		return "", err
	}
	backup := path + ".ratline-backup-" + suffix
	if err := CopyFile(path, backup, fi.Mode().Perm(), uid, gid); err != nil {
		return "", err
	}
	return backup, nil
}

// DirSize sums the apparent size of every regular file under path. Entries it
// cannot read are skipped: a per-user disk figure is worth reporting even when
// one subdirectory refuses.
func DirSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	if err != nil {
		return total, rlerr.Wrap(err, rlerr.CodeGeneric, "measuring %s", path)
	}
	return total, nil
}

// fsyncDir flushes a directory entry so a rename survives a power loss.
func fsyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "opening %s to flush it", dir)
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		// Some platforms and filesystems refuse fsync on a directory. The
		// rename itself has already happened, so this is not worth failing on.
		if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.ENOTTY) {
			return nil
		}
		return rlerr.Wrap(err, rlerr.CodeGeneric, "flushing directory %s", dir)
	}
	return nil
}

// MkdirAllMode creates a directory and any missing parents, then sets the exact
// mode on the leaf. Parents get 0755, which is right for the /etc and /var trees
// ratline creates; anything that needs to be tighter is created individually.
func MkdirAllMode(path string, mode fs.FileMode) error {
	// Every level this call creates gets the requested mode, not just the leaf.
	//
	// os.MkdirAll applies the process umask, which is 0027 while provisioning — so
	// asking for a 0755 directory three levels down produced 0750 parents and a 0755
	// leaf that nothing unprivileged could reach through them. That silently broke
	// two separate things: nginx could not traverse into the ACME webroot's
	// .well-known, so every HTTP-01 challenge 404'd; and no tenant could traverse
	// into /opt/ratline, so no site could execute its own managed interpreter.
	//
	// Directories that already exist are left alone. Walking up to /opt or /var and
	// chmod'ing them because they happened to be on the way would be a destructive
	// side effect on somebody else's filesystem.
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return rlerr.Genericf("MkdirAllMode needs an absolute path, got %q", path)
	}

	// The components that do not exist yet, deepest last.
	var missing []string
	for cur := clean; ; cur = filepath.Dir(cur) {
		if _, err := os.Stat(cur); err == nil {
			break
		}
		missing = append([]string{cur}, missing...)
		if parent := filepath.Dir(cur); parent == cur {
			break
		}
	}

	if err := os.MkdirAll(clean, mode); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "creating %s", path)
	}
	for _, dir := range missing {
		if err := Chmod(dir, mode); err != nil {
			return err
		}
	}
	// The leaf is chmod'ed even when it already existed, which is what makes this
	// idempotent for a directory whose mode has drifted.
	return Chmod(clean, mode)
}

// ChownTree sets ownership on a directory and everything under it.
//
// Symlinks are changed with lchown, not followed. Following them would let a symlink
// inside a restored archive — pointing at /etc/shadow, say — hand a tenant ownership of
// a file outside their own tree, which is a privilege escalation delivered by tarball.
//
// Errors on individual entries are collected rather than fatal on the first one: a tree
// left half-owned is worse than one where a single unreadable entry is reported.
func ChownTree(root string, uid, gid int) error {
	if uid == KeepUnchanged && gid == KeepUnchanged {
		return nil
	}
	var failed []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Recorded and skipped rather than returned: aborting the walk on the first
			// unreadable entry would leave the tree half-owned, which is worse than one
			// entry being reported. Every failure is collected and reported below.
			failed = append(failed, path)
			//nolint:nilerr // collected in `failed` and reported after the walk
			return nil
		}
		// os.Lchown on a non-symlink behaves exactly like os.Chown, so this is the
		// correct call for every entry rather than a special case for links.
		if lerr := os.Lchown(path, uid, gid); lerr != nil {
			failed = append(failed, path)
		}
		_ = d
		return nil
	})
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "walking %s to set ownership", root)
	}
	if len(failed) > 0 {
		shown := failed
		if len(shown) > 3 {
			shown = shown[:3]
		}
		return rlerr.Genericf("could not set ownership on %d path(s) under %s, including %s",
			len(failed), root, strings.Join(shown, ", "))
	}
	return nil
}

// CheckNoSymlinks refuses if any path component below trustedBase is a symlink.
//
// The single-component Lstat guards in WriteFileAtomic and EnsureDir catch a directory that
// was swapped for a symlink at the level being written; this catches one swapped higher up
// the tree, which those cannot see because the kernel follows every component before the
// last. Anchored at a directory the tenant cannot modify — /home is root-owned 0755, so a
// tenant can rearrange things inside their own home but cannot replace the home itself —
// and walked downward, lstat'ing each component so a link anywhere along the way is refused.
//
// path must be under trustedBase; both are cleaned first. trustedBase itself is trusted and
// not re-checked.
func CheckNoSymlinks(trustedBase, path string) error {
	base := filepath.Clean(trustedBase)
	target := filepath.Clean(path)
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rlerr.Preconditionf("%s is not under %s", path, trustedBase)
	}
	if rel == "." {
		return nil
	}
	cur := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// Not yet created is fine — there is nothing to redirect through. The
				// components that do exist have been checked.
				return nil
			}
			return rlerr.Wrap(err, rlerr.CodeGeneric, "inspecting %s", cur)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return rlerr.Preconditionf("refusing to operate under %s: %s is a symlink", target, cur).
				WithHint("a directory ratline manages was replaced with a link to somewhere " +
					"else; remove it and let ratline recreate the real directory")
		}
	}
	return nil
}
