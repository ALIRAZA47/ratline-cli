package system

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// attachedOut gives a child a real file as stdout, the way a terminal is a file.
func attachedOut(t *testing.T) (*os.File, func() string) {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f, func() string {
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
}

func TestAttachedOutputGoesStraightToTheTerminalNotACapture(t *testing.T) {
	// A REPL decides whether it is interactive by looking at its own descriptors. A
	// capture buffer in between makes it a batch tool, so the child must get the file
	// itself — and nothing is kept, because a session's output is the operator's.
	r, _ := testRunner(t, false)
	out, read := attachedOut(t)
	res, err := r.Run(context.Background(), Cmd{Name: "echo", Args: []string{"hello"}, Attach: true, Stdout: out})
	if err != nil {
		t.Fatalf("Run = %v", err)
	}
	if got := read(); got != "hello\n" {
		t.Errorf("the terminal received %q, want %q", got, "hello\n")
	}
	if res.Stdout != "" {
		t.Errorf("an attached session's output was captured: %q", res.Stdout)
	}
}

func TestAttachedSessionOutlivesTheTimeoutAndTheContext(t *testing.T) {
	// ratline cancels its context on SIGINT, and Ctrl-C at a database prompt means
	// "abandon this line", not "kill the shell". Nor does a person at a prompt run to a
	// two-minute default.
	r, _ := testRunner(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err := r.Run(ctx, Cmd{Name: "sleep", Args: []string{"0.3"}, Attach: true, Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("an attached session was ended by the timeout or the context: %v", err)
	}
	if time.Since(start) < 250*time.Millisecond {
		t.Error("the session returned before the child finished")
	}

	// The negative: the ordinary path does honour the same timeout, so the test above
	// is measuring Attach and not a sleep that happened to be quick.
	if _, err := r.Run(context.Background(), Cmd{Name: "sleep", Args: []string{"0.3"}, Timeout: 20 * time.Millisecond}); err == nil {
		t.Error("the ordinary path ignored its timeout too; the assertion above proves nothing")
	}
}

func TestAttachedNonZeroExitIsAnExternalErrorWithTheStatus(t *testing.T) {
	r, _ := testRunner(t, false)
	res, err := r.Run(context.Background(), Cmd{Name: "false", Attach: true})
	if err == nil {
		t.Fatal("a failing session reported success")
	}
	if rlerr.CodeOf(err) != rlerr.CodeExternal {
		t.Errorf("code = %v, want external", rlerr.CodeOf(err))
	}
	if res == nil || res.ExitCode != 1 || !strings.Contains(err.Error(), "status 1") {
		t.Errorf("the exit status was lost: res=%+v err=%v", res, err)
	}
}

func TestAttachedMutationIsSkippedUnderDryRun(t *testing.T) {
	r, _ := testRunner(t, true)
	out, read := attachedOut(t)
	res, err := r.Run(context.Background(), Cmd{Name: "echo", Args: []string{"ran"}, Attach: true, Mutates: true, Stdout: out})
	if err != nil {
		t.Fatalf("Run = %v", err)
	}
	if !res.Skipped || read() != "" {
		t.Errorf("an attached mutation ran under --dry-run: skipped=%v out=%q", res.Skipped, read())
	}
}

func TestAttachedStillValidatesArgv(t *testing.T) {
	r, _ := testRunner(t, false)
	if _, err := r.Run(context.Background(), Cmd{Name: "echo", Args: []string{"a\nb"}, Attach: true}); err == nil {
		t.Error("an attached command skipped argv validation")
	}
}

// An id that does not fit the kernel's 32 bits is refused, never narrowed. -1 is the one
// that matters: as uint32 it is 4294967295, which setresuid reads as "leave unchanged",
// so a command meant to drop to a tenant would have kept running as root.
func TestCredentialRefusesIDsThatWouldWrap(t *testing.T) {
	ok, err := credentialFor(&Identity{Name: "acme", UID: 1001, GID: 1001, Groups: []int{33}})
	if err != nil || ok.Uid != 1001 || ok.Gid != 1001 || len(ok.Groups) != 1 || ok.Groups[0] != 33 {
		t.Fatalf("credentialFor(valid) = %+v, %v", ok, err)
	}
	bad := []*Identity{
		{Name: "ghost", UID: -1, GID: 1001},
		{Name: "ghost", UID: 1001, GID: -1},
		{Name: "ghost", UID: 1001, GID: 1001, Groups: []int{-1}},
	}
	// Ids past 32 bits only exist where int is 64 bits wide; a variable, not a constant,
	// so this file still compiles where it is not.
	if strconv.IntSize == 64 {
		var big int64 = 1 << 32
		bad = append(bad,
			&Identity{Name: "big", UID: int(big), GID: 1001},
			&Identity{Name: "sentinel", UID: int(big - 1), GID: 1001})
	}
	for _, id := range bad {
		if c, err := credentialFor(id); err == nil {
			t.Errorf("credentialFor(%+v) = %+v, want a refusal", id, c)
		}
	}
}

// Both paths that drop privileges refuse before anything is started.
func TestRunnersRefuseAnIdentityThatWouldWrap(t *testing.T) {
	ghost := &Identity{Name: "ghost", UID: -1, GID: -1}
	r := NewRunner(NewBinaries(), log.Discard(), false)
	for _, attach := range []bool{false, true} {
		res, err := r.Run(context.Background(), Cmd{Path: "/bin/echo", Args: []string{"hi"}, As: ghost, Attach: attach})
		// The refusal itself, not any error: as a non-root test process the kernel would
		// refuse a setuid on its own, which would make this pass without the check.
		if err == nil || !strings.Contains(err.Error(), "cannot be dropped to") {
			t.Errorf("attach=%v: a -1 identity was not refused by the id check: %+v, %v", attach, res, err)
		}
	}
}
