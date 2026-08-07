package mongo

import (
	"os"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
)

// The connection string must never reach argv.
//
// /proc/PID/cmdline is world-readable, so an admin URI passed as --uri is the password for
// every database on the server, visible to every account on it for as long as the dump
// runs. This is the invariant the whole --config dance exists for, so it is checked on the
// argv the command would actually build rather than trusted.
func TestTheAdminURINeverReachesTheArgumentList(t *testing.T) {
	const secret = "sup3r-s3cret"
	const uri = "mongodb://admin:" + secret + "@127.0.0.1:27017/?authSource=admin"

	m, uriPath := testManager(t)
	if err := os.WriteFile(uriPath, []byte(uri), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := systest.NewFakeRunner()
	m.Runner = fake
	m.Bins = systest.Binaries()

	out := t.TempDir()
	if _, err := m.Dump(t.Context(), "app_example_com", out); err != nil {
		// mongodump is faked, so a failure here is about the argv, not the dump.
		t.Logf("dump returned %v (the fake writes no archive)", err)
	}

	calls := fake.Calls()
	if len(calls) == 0 {
		t.Fatal("no command was run at all")
	}
	for _, c := range calls {
		for _, a := range c.Args {
			if strings.Contains(a, secret) {
				t.Errorf("the admin password is in argv for %s: %q\n"+
					"/proc/PID/cmdline is world-readable, so this is the password for "+
					"every database on the server, visible to every account on it", c.Name, a)
			}
			if strings.HasPrefix(a, "--uri") {
				t.Errorf("%s was given --uri, which puts the connection string in argv: %q",
					c.Name, a)
			}
		}
		// It has to get the URI somehow, and --config is the way that keeps it off argv.
		if c.Name == "mongodump" {
			if !hasFlag(c.Args, "--config") {
				t.Errorf("mongodump was not given --config, so it has no URI at all: %v", c.Args)
			}
			if !hasFlag(c.Args, "--db") {
				t.Errorf("mongodump was not scoped to one database: %v", c.Args)
			}
		}
	}
}

func hasFlag(args []string, want string) bool {
	for _, a := range args {
		if a == want || strings.HasPrefix(a, want+"=") {
			return true
		}
	}
	return false
}

// The staged config is what carries the URI instead, so it has to be unreadable by anyone
// else and has to survive a password containing YAML syntax.
func TestTheStagedConfigIsUnreadableAndQuoted(t *testing.T) {
	m, _ := testManager(t)
	path, cleanup, err := m.stageToolConfig("mongodb://admin:has:colons@h/")
	if err != nil {
		t.Fatalf("staging: %v", err)
	}
	defer cleanup()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("the staged config is %04o; it holds an admin password", perm)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Quoted, or YAML reads the first colon as the end of the value and mongodump gets a
	// truncated URI — which fails in a way that looks like a network problem.
	if !strings.Contains(string(body), `"mongodb://admin:has:colons@h/"`) {
		t.Errorf("the URI is not quoted, so a colon in the password truncates it:\n%s", body)
	}
}

// The staged file must not outlive the command.
func TestTheStagedConfigIsRemovedAfterwards(t *testing.T) {
	m, _ := testManager(t)
	path, cleanup, err := m.stageToolConfig("mongodb://a:b@h/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the staged config is not there to begin with: %v", err)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the staged config survived the command, still holding an admin URI: %v", err)
	}
}

// An archive's filename records which database it came from, which is what lets `db
// restore` put it back without being told.
func TestTheDatabaseIsReadBackFromTheArchiveName(t *testing.T) {
	for name, want := range map[string]string{
		"app_example_com-20260807T120000Z.archive.gz":                  "app_example_com",
		"/var/backups/ratline/databases/x-20260101T000000Z.archive.gz": "x",
		"a_b_c-20261231T235959Z.archive.gz":                            "a_b_c",
		// Not one of ours: no timestamp, so there is nothing to be confident about and
		// `db restore` asks for --into rather than guessing a target to overwrite.
		"someone-elses-dump.archive.gz": "",
		"backup.gz":                     "",
		"":                              "",
	} {
		if got := ArchiveDatabase(name); got != want {
			t.Errorf("ArchiveDatabase(%q) = %q, want %q", name, got, want)
		}
	}
}
