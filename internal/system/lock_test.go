package system

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

func TestLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ratline.lock")

	first, err := AcquireLock(path, 0, "ratline site add")
	if err != nil {
		t.Fatalf("first AcquireLock = %v", err)
	}

	// flock is per open file description, so a second acquisition in the same
	// process genuinely contends, exactly like a second invocation would.
	start := time.Now()
	_, err = AcquireLock(path, 300*time.Millisecond, "ratline user add")
	if err == nil {
		t.Fatal("the second AcquireLock succeeded while the first held the lock")
	}
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Errorf("it waited only %s, less than the requested timeout", elapsed)
	}
	if !rlerr.Is(err, rlerr.CodeLocked) {
		t.Errorf("code = %v, want locked (exit 5)", rlerr.CodeOf(err))
	}
	// The message must name the holder, so an operator knows what to wait for.
	if !strings.Contains(err.Error(), "site add") {
		t.Errorf("error %q does not name the holding command", err)
	}

	if err := first.Release(); err != nil {
		t.Fatalf("Release = %v", err)
	}

	second, err := AcquireLock(path, 0, "ratline user add")
	if err != nil {
		t.Fatalf("AcquireLock after release = %v", err)
	}
	if err := second.Release(); err != nil {
		t.Errorf("Release = %v", err)
	}
	// Releasing twice is harmless.
	if err := second.Release(); err != nil {
		t.Errorf("second Release = %v, want nil", err)
	}
}

func TestLockCreatesItsDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "ratline.lock")
	l, err := AcquireLock(path, 0, "test")
	if err != nil {
		t.Fatalf("AcquireLock = %v", err)
	}
	defer l.Release()
	if !Exists(path) {
		t.Error("the lock file was not created")
	}
}
