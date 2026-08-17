package mysqld

import (
	"context"
	"os"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/mysql"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// InstallOptions is what the operator chose.
type InstallOptions struct {
	AdminUser string
	// Password is the admin account's password. It arrived on stdin or from a prompt —
	// never argv — and leaves this struct only through a 0600 defaults-file.
	Password string
}

// InstallResult reports what happened, for output and for the caller's attach step.
type InstallResult struct {
	ServerVersion    string `json:"server_version"`
	Flavour          string `json:"flavour"`
	PackageInstalled bool   `json:"package_installed"`
	AdminUserCreated bool   `json:"admin_user_created"`
	AdminUser        string `json:"admin_user"`
	ConfPath         string `json:"conf_path"`

	// Creds is the admin credential set the caller stores as the defaults-file. Never
	// serialized: it carries the password.
	Creds mysql.Creds `json:"-"`
}

// Install puts MySQL/MariaDB on this host and leaves it reachable only from localhost with
// a managed admin account whose password the operator chose. Every step registers its
// undo; a failure leaves the host as it was, except that the distro packages stay
// installed (stopped and disabled), because unwinding an apt install would mean deciding
// what else those packages were doing there. A re-run continues from wherever it stopped.
func (m *Manager) Install(ctx context.Context, opts InstallOptions) (result *InstallResult, err error) {
	if serr := m.Supported(); serr != nil {
		return nil, serr
	}
	if rerr := m.Bins.Require("apt-get", "systemctl"); rerr != nil {
		return nil, rerr
	}

	d := m.distro()
	adminCreds := mysql.Creds{User: opts.AdminUser, Password: opts.Password, Host: mysql.DefaultHost, Port: Port}

	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)

	result = &InstallResult{
		AdminUser: opts.AdminUser, ConfPath: d.ConfDropIn, Flavour: d.Flavour, Creds: adminCreds,
	}

	if !m.Installed() {
		if err = m.installPackage(ctx, rb, d); err != nil {
			return nil, err
		}
		result.PackageInstalled = true
	}
	if err = m.ensureRunning(ctx, rb, d); err != nil {
		return nil, err
	}

	// What state is the server's authentication in? Three cases, decided by what
	// actually connects, not by what a config file says.
	adminDefaults, err := m.db().StageDefaultsFile(adminCreds)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(adminDefaults) }()

	if _, perr := m.db().PingWith(ctx, adminDefaults); perr == nil {
		// The admin account already exists with this password — an interrupted re-run.
		result.AdminUserCreated = false
	} else {
		// Reach the fresh server as OS root over the local socket, before any password
		// exists, and create the managed admin account there.
		rootDefaults, serr := m.db().StageDefaultsRaw(socketDefaults())
		if serr != nil {
			return nil, serr
		}
		defer func() { _ = os.Remove(rootDefaults) }()
		if _, serr := m.db().PingWith(ctx, rootDefaults); serr != nil {
			return nil, rlerr.Preconditionf("this MySQL server is already secured and ratline did not set it up").
				WithHint("the given admin password does not work and root is not reachable over the " +
					"local socket, so ratline cannot bootstrap it. Attach it instead: create an admin " +
					"user yourself, then 'ratline db connect --engine mysql'")
		}
		if cerr := m.db().CreateAdminUser(ctx, rootDefaults, opts.AdminUser, opts.Password); cerr != nil {
			return nil, cerr
		}
		result.AdminUserCreated = true
		rb.Push("remove the admin user "+opts.AdminUser, func(ctx context.Context) error {
			return m.db().DropUser(ctx, opts.AdminUser, "%")
		})
	}

	// Take ownership of the bind (localhost) and prove the server comes back up and
	// answers the admin credentials over TCP, listening only on localhost.
	if _, err = m.writeConf(ctx, rb, false, true); err != nil {
		return nil, err
	}
	info, err := m.restartAndVerify(ctx, adminDefaults)
	if err != nil {
		return nil, err
	}
	if err = m.verifyBind(ctx, false); err != nil {
		return nil, err
	}
	result.ServerVersion = info.Version
	return result, nil
}

// installPackage installs the distro MySQL-family server. Its undo is not an uninstall —
// it is making sure nothing runs or listens (stopped, disabled), leaving the packages in
// place so a re-run continues from them.
func (m *Manager) installPackage(ctx context.Context, rb *system.Rollback, d Distro) error {
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "apt-get", Args: []string{"update"},
		Env:     system.MinimalEnv("DEBIAN_FRONTEND=noninteractive"),
		Mutates: true, Stream: true, Timeout: 5 * time.Minute, Label: "apt-get update",
	}); err != nil {
		return err
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "apt-get", Args: []string{"install", "-y", d.Package},
		Env:     system.MinimalEnv("DEBIAN_FRONTEND=noninteractive"),
		Mutates: true, Stream: true, Timeout: 15 * time.Minute, Label: "apt-get install " + d.Package,
	}); err != nil {
		return err
	}
	rb.Push("stop and disable "+d.Service+" (the packages stay installed; a re-run continues)",
		func(ctx context.Context) error {
			_, err := m.Runner.Run(ctx, system.Cmd{
				Name: "systemctl", Args: []string{"disable", "--now", d.Service},
				Mutates: true, Label: "disable mysql",
			})
			return err
		})
	return nil
}

// ensureRunning enables the unit and starts it if it is not already answering systemd.
func (m *Manager) ensureRunning(ctx context.Context, rb *system.Rollback, d Distro) error {
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"enable", d.Service},
		Mutates: true, Label: "enable mysql",
	}); err != nil {
		return err
	}
	res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"is-active", d.Service},
		OKExit: []int{3}, Label: "mysql state",
	})
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		return nil
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"start", d.Service},
		Mutates: true, Label: "start mysql",
	}); err != nil {
		return err
	}
	rb.Push("stop mysql", func(ctx context.Context) error {
		_, err := m.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{"stop", d.Service},
			Mutates: true, Label: "stop mysql",
		})
		return err
	})
	return nil
}
