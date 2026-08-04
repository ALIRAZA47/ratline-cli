package system

import (
	"os"
	"runtime"
	"strings"
)

// OSInfo describes the host, for `ratline version`, `ratline doctor` and the
// runtime installers, which choose between deadsnakes and a source build.
type OSInfo struct {
	ID         string   // "ubuntu", "debian"
	VersionID  string   // "24.04"
	Codename   string   // "noble"
	PrettyName string   // "Ubuntu 24.04.1 LTS"
	IDLike     []string // "debian"
	Kernel     string
	Arch       string
}

// DetectOS reads /etc/os-release. It never fails: an unrecognised host still
// gets a usable, if sparse, record, and Supported reports the truth.
func DetectOS() OSInfo {
	info := OSInfo{Arch: runtime.GOARCH}
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok {
				continue
			}
			val = strings.Trim(val, `"'`)
			switch key {
			case "ID":
				info.ID = val
			case "VERSION_ID":
				info.VersionID = val
			case "VERSION_CODENAME":
				info.Codename = val
			case "PRETTY_NAME":
				info.PrettyName = val
			case "ID_LIKE":
				info.IDLike = strings.Fields(val)
			}
		}
	}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		info.Kernel = strings.TrimSpace(string(b))
	}
	if info.PrettyName == "" {
		info.PrettyName = runtime.GOOS
	}
	return info
}

// IsDebianFamily reports whether apt and the Debian filesystem layout apply.
func (o OSInfo) IsDebianFamily() bool {
	if o.ID == "debian" || o.ID == "ubuntu" {
		return true
	}
	for _, l := range o.IDLike {
		if l == "debian" {
			return true
		}
	}
	return false
}

// Supported reports whether this is a host ratline targets. Unsupported hosts
// are warned about rather than refused: the layout may still be close enough,
// and refusing outright helps nobody who has already installed the tool.
func (o OSInfo) Supported() bool { return runtime.GOOS == "linux" && o.IsDebianFamily() }

func (o OSInfo) String() string {
	parts := []string{o.PrettyName}
	if o.Kernel != "" {
		parts = append(parts, "kernel "+o.Kernel)
	}
	parts = append(parts, o.Arch)
	return strings.Join(parts, ", ")
}
