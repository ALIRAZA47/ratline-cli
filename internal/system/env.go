package system

import (
	"fmt"
	"strings"
)

// DefaultPath is the PATH handed to children. ratline builds every child's
// environment from scratch rather than inheriting its own, so that a variable
// set in an operator's shell cannot change what a provisioning step does.
const DefaultPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// MinimalEnv is the environment for children run as root.
func MinimalEnv(extra ...string) []string {
	env := []string{
		"PATH=" + DefaultPath,
		"HOME=/root",
		"SHELL=/bin/sh",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"TERM=dumb",
	}
	// Overrides replace rather than duplicate, for the same reason as UserEnv.
	return overrideEnv(env, extra)
}

// UserEnv is the environment for children run as an unprivileged site owner —
// npm ci, pip install, a build command. Site secrets are never passed here;
// they reach the application only through the unit's EnvironmentFile.
func UserEnv(id *Identity, extra ...string) []string {
	home := "/tmp"
	name := "nobody"
	shell := "/bin/sh"
	if id != nil {
		if id.Home != "" {
			home = id.Home
		}
		if id.Name != "" {
			name = id.Name
		}
		if id.Shell != "" {
			shell = id.Shell
		}
	}
	env := []string{
		"PATH=" + DefaultPath,
		"HOME=" + home,
		"USER=" + name,
		"LOGNAME=" + name,
		"SHELL=" + shell,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"TERM=dumb",
		// Keep package managers from writing caches into unexpected places.
		"XDG_CACHE_HOME=" + home + "/.cache",
		"XDG_CONFIG_HOME=" + home + "/.config",
		"CI=1",
	}
	return overrideEnv(env, extra)
}

// overrideEnv applies overrides by replacing, not by appending.
//
// A duplicate assignment is not portably resolved: os/exec happens to keep the last,
// but a child that re-execs, a shebang interpreter, or anything that reads the block
// itself may take the first. Callers write UserEnv(id, "PATH=...") meaning "this
// PATH", and appending a second one left that intent depending on which reader
// looked — which is how a `#!/usr/bin/env node` script came to exit 127 with the
// right directory sitting in a later PATH entry.
func overrideEnv(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	replaced := make(map[string]bool, len(extra))

	keyOf := func(entry string) string {
		key, _, _ := strings.Cut(entry, "=")
		return key
	}
	override := make(map[string]string, len(extra))
	for _, e := range extra {
		override[keyOf(e)] = e
	}
	for _, e := range base {
		key := keyOf(e)
		if v, ok := override[key]; ok {
			if !replaced[key] {
				out = append(out, v)
				replaced[key] = true
			}
			continue
		}
		out = append(out, e)
	}
	// Anything the base did not define is appended, in the order it was given.
	for _, e := range extra {
		if !replaced[keyOf(e)] {
			out = append(out, e)
			replaced[keyOf(e)] = true
		}
	}
	return out
}

// EnvKV formats a single environment assignment.
func EnvKV(key, value string) string { return fmt.Sprintf("%s=%s", key, value) }
