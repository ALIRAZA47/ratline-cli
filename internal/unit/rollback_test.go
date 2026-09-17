package unit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
)

// Installing a unit pushes an undo that puts the previous one back verbatim.
//
// This is the step every mutating site command depends on for its "a command that
// fails halfway leaves the server as it was" promise: `site scale` re-renders the
// unit, then applies the vhost, then restarts — and if either of the later two fail,
// the unit has to go back or the site is left running a configuration nobody asked
// for. The undo is only worth anything if the stack is actually unwound, which
// TestEveryRollbackStackInTheSitePackageIsUnwound checks on the other side.
//
// The owner deliberately does not exist on the machine running this: unit.Install
// then skips the journal-namespace setup, which writes under /etc/systemd and is not
// the thing under test here.
func TestInstallingAUnitPushesAnUndoThatRestoresThePreviousOne(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Paths.SystemdDir = dir
	runner := systest.NewFakeRunner()
	m := &Manager{Cfg: cfg, Log: log.Discard(), Runner: runner}

	site := &state.Site{
		Domain: "app.example.com", Owner: "nobody-ratline-test", Runtime: "node",
		Slug: "nobody-ratline-test-app_example_com", Enabled: true,
		Entry: "server.js", Listen: "socket", Instances: 1, MemoryMax: "256M",
	}
	path := cfg.UnitPath(site.Owner, site.Domain)

	before := []byte(system.ManagedHeader + "\n[Service]\nMemoryMax=256M\n")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}

	rb := system.NewRollback(log.Discard())
	after := []byte(system.ManagedHeader + "\n[Service]\nMemoryMax=1G\n")
	if err := m.Install(context.Background(), site, after, rb); err != nil {
		t.Fatalf("Install = %v", err)
	}

	// Committed to the disk first, or the undo would have nothing to reverse.
	if got, _ := os.ReadFile(path); string(got) != string(after) {
		t.Fatalf("the new unit was not written; got:\n%s", got)
	}

	rb.Unwind(context.Background())

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the unit is gone after the unwind: %v", err)
	}
	if string(got) != string(before) {
		t.Errorf("the unit was not restored.\nwant:\n%s\ngot:\n%s", before, got)
	}
}

// A unit that did not exist before is removed by the undo, not left behind as an
// empty file or a zero-length one.
func TestTheUndoRemovesAUnitThatWasNewlyCreated(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Paths.SystemdDir = dir
	m := &Manager{Cfg: cfg, Log: log.Discard(), Runner: systest.NewFakeRunner()}

	site := &state.Site{
		Domain: "new.example.com", Owner: "nobody-ratline-test", Runtime: "node",
		Slug: "nobody-ratline-test-new_example_com", Enabled: true,
		Entry: "server.js", Listen: "socket", Instances: 1,
	}
	path := cfg.UnitPath(site.Owner, site.Domain)

	rb := system.NewRollback(log.Discard())
	body := []byte(system.ManagedHeader + "\n[Service]\n")
	if err := m.Install(context.Background(), site, body, rb); err != nil {
		t.Fatalf("Install = %v", err)
	}
	if !system.Exists(path) {
		t.Fatal("the unit was not written")
	}

	rb.Unwind(context.Background())

	if system.Exists(path) {
		leftover, _ := os.ReadFile(path)
		t.Errorf("%s survived the unwind with:\n%s", filepath.Base(path), leftover)
	}
}

// A committed stack does nothing when it is later unwound, or a successful command
// would undo itself.
func TestACommittedStackDoesNotRestoreAnything(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Paths.SystemdDir = dir
	m := &Manager{Cfg: cfg, Log: log.Discard(), Runner: systest.NewFakeRunner()}

	site := &state.Site{
		Domain: "app.example.com", Owner: "nobody-ratline-test", Runtime: "node",
		Slug: "nobody-ratline-test-app_example_com", Enabled: true,
		Entry: "server.js", Listen: "socket", Instances: 1,
	}
	path := cfg.UnitPath(site.Owner, site.Domain)
	if err := os.WriteFile(path, []byte(system.ManagedHeader+"\nold\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rb := system.NewRollback(log.Discard())
	body := []byte(system.ManagedHeader + "\nnew\n")
	if err := m.Install(context.Background(), site, body, rb); err != nil {
		t.Fatalf("Install = %v", err)
	}
	rb.Commit()
	rb.Unwind(context.Background())

	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "new") {
		t.Errorf("a committed stack undid its own work; the unit reads:\n%s", got)
	}
}
