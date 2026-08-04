// Package buildinfo carries values stamped in at link time by the Makefile.
package buildinfo

import (
	"fmt"
	"runtime"
)

// Overridden with -ldflags "-X github.com/ALIRAZA47/ratline-cli/internal/buildinfo.Version=..."
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Short is the one-line version used in log records and audit entries.
func Short() string { return fmt.Sprintf("%s (%s)", Version, Commit) }

// Full is the version line printed by `ratline version`.
func Full() string {
	return fmt.Sprintf("ratline %s commit=%s built=%s %s/%s %s",
		Version, Commit, Date, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

// GoVersion reports the toolchain that produced this binary.
func GoVersion() string { return runtime.Version() }

// Platform reports the target this binary was built for.
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }
