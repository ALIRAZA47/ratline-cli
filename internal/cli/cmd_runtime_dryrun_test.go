package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// `ratline runtime install node 22 --with-pm2 --dry-run` failed with
//
//	error: npm reported success but there is no /opt/ratline/runtimes/node/22/bin/pm2
//
// The npm command is Mutates, so the Runner skipped it — and then the
// post-install verification ran anyway and reported the absence of the very
// thing the rehearsal had deliberately declined to install. A dry run cannot be
// allowed to fail on that.
func TestInstallPM2DryRunDoesNotVerifyWhatItDidNotInstall(t *testing.T) {
	runtimes := t.TempDir()
	// npm has to exist, or installPM2 fails its precondition before reaching the
	// code under test. pm2 deliberately does not — that is the whole case.
	bin := filepath.Join(runtimes, "node", "22", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Paths.RuntimesDir = runtimes

	g := &Globals{
		Cfg:    cfg,
		Log:    log.Discard(),
		DryRun: true,
		Runner: system.NewRunner(nil, log.Discard(), true),
	}

	if err := g.installPM2(context.Background(), "22", ""); err != nil {
		t.Fatalf("installPM2 under --dry-run = %v, want nil: the rehearsal must not report\n"+
			"the absence of a binary it chose not to install", err)
	}

	if _, err := os.Stat(filepath.Join(bin, "pm2")); err == nil {
		t.Error("the rehearsal installed pm2 for real")
	}
}

// The precondition that runs BEFORE the install is still a real precondition,
// and a rehearsal should report it — otherwise the dry run would pass on a Node
// version that cannot host PM2 at all.
func TestInstallPM2DryRunStillReportsAMissingNpm(t *testing.T) {
	cfg := config.Default()
	cfg.Paths.RuntimesDir = t.TempDir() // no node/22 at all

	g := &Globals{
		Cfg:    cfg,
		Log:    log.Discard(),
		DryRun: true,
		Runner: system.NewRunner(nil, log.Discard(), true),
	}

	err := g.installPM2(context.Background(), "22", "")
	if err == nil {
		t.Fatal("installPM2 under --dry-run = nil, want an error: npm is genuinely absent")
	}
	if !strings.Contains(err.Error(), "no npm at") {
		t.Errorf("error = %v, want one naming the missing npm", err)
	}
}
