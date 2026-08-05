//go:build unix

package system

import (
	"syscall"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// ProvisioningUmask is applied once at startup so that anything created without
// an explicit mode still lands at 0640/0750 rather than 0644/0755.
const ProvisioningUmask = 0o027

// SetUmask sets the process umask and returns the previous value.
func SetUmask(mask int) int { return syscall.Umask(mask) }

// FreeBytes reports the space available to a non-root writer under path.
func FreeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, rlerr.Wrap(err, rlerr.CodeGeneric, "checking free space on %s", path)
	}
	return st.Bavail * uint64(st.Bsize), nil
}
