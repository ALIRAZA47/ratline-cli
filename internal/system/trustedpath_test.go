package system

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A tenant owns their home, so they can turn any directory below it into a symlink
// between two of ratline's operations. Checking the immediate parent with Lstat only
// sees a swap at that level; the walk has to refuse a link anywhere along the path,
// and it has to do so by never resolving through one rather than by looking first.
func TestOpenDirNoFollowRefusesAUserOwnedSymlinkAnywhereInThePath(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "a", "b", "c")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(elsewhere, "b", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(base, "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(base, "a")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// The path resolves perfectly well through the link; that is the point.
	if _, err := os.Stat(real); err != nil {
		t.Fatalf("the fixture does not resolve: %v", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: the test's own symlink is root-owned and legitimately followed")
	}
	_, err := OpenDirNoFollow(real)
	if err == nil {
		t.Fatal("OpenDirNoFollow resolved through a user-owned symlink in the middle of the path")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("the refusal should name the symlink: %v", err)
	}
}

// Root's own links — /var → /private/var on macOS, /bin → usr/bin on a merged-usr
// Linux — are part of the filesystem's layout, not an attack, and refusing them would
// make paths.run_dir: /var/run/ratline unusable. They are followed, and what the walk
// ends on is the same directory the kernel would have reached.
func TestOpenDirNoFollowFollowsRootOwnedSymlinks(t *testing.T) {
	var link string
	for _, candidate := range []string{"/var", "/etc", "/tmp", "/bin", "/lib", "/var/run", "/var/lock"} {
		fi, err := os.Lstat(candidate)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid == 0 {
			link = candidate
			break
		}
	}
	if link == "" {
		t.Skip("no root-owned symlink among the usual suspects on this host")
	}
	d, err := OpenDirNoFollow(link)
	if err != nil {
		t.Fatalf("OpenDirNoFollow(%s) refused a root-owned link: %v", link, err)
	}
	defer d.Close()
	got, err := d.Stat()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(link)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(got, want) {
		t.Errorf("the walk through %s ended somewhere other than where the kernel resolves it", link)
	}
}

func TestOpenDirNoFollowReportsAMissingPathAsNotExist(t *testing.T) {
	_, err := OpenDirNoFollow(filepath.Join(t.TempDir(), "nope", "deeper"))
	if err == nil {
		t.Fatal("opened a directory that does not exist")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing component should surface as fs.ErrNotExist so callers can treat it as absent: %v", err)
	}
}

// .env and a site's logs live in directories the tenant owns, so before root reads
// one on the tenant's behalf the thing at that path has to be a regular file the
// tenant owns: not a link to another tenant's secrets, not a FIFO that parks root's
// process for ever, not a hard link to something of root's.
func TestReadFileNoFollowRefusesLinksForeignOwnersAndFIFOs(t *testing.T) {
	dir := t.TempDir()
	me := os.Geteuid()

	regular := filepath.Join(dir, "regular")
	if err := os.WriteFile(regular, []byte("KEY=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := ReadFileNoFollow(regular, 1<<20, me)
	if err != nil || string(data) != "KEY=value\n" {
		t.Fatalf("a regular file owned by the caller must read: %q, %v", data, err)
	}
	if _, err := ReadFileNoFollow(regular, 1<<20, KeepUnchanged); err != nil {
		t.Fatalf("KeepUnchanged must accept any owner: %v", err)
	}
	if _, err := ReadFileNoFollow(regular, 4, me); err == nil {
		t.Error("the size limit was not enforced")
	}

	if _, err := ReadFileNoFollow(regular, 1<<20, me+1); err == nil {
		t.Error("a file owned by somebody else was read on the tenant's behalf")
	} else if !strings.Contains(err.Error(), "owned by") {
		t.Errorf("the refusal should say whose the file is: %v", err)
	}

	link := filepath.Join(dir, "link")
	if err := os.Symlink(regular, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ReadFileNoFollow(link, 1<<20, me); err == nil {
		t.Error("a symlink was followed")
	} else if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("the refusal should name the symlink: %v", err)
	}

	fifo := filepath.Join(dir, "fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadFileNoFollow(fifo, 1<<20, me)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a FIFO was read as if it were a file")
		} else if !strings.Contains(err.Error(), "regular file") {
			t.Errorf("the refusal should say it is not a regular file: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening a FIFO with nobody writing to it blocked; a tenant could park root here for ever")
	}

	_, err = ReadFileNoFollow(filepath.Join(dir, "absent"), 1<<20, me)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing file must surface as fs.ErrNotExist: %v", err)
	}
}

// The old guard Lstat'ed the immediate parent and then wrote by path. A link one
// level higher was invisible to it and followed by the kernel.
func TestWriteFileAtomicRefusesASymlinkedGrandparent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: the test's own symlink is root-owned and legitimately followed")
	}
	base := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(elsewhere, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(base, "outer")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	target := filepath.Join(base, "outer", "inner", "file")
	if err := WriteFileAtomic(target, []byte("x"), 0o600, KeepUnchanged, KeepUnchanged); err == nil {
		t.Fatal("wrote through a symlinked grandparent")
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "inner", "file")); err == nil {
		t.Fatal("the write landed where the link pointed")
	}
	entries, _ := os.ReadDir(filepath.Join(elsewhere, "inner"))
	if len(entries) != 0 {
		t.Errorf("a temporary file was left behind: %v", entries)
	}
}

func TestEnsureDirRefusesASymlinkedParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: the test's own symlink is root-owned and legitimately followed")
	}
	base := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(base, "parent")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	created, err := EnsureDir(filepath.Join(base, "parent", "child"), 0o750, KeepUnchanged, KeepUnchanged)
	if err == nil || created {
		t.Fatal("EnsureDir created a directory through a symlinked parent")
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "child")); err == nil {
		t.Fatal("the directory landed where the link pointed")
	}
}

func TestEnsureDirSetsTheExactModeOnWhatItCreated(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	path := filepath.Join(t.TempDir(), "d")
	created, err := EnsureDir(path, 0o755, KeepUnchanged, KeepUnchanged)
	if err != nil || !created {
		t.Fatalf("EnsureDir = %v, %v", created, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %04o, want 0755 regardless of umask", fi.Mode().Perm())
	}
	if created, err := EnsureDir(path, 0o755, KeepUnchanged, KeepUnchanged); err != nil || created {
		t.Errorf("second EnsureDir = %v, %v; want false, nil", created, err)
	}
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureDir(file, 0o755, KeepUnchanged, KeepUnchanged); err == nil {
		t.Error("a regular file was accepted as a directory")
	}
}
