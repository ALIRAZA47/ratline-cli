//go:build linux

package system

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// GrantGroupRead adds gid, with read permission, to f's access ACL. The owner's, the owning
// group's and everyone else's permissions stay exactly what the mode says.
//
// On a descriptor, never on a path: f was opened with O_NOFOLLOW by a caller that has
// already decided it is the file it meant, and nothing renamed underneath it between then
// and now can change which inode this applies to.
func GrantGroupRead(f *os.File, gid int) error {
	fi, err := f.Stat()
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "inspecting %s", f.Name())
	}
	if !fi.Mode().IsRegular() {
		return rlerr.Preconditionf("refusing to set an ACL on %s: it is not a regular file", f.Name())
	}
	return setACL(f, XattrACLAccess, groupReadACL(fi.Mode(), gid, aclRead))
}

// SetDefaultGroupRead gives dir a default ACL under which every file created inside it is
// readable by gid, and every subdirectory traversable. The directory's own access ACL is
// left alone; its mode already says who may list it.
func SetDefaultGroupRead(dir *os.File, gid int) error {
	fi, err := dir.Stat()
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "inspecting %s", dir.Name())
	}
	if !fi.IsDir() {
		return rlerr.Preconditionf("refusing to set a default ACL on %s: it is not a directory", dir.Name())
	}
	return setACL(dir, XattrACLDefault, groupReadACL(fi.Mode(), gid, aclRead|aclExecute))
}

// DefaultACLGrantsGroupRead reports whether dir's default ACL lets gid read what is
// created inside it — the check `doctor` makes of a site's journal directory.
func DefaultACLGrantsGroupRead(dir *os.File, gid int) (bool, error) {
	entries, err := getACL(dir, XattrACLDefault)
	if err != nil {
		return false, err
	}
	return aclGrantsGroup(entries, gid, aclRead), nil
}

// ACLGrantsGroupRead reports whether f's access ACL lets gid read it.
func ACLGrantsGroupRead(f *os.File, gid int) (bool, error) {
	entries, err := getACL(f, XattrACLAccess)
	if err != nil {
		return false, err
	}
	return aclGrantsGroup(entries, gid, aclRead), nil
}

func setACL(f *os.File, attr string, entries []aclEntry) error {
	if err := unix.Fsetxattr(int(f.Fd()), attr, encodeACL(entries), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			return rlerr.Wrap(err, rlerr.CodePrecondition, "the filesystem holding %s does not support POSIX ACLs", f.Name()).
				WithHint("ext4, xfs and btrfs support them by default; check the mount options of the filesystem")
		}
		return rlerr.Wrap(err, rlerr.CodeGeneric, "setting the ACL on %s", f.Name())
	}
	return nil
}

func getACL(f *os.File, attr string) ([]aclEntry, error) {
	buf := make([]byte, 4096)
	n, err := unix.Fgetxattr(int(f.Fd()), attr, buf)
	if err != nil {
		if errors.Is(err, unix.ENODATA) {
			return nil, nil
		}
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			return nil, rlerr.Wrap(err, rlerr.CodePrecondition, "the filesystem holding %s does not support POSIX ACLs", f.Name())
		}
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the ACL of %s", f.Name())
	}
	return decodeACL(buf[:n])
}
