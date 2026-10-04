package system

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
