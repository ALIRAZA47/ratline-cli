//go:build unix

package cli

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// The other interactive tests drive the wizards over buffers with the TTY flags
// forced true, which exercises the wizard logic but bypasses the detection that
// decides whether a wizard runs at all. These tests use a real pseudo-terminal,
// so term.IsTerminal actually returns true and the whole path — detection,
// prompting, degradation — is the one an operator gets.
//
// Unix-only: there is no pty to open otherwise.

// buildTestBinary compiles ratline once for the pty tests.
func buildTestBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ratline")
	// The repository root is two levels up from internal/cli.
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/ratline")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the test binary: %v\n%s", err, out)
	}
	return bin
}

// runOnPTY runs the binary attached to a pseudo-terminal, writes the given input,
// and returns everything it printed.
func runOnPTY(t *testing.T, bin string, env []string, input string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(append(os.Environ(), "GODEBUG=netdns=go"), env...)

	f, err := pty.Start(cmd)
	if err != nil {
		t.Skipf("no pty available in this environment: %v", err)
	}
	defer f.Close()

	// A terminal wide enough that the narrow-window degradation does not kick in
	// and change what is being asserted.
	if err := pty.Setsize(f, &pty.Winsize{Rows: 40, Cols: 100}); err != nil {
		t.Logf("could not set the window size: %v", err)
	}

	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		// Copy until the child closes the pty. The read error at EOF is expected.
		_, _ = io.Copy(&b, f)
		done <- b.String()
	}()

	if input != "" {
		if _, err := f.WriteString(input); err != nil {
			t.Logf("writing to the pty: %v", err)
		}
	}

	_ = cmd.Wait()
	_ = f.Close()

	select {
	case out := <-done:
		return out
	case <-time.After(20 * time.Second):
		t.Fatal("the command did not finish within 20s on a pty; it is probably blocked on a prompt")
		return ""
	}
}

// On a real terminal, colour is enabled and the output carries ANSI escapes. This
// is the positive control for the degradation tests: without it, "no escapes"
// would pass trivially.
func TestPTYEnablesColour(t *testing.T) {
	bin := buildTestBinary(t)
	out := runOnPTY(t, bin, nil, "", "--config", filepath.Join(t.TempDir(), "absent.yaml"), "nonesuch")

	if !strings.Contains(out, "\033[") {
		t.Errorf("no ANSI escapes on a real terminal, so colour detection is not working:\n%q", out)
	}
	if !strings.Contains(out, "error") {
		t.Errorf("the error was not printed:\n%s", out)
	}
}

// NO_COLOR must win over a real terminal. This is the assertion the buffer-based
// test cannot make, because a buffer has no colour to suppress.
func TestPTYRespectsNoColor(t *testing.T) {
	bin := buildTestBinary(t)
	out := runOnPTY(t, bin, []string{"NO_COLOR=1"}, "",
		"--config", filepath.Join(t.TempDir(), "absent.yaml"), "nonesuch")

	if strings.Contains(out, "\033[") {
		t.Errorf("NO_COLOR was ignored on a terminal:\n%q", out)
	}
	if !strings.Contains(out, "error") {
		t.Errorf("the error was not printed at all:\n%s", out)
	}
}

// TERM=dumb likewise.
func TestPTYRespectsDumbTerminal(t *testing.T) {
	bin := buildTestBinary(t)
	out := runOnPTY(t, bin, []string{"TERM=dumb"}, "",
		"--config", filepath.Join(t.TempDir(), "absent.yaml"), "nonesuch")

	if strings.Contains(out, "\033[") {
		t.Errorf("TERM=dumb still produced escapes:\n%q", out)
	}
}

// --no-input on a real terminal must still refuse to prompt. This is the case
// that matters most: the terminal is genuinely there, so only the flag stops a
// wizard from opening and blocking a script that happens to run interactively.
func TestPTYNoInputStillRefusesToPrompt(t *testing.T) {
	bin := buildTestBinary(t)
	cfg := filepath.Join(t.TempDir(), "absent.yaml")

	// `site add` with no domain would open the wizard on a terminal. With
	// --no-input it must fail fast instead — and the 20s guard in runOnPTY is what
	// catches it if it blocks.
	out := runOnPTY(t, bin, nil, "", "--config", cfg, "--no-input", "site", "add")

	lower := strings.ToLower(out)
	// It cannot get as far as the wizard, so what it reports is either the missing
	// argument or the privilege check — never a prompt.
	if strings.Contains(lower, "domain:") || strings.Contains(lower, "which tenant") {
		t.Errorf("--no-input opened the wizard on a terminal:\n%s", out)
	}
	if !strings.Contains(lower, "error") {
		t.Errorf("expected a refusal, got:\n%s", out)
	}
}

// In a narrow window, ratline's own decoration must respect the width — but a
// long value must not.
//
// A path, a URL or a fingerprint is only useful if it can be copied, and both
// truncating and hard-wrapping one destroy that. The terminal soft-wraps them,
// which is the right outcome. So what this asserts is the distinction: structure
// is bounded, content is emitted whole.
func TestPTYNarrowWindowBoundsDecorationNotContent(t *testing.T) {
	bin := buildTestBinary(t)
	longDir := filepath.Join(t.TempDir(), strings.Repeat("deeply-nested-", 4)+"config")
	if err := os.MkdirAll(longDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(longDir, "absent.yaml")

	cmd := exec.Command(bin, "--config", cfgPath, "version")
	cmd.Env = append(os.Environ(), "GODEBUG=netdns=go")

	f, err := pty.Start(cmd)
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer f.Close()
	if err := pty.Setsize(f, &pty.Winsize{Rows: 24, Cols: 30}); err != nil {
		t.Skipf("could not size the pty: %v", err)
	}

	// The copy has to finish before the buffer is read, or the read races the
	// write. A channel rather than a sleep: a sleep is both slower and still a race.
	collected := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, f)
		collected <- b.String()
	}()
	_ = cmd.Wait()
	_ = f.Close()

	var raw string
	select {
	case raw = <-collected:
	case <-time.After(20 * time.Second):
		t.Fatal("the pty output was never closed")
	}
	out := stripANSI(raw)

	// The path is emitted intact, so it can be copied out of the terminal. The pty
	// inserts its own line breaks when soft-wrapping, so the comparison is against
	// the output with those removed.
	flat := strings.NewReplacer("\r", "", "\n", "").Replace(out)
	if !strings.Contains(flat, cfgPath) {
		t.Errorf("the configuration path was truncated or wrapped destructively, so it cannot be copied.\nwant to find: %s\ngot:\n%s", cfgPath, out)
	}

	// And no line consists of ratline's own padding running past the edge — the
	// label column is what it controls, and it must stay narrow.
	for _, line := range strings.Split(out, "\n") {
		clean := strings.TrimRight(line, "\r")
		if strings.TrimSpace(clean) == "" {
			continue
		}
		// Leading whitespace is the only padding ratline emits here.
		if lead := len(clean) - len(strings.TrimLeft(clean, " ")); lead > 30 {
			t.Errorf("ratline padded a line with %d spaces in a 30-column window: %q", lead, clean)
		}
	}
}

// The version output has to be readable on a terminal, since it is the first
// thing anyone runs and what they paste into a bug report.
func TestPTYVersionIsReadable(t *testing.T) {
	bin := buildTestBinary(t)
	out := runOnPTY(t, bin, nil, "", "--config", filepath.Join(t.TempDir(), "absent.yaml"), "version")

	for _, want := range []string{"ratline", "config", "nginx"} {
		if !strings.Contains(out, want) {
			t.Errorf("the version output is missing %q:\n%s", want, out)
		}
	}
	// It must not claim a config file that is not there.
	if !strings.Contains(out, "not present") {
		t.Errorf("a missing configuration file was not reported:\n%s", out)
	}
}

// A prompt that reaches a real terminal must be answerable, and the answer must
// be acted on. This is the round trip the buffer tests approximate.
func TestPTYConfirmationIsAnswerable(t *testing.T) {
	bin := buildTestBinary(t)
	cfg := filepath.Join(t.TempDir(), "absent.yaml")

	// `user delete` on a non-existent user reaches the privilege check first, so
	// what this really proves is that a terminal-attached run completes rather than
	// hanging waiting for input nobody sends. The 20s guard is the assertion.
	out := runOnPTY(t, bin, nil, "\n", "--config", cfg, "user", "delete", "nobody-here")
	if strings.TrimSpace(out) == "" {
		t.Error("a terminal-attached run produced no output at all")
	}
}

// stripANSI removes escape sequences so a width assertion measures visible text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			// Skip to the end of the sequence: a letter terminates a CSI.
			j := i + 1
			for j < len(s) && !(s[j] >= 'A' && s[j] <= 'Z') && !(s[j] >= 'a' && s[j] <= 'z') {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

var _ = bufio.NewReader
