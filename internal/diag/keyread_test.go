package diag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

// Key diagnosis reads the key owner's authorized_keys — a file the tenant owns and can point
// at /dev/zero or grow without bound. An unbounded os.ReadFile of it in the root diagnosis
// process is a denial of service; the read has to be bounded, the way every other
// authorized_keys reader in the tree already is. A file larger than the limit stands in for
// the device symlink, deterministically.
func TestKeyDiagnosisBoundsATenantControlledRead(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Paths.HomeBase = root
	env := &Env{Cfg: cfg, Log: log.Discard()}

	owner := "acme"
	sshDir := filepath.Join(cfg.HomeDir(owner), ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oversize := make([]byte, cfg.SSH.MaxAuthKeysBytes+4096)
	if err := os.WriteFile(filepath.Join(sshDir, "authorized_keys"), oversize, 0o600); err != nil {
		t.Fatal(err)
	}

	checks := KeyChecks(env, &state.Key{
		ID: "k_1", Scope: "user", Owner: owner, Algorithm: "ssh-ed25519",
		Fingerprint: "SHA256:abc", Blob: "AAAAC3NzaC1lZDI1NTE5",
	})
	var installed *Check
	for i := range checks {
		if checks[i].ID == "installed" {
			installed = &checks[i]
		}
	}
	if installed == nil {
		t.Fatal("no 'installed' check in KeyChecks")
	}

	res := installed.Run(context.Background())
	if res.Verdict != Failed {
		t.Fatalf("verdict = %v, want Failed for an oversized authorized_keys", res.Verdict)
	}
	// The distinguishing evidence: the bounded reader refused the file, rather than the old
	// path reading it whole and then reporting the key merely absent.
	if !strings.Contains(res.Detail, "could not be read") {
		t.Errorf("detail = %q, want it to report the file could not be read (bounded)", res.Detail)
	}
}
