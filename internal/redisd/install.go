package redisd

import (
	"context"
	"os"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/redis"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// InstallOptions is what the operator chose.
type InstallOptions struct {
	// Password is the admin (default user) password. It arrives on stdin or from a
	// prompt — never argv — and leaves this struct only through the 0640 aclfile and the
	// 0600 URI file.
	Password string
}

// InstallResult reports what happened, for output and the caller's attach step.
type InstallResult struct {
	ServerVersion    string `json:"server_version"`
	PackageInstalled bool   `json:"package_installed"`
	ConfPath         string `json:"conf_path"`

	// AdminURI is the credentialed connection string the caller stores. Never
	// serialized: the password is in it.
	AdminURI string `json:"-"`
}

// Install puts Redis on this host and leaves it requiring a password, bound to localhost.
// A fresh server has no password; ratline writes an aclfile giving the default user the
// password the operator chose, points the stock redis.conf at ratline's include, restarts,
// and proves the server answers those credentials and refuses an unauthenticated one. On a
// re-run the existing aclfile is left alone — it holds the users `db create` added — and the
// given password is verified against it.
func (m *Manager) Install(ctx context.Context, opts InstallOptions) (result *InstallResult, err error) {
	if m.OS.ID != "ubuntu" && m.OS.ID != "debian" {
		return nil, rlerr.Preconditionf("ratline installs Redis from the distribution package, and this host is %q", m.OS.PrettyName).
			WithHint("install redis-server however this distribution does, then 'ratline db connect --engine redis'")
	}
	if rerr := m.Bins.Require("apt-get", "systemctl"); rerr != nil {
		return nil, rerr
	}

	conf := m.confState()
	adminURI := redis.LocalAdminURI(opts.Password)
	aclExists := fileExists(m.aclFile())

	// A Redis ratline did not set up is attached, not adopted. The managed include and
	// the aclfile are the markers; absent both on an already-installed server, it is
	// someone else's.
	if m.Installed() && !conf.Managed && !aclExists {
		return nil, rlerr.Preconditionf("a Redis server is already installed on this host, and ratline did not set it up").
			WithHint("attach it instead: set a password and create an admin, then 'ratline db connect --engine redis'")
	}

	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)

	result = &InstallResult{ConfPath: ConfPath, AdminURI: adminURI}

	if !m.Installed() {
		if err = m.installPackage(ctx, rb); err != nil {
			return nil, err
		}
		result.PackageInstalled = true
	}
	if err = m.ensureRunning(ctx, rb); err != nil {
		return nil, err
	}

	// The aclfile is written only when it does not exist: on a re-run it already holds
	// the admin plus every user `db create` added, and rewriting it would drop them.
	if !aclExists {
		if err = m.writeACLFile(rb, opts.Password); err != nil {
			return nil, err
		}
	}
	if _, err = m.writeConf(ctx, rb, false, true); err != nil {
		return nil, err
	}
	if err = m.ensureInclude(rb); err != nil {
		return nil, err
	}

	info, err := m.restartAndVerify(ctx, adminURI)
	if err != nil {
		if aclExists {
			return nil, rlerr.Wrap(err, rlerr.CodePrecondition,
				"this Redis already requires a password, and the one given did not work").
				WithHint("re-run with the password from the earlier install")
		}
		return nil, err
	}
	if err = m.verifyBind(ctx, false); err != nil {
		return nil, err
	}
	result.ServerVersion = info.Version
	return result, nil
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func (m *Manager) installPackage(ctx context.Context, rb *system.Rollback) error {
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "apt-get", Args: []string{"update"},
		Env: system.MinimalEnv("DEBIAN_FRONTEND=noninteractive"), Mutates: true, Stream: true,
		Timeout: 5 * time.Minute, Label: "apt-get update",
	}); err != nil {
		return err
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{
		Name: "apt-get", Args: []string{"install", "-y", Package},
		Env: system.MinimalEnv("DEBIAN_FRONTEND=noninteractive"), Mutates: true, Stream: true,
		Timeout: 15 * time.Minute, Label: "apt-get install redis-server",
	}); err != nil {
		return err
	}
	rb.Push("stop and disable redis-server (the packages stay installed; a re-run continues)",
		func(ctx context.Context) error {
			_, err := m.Runner.Run(ctx, system.Cmd{Name: "systemctl", Args: []string{"disable", "--now", ServiceName}, Mutates: true, Label: "disable redis"})
			return err
		})
	return nil
}

func (m *Manager) ensureRunning(ctx context.Context, rb *system.Rollback) error {
	if _, err := m.Runner.Run(ctx, system.Cmd{Name: "systemctl", Args: []string{"enable", ServiceName}, Mutates: true, Label: "enable redis"}); err != nil {
		return err
	}
	res, err := m.Runner.Run(ctx, system.Cmd{Name: "systemctl", Args: []string{"is-active", ServiceName}, OKExit: []int{3}, Label: "redis state"})
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		return nil
	}
	if _, err := m.Runner.Run(ctx, system.Cmd{Name: "systemctl", Args: []string{"start", ServiceName}, Mutates: true, Label: "start redis"}); err != nil {
		return err
	}
	rb.Push("stop redis", func(ctx context.Context) error {
		_, err := m.Runner.Run(ctx, system.Cmd{Name: "systemctl", Args: []string{"stop", ServiceName}, Mutates: true, Label: "stop redis"})
		return err
	})
	return nil
}
