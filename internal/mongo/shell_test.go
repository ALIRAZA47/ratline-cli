package mongo

import (
	"os"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

const shellTestURI = "mongodb://admin:Sh3llS3cret@127.0.0.1:27017/?authSource=admin"

func shellManager(t *testing.T) *Manager {
	t.Helper()
	m, path := testManager(t)
	if err := os.WriteFile(path, []byte(shellTestURI+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return m
}

// assertArgvHoldsNoSecret is the property `db shell` exists to keep: /proc/PID/cmdline
// is world-readable, so neither the password nor a URI carrying it may be in argv.
func assertArgvHoldsNoSecret(t *testing.T, c system.Cmd) {
	t.Helper()
	for i, a := range c.Args {
		for _, secret := range []string{"Sh3llS3cret", "mongodb://", "admin:"} {
			if strings.Contains(a, secret) {
				t.Errorf("argv[%d] = %q carries %q, which every account on the server can read", i, a, secret)
			}
		}
	}
}

func envValue(env []string, key string) (string, bool) {
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

func TestShellCommandKeepsTheAdminURIOutOfArgv(t *testing.T) {
	m := shellManager(t)
	c, server, err := m.ShellCommand(ShellOptions{Database: "shop", Term: "xterm-256color"})
	if err != nil {
		t.Fatalf("ShellCommand = %v", err)
	}
	assertArgvHoldsNoSecret(t, c)

	want := []string{"--nodb", "--quiet", "--shell", "--eval=" + ShellConnect}
	if strings.Join(c.Args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %q\nwant   %q", c.Args, want)
	}
	if c.Name != "mongosh" || !c.Attach || !c.Mutates {
		t.Errorf("cmd = %+v; want mongosh, attached to the terminal, and skipped by --dry-run", c)
	}
	// Where the secret is supposed to be.
	if v, _ := envValue(c.Env, "RATLINE_MONGO_URI"); v != shellTestURI {
		t.Errorf("RATLINE_MONGO_URI = %q, want the admin URI", v)
	}
	if v, _ := envValue(c.Env, "RATLINE_MONGO_DB"); v != "shop" {
		t.Errorf("RATLINE_MONGO_DB = %q, want shop", v)
	}
	if v, _ := envValue(c.Env, "TERM"); v != "xterm-256color" {
		t.Errorf("TERM = %q; a prompt cannot edit lines on a dumb terminal", v)
	}
	if strings.Contains(server, "Sh3llS3cret") {
		t.Errorf("the server shown to the operator is not redacted: %q", server)
	}
}

func TestShellCommandWithoutADatabaseSetsNone(t *testing.T) {
	m := shellManager(t)
	c, _, err := m.ShellCommand(ShellOptions{})
	if err != nil {
		t.Fatalf("ShellCommand = %v", err)
	}
	if _, ok := envValue(c.Env, "RATLINE_MONGO_DB"); ok {
		t.Error("RATLINE_MONGO_DB was set with no database asked for")
	}
	if v, _ := envValue(c.Env, "TERM"); v != "dumb" {
		t.Errorf("TERM = %q with none passed, want MinimalEnv's dumb", v)
	}
}

func TestShellEvalRunsAfterConnectingAndCannotBecomeAFlag(t *testing.T) {
	// mongosh runs every --eval before any --file, in order, so the connect has to be
	// the first --eval and the operator's the second. And each is one --eval=… element:
	// mongosh reads the two-element form's value as an option when it starts with a
	// dash, which would have made this a different server rather than some JavaScript.
	m := shellManager(t)
	c, _, err := m.ShellCommand(ShellOptions{Eval: "--host=203.0.113.9"})
	if err != nil {
		t.Fatalf("ShellCommand = %v", err)
	}
	want := []string{"--nodb", "--quiet", "--eval=" + ShellConnect, "--eval=--host=203.0.113.9"}
	if strings.Join(c.Args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %q\nwant   %q", c.Args, want)
	}
	for _, a := range c.Args {
		if a == "--shell" {
			t.Error("--eval still opened a prompt")
		}
	}
	assertArgvHoldsNoSecret(t, c)
}

func TestShellConnectIsOneStaticLine(t *testing.T) {
	// The runner refuses a newline in any argument, and nothing variable belongs here.
	if strings.ContainsAny(ShellConnect, "\n\r") {
		t.Error("ShellConnect spans lines; the runner would refuse it")
	}
	for _, want := range []string{"connect(process.env.RATLINE_MONGO_URI)", "delete process.env.RATLINE_MONGO_URI"} {
		if !strings.Contains(ShellConnect, want) {
			t.Errorf("ShellConnect does not contain %q", want)
		}
	}
	if err := system.ValidateArgv([]string{"--eval=" + ShellConnect}); err != nil {
		t.Errorf("the runner would refuse the connect line: %v", err)
	}
}

func TestShellCommandRefusesABadDatabaseBeforeReadingTheURI(t *testing.T) {
	m, _ := testManager(t) // no URI file: reaching it would be a different error
	for _, name := range []string{"--host=evil", "a/b", "shop.orders", "a b"} {
		_, _, err := m.ShellCommand(ShellOptions{Database: name})
		if err == nil {
			t.Errorf("database %q was accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "connection string") {
			t.Errorf("database %q reached the URI before being validated: %v", name, err)
		}
	}
}
