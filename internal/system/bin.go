// Package system wraps every privileged interaction with the host: running
// external programs, writing files atomically, taking the global lock, and
// checking the process's own privileges.
package system

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// defaultCandidates maps a logical binary name onto the absolute paths where
// it may live. ratline never inherits PATH for its own lookups: an attacker who
// can influence PATH must not be able to influence which useradd we run.
//
// Deliberately absent: sh, bash and any other shell. Not having a shell in the
// registry makes "never build shell strings" a structural property rather than
// a coding convention.
var defaultCandidates = map[string][]string{
	// accounts
	"useradd":  {"/usr/sbin/useradd", "/sbin/useradd"},
	"usermod":  {"/usr/sbin/usermod", "/sbin/usermod"},
	"userdel":  {"/usr/sbin/userdel", "/sbin/userdel"},
	"groupadd": {"/usr/sbin/groupadd", "/sbin/groupadd"},
	"groupdel": {"/usr/sbin/groupdel", "/sbin/groupdel"},
	"gpasswd":  {"/usr/bin/gpasswd", "/bin/gpasswd"},
	"chpasswd": {"/usr/sbin/chpasswd", "/sbin/chpasswd"},
	"passwd":   {"/usr/bin/passwd", "/bin/passwd"},
	"chage":    {"/usr/bin/chage", "/bin/chage"},
	"getent":   {"/usr/bin/getent", "/bin/getent"},
	"visudo":   {"/usr/sbin/visudo", "/sbin/visudo"},

	// web and process supervision
	"nginx":           {"/usr/sbin/nginx", "/usr/local/sbin/nginx"},
	"systemctl":       {"/usr/bin/systemctl", "/bin/systemctl"},
	"journalctl":      {"/usr/bin/journalctl", "/bin/journalctl"},
	"systemd-analyze": {"/usr/bin/systemd-analyze", "/bin/systemd-analyze"},
	"logrotate":       {"/usr/sbin/logrotate", "/sbin/logrotate"},
	"kill":            {"/bin/kill", "/usr/bin/kill"},
	// Used to confirm an nginx reload actually landed, by looking for workers that
	// did not exist before it. Absent on a minimal host, which the caller treats as
	// "cannot observe" rather than as a failure.
	"pgrep": {"/usr/bin/pgrep", "/bin/pgrep"},

	// ssh
	"ssh-keygen":  {"/usr/bin/ssh-keygen", "/bin/ssh-keygen"},
	"ssh-keyscan": {"/usr/bin/ssh-keyscan"},
	"sshd":        {"/usr/sbin/sshd", "/sbin/sshd"},
	"ssh":         {"/usr/bin/ssh", "/bin/ssh"},
	"sftp-server": {"/usr/lib/openssh/sftp-server", "/usr/libexec/openssh/sftp-server", "/usr/lib/ssh/sftp-server"},

	// databases. `ratline db install` puts the server itself on this host; everything
	// else provisions inside a MongoDB server it is pointed at, so an absent mongosh
	// is a precondition to report rather than a bug.
	"mongosh": {"/usr/bin/mongosh", "/usr/local/bin/mongosh", "/snap/bin/mongosh", "/opt/mongodb/bin/mongosh"},
	// mongod is only ever run to validate a staged configuration file
	// (--outputConfig); the real server is started through systemctl.
	"mongod": {"/usr/bin/mongod", "/usr/local/bin/mongod", "/opt/mongodb/bin/mongod"},
	// MySQL / MariaDB. `db install --engine mysql` uses the distro package, so the
	// client and server binaries live in the standard locations; the mysql/mariadb
	// names alias the same tools on a MariaDB host. mysqld is only run to validate a
	// staged config (--validate-config), like mongod above.
	"mysql":     {"/usr/bin/mysql", "/usr/local/bin/mysql", "/usr/bin/mariadb"},
	"mysqld":    {"/usr/sbin/mysqld", "/usr/bin/mysqld", "/usr/sbin/mariadbd", "/usr/bin/mariadbd"},
	"mysqldump": {"/usr/bin/mysqldump", "/usr/local/bin/mysqldump", "/usr/bin/mariadb-dump"},
	// The database tools ship separately from mongosh — mongodb-database-tools, which is
	// not a dependency of the shell — so these are absent on plenty of servers where
	// mongosh works fine. `db dump` says which package to install rather than reporting a
	// missing binary.
	"mongodump":    {"/usr/bin/mongodump", "/usr/local/bin/mongodump", "/opt/mongodb/bin/mongodump"},
	"mongorestore": {"/usr/bin/mongorestore", "/usr/local/bin/mongorestore", "/opt/mongodb/bin/mongorestore"},

	// tls
	"certbot": {"/usr/bin/certbot", "/snap/bin/certbot", "/usr/local/bin/certbot"},
	"openssl": {"/usr/bin/openssl", "/bin/openssl"},

	// deploy and runtimes
	"git":     {"/usr/bin/git", "/bin/git"},
	"tar":     {"/usr/bin/tar", "/bin/tar"},
	"rsync":   {"/usr/bin/rsync", "/bin/rsync"},
	"python3": {"/usr/bin/python3"},

	// firewall. Only `db access` touches it, and only for MongoDB's port: ratline
	// does not otherwise manage the firewall, and it never enables or disables ufw —
	// enabling a firewall is how an operator locks themselves out of SSH, and that
	// decision stays with them.
	"ufw": {"/usr/sbin/ufw", "/sbin/ufw"},

	// quotas and host facts
	"setquota": {"/usr/sbin/setquota", "/sbin/setquota"},
	"repquota": {"/usr/sbin/repquota", "/sbin/repquota"},
	"quotaon":  {"/usr/sbin/quotaon", "/sbin/quotaon"},
	"du":       {"/usr/bin/du", "/bin/du"},
	"ip":       {"/usr/sbin/ip", "/sbin/ip", "/usr/bin/ip"},
	// Same package as ip (iproute2). Used to prove what mongod is actually bound to,
	// because the config file saying so is not the same fact.
	"ss":      {"/usr/bin/ss", "/bin/ss", "/usr/sbin/ss"},
	"apt-get": {"/usr/bin/apt-get"},
}

// Binaries resolves logical binary names to verified absolute paths.
type Binaries struct {
	mu         sync.RWMutex
	candidates map[string][]string
	resolved   map[string]string
}

// NewBinaries returns a registry seeded with the default candidate paths.
func NewBinaries() *Binaries {
	c := make(map[string][]string, len(defaultCandidates))
	for k, v := range defaultCandidates {
		c[k] = append([]string(nil), v...)
	}
	return &Binaries{candidates: c, resolved: map[string]string{}}
}

// Set pins a logical name to an absolute path, bypassing discovery. Used by
// the integration harness and by `ratline init` on unusual layouts.
func (b *Binaries) Set(name, path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resolved[name] = path
}

// LoadOverridesFromEnv honours RATLINE_BIN_<NAME>=/abs/path, with the name
// upper-cased and hyphens replaced by underscores (RATLINE_BIN_SSH_KEYGEN).
// This exists so the Docker integration harness can point at test doubles
// without patching the registry from inside the binary.
func (b *Binaries) LoadOverridesFromEnv(environ []string) error {
	const prefix = "RATLINE_BIN_"
	for _, kv := range environ {
		if !strings.HasPrefix(kv, prefix) {
			continue
		}
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		name := strings.ToLower(strings.ReplaceAll(kv[len(prefix):eq], "_", "-"))
		path := kv[eq+1:]
		if !filepath.IsAbs(path) {
			return rlerr.Usagef("%s must be an absolute path, got %q", kv[:eq], path)
		}
		b.Set(name, path)
	}
	return nil
}

// Path resolves a logical name to a verified absolute path.
//
// Verification is deliberate: the file must be a regular executable and must
// not be writable by group or other. Running a world-writable binary as root
// is a privilege-escalation primitive, and it is cheap to refuse.
func (b *Binaries) Path(name string) (string, error) {
	b.mu.RLock()
	if p, ok := b.resolved[name]; ok {
		b.mu.RUnlock()
		return p, nil
	}
	cands := b.candidates[name]
	b.mu.RUnlock()

	if len(cands) == 0 {
		return "", rlerr.Preconditionf("no candidate path is registered for the %q binary", name).
			WithHint("this is a ratline bug; set RATLINE_BIN_%s to an absolute path to work around it",
				strings.ToUpper(strings.ReplaceAll(name, "-", "_")))
	}

	var problems []string
	for _, c := range cands {
		fi, err := os.Stat(c)
		if err != nil {
			continue
		}
		if err := checkExecutable(c, fi); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		b.mu.Lock()
		b.resolved[name] = c
		b.mu.Unlock()
		return c, nil
	}

	e := rlerr.Preconditionf("%s is not installed (looked in %s)", name, strings.Join(cands, ", "))
	if len(problems) > 0 {
		e = rlerr.Preconditionf("%s was found but is not safe to execute: %s", name, strings.Join(problems, "; "))
	}
	return "", e.WithHint("install it, or set RATLINE_BIN_%s to its absolute path",
		strings.ToUpper(strings.ReplaceAll(name, "-", "_")))
}

// Available reports whether a binary resolves, without raising an error.
func (b *Binaries) Available(name string) bool {
	_, err := b.Path(name)
	return err == nil
}

// Require resolves several binaries at once and reports every missing one
// together, so an operator fixes their server in a single pass.
func (b *Binaries) Require(names ...string) error {
	var missing []string
	for _, n := range names {
		if _, err := b.Path(n); err != nil {
			missing = append(missing, n)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return rlerr.Preconditionf("required commands are missing: %s", strings.Join(missing, ", ")).
		WithHint("on Debian and Ubuntu: apt-get install %s", strings.Join(missing, " "))
}

// Names lists every logical name the registry knows about.
func (b *Binaries) Names() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, 0, len(b.candidates))
	for k := range b.candidates {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func checkExecutable(path string, fi os.FileInfo) error {
	if fi.IsDir() || !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is writable by group or other (mode %04o)", path, fi.Mode().Perm())
	}
	return nil
}

// CheckRootOwnedExecutable validates a program ratline will have run as root.
//
// For a certbot DNS hook: certbot executes it as root with the validation token in its
// environment, so anyone who can write it can run arbitrary code as root on a server
// holding every tenant's keys. The three conditions are the same ones ratline applies to
// its own binary — regular, executable, not writable by group or other — plus root
// ownership, which matters more here because the path comes from a flag rather than from
// where the binary happens to be installed.
func CheckRootOwnedExecutable(path string) error {
	if !filepath.IsAbs(path) {
		return rlerr.Usagef("%s must be an absolute path", path).
			WithHint("certbot runs it from a working directory you do not control")
	}
	fi, err := os.Stat(path)
	if err != nil {
		return rlerr.Preconditionf("cannot read %s", path)
	}
	if err := checkExecutable(path, fi); err != nil {
		return rlerr.Preconditionf("%s", err.Error()).
			WithHint("chmod 0755 %s", path)
	}
	if uid, ok := ownerUID(fi); ok && uid != 0 {
		return rlerr.Preconditionf("%s is owned by uid %d, not root", path, uid).
			WithHint("certbot runs this as root, so a non-root owner can change what root "+
				"executes: chown root:root %s", path)
	}
	return nil
}

// ownerUID reads a file's owning uid. The second result is false on a platform where
// the syscall stat is not available, so callers skip the check rather than fail it.
func ownerUID(fi os.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}
