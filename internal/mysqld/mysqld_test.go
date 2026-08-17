package mysqld

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
)

// mysqlServerFake is a small model of the server the flows talk to through the mysql
// client: root authenticates over the socket until an admin exists, the admin
// authenticates with its password, and the bind follows the managed drop-in after a
// restart. Modeling the relationships rather than scripting calls keeps the verification
// assertions honest — a flow that widens the bind without restarting fails here as it
// would on a real host.
type mysqlServerFake struct {
	mu sync.Mutex

	adminUser     string
	adminPassword string
	adminExists   bool
	rejectSocket  bool // models a server whose root is password-secured (no socket auth)

	bindRemote bool
	confPath   string
}

// run answers a mysql invocation. It reads the SQL from stdin and the credentials from the
// staged defaults-file named in argv.
func (f *mysqlServerFake) run(c system.Cmd) (*system.Result, error) {
	var defaults string
	for _, a := range c.Args {
		if strings.HasPrefix(a, "--defaults-extra-file=") {
			defaults = strings.TrimPrefix(a, "--defaults-extra-file=")
		}
	}
	body, _ := os.ReadFile(defaults)
	creds := string(body)
	sql := ""
	if c.Stdin != nil {
		var sb strings.Builder
		_, _ = sb.WriteString("")
		buf := make([]byte, 4096)
		for {
			n, err := c.Stdin.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		sql = sb.String()
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	isRootSocket := strings.Contains(creds, "user=root") && !strings.Contains(creds, "password=") && !f.rejectSocket
	authedAsAdmin := f.adminExists &&
		strings.Contains(creds, "user="+f.adminUser) &&
		strings.Contains(creds, "password="+f.adminPassword)

	fail := func(msg string) (*system.Result, error) {
		return &system.Result{ExitCode: 1, Stderr: msg}, fmt.Errorf("mysql exited 1")
	}
	if !isRootSocket && !authedAsAdmin {
		return fail("ERROR 1045 (28000): Access denied")
	}
	// A CREATE USER for the admin makes future admin auth succeed.
	if strings.Contains(sql, "CREATE USER") && strings.Contains(sql, "'"+f.adminUser+"'@'%'") {
		f.adminExists = true
	}
	if strings.Contains(sql, "SELECT VERSION()") || sql == "" {
		return &system.Result{Stdout: "8.0.36\n"}, nil
	}
	return &system.Result{Stdout: ""}, nil
}

func (f *mysqlServerFake) applyConf() {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, err := os.ReadFile(f.confPath)
	f.bindRemote = err == nil && strings.Contains(string(body), "0.0.0.0")
}

func (f *mysqlServerFake) listening() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	addr := "127.0.0.1"
	if f.bindRemote {
		addr = "0.0.0.0"
	}
	return "LISTEN 0 80 " + addr + ":3306 0.0.0.0:*\n"
}

type splitRunner struct {
	fake  *systest.FakeRunner
	mysql *mysqlServerFake
}

func (r *splitRunner) Run(ctx context.Context, c system.Cmd) (*system.Result, error) {
	switch c.Name {
	case "mysql":
		return r.mysql.run(c)
	case "ss":
		return &system.Result{Stdout: r.mysql.listening()}, nil
	}
	res, err := r.fake.Run(ctx, c)
	if c.Name == "systemctl" && len(c.Args) > 0 && c.Args[0] == "restart" && err == nil {
		r.mysql.applyConf()
	}
	return res, err
}

const activeDenyStatus = "Status: active\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n"

func testManager(t *testing.T, installed bool) (*Manager, *systest.FakeRunner, *mysqlServerFake) {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"etc/mysql/mysql.conf.d", "etc/mysql/mariadb.conf.d", "run", "etc/ratline/db"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Paths.RunDir = filepath.Join(dir, "run")
	cfg.Paths.MySQLDefaultsFile = filepath.Join(dir, "etc", "ratline", "db", "mysql.cnf")

	bins := system.NewBinaries()
	for _, b := range []string{"apt-get", "systemctl", "mysql", "mysqld", "ufw", "ss"} {
		bins.Set(b, "/usr/bin/"+b)
	}
	st, err := state.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fake := systest.NewFakeRunner()
	fake.ExpectOutput("ufw status verbose", activeDenyStatus)
	fake.Expect("systemctl is-active mysql", systest.Response{ExitCode: 3})

	m := &Manager{
		Cfg: cfg, Log: log.Discard(), Bins: bins, State: st,
		OS:     system.OSInfo{ID: "ubuntu", Codename: "jammy", Arch: "amd64"},
		FSRoot: dir, StartWait: 50 * time.Millisecond, PollInterval: time.Millisecond,
		InstalledProbe: func() bool { return installed },
	}
	mf := &mysqlServerFake{
		adminUser: "admin", adminPassword: "a-password-long-enough",
		confPath: filepath.Join(dir, m.distro().ConfDropIn),
	}
	m.Runner = &splitRunner{fake: fake, mysql: mf}
	return m, fake, mf
}

func writeManagedConf(t *testing.T, m *Manager, remote bool) {
	t.Helper()
	if err := os.WriteFile(m.confPath(), RenderConf(remote), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeAdminDefaults(t *testing.T, m *Manager) {
	t.Helper()
	body := "[client]\nuser=admin\npassword=a-password-long-enough\nhost=127.0.0.1\nport=3306\n"
	if err := os.WriteFile(m.Cfg.Paths.MySQLDefaultsFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func opts() InstallOptions {
	return InstallOptions{AdminUser: "admin", Password: "a-password-long-enough"}
}

func TestInstallFreshHost(t *testing.T) {
	m, fake, mf := testManager(t, false)
	res, err := m.Install(context.Background(), opts())
	if err != nil {
		t.Fatal(err)
	}
	if !res.PackageInstalled || !res.AdminUserCreated {
		t.Errorf("result = %+v, want package installed and admin created", res)
	}
	if res.ServerVersion != "8.0.36" {
		t.Errorf("server version = %q", res.ServerVersion)
	}
	for _, key := range []string{"apt-get install -y mysql-server", "systemctl enable mysql",
		"systemctl start mysql", "systemctl restart mysql"} {
		if !fake.Called(key) {
			t.Errorf("%q was never run; calls: %v", key, fake.Keys())
		}
	}
	if s := m.confState(); !s.Managed || s.Remote {
		t.Errorf("conf state after install = %+v", s)
	}
	if mf.bindRemote {
		t.Error("the server is listening beyond localhost after a plain install")
	}
}

func TestInstallRefusesASecuredForeignServer(t *testing.T) {
	m, _, mf := testManager(t, true)
	// Installed, no managed drop-in, admin creds don't work and root socket is closed.
	mf.adminExists = false
	mf.adminPassword = "something-else"
	// Make root-socket auth fail too by flipping the fake: an empty adminUser never matches
	// root socket... instead, simulate a secured root by rejecting the socket. We model that
	// by setting a sentinel the fake checks.
	mf.rejectSocket = true

	_, err := m.Install(context.Background(), opts())
	if err == nil {
		t.Fatal("a secured foreign server was adopted")
	}
	if !strings.Contains(err.Error(), "already secured") {
		t.Errorf("err = %v", err)
	}
}

func TestInstallAgainVerifiesWithoutRecreating(t *testing.T) {
	m, fake, mf := testManager(t, true)
	writeManagedConf(t, m, false)
	writeAdminDefaults(t, m)
	mf.adminExists = true

	res, err := m.Install(context.Background(), opts())
	if err != nil {
		t.Fatal(err)
	}
	if res.PackageInstalled || res.AdminUserCreated {
		t.Errorf("a re-run reported doing work: %+v", res)
	}
	_ = fake
}

// accessManager is a host where install has finished: managed localhost conf, stored admin
// defaults, an existing admin the credentials open.
func accessManager(t *testing.T) (*Manager, *systest.FakeRunner, *mysqlServerFake) {
	t.Helper()
	m, fake, mf := testManager(t, true)
	writeManagedConf(t, m, false)
	writeAdminDefaults(t, m)
	mf.adminExists = true
	return m, fake, mf
}

func TestAccessAllowOpensBindThenRevokeCloses(t *testing.T) {
	m, fake, mf := accessManager(t)
	ctx := context.Background()

	res, err := m.AccessAllow(ctx, "203.0.113.19", "office", "test")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OpenedNetwork || res.Address != "203.0.113.19/32" {
		t.Errorf("result = %+v", res)
	}
	if !fake.Called("ufw allow proto tcp from 203.0.113.19/32 to any port 3306") {
		t.Errorf("no ufw rule was added; calls: %v", fake.Keys())
	}
	if !m.confState().Remote || !mf.bindRemote {
		t.Error("the server was not reconfigured to listen beyond localhost")
	}

	// A second address is a rule, not a restart.
	restarts := fake.CountCalls("systemctl restart")
	if res, err = m.AccessAllow(ctx, "198.51.100.0/24", "", "test"); err != nil {
		t.Fatal(err)
	}
	if res.OpenedNetwork {
		t.Error("the second address claims to have opened the network")
	}
	if fake.CountCalls("systemctl restart") != restarts {
		t.Error("the second address restarted mysql")
	}

	// Revoking the last one closes the network again — but there are two, so revoke both.
	if _, err = m.AccessRevoke(ctx, "198.51.100.0/24"); err != nil {
		t.Fatal(err)
	}
	if !m.confState().Remote {
		t.Error("revoking one of two addresses closed the bind")
	}
	rres, err := m.AccessRevoke(ctx, "203.0.113.19")
	if err != nil {
		t.Fatal(err)
	}
	if !rres.ClosedNetwork {
		t.Error("revoking the last address did not close the network")
	}
	if m.confState().Remote || mf.bindRemote {
		t.Error("the server still listens beyond localhost after the last revoke")
	}
}

func TestAccessAllowRefusesWithoutAGuardingFirewall(t *testing.T) {
	m, fake, _ := accessManager(t)
	fake.ExpectOutput("ufw status verbose", "Status: inactive\n")
	_, err := m.AccessAllow(context.Background(), "203.0.113.19", "", "test")
	if err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("err = %v", err)
	}
	if fake.Called("ufw allow") || m.confState().Remote {
		t.Error("a refusal still changed the host")
	}
}

func TestCheckExposure(t *testing.T) {
	ctx := context.Background()
	t.Run("localhost only", func(t *testing.T) {
		m, _, _ := accessManager(t)
		exp, err := m.CheckExposure(ctx)
		if err != nil || !exp.Present || exp.Remote {
			t.Errorf("exposure = %+v, %v", exp, err)
		}
	})
	t.Run("remote unguarded is the finding", func(t *testing.T) {
		m, fake, _ := accessManager(t)
		if _, err := m.AccessAllow(ctx, "203.0.113.19", "", "test"); err != nil {
			t.Fatal(err)
		}
		fake.ExpectOutput("ufw status verbose", "Status: inactive\n")
		exp, err := m.CheckExposure(ctx)
		if err != nil || !exp.Remote || exp.Guarded {
			t.Errorf("exposure = %+v, %v", exp, err)
		}
	})
}
