package redis

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

type capRunner struct {
	calls []capCall
	out   string
}

type capCall struct {
	args  []string
	env   []string
	stdin string
}

func (r *capRunner) Run(_ context.Context, c system.Cmd) (*system.Result, error) {
	var sb strings.Builder
	if c.Stdin != nil {
		_, _ = io.Copy(&sb, c.Stdin)
	}
	r.calls = append(r.calls, capCall{args: append([]string(nil), c.Args...), env: append([]string(nil), c.Env...), stdin: sb.String()})
	return &system.Result{Stdout: r.out}, nil
}

func (r *capRunner) last() capCall { return r.calls[len(r.calls)-1] }

func testManager(t *testing.T, out string) (*Manager, *capRunner) {
	t.Helper()
	dir := t.TempDir()
	uriFile := filepath.Join(dir, "redis.uri")
	if err := os.WriteFile(uriFile, []byte("redis://:adminpass@127.0.0.1:6379\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Paths.RedisURIFile = uriFile
	r := &capRunner{out: out}
	bins := system.NewBinaries()
	bins.Set("redis-cli", "/usr/bin/redis-cli")
	return &Manager{Cfg: cfg, Log: log.Discard(), Runner: r, Bins: bins}, r
}

func TestCreateKeyspaceUserConfinesAndKeepsSecretsOffArgv(t *testing.T) {
	m, r := testManager(t, "")
	pw, err := m.CreateKeyspaceUser(context.Background(), "shop", "shop_app", "readWrite", "")
	if err != nil {
		t.Fatal(err)
	}
	call := r.last()

	// The admin password is in REDISCLI_AUTH, never argv.
	joinedArgs := strings.Join(call.args, " ")
	if strings.Contains(joinedArgs, "adminpass") {
		t.Errorf("admin password leaked into argv: %v", call.args)
	}
	foundAuth := false
	for _, e := range call.env {
		if e == "REDISCLI_AUTH=adminpass" {
			foundAuth = true
		}
		if strings.Contains(e, pw) {
			// The new user's password must not be in the environment either — it goes on stdin.
			t.Errorf("new user password leaked into env: %q", e)
		}
	}
	if !foundAuth {
		t.Errorf("REDISCLI_AUTH not set; env = %v", call.env)
	}
	// The new user's password is in the ACL SETUSER command on stdin, not argv.
	if strings.Contains(joinedArgs, pw) {
		t.Errorf("new user password leaked into argv: %v", call.args)
	}
	if !strings.Contains(call.stdin, "ACL SETUSER shop_app reset on >"+pw+" ") {
		t.Errorf("ACL SETUSER not as expected:\n%s", call.stdin)
	}
	// Confined to the keyspace, with read+write, and never the dangerous category.
	for _, want := range []string{"~shop:*", "&shop:*", "+@read", "+@write", "-@dangerous"} {
		if !strings.Contains(call.stdin, want) {
			t.Errorf("ACL rule missing %q:\n%s", want, call.stdin)
		}
	}
	// And it is persisted.
	if !strings.Contains(call.stdin, "ACL SAVE") {
		t.Errorf("ACL SAVE missing:\n%s", call.stdin)
	}
}

func TestRoleCategories(t *testing.T) {
	cases := map[string]string{
		"read":      "+@read -@dangerous",
		"readWrite": "+@read +@write -@dangerous",
		"dbOwner":   "+@all -@dangerous",
	}
	for role, want := range cases {
		if got := roleCategories(role); got != want {
			t.Errorf("roleCategories(%q) = %q, want %q", role, got, want)
		}
	}
}

func TestPingParsesVersion(t *testing.T) {
	m, _ := testManager(t, "# Server\r\nredis_version:7.0.15\r\nredis_mode:standalone\r\n")
	info, err := m.Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "7.0.15" {
		t.Errorf("version = %q", info.Version)
	}
}

func TestAdminURIRefusesWorldReadable(t *testing.T) {
	m, _ := testManager(t, "")
	if _, err := m.AdminURI(); err != nil {
		t.Fatalf("0600 file should be accepted: %v", err)
	}
	if err := os.Chmod(m.Cfg.Paths.RedisURIFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AdminURI(); err == nil || !strings.Contains(err.Error(), "0644") {
		t.Errorf("a 0644 URI file should be refused naming the mode, got: %v", err)
	}
}

func TestConnectionURIAndRedact(t *testing.T) {
	m, _ := testManager(t, "")
	uri := m.ConnectionURI("shop_app", "p@ss/w:rd")
	if !strings.HasPrefix(uri, "redis://shop_app:") || !strings.Contains(uri, "@127.0.0.1:6379/0") {
		t.Errorf("uri = %q", uri)
	}
	if strings.Contains(uri, "p@ss/w:rd") {
		t.Errorf("password not encoded: %q", uri)
	}
	if r := Redact(uri); !strings.Contains(r, "REDACTED") {
		t.Errorf("Redact did not hide the password: %q", r)
	}
}

func TestFirstErrorDetectsRedisReplies(t *testing.T) {
	if firstError("OK\n(error) WRONGPASS invalid username-password pair\n") == "" {
		t.Error("did not detect an error reply")
	}
	if firstError("OK\nPONG\n") != "" {
		t.Error("false positive on clean output")
	}
}
