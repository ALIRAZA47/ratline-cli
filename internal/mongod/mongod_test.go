package mongod

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

// mongoServerFake stands in for the mongod the flows talk to through mongosh. It is a
// tiny model of the real relationships, not a script of expected calls: restarting the
// service applies whatever the staged config says, an authenticated connection works
// only if the credentials exist, and getCmdLineOpts reports what the running process
// enforces. That keeps the verification assertions honest — a flow that forgets to
// write the config before restarting fails these tests the same way it would fail on a
// server.
type mongoServerFake struct {
	mu sync.Mutex

	// authEnforced and bindRemote are what the running process does, flipped by
	// "systemctl restart" according to the config file — exactly the file-vs-process
	// gap the flows have to bridge and verify.
	authEnforced bool
	bindRemote   bool
	// credentialsWork is whether an authenticated connection opens: the admin user
	// exists and the password offered is its password.
	credentialsWork bool
	// brokenConfApply simulates something else deciding the server's configuration —
	// a restart that does not pick the file up.
	brokenConfApply bool
	// userExists makes createAdminUser report the user is already there.
	userExists bool

	confPath string
	ops      []string
}

func (f *mongoServerFake) restarted() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.brokenConfApply {
		return
	}
	body, err := os.ReadFile(f.confPath)
	f.authEnforced = err == nil && strings.Contains(string(body), "authorization: enabled")
	f.bindRemote = err == nil && strings.Contains(string(body), "bindIpAll: true")
}

// listening is what `ss -Hltn` would print for this server's socket.
func (f *mongoServerFake) listening() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	addr := "127.0.0.1"
	if f.bindRemote {
		addr = "0.0.0.0"
	}
	return "LISTEN 0      4096   " + addr + ":27017    0.0.0.0:*\n" +
		"LISTEN 0      511    0.0.0.0:80       0.0.0.0:*\n"
}

func (f *mongoServerFake) run(c system.Cmd) (*system.Result, error) {
	env := map[string]string{}
	for _, kv := range c.Env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	op := env["RATLINE_MONGO_OP"]
	creds := strings.Contains(env["RATLINE_MONGO_URI"], "@")

	f.mu.Lock()
	defer f.mu.Unlock()
	mode := "plain"
	if creds {
		mode = "creds"
	}
	f.ops = append(f.ops, op+":"+mode)

	failure := func(msg string) (*system.Result, error) {
		res := &system.Result{ExitCode: 1, Stdout: fmt.Sprintf(`{"ok":false,"error":%q}`, msg)}
		return res, fmt.Errorf("mongosh exited 1")
	}
	if creds && !f.credentialsWork {
		return failure("Authentication failed.")
	}
	switch op {
	case "ping":
		return &system.Result{Stdout: fmt.Sprintf(
			`{"ok":true,"version":"8.0.12","topology":"standalone","auth_enabled":%v}`, f.authEnforced)}, nil
	case "createAdminUser":
		if f.userExists {
			return failure("User \"admin@admin\" already exists")
		}
		f.userExists, f.credentialsWork = true, true
		return &system.Result{Stdout: `{"ok":true,"username":"admin","auth_db":"admin","role":"root"}`}, nil
	case "dropUser":
		f.userExists, f.credentialsWork = false, false
		return &system.Result{Stdout: `{"ok":true,"dropped":"admin"}`}, nil
	}
	return failure("unknown operation: " + op)
}

func (f *mongoServerFake) opList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ops...)
}

// splitRunner sends mongosh to the server fake and everything else to the scripted
// FakeRunner, flipping the fake's enforcement state on a service restart.
type splitRunner struct {
	fake  *systest.FakeRunner
	mongo *mongoServerFake
}

func (r *splitRunner) Run(ctx context.Context, c system.Cmd) (*system.Result, error) {
	if c.Name == "mongosh" {
		return r.mongo.run(c)
	}
	if c.Name == "ss" {
		return &system.Result{Stdout: r.mongo.listening()}, nil
	}
	res, err := r.fake.Run(ctx, c)
	if c.Name == "systemctl" && len(c.Args) > 0 && c.Args[0] == "restart" && err == nil {
		r.mongo.restarted()
	}
	return res, err
}

const activeDenyStatus = "Status: active\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n"

func testManager(t *testing.T, installed bool) (*Manager, *systest.FakeRunner, *mongoServerFake) {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"etc/apt/sources.list.d", "usr/share/keyrings", "run"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Paths.RunDir = filepath.Join(dir, "run")
	cfg.Paths.MongoURIFile = filepath.Join(dir, "etc", "mongodb.uri")

	bins := system.NewBinaries()
	for _, b := range []string{"apt-get", "systemctl", "mongosh", "mongod", "ufw", "ss"} {
		bins.Set(b, "/usr/bin/"+b)
	}

	st, err := state.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fake := systest.NewFakeRunner()
	fake.ExpectOutput("ufw status verbose", activeDenyStatus)
	// A stopped unit answers is-active with exit 3.
	fake.Expect("systemctl is-active mongod", systest.Response{ExitCode: 3})

	mf := &mongoServerFake{confPath: filepath.Join(dir, ConfPath)}
	m := &Manager{
		Cfg: cfg, Log: log.Discard(), Bins: bins, State: st,
		OS:     system.OSInfo{ID: "ubuntu", Codename: "jammy", Arch: "amd64"},
		Runner: &splitRunner{fake: fake, mongo: mf},
		FSRoot: dir,
		// Fast enough that a deliberate timeout failure does not slow the suite.
		StartWait: 50 * time.Millisecond, PollInterval: time.Millisecond,
		InstalledProbe: func() bool { return installed },
	}
	return m, fake, mf
}

// writeManagedConf puts a ratline-rendered config on disk, the state every access test
// starts from.
func writeManagedConf(t *testing.T, m *Manager, remote bool) {
	t.Helper()
	body, err := RenderConf(remote)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.confPath(), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeAdminURIFile stores an attached admin connection string the way `db connect`
// would, so flows that read it back find one.
func writeAdminURIFile(t *testing.T, m *Manager) {
	t.Helper()
	if err := os.WriteFile(m.Cfg.Paths.MongoURIFile,
		[]byte("mongodb://admin:pw@127.0.0.1:27017/admin?authSource=admin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
