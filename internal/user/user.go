// Package user is the tenant lifecycle: creating a system account with its own
// group, home tree and service identity, and taking it away again.
//
// A ratline user is a sandbox boundary. Making one cheap is a deliberate design
// goal, because "one user per site" is the recommended answer whenever a site is
// run by someone you do not fully trust — see SECURITY.md.
package user

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// Manager performs user operations.
type Manager struct {
	Cfg     *config.Config
	Log     *log.Logger
	Runner  system.Runner
	State   *state.Store
	Invoker string
	DryRun  bool

	// SudoersDir overrides where sudo drop-ins are written. Empty means
	// DefaultSudoersDir; only the tests set it.
	SudoersDir string
}

// AddOptions is the resolved form of `ratline user add`.
type AddOptions struct {
	Name          string
	Shell         string
	Comment       string
	PasswordLogin bool
	SFTPOnly      bool
	Quota         string
	MemoryMax     string
}

// Add creates a system account and its home tree.
//
// Everything is staged onto a rollback stack: a failure half way through removes
// the account rather than leaving a half-provisioned tenant for the operator to
// work out.
func (m *Manager) Add(ctx context.Context, opts AddOptions) (u *state.User, err error) {
	// The name is validated first, because it is about to be used as a state key.
	// The *collision* checks — is this account taken, is this group taken — come
	// after the idempotence check below, and that order matters: ratline creates a
	// group per user, so on a re-run the group it made itself is already there and a
	// collision check run first refuses with "a group named alice already exists".
	// That broke `user add` being safe to run twice, which every script depends on.
	if err := validate.Username(opts.Name); err != nil {
		return nil, err
	}

	// Idempotency: an identical re-run succeeds and changes nothing.
	if existing, err := m.State.GetUser(ctx, opts.Name); err == nil {
		if system.UserExists(opts.Name) {
			m.Log.Info("already configured", "user", opts.Name)
			return existing, nil
		}
		// State says the user exists but the system disagrees. Reconcile is the
		// tool for that; refusing here avoids guessing which one is right.
		return nil, rlerr.Preconditionf("%q is recorded in ratline's state but has no system account", opts.Name).
			WithHint("run 'ratline reconcile' to see the drift, or 'ratline reconcile --fix' to repair it")
	}
	if system.UserExists(opts.Name) {
		return nil, rlerr.Preconditionf("the system user %q already exists but was not created by ratline", opts.Name).
			WithHint("ratline will not adopt an account it did not create; pick another name, " +
				"or remove that account first if it is unused")
	}

	// Now that this is known to be a genuinely new tenant, the rest of the
	// validation — including the group collision — is meaningful.
	if err := m.validateAdd(ctx, &opts); err != nil {
		return nil, err
	}

	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)

	home := m.Cfg.HomeDir(opts.Name)
	m.Log.Info("creating the user", "user", opts.Name, "home", home, "shell", opts.Shell)

	// --user-group gives the tenant its own group, which is what makes the
	// 0750 home and the nginx group grant work.
	args := []string{
		"--create-home",
		"--home-dir", home,
		"--shell", opts.Shell,
		"--user-group",
	}
	if opts.Comment != "" {
		args = append(args, "--comment", opts.Comment)
	}
	args = append(args, opts.Name)
	if _, err := m.Runner.Run(ctx, system.Cmd{Name: "useradd", Args: args, Mutates: true, Label: "useradd"}); err != nil {
		return nil, err
	}
	rb.Push("created the system user "+opts.Name, func(ctx context.Context) error {
		_, err := m.Runner.Run(ctx, system.Cmd{
			Name: "userdel", Args: []string{"--remove", opts.Name}, Mutates: true, OKExit: []int{6},
		})
		return err
	})

	// Key-only login is the default. useradd already leaves the password
	// unusable, but locking explicitly means the state is not an accident of
	// the distribution's defaults.
	if !opts.PasswordLogin {
		if _, err := m.Runner.Run(ctx, system.Cmd{
			Name: "usermod", Args: []string{"--lock", opts.Name}, Mutates: true, Label: "lock password",
		}); err != nil {
			return nil, err
		}
	}

	if err := m.buildHomeTree(ctx, opts.Name, rb); err != nil {
		return nil, err
	}
	if err := m.grantNginxGroup(ctx, opts.Name, rb); err != nil {
		return nil, err
	}
	if opts.Quota != "" {
		if err := m.applyQuota(ctx, opts.Name, opts.Quota); err != nil {
			return nil, err
		}
	}

	u = &state.User{
		Name:          opts.Name,
		Home:          home,
		Shell:         opts.Shell,
		Comment:       opts.Comment,
		Quota:         opts.Quota,
		MemoryMax:     opts.MemoryMax,
		SFTPOnly:      opts.SFTPOnly,
		PasswordLogin: opts.PasswordLogin,
		CreatedAt:     time.Now().UTC(),
		CreatedBy:     m.Invoker,
	}
	// Under --dry-run the account does not exist, so there are no ids to read.
	if !m.DryRun {
		id, lerr := system.LookupIdentity(opts.Name)
		if lerr != nil {
			return nil, lerr
		}
		u.UID, u.GID = id.UID, id.GID
	}
	// A preview must leave no record; see the same guard in site.Add.
	if m.DryRun {
		m.Log.Info("would record the tenant in state", "user", opts.Name)
	} else {
		if err := m.State.PutUser(ctx, u); err != nil {
			return nil, err
		}
		rb.Push("recorded the user in state", func(ctx context.Context) error {
			return m.State.DeleteUser(ctx, opts.Name)
		})
	}

	m.Log.Info("user created", "user", opts.Name, "uid", u.UID, "home", home)
	return u, nil
}

func (m *Manager) validateAdd(ctx context.Context, opts *AddOptions) error {
	if err := validate.UsernameAvailable(opts.Name, validate.UserPolicy{
		Reserved:    m.Cfg.Users.Reserved,
		UserExists:  nil, // checked separately, so the message can distinguish the cases
		GroupExists: func(n string) bool { return system.GroupExists(n) },
	}); err != nil {
		return err
	}
	if opts.Shell == "" {
		opts.Shell = m.Cfg.Defaults.Shell
	}
	if opts.SFTPOnly {
		// An SFTP-only tenant gets no shell at all: the sshd Match block forces
		// internal-sftp, and a real shell would only be a way around it.
		opts.Shell = "/usr/sbin/nologin"
	}
	if _, err := validate.AbsClean(opts.Shell); err != nil {
		return err
	}
	if !m.DryRun && !system.Exists(opts.Shell) {
		return rlerr.Preconditionf("the shell %s does not exist", opts.Shell).
			WithHint("check /etc/shells, or pass --shell with a path that exists")
	}
	if opts.Comment != "" {
		// This lands in /etc/passwd, which is colon-separated and line-based.
		if strings.ContainsAny(opts.Comment, ":\n\r\x00") {
			return rlerr.Usagef("the comment may not contain a colon, a newline or a NUL byte")
		}
		if len(opts.Comment) > 128 {
			return rlerr.Usagef("the comment is longer than 128 characters")
		}
	}
	if opts.Quota != "" {
		if _, err := validate.Size(opts.Quota); err != nil {
			return err
		}
		if !m.Cfg.Users.QuotaEnabled {
			return rlerr.Preconditionf("--quota was given but quota support is turned off").
				WithHint("mount the filesystem with usrquota and set users.quota_enabled: true in %s", m.Cfg.SourcePath)
		}
	}
	if opts.MemoryMax != "" {
		if _, err := validate.Size(opts.MemoryMax); err != nil {
			return err
		}
	}
	return nil
}

// buildHomeTree creates the per-user directories with exact modes.
//
// The home is 0750, never 0755: nginx reaches a site's public files by being a
// member of the tenant's group, so the world never needs to traverse the home.
func (m *Manager) buildHomeTree(ctx context.Context, name string, rb *system.Rollback) error {
	if m.DryRun {
		m.Log.Info("would create the home tree", "user", name, "home", m.Cfg.HomeDir(name))
		return nil
	}
	id, err := system.LookupIdentity(name)
	if err != nil {
		return err
	}
	home := m.Cfg.HomeDir(name)

	// useradd created the home from /etc/skel with the distribution's mode;
	// tighten it to ratline's.
	if err := system.Chmod(home, os.FileMode(m.Cfg.HomeFileMode())); err != nil {
		return err
	}
	if err := system.Chown(home, id.UID, id.GID); err != nil {
		return err
	}

	for _, d := range []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Join(home, ".ssh"), 0o700},
		{filepath.Join(home, "logs"), 0o750},
	} {
		created, err := system.EnsureDir(d.path, d.mode, id.UID, id.GID)
		if err != nil {
			return err
		}
		if created {
			path := d.path
			rb.Push("created "+path, func(context.Context) error { return os.RemoveAll(path) })
		}
	}

	// An empty authorized_keys with the right mode means the first `key add`
	// never has to guess, and sshd never refuses over permissions.
	authKeys := filepath.Join(home, ".ssh", "authorized_keys")
	if !system.Exists(authKeys) {
		if err := system.WriteFileAtomic(authKeys, nil, 0o600, id.UID, id.GID); err != nil {
			return err
		}
		rb.Push("created "+authKeys, func(context.Context) error { return os.Remove(authKeys) })
	}
	return nil
}

// grantNginxGroup adds the web server to the tenant's group.
//
// This is how nginx reads a site's public/ directory. The alternative — making
// the home world-readable — would expose every tenant's files to every other
// tenant, so it is never done.
func (m *Manager) grantNginxGroup(ctx context.Context, name string, rb *system.Rollback) error {
	nginxUser := m.Cfg.Users.NginxUser
	if nginxUser == "" {
		return nil
	}
	if !m.DryRun && !system.UserExists(nginxUser) {
		m.Log.Warn("the web server account does not exist, so no group was granted",
			"account", nginxUser, "fix", "install nginx, then run 'ratline reconcile --fix'")
		return nil
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "usermod", Args: []string{"--append", "--groups", name, nginxUser},
		Mutates: true, Label: "grant nginx group",
	}); err != nil {
		return err
	}
	rb.Push(fmt.Sprintf("added %s to the %s group", nginxUser, name), func(ctx context.Context) error {
		_, err := m.Runner.Run(ctx, system.Cmd{
			Name: "gpasswd", Args: []string{"--delete", nginxUser, name}, Mutates: true, OKExit: []int{3},
		})
		return err
	})
	return nil
}

func (m *Manager) applyQuota(ctx context.Context, name, size string) error {
	bytes, err := validate.Size(size)
	if err != nil {
		return err
	}
	blocks := bytes / 1024
	// A soft limit slightly under the hard one gives the tenant a grace period
	// rather than a sudden write failure.
	soft := blocks * 95 / 100
	_, err = m.Runner.Run(ctx, system.Cmd{
		Name: "setquota",
		Args: []string{"-u", name,
			fmt.Sprint(soft), fmt.Sprint(blocks), "0", "0",
			m.Cfg.Paths.HomeBase},
		Mutates: true, Label: "setquota",
	})
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition, "could not apply the quota for %s", name).
			WithHint("the filesystem holding %s must be mounted with usrquota, and quotas must be on: quotaon -v %s",
				m.Cfg.Paths.HomeBase, m.Cfg.Paths.HomeBase)
	}
	return nil
}

// Info is the report behind `ratline user show`.
type Info struct {
	*state.User
	Sites     []*state.Site `json:"sites,omitempty"`
	DiskBytes int64         `json:"disk_bytes"`
	DiskHuman string        `json:"disk_human"`
	KeyCount  int           `json:"key_count"`
	Units     []UnitState   `json:"units,omitempty"`
	OnSystem  bool          `json:"on_system"`
}

// UnitState is one site service's status.
type UnitState struct {
	Unit   string `json:"unit"`
	Domain string `json:"domain"`
	Active string `json:"active"`
	Sub    string `json:"sub,omitempty"`
}

// Show gathers everything about a user.
func (m *Manager) Show(ctx context.Context, name string) (*Info, error) {
	u, err := m.State.GetUser(ctx, name)
	if err != nil {
		return nil, err
	}
	info := &Info{User: u, OnSystem: system.UserExists(name)}

	if info.Sites, err = m.State.ListSites(ctx, state.SiteFilter{Owner: name}); err != nil {
		return nil, err
	}
	keys, err := m.State.ListKeys(ctx, state.KeyFilter{Scope: state.ScopeUser, Owner: name})
	if err != nil {
		return nil, err
	}
	info.KeyCount = len(keys)

	if system.Exists(u.Home) {
		if info.DiskBytes, err = system.DirSize(u.Home); err != nil {
			m.Log.Debug("could not measure the home directory", "err", err)
		}
	}
	info.DiskHuman = validate.FormatSize(info.DiskBytes)

	for _, site := range info.Sites {
		if !site.Dynamic() {
			continue
		}
		unit := validate.UnitName(name, site.Domain)
		st := UnitState{Unit: unit, Domain: site.Domain, Active: "unknown"}
		res, _ := m.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{"is-active", unit}, OKExit: []int{1, 3, 4},
		})
		if res != nil {
			st.Active = strings.TrimSpace(res.Out())
		}
		info.Units = append(info.Units, st)
	}
	return info, nil
}

// SetDisabled locks or unlocks an account.
//
// Disabling stops every site the tenant owns and locks the password and shell.
// Re-enabling reverses both. Neither deletes anything, so it is the safe first
// response to "this client has not paid" or "this account is compromised".
func (m *Manager) SetDisabled(ctx context.Context, name string, disabled bool) error {
	u, err := m.State.GetUser(ctx, name)
	if err != nil {
		return err
	}

	lockArg := "--unlock"
	shell := u.Shell
	if disabled {
		lockArg = "--lock"
		shell = "/usr/sbin/nologin"
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "usermod", Args: []string{lockArg, name}, Mutates: true, Label: "usermod",
	}); err != nil {
		return err
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "usermod", Args: []string{"--shell", shell, name}, Mutates: true, Label: "usermod",
	}); err != nil {
		return err
	}
	return m.State.SetUserDisabled(ctx, name, disabled)
}

// DeleteOptions is the resolved form of `ratline user delete`.
type DeleteOptions struct {
	Name      string
	Purge     bool
	BackupDir string
}

// Delete removes an account.
//
// It refuses while the tenant still owns sites unless --purge, because deleting
// a home out from under a running service leaves nginx serving 502s and a
// systemd unit that cannot start.
func (m *Manager) Delete(ctx context.Context, opts DeleteOptions) error {
	u, err := m.State.GetUser(ctx, opts.Name)
	if err != nil {
		return err
	}
	sites, err := m.State.ListSites(ctx, state.SiteFilter{Owner: opts.Name})
	if err != nil {
		return err
	}
	if len(sites) > 0 && !opts.Purge {
		names := make([]string, 0, len(sites))
		for _, s := range sites {
			names = append(names, s.Domain)
		}
		return rlerr.Preconditionf("%s still owns %d site(s): %s", opts.Name, len(sites), strings.Join(names, ", ")).
			WithHint("delete them first with 'ratline site delete <domain> --purge', or pass --purge to remove everything")
	}
	if opts.BackupDir != "" {
		if err := m.backupHome(ctx, u, opts.BackupDir); err != nil {
			return err
		}
	}

	// The account is removed with its home; the group goes with it because
	// --user-group created it.
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "userdel", Args: []string{"--remove", opts.Name}, Mutates: true, Label: "userdel",
		// 6 means "no such user", which is success for an idempotent delete.
		OKExit: []int{6},
	}); err != nil {
		return err
	}
	// userdel leaves the home behind if anything under it is busy, and a
	// residual home is exactly what `site delete --purge` is checked against.
	if !m.DryRun && system.Exists(u.Home) {
		if err := os.RemoveAll(u.Home); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "removing %s", u.Home)
		}
	}
	// The keys go with the account.
	//
	// Removing the home takes authorized_keys with it, so the grant stops working — but
	// the rows stayed, and `key list` went on showing keys for a tenant that no longer
	// existed while `doctor` reported the server clean. A privilege audit that lists
	// grants against a deleted account is worse than one that lists nothing: the reader
	// cannot tell a stale row from a live one.
	orphans, err := m.State.ListKeys(ctx, state.KeyFilter{Scope: "user", Owner: opts.Name, IncludeRevoked: true})
	if err != nil {
		return err
	}
	for _, k := range orphans {
		if err := m.State.DeleteKey(ctx, k.ID); err != nil {
			return err
		}
	}
	if err := m.State.DeleteUser(ctx, opts.Name); err != nil {
		return err
	}
	m.Log.Info("user deleted", "user", opts.Name,
		"sites_removed", len(sites), "keys_removed", len(orphans))
	return nil
}

func (m *Manager) backupHome(ctx context.Context, u *state.User, dir string) error {
	if _, err := system.EnsureDir(dir, 0o700, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	archive := filepath.Join(dir, fmt.Sprintf("%s-%s.tar.gz", u.Name, stamp))
	m.Log.Info("backing up the home directory", "user", u.Name, "archive", archive)
	_, err := m.Runner.Run(ctx, system.Cmd{
		Name: "tar",
		// -C plus a relative path keeps absolute paths out of the archive, so it
		// can be restored anywhere.
		Args:    []string{"--create", "--gzip", "--file", archive, "-C", filepath.Dir(u.Home), filepath.Base(u.Home)},
		Mutates: true, Timeout: 30 * time.Minute, Label: "tar",
	})
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeExternal, "backing up %s failed; nothing was deleted", u.Home)
	}
	return nil
}

// SetPassword sets a password from a reader, never from argv.
func (m *Manager) SetPassword(ctx context.Context, name, password string) error {
	if _, err := m.State.GetUser(ctx, name); err != nil {
		return err
	}
	if strings.ContainsAny(password, "\n\r\x00:") {
		return rlerr.Usagef("the password may not contain a newline, a NUL byte or a colon")
	}
	if len(password) < 12 {
		return rlerr.Usagef("the password is shorter than 12 characters").
			WithHint("keys are preferred over passwords; see 'ratline key add --help'")
	}
	// chpasswd reads from stdin, which keeps the secret out of the process
	// table, out of the shell history and out of the audit log.
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name:    "chpasswd",
		Stdin:   strings.NewReader(name + ":" + password + "\n"),
		Mutates: true, Label: "chpasswd",
	}); err != nil {
		return err
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "usermod", Args: []string{"--unlock", name}, Mutates: true,
	}); err != nil {
		return err
	}
	u, err := m.State.GetUser(ctx, name)
	if err != nil {
		return err
	}
	u.PasswordLogin = true
	return m.State.PutUser(ctx, u)
}
