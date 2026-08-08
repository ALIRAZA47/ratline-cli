package sshkey

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// The one that matters.
//
// sshd_config(5) on RevokedKeys: "if this file is not readable, then public key
// authentication will be refused for all users." Not the keys on the list — every key, for
// every account. So a drop-in naming a file that is not there does not weaken the backstop,
// it closes the server, and there is no recovery over SSH because SSH is what broke.
//
// This is the test that did not exist when a live server was locked out by exactly that.
func TestTheDropInNeverNamesARevocationListThatIsNotThere(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "revoked")
	if err := os.WriteFile(present, RenderRevoked(nil), 0o644); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(dir, "does-not-exist")

	// A list that is there is named, because the backstop is worth having.
	withList := string(RenderDropIn(nil, present))
	if !strings.Contains(withList, "RevokedKeys "+present) {
		t.Errorf("the drop-in does not name a list that exists, so a revoked key would "+
			"keep working:\n%s", withList)
	}

	// And an empty path produces no directive at all — which is what ApplyDropIn falls
	// back to when it cannot create the file.
	without := string(RenderDropIn(nil, ""))
	if strings.Contains(without, "RevokedKeys") {
		t.Errorf("the drop-in names a revocation list when it was given none:\n%s", without)
	}

	// The render itself is pure and will emit whatever path it is handed; the guarantee is
	// that ApplyDropIn never hands it a path it has not ensured. That is asserted here on
	// the check the verify step uses, because it is the same question.
	if err := revokedListIsReadable(absent); err == nil {
		t.Error("a RevokedKeys path that cannot be opened was accepted; in that state sshd " +
			"refuses every public key on the server")
	}
	if err := revokedListIsReadable(present); err != nil {
		t.Errorf("a readable list was rejected: %v", err)
	}
}

// sshd's own ways of saying "there is no list" have to be accepted, or the verify step
// refuses a perfectly good server.
func TestNoRevocationListIsNotAFailure(t *testing.T) {
	for _, value := range []string{"", "   ", "none", "None", "NONE"} {
		if err := revokedListIsReadable(value); err != nil {
			t.Errorf("revokedListIsReadable(%q) = %v, want no error", value, err)
		}
	}
}

// The failure has to say what it means. "cannot read /etc/ratline/revoked" is a shrug;
// the thing the operator needs to know is that no key works at all, and which command
// fixes it — because they are about to lose the session they are reading it in.
func TestTheRefusalExplainsThatNoKeyWillWork(t *testing.T) {
	err := revokedListIsReadable(filepath.Join(t.TempDir(), "absent"))
	if err == nil {
		t.Fatal("no error")
	}
	msg := err.Error()
	for _, want := range []string{"refuses every public key", "every account"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message does not say %q:\n%s", want, msg)
		}
	}
	var e *rlerr.Error
	if !errors.As(err, &e) || !strings.Contains(e.Hint, "key sync") {
		t.Errorf("the hint does not name a command that fixes it: %q", rlerr.Hint(err))
	}
}

// An existing but unreadable list is the same lockout, and more confusing because
// everything looks in place.
func TestAnUnreadableListIsTreatedLikeAMissingOne(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, which can read a 0000 file")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "revoked")
	if err := os.WriteFile(path, []byte(""), 0o000); err != nil {
		t.Fatal(err)
	}
	if err := revokedListIsReadable(path); err == nil {
		t.Error("a list that exists but cannot be opened was accepted; sshd refuses every " +
			"key in that state just as it does for a missing one")
	}
}

// ApplyDropIn must create the list before naming it, and must decline to name it if it
// cannot.
//
// Tested through revokedPathForDropIn rather than ApplyDropIn itself, because ApplyDropIn
// reads and can rewrite the host's real /etc/ssh/sshd_config — a test that might edit the
// machine's SSH configuration to prove a point about SSH configuration is not a trade worth
// making.
func TestTheListIsCreatedBeforeTheDropInNamesIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "revoked")

	m := &Manager{Cfg: cfgWithRevoked(path), Log: log.Discard()}
	got := m.revokedPathForDropIn()
	if got != path {
		t.Fatalf("revokedPathForDropIn() = %q, want %q", got, path)
	}
	// Created, with content sshd can parse — an empty list refuses nothing, which is right
	// for a server that has revoked nothing. Absent is the state that refuses everything.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the list was named but not created: %v", err)
	}
	if err := revokedListIsReadable(path); err != nil {
		t.Errorf("the list it created is not readable: %v", err)
	}
}

// If the list cannot be created, the directive is dropped rather than left dangling.
func TestAnUncreatableListIsNotNamedAtAll(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, which can write anywhere")
	}
	// A path under a file, so the directory cannot be created.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "revoked")

	m := &Manager{Cfg: cfgWithRevoked(path), Log: log.Discard()}
	if got := m.revokedPathForDropIn(); got != "" {
		t.Errorf("revokedPathForDropIn() = %q for a path it cannot create; the drop-in would "+
			"name a file sshd cannot read, and no key would work", got)
	}
	// And the rendered drop-in therefore carries no directive at all.
	if body := string(RenderDropIn(nil, "")); strings.Contains(body, "RevokedKeys") {
		t.Errorf("the drop-in still names a revocation list:\n%s", body)
	}
}

// cfgWithRevoked is a default configuration pointed at a temporary revocation list.
func cfgWithRevoked(path string) *config.Config {
	c := config.Default()
	c.SSH.RevokedKeys = path
	return c
}
