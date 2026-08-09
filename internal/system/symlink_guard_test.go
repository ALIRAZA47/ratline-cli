package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ratline writes as root into directories a tenant owns. If a tenant replaces one of those
// directories with a symlink to, say, /etc/cron.d, a write that used os.Stat would follow
// it and land as root wherever the tenant pointed. These guards refuse that.
func TestWriteFileAtomicRefusesASymlinkedParent(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	// Writing into the symlinked parent must be refused, even though it resolves to a real
	// directory — that is exactly the redirection the guard exists for.
	err := WriteFileAtomic(filepath.Join(link, "f"), []byte("x"), 0o600, KeepUnchanged, KeepUnchanged)
	if err == nil {
		t.Fatal("wrote through a symlinked parent directory")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("the refusal should mention the symlink: %v", err)
	}
	// A write into the real directory still works.
	if err := WriteFileAtomic(filepath.Join(real, "f"), []byte("x"), 0o600, KeepUnchanged, KeepUnchanged); err != nil {
		t.Errorf("a write into a real directory was refused: %v", err)
	}
}

func TestEnsureDirRefusesAnExistingSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "logs")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	// EnsureDir must not accept the symlink as "the directory already exists".
	if _, err := EnsureDir(link, 0o750, KeepUnchanged, KeepUnchanged); err == nil {
		t.Error("EnsureDir accepted a symlink as its directory")
	}
	// A fresh directory is created normally.
	fresh := filepath.Join(dir, "fresh")
	created, err := EnsureDir(fresh, 0o750, KeepUnchanged, KeepUnchanged)
	if err != nil || !created {
		t.Errorf("EnsureDir(%q) = created=%v err=%v, want created,nil", fresh, created, err)
	}
}

func TestCheckNoSymlinksWalksTheWholePath(t *testing.T) {
	base := t.TempDir()
	// base/a/b/c, all real.
	real := filepath.Join(base, "a", "b", "c")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckNoSymlinks(base, real); err != nil {
		t.Errorf("a clean path was refused: %v", err)
	}
	// A component that does not exist yet is fine — nothing to redirect through.
	if err := CheckNoSymlinks(base, filepath.Join(real, "not-yet", "here")); err != nil {
		t.Errorf("a not-yet-created tail was refused: %v", err)
	}
	// Replace a *middle* component with a symlink: the single-level guards cannot see this,
	// which is the whole reason this walk exists.
	elsewhere := filepath.Join(base, "elsewhere")
	if err := os.MkdirAll(filepath.Join(elsewhere, "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(base, "a", "b")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(base, "a", "b")); err != nil {
		t.Fatal(err)
	}
	if err := CheckNoSymlinks(base, real); err == nil {
		t.Error("a symlinked middle component was not caught")
	}
	// A path outside the base is refused rather than walked.
	if err := CheckNoSymlinks(base, filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("a path outside the trusted base was accepted")
	}
}

// ratline chmods files it has just written into directories a tenant owns — a site's deploy
// key above all. If the tenant swaps the file for a symlink to one of root's between the
// write and the chmod, a plain os.Chmod would follow it and change the target's mode as root
// (a private key one race from world-readable). ChmodNoFollow refuses the symlink instead.
func TestChmodNoFollowRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(victim, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := ChmodNoFollow(link, 0o644); err == nil {
		t.Error("ChmodNoFollow followed a symlink")
	}
	if fi, err := os.Lstat(victim); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("the symlink target's mode changed to %04o; the chmod was followed", fi.Mode().Perm())
	}

	// A real regular file is chmod'd normally.
	real := filepath.Join(dir, "real")
	if err := os.WriteFile(real, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ChmodNoFollow(real, 0o644); err != nil {
		t.Errorf("ChmodNoFollow on a real file = %v", err)
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %04o, want 0644", fi.Mode().Perm())
	}

	// A non-regular file (here a directory) is refused: ratline only means to chmod files.
	if err := ChmodNoFollow(dir, 0o700); err == nil {
		t.Error("ChmodNoFollow accepted a directory")
	}
}
