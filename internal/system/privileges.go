package system

import (
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// RequireRoot refuses to continue without EUID 0.
//
// Everything ratline does — creating accounts, writing under /etc, reloading
// systemd — needs root. Checking once up front produces one clear message
// instead of a partial run that fails at its third step.
func RequireRoot() error {
	if os.Geteuid() == 0 {
		return nil
	}
	return rlerr.Preconditionf("ratline must run as root; the current effective UID is %d", os.Geteuid()).
		WithHint("re-run it with sudo")
}

// SelfPath returns the absolute, symlink-resolved path of this binary.
func SelfPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "determining this binary's path")
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return p, nil
}

// CheckSelfBinary refuses to run a binary that an unprivileged user could
// replace. Anyone who can write /usr/local/bin/ratline, or the directory that
// holds it, can run arbitrary code as root the next time an operator types
// sudo ratline.
func CheckSelfBinary() error {
	p, err := SelfPath()
	if err != nil {
		return err
	}
	return CheckExecutablePermissions(p)
}

// CheckExecutablePermissions verifies that a privileged executable and its
// directory cannot be modified by non-root users.
func CheckExecutablePermissions(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "inspecting %s", path)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return rlerr.Preconditionf("%s is writable by group or other (mode %04o)", path, fi.Mode().Perm()).
			WithHint("chown root:root %s && chmod 0755 %s", path, path)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 {
		return rlerr.Preconditionf("%s is owned by UID %d rather than root", path, st.Uid).
			WithHint("chown root:root %s", path)
	}

	dir := filepath.Dir(path)
	dfi, err := os.Stat(dir)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "inspecting %s", dir)
	}
	// A group- or world-writable directory lets an attacker swap the binary,
	// unless the sticky bit stops them removing files they do not own.
	if dfi.Mode().Perm()&0o022 != 0 && dfi.Mode()&fs.ModeSticky == 0 {
		return rlerr.Preconditionf("%s lives in %s, which is writable by group or other (mode %04o)",
			filepath.Base(path), dir, dfi.Mode().Perm()).
			WithHint("install ratline in a root-owned directory such as /usr/local/bin")
	}
	return nil
}

// Invoker identifies the human behind the invocation, for the audit trail.
type Invoker struct {
	UID      int
	Name     string
	SudoUser string
	SudoUID  int
}

// CurrentInvoker reads the real UID and the sudo environment. Under sudo the
// real UID is 0 too, so SUDO_USER is the only record of who actually typed the
// command — the audit log is worth much less without it.
func CurrentInvoker() Invoker {
	inv := Invoker{UID: os.Getuid(), SudoUID: -1}
	if u, err := user.LookupId(strconv.Itoa(inv.UID)); err == nil {
		inv.Name = u.Username
	}
	inv.SudoUser = os.Getenv("SUDO_USER")
	if s := os.Getenv("SUDO_UID"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			inv.SudoUID = n
		}
	}
	return inv
}

// Display names the invoker for logs: "ali (via sudo)" or "root".
func (i Invoker) Display() string {
	switch {
	case i.SudoUser != "" && i.SudoUser != i.Name:
		return i.SudoUser + " (via sudo)"
	case i.Name != "":
		return i.Name
	default:
		return strconv.Itoa(i.UID)
	}
}

// LookupIdentity resolves a system account into an Identity usable as Cmd.As.
func LookupIdentity(name string) (*Identity, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodePrecondition, "no such system user: %s", name)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "parsing UID %q for %s", u.Uid, name)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "parsing GID %q for %s", u.Gid, name)
	}
	id := &Identity{Name: u.Username, UID: uid, GID: gid, Home: u.HomeDir}
	if gids, err := u.GroupIds(); err == nil {
		for _, g := range gids {
			if n, err := strconv.Atoi(g); err == nil {
				id.Groups = append(id.Groups, n)
			}
		}
	}
	return id, nil
}

// LookupGroupID resolves a group name to its GID.
func LookupGroupID(name string) (int, error) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, rlerr.Wrap(err, rlerr.CodePrecondition, "no such group: %s", name)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return 0, rlerr.Wrap(err, rlerr.CodeGeneric, "parsing GID %q for group %s", g.Gid, name)
	}
	return gid, nil
}

// UserExists reports whether a system account with this name is present.
func UserExists(name string) bool {
	_, err := user.Lookup(name)
	return err == nil
}

// GroupExists reports whether a group with this name is present.
func GroupExists(name string) bool {
	_, err := user.LookupGroup(name)
	return err == nil
}
