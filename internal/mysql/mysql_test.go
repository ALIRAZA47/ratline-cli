package mysql

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// capRunner records every command and the SQL it received on stdin, and answers with a
// canned stdout. It is enough to prove what SQL ratline builds and — the load-bearing
// property — that a password never lands in argv.
type capRunner struct {
	calls []capCall
	out   string
}

type capCall struct {
	args  []string
	stdin string
}

func (r *capRunner) Run(_ context.Context, c system.Cmd) (*system.Result, error) {
	var sb strings.Builder
	if c.Stdin != nil {
		_, _ = io.Copy(&sb, c.Stdin)
	}
	r.calls = append(r.calls, capCall{args: append([]string(nil), c.Args...), stdin: sb.String()})
	return &system.Result{Stdout: r.out}, nil
}

func (r *capRunner) last() capCall { return r.calls[len(r.calls)-1] }

func testManager(t *testing.T, out string) (*Manager, *capRunner) {
	t.Helper()
	dir := t.TempDir()
	defaults := filepath.Join(dir, "mysql.cnf")
	if err := os.WriteFile(defaults, []byte("[client]\nuser=admin\npassword=adminpw\nhost=127.0.0.1\nport=3306\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Paths.MySQLDefaultsFile = defaults
	cfg.Paths.RunDir = filepath.Join(dir, "run")
	r := &capRunner{out: out}
	bins := system.NewBinaries()
	bins.Set("mysql", "/usr/bin/mysql")
	return &Manager{Cfg: cfg, Log: log.Discard(), Runner: r, Bins: bins, State: nil}, r
}

func TestCreateUserBuildsScopedGrantAndKeepsPasswordOffArgv(t *testing.T) {
	m, r := testManager(t, "")
	pw, err := m.CreateUser(context.Background(), "shop", "shop_app", "readWrite", "")
	if err != nil {
		t.Fatal(err)
	}
	call := r.last()

	// The credential file is the only credential-bearing argument, and it is first.
	if len(call.args) == 0 || !strings.HasPrefix(call.args[0], "--defaults-extra-file=") {
		t.Fatalf("first arg is not the defaults-file: %v", call.args)
	}
	// The generated password must never appear in argv — /proc/PID/cmdline is world-readable.
	for _, a := range call.args {
		if strings.Contains(a, pw) {
			t.Errorf("the password leaked into argv: %q", a)
		}
	}
	// It travels on stdin, inside the CREATE USER statement, scoped to the database.
	if !strings.Contains(call.stdin, "CREATE USER IF NOT EXISTS 'shop_app'@'%' IDENTIFIED BY '"+pw+"'") {
		t.Errorf("CREATE USER not as expected:\n%s", call.stdin)
	}
	if !strings.Contains(call.stdin, "GRANT SELECT, INSERT, UPDATE, DELETE ON `shop`.* TO 'shop_app'@'%'") {
		t.Errorf("GRANT not scoped/privileged as expected:\n%s", call.stdin)
	}
}

func TestRolePrivilegeMapping(t *testing.T) {
	cases := map[string]string{
		"read":      "GRANT SELECT ON `shop`.*",
		"readWrite": "GRANT SELECT, INSERT, UPDATE, DELETE ON `shop`.*",
		"dbOwner":   "GRANT ALL PRIVILEGES ON `shop`.*",
	}
	for role, want := range cases {
		m, r := testManager(t, "")
		if _, err := m.CreateUser(context.Background(), "shop", "u_"+role, role, "pw"); err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if !strings.Contains(r.last().stdin, want) {
			t.Errorf("role %s: want %q in:\n%s", role, want, r.last().stdin)
		}
	}
}

func TestPingParsesVersion(t *testing.T) {
	m, _ := testManager(t, "8.0.36\n")
	info, err := m.Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "8.0.36" || !info.AuthEnabled {
		t.Errorf("info = %+v", info)
	}
}

func TestValidationRefusesDangerousNamesBeforeRunning(t *testing.T) {
	m, r := testManager(t, "")
	if err := m.CreateDatabase(context.Background(), "shop`; DROP DATABASE mysql;--"); err == nil {
		t.Fatal("a database name with a backtick and semicolon was accepted")
	}
	if len(r.calls) != 0 {
		t.Errorf("a command ran despite the invalid name: %v", r.calls)
	}
}

func TestAdminDefaultsFileRefusesWorldReadable(t *testing.T) {
	m, _ := testManager(t, "")
	if _, err := m.AdminDefaultsFile(); err != nil {
		t.Fatalf("a 0600 file should be accepted: %v", err)
	}
	if err := os.Chmod(m.Cfg.Paths.MySQLDefaultsFile, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := m.AdminDefaultsFile()
	if err == nil || !strings.Contains(err.Error(), "0644") {
		t.Errorf("a 0644 defaults-file should be refused naming the mode, got: %v", err)
	}
}

func TestConnectionURIAndRedact(t *testing.T) {
	m, _ := testManager(t, "")
	uri := m.ConnectionURI("shop", "shop_app", "p@ss/w:rd")
	if !strings.HasPrefix(uri, "mysql://shop_app:") || !strings.Contains(uri, "@127.0.0.1:3306/shop") {
		t.Errorf("uri = %q", uri)
	}
	// A password with URI-hostile characters must be percent-encoded, not raw.
	if strings.Contains(uri, "p@ss/w:rd") {
		t.Errorf("password was not encoded in %q", uri)
	}
	if r := Redact(uri); strings.Contains(r, "ss") && !strings.Contains(r, "REDACTED") {
		t.Errorf("Redact did not hide the password: %q", r)
	}
}

func TestDefaultDatabaseName(t *testing.T) {
	cases := map[string]string{
		"shop.example.com": "shop_example_com",
		"9lives.io":        "db_9lives_io",
	}
	for domain, want := range cases {
		if got := DefaultDatabaseName(domain); got != want {
			t.Errorf("DefaultDatabaseName(%q) = %q, want %q", domain, got, want)
		}
	}
}
