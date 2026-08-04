package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.conf")

	if err := WriteFileAtomic(path, []byte("first\n"), 0o640, KeepUnchanged, KeepUnchanged); err != nil {
		t.Fatalf("WriteFileAtomic = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "first\n" {
		t.Fatalf("read back %q, %v", data, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// The mode must be exact, not filtered through the process umask.
	if got := fi.Mode().Perm(); got != 0o640 {
		t.Errorf("mode = %04o, want 0640", got)
	}

	if err := WriteFileAtomic(path, []byte("second\n"), 0o600, KeepUnchanged, KeepUnchanged); err != nil {
		t.Fatalf("overwrite = %v", err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "second\n" {
		t.Errorf("after overwrite = %q", data)
	}

	// No temporary files may be left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "ratline-") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}

func TestWriteFileAtomicRequiresAnExistingParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "file.conf")
	err := WriteFileAtomic(path, []byte("x"), 0o644, KeepUnchanged, KeepUnchanged)
	if err == nil {
		t.Fatal("WriteFileAtomic created a missing parent directory; it must refuse, because it cannot know the right mode and owner")
	}
	// The hint is where the operator is told what is actually wrong.
	if !strings.Contains(rlerr.Hint(err), "does not exist") {
		t.Errorf("hint %q does not explain that the parent is missing", rlerr.Hint(err))
	}
}

func TestWriteFileAtomicPreserveKeepsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomicPreserve(path, []byte("new\n"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomicPreserve = %v", err)
	}
	fi, _ := os.Stat(path)
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %04o, want the original 0600", got)
	}
	// A file that does not exist yet gets the fallback mode.
	fresh := filepath.Join(dir, "fresh")
	if err := WriteFileAtomicPreserve(fresh, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	fi, _ = os.Stat(fresh)
	if got := fi.Mode().Perm(); got != 0o640 {
		t.Errorf("fallback mode = %04o, want 0640", got)
	}
}

func TestEnsureDir(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "site")

	created, err := EnsureDir(path, 0o750, KeepUnchanged, KeepUnchanged)
	if err != nil || !created {
		t.Fatalf("EnsureDir = %v, created=%v", err, created)
	}
	fi, _ := os.Stat(path)
	if got := fi.Mode().Perm(); got != 0o750 {
		t.Errorf("mode = %04o, want 0750", got)
	}

	// Idempotent, and reports that it did nothing so rollback knows not to
	// remove a directory it did not create.
	created, err = EnsureDir(path, 0o750, KeepUnchanged, KeepUnchanged)
	if err != nil || created {
		t.Errorf("second EnsureDir = %v, created=%v; want nil, false", err, created)
	}

	file := filepath.Join(base, "afile")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureDir(file, 0o750, KeepUnchanged, KeepUnchanged); err == nil {
		t.Error("EnsureDir accepted a path that is a regular file")
	}
}

func TestEnsureSymlink(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "sites-available", "example.com.conf")
	link := filepath.Join(base, "sites-enabled", "example.com.conf")
	for _, d := range []string{filepath.Dir(target), filepath.Dir(link)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(target, []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := EnsureSymlink(target, link)
	if err != nil || !changed {
		t.Fatalf("EnsureSymlink = %v, changed=%v", err, changed)
	}
	changed, err = EnsureSymlink(target, link)
	if err != nil || changed {
		t.Errorf("second EnsureSymlink = %v, changed=%v; want nil, false", err, changed)
	}

	// Repointing an existing link is fine.
	other := filepath.Join(base, "sites-available", "other.conf")
	if err := os.WriteFile(other, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := EnsureSymlink(other, link); err != nil || !changed {
		t.Errorf("repointing = %v, changed=%v", err, changed)
	}

	// Clobbering an operator's real file is not.
	real := filepath.Join(base, "sites-enabled", "handwritten.conf")
	if err := os.WriteFile(real, []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureSymlink(target, real); err == nil {
		t.Error("EnsureSymlink replaced a regular file")
	}
	if data, _ := os.ReadFile(real); string(data) != "# mine\n" {
		t.Error("the operator's file was modified")
	}
}

func TestManagedHeaderGuardsDeletion(t *testing.T) {
	dir := t.TempDir()
	mine := filepath.Join(dir, "mine.conf")
	theirs := filepath.Join(dir, "theirs.conf")
	if err := os.WriteFile(mine, []byte(ManagedHeader+"\nserver {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(theirs, []byte("server { # hand written\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if ok, err := HasManagedHeader(mine); err != nil || !ok {
		t.Errorf("HasManagedHeader(mine) = %v, %v", ok, err)
	}
	if ok, err := HasManagedHeader(theirs); err != nil || ok {
		t.Errorf("HasManagedHeader(theirs) = %v, %v", ok, err)
	}
	if ok, err := HasManagedHeader(filepath.Join(dir, "absent")); err != nil || ok {
		t.Errorf("HasManagedHeader on a missing file = %v, %v; want false, nil", ok, err)
	}

	if err := RemoveManaged(mine); err != nil {
		t.Errorf("RemoveManaged on our own file = %v", err)
	}
	if Exists(mine) {
		t.Error("RemoveManaged did not delete our file")
	}
	if err := RemoveManaged(theirs); err == nil {
		t.Error("RemoveManaged deleted a file ratline did not create")
	}
	if !Exists(theirs) {
		t.Error("the operator's file was deleted")
	}
}

func TestBackupFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sshd_config")
	if err := os.WriteFile(path, []byte("PermitRootLogin no\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backup, err := BackupFile(path, "20260804")
	if err != nil {
		t.Fatalf("BackupFile = %v", err)
	}
	data, err := os.ReadFile(backup)
	if err != nil || string(data) != "PermitRootLogin no\n" {
		t.Fatalf("backup contents = %q, %v", data, err)
	}
	fi, _ := os.Stat(backup)
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("backup mode = %04o, want the original 0600", got)
	}
}

func TestReadFileLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileLimit(path, 200); err != nil {
		t.Errorf("ReadFileLimit within the limit = %v", err)
	}
	if _, err := ReadFileLimit(path, 50); err == nil {
		t.Error("ReadFileLimit accepted a file over the limit")
	}
}

func TestDirSizeAndFreeBytes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	size, err := DirSize(dir)
	if err != nil {
		t.Fatalf("DirSize = %v", err)
	}
	if size != 3072 {
		t.Errorf("DirSize = %d, want 3072", size)
	}
	free, err := FreeBytes(dir)
	if err != nil {
		t.Fatalf("FreeBytes = %v", err)
	}
	if free == 0 {
		t.Error("FreeBytes reported zero free space")
	}
}
