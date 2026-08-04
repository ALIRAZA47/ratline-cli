// Package validate holds every input validator in one place.
//
// Usernames, domains and paths routinely arrive from a web form by way of an
// automation layer, so each one is treated as hostile. The validators are pure
// functions with no system access, which is what makes them cheap to fuzz.
package validate

import (
	"regexp"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// MaxUsernameLen matches the practical limit for a Linux account name that also
// has to fit inside a systemd unit name and an nginx log path.
const MaxUsernameLen = 32

var usernameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// DefaultReservedNames are never handed to a tenant. Some are system accounts,
// some would collide with ratline's own identifiers, and some are simply
// confusing to see in a process list.
var DefaultReservedNames = []string{
	"admin", "administrator", "adm", "backup", "bin", "daemon", "games", "gnats",
	"irc", "list", "lp", "mail", "man", "messagebus", "news", "nginx", "nobody",
	"proxy", "ratline", "root", "sshd", "sync", "sys", "syslog", "systemd-network",
	"systemd-resolve", "systemd-timesync", "tss", "ubuntu", "debian", "uucp",
	"www-data", "postgres", "mysql", "redis", "certbot",
}

// Username checks syntax only. Availability against /etc/passwd is a separate
// step because it touches the system.
func Username(name string) error {
	if name == "" {
		return rlerr.Usagef("the username is empty")
	}
	if len(name) > MaxUsernameLen {
		return rlerr.Usagef("the username %q is %d characters long; the limit is %d", name, len(name), MaxUsernameLen)
	}
	if !usernameRe.MatchString(name) {
		return rlerr.Usagef("invalid username %q", name).
			WithHint("use lowercase letters, digits, underscores and hyphens, starting with a letter or underscore, " +
				"for example \"acme-web\"")
	}
	if strings.HasSuffix(name, "-") {
		return rlerr.Usagef("invalid username %q: it must not end with a hyphen", name)
	}
	if !strings.ContainsAny(name, "abcdefghijklmnopqrstuvwxyz0123456789") {
		return rlerr.Usagef("invalid username %q: it needs at least one letter or digit", name)
	}
	return nil
}

// UserPolicy carries the environment-dependent half of username validation.
type UserPolicy struct {
	// Reserved extends DefaultReservedNames from config.
	Reserved []string
	// UserExists and GroupExists are injected so this package stays pure.
	UserExists  func(string) bool
	GroupExists func(string) bool
}

// UsernameAvailable applies the reserved list and the on-disk checks.
func UsernameAvailable(name string, p UserPolicy) error {
	if err := Username(name); err != nil {
		return err
	}
	reserved := make(map[string]bool, len(DefaultReservedNames)+len(p.Reserved))
	for _, r := range DefaultReservedNames {
		reserved[r] = true
	}
	for _, r := range p.Reserved {
		reserved[strings.ToLower(strings.TrimSpace(r))] = true
	}
	if reserved[name] {
		return rlerr.Preconditionf("%q is a reserved name", name).
			WithHint("pick a different name; the reserved list lives under users.reserved in /etc/ratline/config.yaml")
	}
	if p.UserExists != nil && p.UserExists(name) {
		return rlerr.Preconditionf("the system user %q already exists", name).
			WithHint("use 'ratline user show %s' to inspect it, or choose another name", name)
	}
	if p.GroupExists != nil && p.GroupExists(name) {
		return rlerr.Preconditionf("a group named %q already exists", name).
			WithHint("ratline creates a group per user, and it will not adopt a group it did not create")
	}
	return nil
}
