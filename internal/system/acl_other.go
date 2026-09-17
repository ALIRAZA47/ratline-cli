//go:build !linux

package system

import (
	"os"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// The ACL calls exist only so the packages that use them build on the machines the unit
// suite runs on. ratline itself runs on Linux, where acl_linux.go has the real ones; a
// caller on any other platform gets a precondition error, never a silent success.

func aclUnsupported(name string) error {
	return rlerr.Preconditionf("POSIX ACLs on %s: only supported on Linux", name)
}

// GrantGroupRead is not available on this platform.
func GrantGroupRead(f *os.File, _ int) error { return aclUnsupported(f.Name()) }

// SetDefaultGroupRead is not available on this platform.
func SetDefaultGroupRead(dir *os.File, _ int) error { return aclUnsupported(dir.Name()) }

// DefaultACLGrantsGroupRead is not available on this platform.
func DefaultACLGrantsGroupRead(dir *os.File, _ int) (bool, error) {
	return false, aclUnsupported(dir.Name())
}

// ACLGrantsGroupRead is not available on this platform.
func ACLGrantsGroupRead(f *os.File, _ int) (bool, error) { return false, aclUnsupported(f.Name()) }
