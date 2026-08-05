package site

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
)

// A restore extracts an archive as root. Where that archive came from is not knowable —
// it may have been copied between servers, kept on a share, or handed over by whoever is
// migrating in. So the contents are treated as input, and the checks that matter are the
// refusals.

func restoreManager(t *testing.T) (*Manager, *systest.FakeRunner) {
	t.Helper()
	runner := systest.NewFakeRunner()
	cfg := config.Default()
	cfg.Paths.HomeBase = t.TempDir()
	return &Manager{Cfg: cfg, Log: log.Discard(), Runner: runner}, runner
}

func TestAnArchiveThatWouldWriteOutsideItsDirectoryIsRefused(t *testing.T) {
	// tar happily extracts "../../etc/nginx/sites-enabled/evil.conf" if asked, and this
	// runs as root. Rejected rather than sanitised: there is no legitimate reason for one
	// of ratline's own backups to contain a traversal or an absolute path, so a sanitiser
	// would only be a way to keep accepting a hostile archive.
	for _, tc := range []struct {
		name, listing, wantErr string
	}{
		{
			"a traversing member",
			"example.com/\nexample.com/../../etc/nginx/evil.conf\n",
			"traversing",
		},
		{
			"a traversal at the root",
			"../outside/\n../outside/file\n",
			"traversing",
		},
		{
			"an absolute member",
			"example.com/\n/etc/shadow\n",
			"absolute",
		},
		{
			"two top-level directories, so which one is the site is a guess",
			"example.com/\nother.example.com/\n",
			"top-level",
		},
		{
			"nothing at all",
			"",
			"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr, runner := restoreManager(t)
			runner.ExpectOutput("tar --list --file /backups/a.tar.gz", tc.listing)
			_, err := mgr.archiveRoot(context.Background(), "/backups/a.tar.gz")
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("the refusal should mention %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestAWellFormedArchiveNamesItsOneDirectory(t *testing.T) {
	// The shape `ratline backup` writes: tar -C <parent> <basename>, so one top-level
	// directory and relative paths throughout. The `./` prefix is accepted because some
	// tar implementations add it.
	for _, tc := range []struct{ listing, want string }{
		{"example.com/\nexample.com/app/\nexample.com/.env\n", "example.com"},
		{"./acme/\n./acme/.ssh/\n./acme/site/\n", "acme"},
		{"example.com/\nexample.com/.ratline/site.yaml\n", "example.com"},
	} {
		mgr, runner := restoreManager(t)
		runner.ExpectOutput("tar --list --file /b/a.tar.gz", tc.listing)
		got, err := mgr.archiveRoot(context.Background(), "/b/a.tar.gz")
		if err != nil {
			t.Errorf("a well-formed archive was refused: %v", err)
			continue
		}
		if got != tc.want {
			t.Errorf("archiveRoot = %q, want %q", got, tc.want)
		}
	}
}

func TestSomethingThatIsNotATarIsReportedAsSuch(t *testing.T) {
	mgr, runner := restoreManager(t)
	runner.ExpectFailure("tar --list --file /b/notes.txt", 2, "tar: This does not look like a tar archive")
	_, err := mgr.archiveRoot(context.Background(), "/b/notes.txt")
	if err == nil {
		t.Fatal("a non-archive should be refused")
	}
	if !strings.Contains(err.Error(), "readable tar archive") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRestoreRefusesAnAccountThatIsNotOnThisServer(t *testing.T) {
	// The archive holds files owned by uid numbers from another server. Inventing the
	// account would produce a tenant nobody can log in as, owning files whose uid
	// matches nothing — so it refuses and names the command that makes one properly.
	mgr, _ := restoreManager(t)
	_, err := mgr.requireOwner("definitely-not-a-real-account-xyz")
	if err == nil {
		t.Fatal("a missing account must be refused")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unexpected error: %v", err)
	}
	if hint := rlerr.Hint(err); !strings.Contains(hint, "user add") {
		t.Errorf("the refusal should name the command that creates one, got hint: %q", hint)
	}
}

func TestRestoringOverAnExistingDirectoryNeedsForce(t *testing.T) {
	// A restore over a live site destroys what is there. --force is the acknowledgement,
	// and the message says what --force actually does — the previous directory is kept
	// until the rest of the restore has succeeded, not deleted up front.
	mgr, _ := restoreManager(t)
	target := filepath.Join(mgr.Cfg.Paths.HomeBase, "acme", "app.example.com")
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(mgr.Cfg.Paths.HomeBase, "staged")
	if err := os.MkdirAll(staged, 0o750); err != nil {
		t.Fatal(err)
	}
	id := &system.Identity{Name: "acme", UID: os.Getuid(), GID: os.Getgid()}

	_, _, err := mgr.swapIn(context.Background(), staged, target, id, false)
	if err == nil {
		t.Fatal("restoring over an existing directory without --force must be refused")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("unexpected error: %v", err)
	}
	if hint := rlerr.Hint(err); !strings.Contains(hint, "--force") {
		t.Errorf("the refusal should name --force, got hint: %q", hint)
	}
}

func TestTheSwapKeepsThePreviousDirectoryUntilItSucceeds(t *testing.T) {
	// The rollback for every later step is "put the old directory back", so it has to
	// still exist. Deleting it before the state row, the vhost and the unit are in place
	// would make a failure at any of those unrecoverable.
	mgr, _ := restoreManager(t)
	target := filepath.Join(mgr.Cfg.Paths.HomeBase, "acme", "app.example.com")
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "old-marker"), []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(mgr.Cfg.Paths.HomeBase, "staged")
	if err := os.MkdirAll(staged, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "new-marker"), []byte("new"), 0o640); err != nil {
		t.Fatal(err)
	}
	id := &system.Identity{Name: "acme", UID: os.Getuid(), GID: os.Getgid()}

	replaced, kept, err := mgr.swapIn(context.Background(), staged, target, id, true)
	if err != nil {
		t.Fatalf("swapIn = %v", err)
	}
	if !replaced {
		t.Error("replacing an existing directory should be reported as such")
	}
	if kept == "" {
		t.Fatal("the previous directory was not kept, so nothing after this could roll back")
	}
	if !system.Exists(filepath.Join(kept, "old-marker")) {
		t.Errorf("the kept copy at %s does not hold the previous contents", kept)
	}
	if !system.Exists(filepath.Join(target, "new-marker")) {
		t.Error("the restored contents are not at the target")
	}
	// And the mode is the one nginx needs: 0750, so the group can traverse and the rest
	// of the world cannot.
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o750 {
		t.Errorf("the restored directory is mode %04o, want 0750", perm)
	}
}

func TestASwapOntoNothingReportsNoReplacement(t *testing.T) {
	mgr, _ := restoreManager(t)
	target := filepath.Join(mgr.Cfg.Paths.HomeBase, "acme", "fresh.example.com")
	staged := filepath.Join(mgr.Cfg.Paths.HomeBase, "staged")
	if err := os.MkdirAll(staged, 0o750); err != nil {
		t.Fatal(err)
	}
	id := &system.Identity{Name: "acme", UID: os.Getuid(), GID: os.Getgid()}

	replaced, kept, err := mgr.swapIn(context.Background(), staged, target, id, false)
	if err != nil {
		t.Fatalf("swapIn onto an absent target = %v", err)
	}
	if replaced || kept != "" {
		t.Errorf("nothing was there, so replaced=%v kept=%q are both wrong", replaced, kept)
	}
	if !system.IsDir(target) {
		t.Error("the target does not exist after the swap")
	}
}

func TestChownTreeDoesNotFollowSymlinks(t *testing.T) {
	// A symlink inside a restored archive pointing at a file outside the tree would,
	// if followed, hand a tenant ownership of that file. Delivered by tarball, extracted
	// as root. lchown is the whole defence.
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside")
	if err := os.WriteFile(outside, []byte("not yours"), 0o600); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(tree, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(tree, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	before, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}

	// Chowning to the current owner is a no-op that still exercises the walk; a test
	// that needed a second uid would need root.
	if err := system.ChownTree(tree, os.Getuid(), os.Getgid()); err != nil {
		t.Fatalf("ChownTree = %v", err)
	}
	after, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode() != after.Mode() {
		t.Error("the target outside the tree was modified through the symlink")
	}
	if !system.Exists(filepath.Join(tree, "link")) {
		t.Error("the symlink was removed")
	}
}
