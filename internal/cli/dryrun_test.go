package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

// `--dry-run` promises to change nothing. The promise is only worth something if it
// is checked at the level it can be broken at.
//
// It was broken: `site add --dry-run` wrote a real row and allocated a real port, so
// the next real `site add` refused with "already exists with a different
// configuration" against a site that had never been created. Guarding the two call
// sites fixed that instance; this test is what stops the next one, because there are
// thirty-five write sites in the state package and no single wrapper to guard.

// dryRunFixture builds a config and a state database on disk, and returns the
// config path plus a function that hashes the database.
func dryRunFixture(t *testing.T) (configPath string, hashDB func() string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")

	// A real database with a real tenant, so the commands under test get past their
	// preconditions and reach the writes.
	st, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	if err := st.PutUser(t.Context(), &state.User{
		Name: "acme", Home: filepath.Join(dir, "home", "acme"), Shell: "/bin/sh",
	}); err != nil {
		t.Fatalf("PutUser = %v", err)
	}
	st.Close()

	configPath = filepath.Join(dir, "config.yaml")
	body := "version: 1\npaths:\n  state_db: " + dbPath +
		"\n  home_base: " + filepath.Join(dir, "home") +
		"\n  lock: " + filepath.Join(dir, "lock") +
		"\n  run_dir: " + filepath.Join(dir, "run") +
		"\n  audit_log: " + filepath.Join(dir, "audit.log") +
		"\n  nginx_sites_available: " + filepath.Join(dir, "sites-available") +
		"\n  nginx_sites_enabled: " + filepath.Join(dir, "sites-enabled") +
		"\n  nginx_snippets: " + filepath.Join(dir, "snippets") +
		"\n  nginx_custom: " + filepath.Join(dir, "custom") +
		"\n  systemd_dir: " + filepath.Join(dir, "systemd") +
		"\n  logrotate_dir: " + filepath.Join(dir, "logrotate") +
		"\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	hashDB = func() string {
		data, err := os.ReadFile(dbPath)
		if err != nil {
			t.Fatalf("reading the database: %v", err)
		}
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}
	return configPath, hashDB
}

func TestDryRunLeavesTheDatabaseUntouched(t *testing.T) {
	// Every mutating command that reaches a state write, run as a preview. The
	// database is hashed before and after: a single byte of difference is a broken
	// promise, whichever of the write sites did it.
	for _, args := range [][]string{
		{"site", "add", "app.example.com", "--user", "acme", "--runtime", "static"},
		{"site", "add", "api.example.com", "--user", "acme", "--runtime", "python",
			"--app-module", "app.main:app"},
		{"site", "add", "node.example.com", "--user", "acme", "--runtime", "node",
			"--entry", "server.js", "--listen", "port"},
		{"user", "add", "beta"},
		{"db", "install"},
		{"db", "access", "allow", "203.0.113.19"},
		{"db", "access", "revoke", "203.0.113.19"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			configPath, hashDB := dryRunFixture(t)
			before := hashDB()

			out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
			g := NewGlobals()
			g.Stdout, g.Stderr, g.Stdin = out, errOut, strings.NewReader("")
			g.Log = log.Discard()
			full := append([]string{"--config", configPath, "--dry-run", "--no-input"}, args...)
			code := Run(g, full)

			// The exit code is not the point — on a machine without root this refuses
			// before it starts, and that is fine. What matters is that whatever it did
			// reach, it wrote nothing.
			if after := hashDB(); after != before {
				t.Errorf("--dry-run changed the state database (exit %d)\nstdout: %s\nstderr: %s",
					code, out.String(), errOut.String())
			}
		})
	}
}
