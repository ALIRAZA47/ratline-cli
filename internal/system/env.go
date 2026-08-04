package system

import "fmt"

// DefaultPath is the PATH handed to children. ratline builds every child's
// environment from scratch rather than inheriting its own, so that a variable
// set in an operator's shell cannot change what a provisioning step does.
const DefaultPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// MinimalEnv is the environment for children run as root.
func MinimalEnv() []string {
	return []string{
		"PATH=" + DefaultPath,
		"HOME=/root",
		"SHELL=/bin/sh",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"TERM=dumb",
	}
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
	return append(env, extra...)
}

// EnvKV formats a single environment assignment.
func EnvKV(key, value string) string { return fmt.Sprintf("%s=%s", key, value) }
