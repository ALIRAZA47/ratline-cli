package system

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// testRunner builds a real runner whose registry points at the small set of
// binaries that exist on any Unix host.
func testRunner(t *testing.T, dryRun bool) (Runner, *Binaries) {
	t.Helper()
	bins := NewBinaries()
	for name, candidates := range map[string][]string{
		"echo":  {"/bin/echo", "/usr/bin/echo"},
		"env":   {"/usr/bin/env", "/bin/env"},
		"sleep": {"/bin/sleep", "/usr/bin/sleep"},
		"false": {"/usr/bin/false", "/bin/false"},
		"cat":   {"/bin/cat", "/usr/bin/cat"},
		"tail":  {"/usr/bin/tail", "/bin/tail"},
	} {
		found := ""
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && fi.Mode().IsRegular() {
				found = c
				break
			}
		}
		if found == "" {
			t.Skipf("%s not found on this host", name)
		}
		bins.Set(name, found)
	}
	return NewRunner(bins, log.Discard(), dryRun), bins
}

func TestRunCapturesOutput(t *testing.T) {
	r, _ := testRunner(t, false)
	res, err := r.Run(context.Background(), Cmd{Name: "echo", Args: []string{"hello", "world"}})
	if err != nil {
		t.Fatalf("Run = %v", err)
	}
	if res.Out() != "hello world" {
		t.Errorf("stdout = %q, want %q", res.Out(), "hello world")
	}
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", res.ExitCode)
	}
	if res.Skipped {
		t.Error("a non-dry-run command reported itself skipped")
	}
}

func TestRunNonZeroExitIsAnExternalError(t *testing.T) {
	r, _ := testRunner(t, false)
	res, err := r.Run(context.Background(), Cmd{Name: "false"})
	if err == nil {
		t.Fatal("Run on a failing command returned nil")
	}
	if !rlerr.Is(err, rlerr.CodeExternal) {
		t.Errorf("code = %v, want external", rlerr.CodeOf(err))
	}
	if res == nil || res.ExitCode == 0 {
		t.Errorf("result = %+v, want a non-zero exit code", res)
	}
	// The message must name the command and the code, not just "exit status 1".
	if !strings.Contains(err.Error(), "exit 1") {
		t.Errorf("error message %q does not report the exit code", err)
	}
}

func TestRunOKExitTreatsAnExitCodeAsSuccess(t *testing.T) {
	r, _ := testRunner(t, false)
	if _, err := r.Run(context.Background(), Cmd{Name: "false", OKExit: []int{1}}); err != nil {
		t.Errorf("Run with OKExit=[1] = %v, want nil", err)
	}
}

func TestRunScrubsTheEnvironment(t *testing.T) {
	r, _ := testRunner(t, false)
	t.Setenv("RATLINE_SHOULD_NOT_LEAK", "secret")

	res, err := r.Run(context.Background(), Cmd{Name: "env"})
	if err != nil {
		t.Fatalf("Run = %v", err)
	}
	if strings.Contains(res.Stdout, "RATLINE_SHOULD_NOT_LEAK") {
		t.Error("a variable from ratline's own environment leaked into the child")
	}
	if !strings.Contains(res.Stdout, "PATH="+DefaultPath) {
		t.Errorf("child PATH is not the fixed one; got:\n%s", res.Stdout)
	}
}

func TestRunPassesStdin(t *testing.T) {
	r, _ := testRunner(t, false)
	res, err := r.Run(context.Background(), Cmd{Name: "cat", Stdin: strings.NewReader("piped")})
	if err != nil {
		t.Fatalf("Run = %v", err)
	}
	if res.Out() != "piped" {
		t.Errorf("stdout = %q, want %q", res.Out(), "piped")
	}
}

// waitForWriter closes found the first time needle appears in its input.
type waitForWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	needle string
	once   sync.Once
	found  chan struct{}
}

func (w *waitForWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	if strings.Contains(w.buf.String(), w.needle) {
		w.once.Do(func() { close(w.found) })
	}
	return len(p), nil
}

func TestRunTeesCmdStdoutBeforeTheChildExits(t *testing.T) {
	// tail -f prints the file's existing line and then follows for ever, so
	// the line can only reach Cmd.Stdout if output is teed as it arrives.
	r, _ := testRunner(t, false)
	logFile := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(logFile, []byte("a line to stream\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &waitForWriter{needle: "a line to stream", found: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		res *Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := r.Run(ctx, Cmd{Name: "tail", Args: []string{"-n", "5", "-f", logFile}, Stdout: w})
		done <- outcome{res, err}
	}()

	select {
	case <-w.found:
		// Streamed while the child was still running.
	case o := <-done:
		t.Fatalf("Run returned (err=%v) without the line ever reaching Cmd.Stdout", o.err)
	case <-time.After(5 * time.Second):
		t.Fatal("the line never reached Cmd.Stdout while the child ran: output is not teed")
	}

	cancel()
	o := <-done
	// The capture must keep working alongside the tee: failure messages and
	// callers still read Result.
	if o.res == nil || !strings.Contains(o.res.Stdout, "a line to stream") {
		t.Errorf("Result.Stdout lost the output when a tee was attached: %+v", o.res)
	}
}

func TestRunTimeoutIsReportedClearly(t *testing.T) {
	r, _ := testRunner(t, false)
	start := time.Now()
	_, err := r.Run(context.Background(), Cmd{Name: "sleep", Args: []string{"30"}, Timeout: 300 * time.Millisecond})
	if err == nil {
		t.Fatal("Run past its timeout returned nil")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("the timeout took %s to fire", elapsed)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error %q does not mention the timeout", err)
	}
}

func TestRunCancellationIsReportedClearly(t *testing.T) {
	r, _ := testRunner(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	_, err := r.Run(ctx, Cmd{Name: "sleep", Args: []string{"30"}})
	if err == nil {
		t.Fatal("Run on a cancelled context returned nil")
	}
	if !strings.Contains(err.Error(), "interrupted") {
		t.Errorf("error %q does not report the interruption", err)
	}
}

func TestDryRunSkipsMutationsButStillReads(t *testing.T) {
	r, _ := testRunner(t, true)

	res, err := r.Run(context.Background(), Cmd{Name: "echo", Args: []string{"mutating"}, Mutates: true})
	if err != nil {
		t.Fatalf("Run = %v", err)
	}
	if !res.Skipped {
		t.Error("a mutating command ran under --dry-run")
	}
	if res.Stdout != "" {
		t.Errorf("a skipped command produced output %q", res.Stdout)
	}

	// Reads must still execute: a preview built from stale facts is worse than
	// no preview at all.
	res, err = r.Run(context.Background(), Cmd{Name: "echo", Args: []string{"reading"}})
	if err != nil {
		t.Fatalf("Run = %v", err)
	}
	if res.Skipped || res.Out() != "reading" {
		t.Errorf("a read was skipped under --dry-run: %+v", res)
	}
}

func TestRunRejectsAnUnsafeCommandSpec(t *testing.T) {
	r, _ := testRunner(t, false)
	cases := map[string]Cmd{
		"neither Name nor Path":  {},
		"both Name and Path":     {Name: "echo", Path: "/bin/echo"},
		"relative path":          {Path: "bin/echo"},
		"unclean path":           {Path: "/bin/../bin/echo"},
		"NUL in an argument":     {Name: "echo", Args: []string{"a\x00b"}},
		"newline in an argument": {Name: "echo", Args: []string{"a\nb"}},
	}
	for why, c := range cases {
		if _, err := r.Run(context.Background(), c); err == nil {
			t.Errorf("Run accepted a command with %s", why)
		}
	}
}

func TestBinariesRejectAWritableBinary(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/tool"
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Explicitly, because os.WriteFile's mode is filtered by the umask.
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatal(err)
	}
	b := NewBinaries()
	b.candidates["tool"] = []string{path}
	if _, err := b.Path("tool"); err == nil {
		t.Error("Path accepted a world-writable binary")
	}

	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Path("tool"); err != nil {
		t.Errorf("Path rejected a 0755 binary: %v", err)
	}
}

func TestBinariesMissingCommandNamesTheFix(t *testing.T) {
	b := NewBinaries()
	b.candidates["nonesuch"] = []string{"/definitely/not/here"}
	_, err := b.Path("nonesuch")
	if err == nil {
		t.Fatal("Path found a binary that does not exist")
	}
	if !rlerr.Is(err, rlerr.CodePrecondition) {
		t.Errorf("code = %v, want precondition", rlerr.CodeOf(err))
	}
	if rlerr.Hint(err) == "" {
		t.Error("a missing-binary error carries no hint")
	}
}

func TestBinariesEnvOverride(t *testing.T) {
	b := NewBinaries()
	if err := b.LoadOverridesFromEnv([]string{"RATLINE_BIN_SSH_KEYGEN=/opt/fake/ssh-keygen"}); err != nil {
		t.Fatalf("LoadOverridesFromEnv = %v", err)
	}
	got, err := b.Path("ssh-keygen")
	if err != nil {
		t.Fatalf("Path = %v", err)
	}
	if got != "/opt/fake/ssh-keygen" {
		t.Errorf("Path = %q, want the override", got)
	}
	if err := b.LoadOverridesFromEnv([]string{"RATLINE_BIN_NGINX=relative/path"}); err == nil {
		t.Error("LoadOverridesFromEnv accepted a relative path")
	}
}

func TestBinariesHaveNoShell(t *testing.T) {
	// Not having a shell in the registry is what makes "never build shell
	// strings" structural rather than a convention.
	b := NewBinaries()
	for _, name := range []string{"sh", "bash", "zsh", "dash", "eval"} {
		if b.candidates[name] != nil {
			t.Errorf("the registry contains a shell: %q", name)
		}
	}
}
