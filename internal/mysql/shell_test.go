package mysql

import (
	"io"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// assertArgvHoldsNoPassword: /proc/PID/cmdline is world-readable, so the admin password
// may reach the client only through the 0600 defaults-file.
func assertArgvHoldsNoPassword(t *testing.T, c system.Cmd) {
	t.Helper()
	for i, a := range c.Args {
		if strings.Contains(a, "adminpw") || strings.Contains(a, "--password") || strings.HasPrefix(a, "-p") {
			t.Errorf("argv[%d] = %q puts the admin password where every account can read it", i, a)
		}
	}
	for _, e := range c.Env {
		if strings.Contains(e, "adminpw") || strings.HasPrefix(e, "MYSQL_PWD=") {
			t.Errorf("env %q carries the password; the defaults-file is where it belongs", e)
		}
	}
}

func TestShellCommandReadsTheStoredDefaultsFile(t *testing.T) {
	m, _ := testManager(t, "")
	c, server, err := m.ShellCommand(ShellOptions{Database: "shop", Term: "xterm"})
	if err != nil {
		t.Fatalf("ShellCommand = %v", err)
	}
	assertArgvHoldsNoPassword(t, c)
	want := []string{"--defaults-extra-file=" + m.Cfg.Paths.MySQLDefaultsFile, "--database=shop"}
	if strings.Join(c.Args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %q\nwant   %q", c.Args, want)
	}
	if !c.Attach || !c.Mutates || c.Stdin != nil {
		t.Errorf("an interactive session must own the terminal and be skipped by --dry-run: %+v", c)
	}
	if server != "127.0.0.1:3306" {
		t.Errorf("server = %q", server)
	}
}

func TestShellExecuteTravelsOnStdinNotAsDashE(t *testing.T) {
	// An operator's SQL is as likely as ratline's own to carry an IDENTIFIED BY.
	m, _ := testManager(t, "")
	sql := "ALTER USER 'app'@'%' IDENTIFIED BY 'n3wSecret'"
	c, _, err := m.ShellCommand(ShellOptions{Execute: sql})
	if err != nil {
		t.Fatalf("ShellCommand = %v", err)
	}
	for _, a := range c.Args {
		if strings.Contains(a, "n3wSecret") || a == "-e" || strings.HasPrefix(a, "--execute") {
			t.Errorf("the SQL reached argv: %q", c.Args)
		}
	}
	if c.Args[0] != "--defaults-extra-file="+m.Cfg.Paths.MySQLDefaultsFile {
		t.Errorf("--defaults-extra-file must come first or the client ignores it: %q", c.Args)
	}
	if c.Stdin == nil {
		t.Fatal("the SQL went nowhere")
	}
	b, _ := io.ReadAll(c.Stdin)
	if strings.TrimSpace(string(b)) != sql {
		t.Errorf("stdin = %q, want the SQL", b)
	}
	var table bool
	for _, a := range c.Args {
		table = table || a == "--table"
	}
	if !table {
		t.Error("batch output without --table is tab-separated, meant for programs, not a person")
	}
}

func TestShellCommandRefusesABadDatabaseFirst(t *testing.T) {
	m, _ := testManager(t, "")
	for _, name := range []string{"--host=evil", "shop;drop", "a b", "../x"} {
		if _, _, err := m.ShellCommand(ShellOptions{Database: name}); err == nil {
			t.Errorf("database %q was accepted", name)
		}
	}
}
