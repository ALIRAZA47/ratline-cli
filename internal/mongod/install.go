package mongod

import (
	"context"
	"os"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/mongo"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// InstallOptions is what the operator chose.
type InstallOptions struct {
	// Version is the release series ("8.0"); empty means the newest this host can
	// install.
	Version string
	// AdminUser is the root-role user created in the admin database.
	AdminUser string
	// Password is the admin user's password. It arrived on stdin or from a prompt —
	// never argv — and it leaves this struct only through the environment of mongosh
	// and the 0600 URI file.
	Password string
}

// InstallResult reports what happened, for output and for the caller's attach step.
type InstallResult struct {
	Version          string `json:"version"`
	ServerVersion    string `json:"server_version"`
	PackageInstalled bool   `json:"package_installed"` // false: it was already there
	AdminUserCreated bool   `json:"admin_user_created"`
	AdminUser        string `json:"admin_user"`
	ConfPath         string `json:"conf_path"`

	// AdminURI is the credentialed connection string for the attach step. Never
	// serialized: the password is in it.
	AdminURI string `json:"-"`
}

// Install puts MongoDB on this host and leaves it enforcing authorization, with a
// root-role admin user whose password the operator chose. Every step registers its
// undo; a failure anywhere leaves the host as it was, except that downloaded packages
// stay on disk — inert, service stopped and disabled — because unwinding an apt
// install would mean deciding on the operator's behalf what else those packages'
// dependencies were doing there. A re-run picks up from wherever the failure left off.
func (m *Manager) Install(ctx context.Context, opts InstallOptions) (result *InstallResult, err error) {
	version, err := ResolveVersion(m.OS, opts.Version)
	if err != nil {
		return nil, err
	}
	if err := m.Bins.Require("apt-get", "systemctl"); err != nil {
		return nil, err
	}

	conf := m.confState()
	adminURI := mongo.LocalAdminURI(opts.AdminUser, opts.Password)

	// A mongod ratline did not set up is attached, not adopted. The managed header is
	// the marker: it survives an interrupted install, so a re-run lands here and
	// continues, while a hand-built server is refused before anything is touched.
	if m.Installed() && conf.Exists && !conf.Managed {
		return nil, rlerr.Preconditionf("a MongoDB server is already installed on this host, and ratline did not set it up").
			WithHint("attach it instead: enable authorization and create an admin user " +
				"yourself, then run 'ratline db connect'")
	}

	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)

	result = &InstallResult{
		Version: version, AdminUser: opts.AdminUser, ConfPath: ConfPath, AdminURI: adminURI,
	}

	if !m.Installed() {
		if err = m.installPackages(ctx, rb, version); err != nil {
			return nil, err
		}
		result.PackageInstalled = true
	}

	if err = m.ensureRunning(ctx, rb); err != nil {
		return nil, err
	}

	// The server is up on localhost. What state it is in decides the rest: a fresh
	// package enforces nothing, an interrupted re-run may already enforce everything.
	info, err := m.waitForServer(ctx, PlainLocalURI)
	if err != nil {
		return nil, err
	}

	var verified *mongo.ServerInfo
	if info.AuthEnabled {
		// An earlier run finished this part. Prove the operator's credentials open
		// the server rather than restarting a healthy one — sites may be using it.
		verified, err = m.db().PingURI(ctx, adminURI)
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodePrecondition,
				"this server already enforces authorization, and that password did not work for %q", opts.AdminUser).
				WithHint("an earlier 'db install' created the admin user with a different " +
					"password; re-run with that one")
		}
	} else {
		created, cerr := m.createAdminUser(ctx, rb, opts, adminURI)
		if cerr != nil {
			return nil, cerr
		}
		result.AdminUserCreated = created

		changed, werr := m.writeConf(ctx, rb, false, true)
		if werr != nil {
			return nil, werr
		}
		if !changed {
			// The file already says authorization is enabled and the running server
			// says it is not. Restarting would probably fix it, but something put the
			// server into this state and a guess here papers over it.
			return nil, rlerr.Externalf("%s already enables authorization, but the running mongod does not enforce it", ConfPath).
				WithHint("check `systemctl cat %s` for command-line overrides, then restart it yourself", ServiceName)
		}

		// Proven with the operator's credentials against the running server. This is
		// the check everything above exists to satisfy: not that the config says
		// authorization, but that the server enforces it and these credentials open it.
		if verified, err = m.restartAndVerify(ctx, adminURI); err != nil {
			return nil, err
		}
		// And the freshly installed server belongs to this machine alone until an
		// operator says otherwise with `db access allow`.
		if err = m.verifyBind(ctx, false); err != nil {
			return nil, err
		}
	}
	result.ServerVersion = verified.Version
	return result, nil
}

// installPackages adds MongoDB's repository — signing key pinned from the binary, not
// the network — and installs the server packages.
func (m *Manager) installPackages(ctx context.Context, rb *system.Rollback, version string) error {
	keyring, err := SigningKey(version)
	if err != nil {
		return err
	}
	line, err := SourceLine(m.OS, version)
	if err != nil {
		return err
	}

	if err := m.writeWithUndo(rb, m.abs(KeyringPath(version)), keyring, 0o644); err != nil {
		return err
	}
	sources := system.ManagedHeader + "\n" +
		"# MongoDB's official repository, added by `ratline db install`.\n" +
		"# The signing key is pinned from the ratline binary, not downloaded.\n" +
		line + "\n"
	if err := m.writeWithUndo(rb, m.abs(SourcesPath(version)), []byte(sources), 0o644); err != nil {
		return err
	}

	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "apt-get", Args: []string{"update"},
		Env:     system.MinimalEnv("DEBIAN_FRONTEND=noninteractive"),
		Mutates: true, Stream: true, Timeout: 5 * time.Minute, Label: "apt-get update",
	}); err != nil {
		return err
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "apt-get", Args: []string{"install", "-y", "mongodb-org"},
		Env:     system.MinimalEnv("DEBIAN_FRONTEND=noninteractive"),
		Mutates: true, Stream: true, Timeout: 15 * time.Minute, Label: "apt-get install mongodb-org",
	}); err != nil {
		return err
	}
	// The undo for an install is not an uninstall — see Install's contract. It is
	// making sure nothing runs or listens: stopped, disabled, and said so.
	rb.Push("stop and disable mongod (the packages stay installed; a re-run continues from them)",
		func(ctx context.Context) error {
			_, err := m.Runner.Run(ctx, system.Cmd{
				Name: "systemctl", Args: []string{"disable", "--now", ServiceName},
				Mutates: true, Label: "disable mongod",
			})
			return err
		})
	return nil
}

// ensureRunning enables the unit and starts it if it is not already answering
// systemd. Enable is unconditional because it is idempotent; start is not a restart,
// so a healthy server serving tenants is not bounced by a re-run.
func (m *Manager) ensureRunning(ctx context.Context, rb *system.Rollback) error {
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"enable", ServiceName},
		Mutates: true, Label: "enable mongod",
	}); err != nil {
		return err
	}
	res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"is-active", ServiceName},
		OKExit: []int{3}, Label: "mongod state",
	})
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		return nil
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"start", ServiceName},
		Mutates: true, Label: "start mongod",
	}); err != nil {
		return err
	}
	rb.Push("stop mongod", func(ctx context.Context) error {
		_, err := m.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{"stop", ServiceName},
			Mutates: true, Label: "stop mongod",
		})
		return err
	})
	return nil
}

// createAdminUser creates the operator's root-role user on a server that does not
// enforce authorization yet. "Already exists" is not a failure by itself — an
// interrupted earlier run may have gotten exactly this far — but only if the password
// the operator gave now is the one that user has; otherwise two runs disagree and no
// guess between them is safe.
func (m *Manager) createAdminUser(ctx context.Context, rb *system.Rollback, opts InstallOptions, adminURI string) (bool, error) {
	err := m.db().CreateAdminUser(ctx, PlainLocalURI, opts.AdminUser, opts.Password)
	if err == nil {
		rb.Push("remove the admin user "+opts.AdminUser, func(ctx context.Context) error {
			// The conf undo has already run by the time this does, so the server is
			// back to not enforcing authorization and the plain URI works.
			return m.db().DropAdminUser(ctx, PlainLocalURI, opts.AdminUser)
		})
		return true, nil
	}
	if !rlerr.Is(err, rlerr.CodePrecondition) {
		return false, err
	}
	// Precondition here is the translate() of "already exists". Prove the password
	// matches before continuing past it.
	if _, perr := m.db().PingURI(ctx, adminURI); perr != nil {
		return false, rlerr.Wrap(err, rlerr.CodePrecondition,
			"the admin user %q already exists and the given password is not its password", opts.AdminUser).
			WithHint("re-run with the password from the earlier attempt, or remove the user: " +
				"mongosh --eval 'db.getSiblingDB(\"admin\").dropAllUsers()' while the server " +
				"still allows local access")
	}
	return false, nil
}

// writeWithUndo writes a root-owned file and registers putting back whatever was
// there — the previous contents, or nothing.
func (m *Manager) writeWithUndo(rb *system.Rollback, path string, body []byte, mode os.FileMode) error {
	previous, readErr := os.ReadFile(path)
	existed := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return rlerr.Wrap(readErr, rlerr.CodeGeneric, "reading %s", path)
	}
	if err := system.WriteFileAtomic(path, body, mode, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}
	rb.Push("restore "+path, func(context.Context) error {
		if !existed {
			return os.Remove(path)
		}
		return system.WriteFileAtomic(path, previous, mode, system.KeepUnchanged, system.KeepUnchanged)
	})
	return nil
}
