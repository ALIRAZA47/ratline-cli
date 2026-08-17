package redisd

import (
	"context"
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

// redisServerFake models the running server: it requires a password once the aclfile with
// one is loaded (on restart), answers INFO to an authenticated client, and refuses an
// unauthenticated PING. bindRemote follows the managed include.
type redisServerFake struct {
	mu            sync.Mutex
	adminPassword string
	passwordSet   bool
	bindRemote    bool
	confPath      string
	aclPath       string
}

func (f *redisServerFake) restarted() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if body, err := os.ReadFile(f.confPath); err == nil {
		f.bindRemote = strings.Contains(string(body), "bind * ")
	}
	if body, err := os.ReadFile(f.aclPath); err == nil {
		if i := strings.Index(string(body), "user default on >"); i >= 0 {
			rest := string(body)[i+len("user default on >"):]
			f.adminPassword = strings.Fields(rest)[0]
			f.passwordSet = true
		}
	}
}

func (f *redisServerFake) run(c system.Cmd) (*system.Result, error) {
	auth := ""
	for _, e := range c.Env {
		if v, ok := strings.CutPrefix(e, "REDISCLI_AUTH="); ok {
			auth = v
		}
	}
	joined := strings.Join(c.Args, " ")
	if c.Stdin != nil {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := c.Stdin.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		joined += " " + sb.String()
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// The no-auth PING used by AuthEnforced (passed as an argument, no stdin).
	if strings.Contains(joined, "PING") {
		if f.passwordSet && auth == "" {
			return &system.Result{Stdout: "NOAUTH Authentication required.\n"}, nil
		}
		return &system.Result{Stdout: "PONG\n"}, nil
	}
	if f.passwordSet && auth != f.adminPassword {
		return &system.Result{Stdout: "(error) NOAUTH Authentication required.\n"}, nil
	}
	if strings.Contains(joined, "INFO") {
		return &system.Result{Stdout: "redis_version:7.0.15\n"}, nil
	}
	return &system.Result{Stdout: "OK\n"}, nil
}

type splitRunner struct {
	fake  *systest.FakeRunner
	redis *redisServerFake
}

func (r *splitRunner) Run(ctx context.Context, c system.Cmd) (*system.Result, error) {
	switch c.Name {
	case "redis-cli":
		return r.redis.run(c)
	case "ss":
		addr := "127.0.0.1"
		r.redis.mu.Lock()
		if r.redis.bindRemote {
			addr = "0.0.0.0"
		}
		r.redis.mu.Unlock()
		return &system.Result{Stdout: "LISTEN 0 511 " + addr + ":6379 0.0.0.0:*\n"}, nil
	}
	res, err := r.fake.Run(ctx, c)
	if c.Name == "systemctl" && len(c.Args) > 0 && c.Args[0] == "restart" && err == nil {
		r.redis.restarted()
	}
	return res, err
}

const activeDenyStatus = "Status: active\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n"

func testManager(t *testing.T, installed bool) (*Manager, *systest.FakeRunner, *redisServerFake) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "etc/redis"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The stock redis.conf must exist for the include append.
	if err := os.WriteFile(filepath.Join(dir, "etc/redis/redis.conf"), []byte("# stock\nport 6379\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Paths.RunDir = filepath.Join(dir, "run")
	cfg.Paths.RedisURIFile = filepath.Join(dir, "etc/redis/ratline.uri")

	bins := system.NewBinaries()
	for _, b := range []string{"apt-get", "systemctl", "redis-cli", "redis-server", "ufw", "ss"} {
		bins.Set(b, "/usr/bin/"+b)
	}
	st, err := state.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fake := systest.NewFakeRunner()
	fake.ExpectOutput("ufw status verbose", activeDenyStatus)
	fake.Expect("systemctl is-active redis-server", systest.Response{ExitCode: 3})

	m := &Manager{
		Cfg: cfg, Log: log.Discard(), Bins: bins, State: st,
		OS:     system.OSInfo{ID: "ubuntu", Codename: "jammy", Arch: "amd64"},
		FSRoot: dir, StartWait: 50 * time.Millisecond, PollInterval: time.Millisecond,
		InstalledProbe: func() bool { return installed },
	}
	rf := &redisServerFake{confPath: m.confPath(), aclPath: m.aclFile()}
	m.Runner = &splitRunner{fake: fake, redis: rf}
	return m, fake, rf
}

func TestInstallFreshHost(t *testing.T) {
	m, fake, rf := testManager(t, false)
	res, err := m.Install(context.Background(), InstallOptions{Password: "a-password-long-enough"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.PackageInstalled || res.ServerVersion != "7.0.15" {
		t.Errorf("result = %+v", res)
	}
	// The aclfile carries the admin password; the include points redis.conf at ratline's file.
	acl, err := os.ReadFile(m.aclFile())
	if err != nil || !strings.Contains(string(acl), "user default on >a-password-long-enough") {
		t.Errorf("aclfile not written as expected: %s (%v)", acl, err)
	}
	main, _ := os.ReadFile(m.mainConf())
	if !strings.Contains(string(main), includeLine) {
		t.Errorf("include not appended to redis.conf:\n%s", main)
	}
	if !fake.Called("apt-get install -y redis-server") || !fake.Called("systemctl restart redis-server") {
		t.Errorf("expected install+restart; calls: %v", fake.Keys())
	}
	if rf.bindRemote {
		t.Error("a fresh install listens beyond localhost")
	}
	if !rf.passwordSet {
		t.Error("the server does not require a password after install")
	}
}

func TestAccessAllowOpensBindThenRevokeCloses(t *testing.T) {
	m, _, rf := testManager(t, true)
	ctx := context.Background()
	// Simulate a completed install: managed conf, admin URI stored, server requiring the password.
	if err := os.WriteFile(m.confPath(), RenderConf(false), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.aclFile(), renderACLFile("adminpass"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.Cfg.Paths.RedisURIFile, []byte("redis://:adminpass@127.0.0.1:6379\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rf.restarted() // load the aclfile → passwordSet, adminPassword

	res, err := m.AccessAllow(ctx, "203.0.113.19", "office", "test")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OpenedNetwork || !m.confState().Remote || !rf.bindRemote {
		t.Errorf("allow did not open the bind: %+v remote=%v", res, rf.bindRemote)
	}

	rr, err := m.AccessRevoke(ctx, "203.0.113.19")
	if err != nil {
		t.Fatal(err)
	}
	if !rr.ClosedNetwork || m.confState().Remote || rf.bindRemote {
		t.Errorf("revoke did not close the bind: %+v remote=%v", rr, rf.bindRemote)
	}
}
