package redis

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

func assertArgvHoldsNoPassword(t *testing.T, c system.Cmd, password string) {
	t.Helper()
	for i, a := range c.Args {
		if strings.Contains(a, password) || a == "-a" || a == "--pass" || strings.Contains(a, "redis://") {
			t.Errorf("argv[%d] = %q exposes the admin password to every account on the server", i, a)
		}
	}
}

func TestShellCommandPutsThePasswordInTheEnvironment(t *testing.T) {
	m, _ := testManager(t, "")
	c, server, err := m.ShellCommand(ShellOptions{Term: "screen"})
	if err != nil {
		t.Fatalf("ShellCommand = %v", err)
	}
	assertArgvHoldsNoPassword(t, c, "adminpass")
	want := []string{"-h", "127.0.0.1", "-p", "6379"}
	if strings.Join(c.Args, " ") != strings.Join(want, " ") {
		t.Errorf("argv = %q, want %q", c.Args, want)
	}
	var auth bool
	for _, e := range c.Env {
		auth = auth || e == "REDISCLI_AUTH=adminpass"
	}
	if !auth {
		t.Errorf("REDISCLI_AUTH is not in the environment: %q", c.Env)
	}
	if !c.Attach || !c.Mutates || c.Stdin != nil {
		t.Errorf("an interactive session must own the terminal and be skipped by --dry-run: %+v", c)
	}
	if server != "127.0.0.1:6379" {
		t.Errorf("server = %q", server)
	}
}

func TestShellCommandNamesAnACLUserAndTLS(t *testing.T) {
	m, _ := testManager(t, "")
	if err := os.WriteFile(m.Cfg.Paths.RedisURIFile, []byte("rediss://boss:b0ssPw@cache.internal:6380\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, _, err := m.ShellCommand(ShellOptions{})
	if err != nil {
		t.Fatalf("ShellCommand = %v", err)
	}
	assertArgvHoldsNoPassword(t, c, "b0ssPw")
	want := "-h cache.internal -p 6380 --user boss --tls"
	if strings.Join(c.Args, " ") != want {
		t.Errorf("argv = %q, want %q", strings.Join(c.Args, " "), want)
	}
}

func TestShellCommandSendsAOneOffCommandOnStdin(t *testing.T) {
	// ACL SETUSER … >password is a command an admin runs; as arguments it would sit in
	// the process table for as long as redis-cli does.
	m, _ := testManager(t, "")
	c, _, err := m.ShellCommand(ShellOptions{Command: []string{"ACL", "SETUSER", "app", ">n3w pass\"x"}})
	if err != nil {
		t.Fatalf("ShellCommand = %v", err)
	}
	assertArgvHoldsNoPassword(t, c, "n3w")
	if c.Stdin == nil {
		t.Fatal("the command went nowhere")
	}
	b, _ := io.ReadAll(c.Stdin)
	if got, want := string(b), `"ACL" "SETUSER" "app" ">n3w pass\"x"`+"\n"; got != want {
		t.Errorf("stdin = %q, want %q", got, want)
	}
}

func TestQuoteCommandKeepsEachArgumentWhole(t *testing.T) {
	got := QuoteCommand([]string{"SET", "a b", "line1\nline2", `back\slash`, "-5"})
	want := `"SET" "a b" "line1\nline2" "back\\slash" "-5"`
	if got != want {
		t.Errorf("QuoteCommand = %s\nwant           %s", got, want)
	}
}
