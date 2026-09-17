//go:build linux

package system

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// The property the journal grant rests on: a default ACL on the directory reaches the files
// created inside it afterwards, and an explicit grant reaches the ones already there — with
// the mode of each left as it was.
func TestADefaultACLMakesNewFilesReadableByTheGroup(t *testing.T) {
	const gid = 65000 // any group; an ACL may name one that does not exist
	dir := t.TempDir()
	d, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := SetDefaultGroupRead(d, gid); err != nil {
		if rlerr.Is(err, rlerr.CodePrecondition) {
			t.Skipf("no ACL support on %s: %v", dir, err)
		}
		t.Fatalf("SetDefaultGroupRead = %v", err)
	}
	if ok, err := DefaultACLGrantsGroupRead(d, gid); err != nil || !ok {
		t.Fatalf("DefaultACLGrantsGroupRead = %v, %v; want true", ok, err)
	}
	if ok, _ := DefaultACLGrantsGroupRead(d, gid+1); ok {
		t.Error("a group that was never named reads the default ACL as a grant")
	}

	// A file journald would create: 0640, nothing said about ACLs.
	created, err := os.OpenFile(filepath.Join(dir, "system.journal"), os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	defer created.Close()
	if ok, err := ACLGrantsGroupRead(created, gid); err != nil || !ok {
		t.Errorf("a file created under the default ACL is not readable by the group: %v, %v", ok, err)
	}
	if fi, _ := created.Stat(); fi.Mode().Perm() != 0o640 {
		t.Errorf("the inherited ACL changed the mode to %04o", fi.Mode().Perm())
	}

	// A file that was there first, granted afterwards.
	before := filepath.Join(t.TempDir(), "system@old.journal")
	if err := os.WriteFile(before, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(before)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if ok, _ := ACLGrantsGroupRead(f, gid); ok {
		t.Fatal("the file was readable by the group before anything was granted")
	}
	if err := GrantGroupRead(f, gid); err != nil {
		t.Fatalf("GrantGroupRead = %v", err)
	}
	if ok, err := ACLGrantsGroupRead(f, gid); err != nil || !ok {
		t.Errorf("GrantGroupRead did not take: %v, %v", ok, err)
	}
	if fi, _ := f.Stat(); fi.Mode().Perm() != 0o640 {
		t.Errorf("the grant changed the mode to %04o", fi.Mode().Perm())
	}
	// Not for a directory pretending to be a file, and not for a file pretending to be a
	// directory: the two calls are for the two things they are named after.
	if err := GrantGroupRead(d, gid); err == nil {
		t.Error("GrantGroupRead accepted a directory")
	}
	if err := SetDefaultGroupRead(f, gid); err == nil {
		t.Error("SetDefaultGroupRead accepted a regular file")
	}
}
