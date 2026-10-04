package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
)

// `db shell` is root-only, so the harness stops at the privilege check. These go at
// g.dbShell, which is everything after cobra — the same approach as `site exec`'s tests,
// for the same reason: a test that stops at "must run as root" proves nothing.

const (
	shellMongoPW = "M0ngoAdminPw"
	shellMySQLPW = "MySQLAdminPw"
	shellRedisPW = "RedisAdminPw"
)

// shellGlobals builds a Globals with credentials for all three engines on disk, one
// recorded database per engine, and a runner that records what it was asked to start.
func shellGlobals(t *testing.T, dryRun, tty bool) (*Globals, *[]system.Cmd, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Features.DBProvisioning = true
	cfg.Paths.StateDB = filepath.Join(dir, "state.db")
	cfg.Paths.RunDir = filepath.Join(dir, "run")
	cfg.Paths.MongoURIFile = filepath.Join(dir, "mongo.uri")
	cfg.Paths.MySQLDefaultsFile = filepath.Join(dir, "mysql.cnf")
	cfg.Paths.RedisURIFile = filepath.Join(dir, "redis.uri")
	for path, body := range map[string]string{
		cfg.Paths.MongoURIFile:      "mongodb://admin:" + shellMongoPW + "@127.0.0.1:27017/?authSource=admin\n",
		cfg.Paths.MySQLDefaultsFile: "[client]\nuser=\"root\"\npassword=\"" + shellMySQLPW + "\"\nhost=127.0.0.1\nport=3306\n",
		cfg.Paths.RedisURIFile:      "redis://:" + shellRedisPW + "@127.0.0.1:6379\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var started []system.Cmd
	fake := systest.NewFakeRunner()
	fake.Hook = func(c system.Cmd) (*system.Result, error) {
		started = append(started, c)
		return &system.Result{}, nil
	}
	logs := &bytes.Buffer{}
	g := &Globals{
		Cfg: cfg, Log: log.New(log.Options{Out: logs, Level: log.LevelDebug}), Runner: fake,
		DryRun: dryRun, StdinTTY: tty,
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Stdin: strings.NewReader(""),
		CmdPath: "ratline db shell",
	}
	ctx := context.Background()
	st, err := g.Store(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.PutUser(ctx, &state.User{Name: "acme", UID: 2000, GID: 2000, Home: "/home/acme", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutDatabase(ctx, &state.Database{Name: "shop", Owner: "acme"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutEngineDatabase(ctx, &state.EngineDatabase{Engine: engineMySQL, Name: "shop", Owner: "acme"}); err != nil {
		t.Fatal(err)
	}
	return g, &started, logs
}

func assertNoSecretInArgv(t *testing.T, c system.Cmd) {
	t.Helper()
	for i, a := range c.Args {
		for _, pw := range []string{shellMongoPW, shellMySQLPW, shellRedisPW} {
			if strings.Contains(a, pw) {
				t.Errorf("%s argv[%d] = %q carries an admin password, readable by every account in /proc",
					c.Name, i, a)
			}
		}
	}
}

func envHas(env []string, entry string) bool {
	for _, e := range env {
		if e == entry {
			return true
		}
	}
	return false
}

func TestDBShellStartsEachEngineWithNoSecretInArgv(t *testing.T) {
	cases := []struct {
		req     dbShellRequest
		name    string
		secrets string // the env entry the secret must be in instead
	}{
		{dbShellRequest{Engine: engineMongo, Database: "shop"}, "mongosh",
			"RATLINE_MONGO_URI=mongodb://admin:" + shellMongoPW + "@127.0.0.1:27017/?authSource=admin"},
		{dbShellRequest{Engine: engineMySQL, Database: "shop"}, "mysql", ""},
		{dbShellRequest{Engine: engineRedis}, "redis-cli", "REDISCLI_AUTH=" + shellRedisPW},
	}
	for _, tc := range cases {
		t.Run(tc.req.Engine, func(t *testing.T) {
			g, started, _ := shellGlobals(t, false, true)
			if err := g.dbShell(context.Background(), tc.req); err != nil {
				t.Fatalf("dbShell = %v", err)
			}
			if len(*started) != 1 {
				t.Fatalf("started %d commands, want 1", len(*started))
			}
			c := (*started)[0]
			if c.Name != tc.name || !c.Attach {
				t.Errorf("started %s attach=%v, want %s attached to the terminal", c.Name, c.Attach, tc.name)
			}
			assertNoSecretInArgv(t, c)
			if tc.secrets != "" && !envHas(c.Env, tc.secrets) {
				t.Errorf("the credential is not in the environment: %q", c.Env)
			}
			if tc.req.Engine == engineMySQL && c.Args[0] != "--defaults-extra-file="+g.Cfg.Paths.MySQLDefaultsFile {
				t.Errorf("mysql argv = %q; the stored 0600 defaults-file must come first", c.Args)
			}
			// The session gets the terminal, not a capture.
			if c.Stdin != g.Stdin || c.Stdout != g.Stdout || c.Stderr != g.Stderr {
				t.Error("the session was not given ratline's own streams")
			}
		})
	}
}

func TestDBShellDryRunStartsNothingAndNamesTheEnvironmentWithoutValues(t *testing.T) {
	for _, engine := range []string{engineMongo, engineMySQL, engineRedis} {
		t.Run(engine, func(t *testing.T) {
			g, started, logs := shellGlobals(t, true, true)
			req := dbShellRequest{Engine: engine}
			if engine != engineRedis {
				req.Database = "shop"
			}
			if err := g.dbShell(context.Background(), req); err != nil {
				t.Fatalf("dbShell --dry-run = %v", err)
			}
			if len(*started) != 0 {
				t.Errorf("--dry-run started %d command(s)", len(*started))
			}
			out := logs.String()
			if !strings.Contains(out, "would open a database shell") {
				t.Errorf("the dry run said nothing about what it would do:\n%s", out)
			}
			for _, pw := range []string{shellMongoPW, shellMySQLPW, shellRedisPW} {
				if strings.Contains(out, pw) {
					t.Errorf("the dry run printed an admin password:\n%s", out)
				}
			}
			if engine == engineMongo && !strings.Contains(out, "RATLINE_MONGO_URI") {
				t.Errorf("the dry run did not name the variable carrying the URI:\n%s", out)
			}
			if engine == engineRedis && !strings.Contains(out, "REDISCLI_AUTH") {
				t.Errorf("the dry run did not name the variable carrying the password:\n%s", out)
			}
		})
	}
}

func TestDBShellRefusesADatabaseRatlineDidNotMake(t *testing.T) {
	for _, engine := range []string{engineMongo, engineMySQL} {
		t.Run(engine, func(t *testing.T) {
			g, started, _ := shellGlobals(t, false, true)
			err := g.dbShell(context.Background(), dbShellRequest{Engine: engine, Database: "shoop"})
			if err == nil {
				t.Fatal("a database with no record was opened; a typo should not become an empty database")
			}
			if !strings.Contains(rlerr.Hint(err), "--unmanaged") {
				t.Errorf("the refusal does not say how to open it anyway: %v / %s", err, rlerr.Hint(err))
			}
			if len(*started) != 0 {
				t.Error("something was started before the refusal")
			}
			// And --unmanaged is the way through.
			if err := g.dbShell(context.Background(),
				dbShellRequest{Engine: engine, Database: "shoop", Unmanaged: true}); err != nil {
				t.Errorf("--unmanaged was refused too: %v", err)
			}
			if len(*started) != 1 {
				t.Errorf("--unmanaged started %d commands, want 1", len(*started))
			}
		})
	}
}

func TestDBShellNeedsATerminalUnlessGivenSomethingToRun(t *testing.T) {
	g, started, _ := shellGlobals(t, false, false)
	err := g.dbShell(context.Background(), dbShellRequest{Engine: engineMongo})
	if err == nil || rlerr.CodeOf(err) != rlerr.CodeUsage {
		t.Fatalf("an interactive session without a terminal = %v, want a usage refusal", err)
	}
	if !strings.Contains(rlerr.Hint(err), "--eval") {
		t.Errorf("the hint does not offer --eval: %s", rlerr.Hint(err))
	}
	// Each one-off form runs without a terminal.
	for _, req := range []dbShellRequest{
		{Engine: engineMongo, Eval: "db.version()"},
		{Engine: engineMySQL, Eval: "SELECT 1"},
		{Engine: engineRedis, Command: []string{"PING"}},
	} {
		if err := g.dbShell(context.Background(), req); err != nil {
			t.Errorf("%s one-off without a terminal = %v", req.Engine, err)
		}
	}
	if len(*started) != 3 {
		t.Fatalf("started %d, want 3", len(*started))
	}
	// The one-off mysql and redis forms carry their input on stdin, not the terminal's.
	for _, c := range (*started)[1:] {
		if c.Stdin == nil || c.Stdin == g.Stdin {
			t.Errorf("%s: the one-off command was not put on stdin", c.Name)
		}
		assertNoSecretInArgv(t, c)
	}
}

func TestDBShellRefusesConnectionOverrides(t *testing.T) {
	// Whatever shape it arrives in, nothing an operator types may point the session at
	// another server or account: that would be ratline's admin password, handed to an
	// address of somebody else's choosing.
	refused := []dbShellRequest{
		{Engine: engineMongo, Database: "--host=203.0.113.9"},
		{Engine: engineMongo, Database: "--uri=mongodb://evil"},
		{Engine: engineMongo, Database: "-u"},
		{Engine: engineMySQL, Database: "--defaults-file=/tmp/mine.cnf"},
		{Engine: engineMySQL, Database: "--socket=/tmp/x.sock"},
		{Engine: engineMySQL, Database: "-hevil"},
		{Engine: engineMySQL, Database: "--password"},
		{Engine: engineRedis, Command: []string{"-h", "203.0.113.9"}},
		{Engine: engineRedis, Command: []string{"-a", "guess"}},
		{Engine: engineRedis, Command: []string{"--user", "default"}},
		{Engine: engineRedis, Command: []string{"--tls"}},
		{Engine: engineMongo, Command: []string{"-h", "evil"}},
	}
	for _, req := range refused {
		g, started, _ := shellGlobals(t, false, true)
		err := g.dbShell(context.Background(), req)
		if err == nil || rlerr.CodeOf(err) != rlerr.CodeUsage {
			t.Errorf("%s %+v = %v, want a usage refusal", req.Engine, req, err)
		}
		if len(*started) != 0 {
			t.Errorf("%+v started a client", req)
		}
	}

	// A Redis value that begins with a dash is a value, not an option.
	g, started, _ := shellGlobals(t, false, true)
	if err := g.dbShell(context.Background(),
		dbShellRequest{Engine: engineRedis, Command: []string{"INCRBY", "shop:n", "-5"}}); err != nil {
		t.Errorf("INCRBY with a negative step was refused: %v", err)
	}
	if len(*started) != 1 {
		t.Error("the redis command did not run")
	}
}

func TestDBShellRefusesTheClientsConnectionFlagsAtTheParser(t *testing.T) {
	// The same property one layer up: ratline has no such flags, so cobra refuses them
	// before root or the configuration are consulted.
	for _, args := range [][]string{
		{"db", "shell", "--host", "evil"},
		{"db", "shell", "--uri", "mongodb://evil"},
		{"db", "shell", "--port=1"},
		{"db", "shell", "-u", "root", "--engine", "mysql"},
		{"db", "shell", "-p", "--engine", "mysql"},
		{"db", "shell", "--defaults-file=/tmp/x", "--engine", "mysql"},
		{"db", "shell", "--socket", "/tmp/s", "--engine", "mysql"},
		{"db", "shell", "-a", "pw", "--engine", "redis"},
		{"db", "shell", "--user", "default", "--engine", "redis"},
	} {
		code, _, _ := harness(t, args...)
		if code != int(rlerr.CodeUsage) {
			t.Errorf("%q exited %d, want %d (usage)", args, code, rlerr.CodeUsage)
		}
	}
}

func TestDBShellRefusesContradictions(t *testing.T) {
	for _, tc := range []struct {
		req  dbShellRequest
		json bool
	}{
		{req: dbShellRequest{Engine: engineRedis, Database: "shop"}},
		{req: dbShellRequest{Engine: engineRedis, Eval: "PING"}},
		{req: dbShellRequest{Engine: engineRedis, Unmanaged: true}},
		{req: dbShellRequest{Engine: engineMongo, Command: []string{"PING"}}},
		{req: dbShellRequest{Engine: engineMySQL, Unmanaged: true}},
		{req: dbShellRequest{Engine: engineMongo}, json: true},
	} {
		if err := tc.req.validate(true, tc.json); err == nil || rlerr.CodeOf(err) != rlerr.CodeUsage {
			t.Errorf("%+v json=%v = %v, want a usage refusal", tc.req, tc.json, err)
		}
	}
}

func TestDBShellDoesNotTakeTheLock(t *testing.T) {
	// A prompt left open must not stop certificate renewals and every deploy. It is still
	// a mutation — the session can change anything — so --dry-run and the panel's
	// fail-safe treat it as one.
	g := NewGlobals()
	root := NewRootCommand(g)
	cmd, _, err := root.Find([]string{"db", "shell"})
	if err != nil || cmd.Name() != "shell" {
		t.Fatalf("db shell is not registered: %v", err)
	}
	if !annotated(cmd, AnnoMutates) || !annotated(cmd, AnnoSkipLock) {
		t.Errorf("annotations = %v; want mutating and skip-lock", cmd.Annotations)
	}
	if annotated(cmd, AnnoAllowNonRoot) {
		t.Error("db shell holds the admin credentials and must be root-only")
	}
}

func TestShellTermPassesOnlyAPlainTerminalName(t *testing.T) {
	for in, want := range map[string]string{
		"xterm-256color": "xterm-256color", "screen.xterm": "screen.xterm", "": "",
		"xterm\nEVIL=1": "", "a b": "", "$(id)": "",
	} {
		if got := shellTerm(in); got != want {
			t.Errorf("shellTerm(%q) = %q, want %q", in, got, want)
		}
	}
}
