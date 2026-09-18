package sshkey

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

func dryRunManager(t *testing.T, dry bool) (*Manager, *config.Config) {
	t.Helper()
	store, err := state.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory() = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.Default()
	// The base exists; the tenant's home under it does not — which is exactly the
	// state `user add --dry-run` leaves, because useradd was only ever printed.
	cfg.Paths.HomeBase = t.TempDir()

	return &Manager{Cfg: cfg, Log: log.Discard(), State: store, DryRun: dry}, cfg
}

// `ratline user add <name> --ssh-key <key> --dry-run` failed with
//
//	error: cannot create /home/vesta/.ssh: opening /home/vesta: no such file or directory
//	hint: its parent directory /home/vesta does not exist yet
//
// because the key write was dry-run guarded but the EnsureDir above it was not.
// A rehearsal reported a failure for something perfectly buildable, and would
// have created the directory for real had the parent existed.
func TestRenderForUserDryRunNeitherFailsNorCreatesTheHome(t *testing.T) {
	m, cfg := dryRunManager(t, true)

	if err := m.renderForUser(context.Background(), "vesta", nil); err != nil {
		t.Fatalf("renderForUser under --dry-run = %v, want nil: a rehearsal must not fail on\n"+
			"the absence of a home that the same rehearsal declined to create", err)
	}

	home := cfg.HomeDir("vesta")
	if _, err := os.Stat(home); err == nil {
		t.Errorf("the rehearsal created %s; --dry-run must write nothing at any layer", home)
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh")); err == nil {
		t.Errorf("the rehearsal created %s/.ssh", home)
	}
}

// The real run still does the work, which is what makes the skip above safe.
//
// It runs as whoever is running the tests, because the real path resolves the
// owner through the system's passwd database — a made-up name fails there long
// before it reaches the directory, which is correct and is why the dry-run case
// above is the only one that can use one.
func TestRenderForUserRealRunWritesTheKeys(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skipf("no current user to run as: %v", err)
	}
	m, cfg := dryRunManager(t, false)
	home := cfg.HomeDir(me.Username)
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := m.renderForUser(context.Background(), me.Username, nil); err != nil {
		t.Fatalf("renderForUser() = %v, want nil", err)
	}

	authorized := filepath.Join(home, ".ssh", "authorized_keys")
	if _, err := os.Stat(authorized); err != nil {
		t.Errorf("the real run did not write %s: %v", authorized, err)
	}
}

// syncRevoked had the same unguarded EnsureDir. It survived only because
// /etc/ratline/ssh exists on an initialised server; pointed at a path that does
// not, the rehearsal would have created it.
func TestSyncRevokedDryRunDoesNotCreateItsDirectory(t *testing.T) {
	m, _ := dryRunManager(t, true)
	dir := filepath.Join(t.TempDir(), "absent")
	m.Cfg.SSH.RevokedKeys = filepath.Join(dir, "revoked_keys")

	if err := m.syncRevoked(context.Background()); err != nil {
		t.Fatalf("syncRevoked under --dry-run = %v, want nil", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("the rehearsal created %s", dir)
	}
}
