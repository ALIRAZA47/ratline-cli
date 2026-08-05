package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// harness builds Globals over buffers. Because the buffers are not *os.File,
// every TTY check reports false, which is exactly the state a CI pipeline runs
// in — the case where a prompt would hang a build.
func harness(t *testing.T, args ...string) (code int, stdout, stderr *bytes.Buffer) {
	t.Helper()
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	g := NewGlobals()
	g.Stdout, g.Stderr, g.Stdin = stdout, stderr, strings.NewReader("")
	g.Log = log.Discard()
	// Point at a config that does not exist so the defaults are used.
	args = append([]string{"--config", t.TempDir() + "/absent.yaml"}, args...)
	return Run(g, args), stdout, stderr
}

func TestHelpExitsZeroAndGroupsCommands(t *testing.T) {
	code, out, _ := harness(t, "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	s := out.String()
	for _, want := range []string{"Usage:", "OPERATIONS", "version", "Examples:", "Flags:", "--json", "--dry-run"} {
		if !strings.Contains(s, want) {
			t.Errorf("help output is missing %q", want)
		}
	}
	// Examples belong below the flags, where the eye lands last.
	if strings.Index(s, "Examples:") < strings.Index(s, "Flags:") {
		t.Error("Examples appears above the flags")
	}
	// No group heading may be printed with nothing under it. Checked structurally
	// rather than by naming a group, so filling in the command tree cannot make
	// this assertion silently vacuous.
	headings := []string{"USERS", "SSH KEYS", "SITES", "CERTIFICATES", "RUNTIMES", "OPERATIONS", "OTHER"}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		isHeading := false
		for _, h := range headings {
			if trimmed == h {
				isHeading = true
			}
		}
		if !isHeading {
			continue
		}
		// The line after a heading must be an indented command, not a blank line.
		if i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) == "" {
			t.Errorf("the group heading %q has no commands under it", trimmed)
		}
	}
	// And every group that has commands is present, so the tree is fully wired.
	for _, want := range []string{"USERS", "SSH KEYS", "SITES", "CERTIFICATES", "RUNTIMES", "OPERATIONS"} {
		if !strings.Contains(s, want) {
			t.Errorf("the %q group is missing from the help output", want)
		}
	}
}

func TestVersionRuns(t *testing.T) {
	code, out, _ := harness(t, "version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "ratline") {
		t.Errorf("version output = %q", out.String())
	}
}

func TestVersionJSONEnvelope(t *testing.T) {
	code, out, _ := harness(t, "--json", "version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("--json output does not parse: %v\n%s", err, out.String())
	}
	if !env.OK {
		t.Error("ok = false on a successful command")
	}
	if env.Command != "ratline version" {
		t.Errorf("command = %q", env.Command)
	}
	if env.Error != nil {
		t.Errorf("error = %+v, want nil", env.Error)
	}
	if env.Data == nil {
		t.Error("data is empty")
	}
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	code, _, errOut := harness(t, "nonesuch")
	if code != int(rlerr.CodeUsage) {
		t.Fatalf("exit code = %d, want %d", code, rlerr.CodeUsage)
	}
	if !strings.Contains(errOut.String(), "error:") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestUnknownFlagIsAUsageError(t *testing.T) {
	code, _, errOut := harness(t, "version", "--nonesuch")
	if code != int(rlerr.CodeUsage) {
		t.Fatalf("exit code = %d, want %d", code, rlerr.CodeUsage)
	}
	if !strings.Contains(errOut.String(), "hint:") {
		t.Errorf("a usage error carries no hint: %q", errOut.String())
	}
}

func TestContradictoryFlagsAreRefused(t *testing.T) {
	// Rather than silently picking a winner: an operator who typed both has one
	// of them wrong, and guessing which would be worse than saying so.
	cases := [][]string{
		{"--quiet", "--verbose", "version"},
		{"--json", "--interactive", "version"},
		{"--interactive", "--no-input", "version"},
		{"--interactive", "--yes", "version"},
	}
	for _, args := range cases {
		code, out, errOut := harness(t, args...)
		if code != int(rlerr.CodeUsage) {
			t.Errorf("%v: exit code = %d, want %d", args, code, rlerr.CodeUsage)
		}
		// Human errors go to stderr; under --json the envelope goes to stdout.
		if !strings.Contains(errOut.String()+out.String(), "contradict") {
			t.Errorf("%v: no explanation was printed (stdout=%q stderr=%q)", args, out.String(), errOut.String())
		}
	}
}

func TestJSONErrorEnvelope(t *testing.T) {
	code, out, _ := harness(t, "--json", "nonesuch")
	if code != int(rlerr.CodeUsage) {
		t.Fatalf("exit code = %d, want %d", code, rlerr.CodeUsage)
	}
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("the error envelope does not parse: %v\n%s", err, out.String())
	}
	if env.OK {
		t.Error("ok = true on a failure")
	}
	if env.Error == nil {
		t.Fatal("error is nil on a failure")
	}
	if env.Error.Code != int(rlerr.CodeUsage) || env.Error.Name != "usage" {
		t.Errorf("error = %+v", env.Error)
	}
}

func TestNoInputIsImpliedWithoutATTY(t *testing.T) {
	g := NewGlobals()
	g.Stdout, g.Stderr, g.Stdin = &bytes.Buffer{}, &bytes.Buffer{}, strings.NewReader("")
	if err := g.resolve(); err != nil {
		t.Fatalf("resolve = %v", err)
	}
	if !g.NoInput {
		t.Error("--no-input was not implied when stdout is not a terminal")
	}
	if g.CanPrompt() {
		t.Error("CanPrompt is true without a terminal; a prompt here would hang a CI job")
	}
	if g.Color {
		t.Error("colour is enabled without a terminal")
	}
}

func TestYesImpliesNoInput(t *testing.T) {
	g := NewGlobals()
	g.Stdout, g.Stderr, g.Stdin = &bytes.Buffer{}, &bytes.Buffer{}, strings.NewReader("")
	g.Yes = true
	if err := g.resolve(); err != nil {
		t.Fatalf("resolve = %v", err)
	}
	if !g.NoInput {
		t.Error("--yes did not imply --no-input")
	}
}

func TestNoColorEnvIsRespected(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	g := NewGlobals()
	g.Stdout, g.Stderr, g.Stdin = &bytes.Buffer{}, &bytes.Buffer{}, strings.NewReader("")
	if err := g.resolve(); err != nil {
		t.Fatalf("resolve = %v", err)
	}
	if g.Color {
		t.Error("NO_COLOR was ignored")
	}
}

func TestConfirmWithoutATTYIsAnInputError(t *testing.T) {
	g := NewGlobals()
	g.Stdout, g.Stderr, g.Stdin = &bytes.Buffer{}, &bytes.Buffer{}, strings.NewReader("y\n")
	g.Log = log.Discard()
	if err := g.resolve(); err != nil {
		t.Fatal(err)
	}
	_, err := g.Confirm("Delete everything?")
	if err == nil {
		t.Fatal("Confirm succeeded without a terminal")
	}
	if !rlerr.Is(err, rlerr.CodeInputRequired) {
		t.Errorf("code = %v, want input_required (exit 10)", rlerr.CodeOf(err))
	}
}

func TestConfirmWithYes(t *testing.T) {
	g := NewGlobals()
	g.Stdout, g.Stderr, g.Stdin = &bytes.Buffer{}, &bytes.Buffer{}, strings.NewReader("")
	g.Log = log.Discard()
	g.Yes = true
	if err := g.resolve(); err != nil {
		t.Fatal(err)
	}
	ok, err := g.Confirm("Proceed?")
	if err != nil || !ok {
		t.Errorf("Confirm with --yes = %v, %v", ok, err)
	}
	if err := g.ConfirmTyped("example.com", "Really delete?"); err != nil {
		t.Errorf("ConfirmTyped with --yes = %v", err)
	}
}

func TestTableRendering(t *testing.T) {
	var out bytes.Buffer
	g := NewGlobals()
	g.Stdout = &out
	tbl := g.Table("domain", "user", "runtime")
	tbl.Row("example.com", "alice", "python")
	tbl.Row("app.example.com", "bob", "node")
	if err := tbl.Render(); err != nil {
		t.Fatalf("Render = %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "DOMAIN") || !strings.Contains(s, "example.com") {
		t.Errorf("table output:\n%s", s)
	}
	if strings.Contains(s, "\033[") {
		t.Error("colour escapes appeared with colour disabled")
	}

	// Under --json the table is silent; the command emits an envelope instead.
	out.Reset()
	g.JSON = true
	if err := g.Table("a").Row("b").Render(); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("the table wrote to stdout under --json: %q", out.String())
	}
}

func TestTableRowsAreAlwaysHeaderWidth(t *testing.T) {
	g := NewGlobals()
	g.Stdout = &bytes.Buffer{}
	tbl := g.Table("a", "b", "c")
	tbl.Row("1")
	tbl.Row("1", "2", "3", "4")
	if got := len(tbl.rows[0]); got != 3 {
		t.Errorf("a short row has %d cells, want 3", got)
	}
	if got := len(tbl.rows[1]); got != 3 {
		t.Errorf("a long row has %d cells, want 3", got)
	}
}

func TestTheCommandTreeBuildsWithoutPanicking(t *testing.T) {
	// cobra panics on a redefined flag, at construction time, so a duplicate
	// registration takes the whole binary down before it can print anything — every
	// command, not just the one with the clash. It surfaced once as a panic inside an
	// unrelated help test, which is a confusing way to learn about it.
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("building the command tree panicked: %v", p)
		}
	}()
	root := NewRootCommand(NewGlobals())

	// And every flag is reachable, which is what a panic would have prevented.
	var commands int
	walk(root, func(c *cobra.Command) {
		commands++
		c.Flags().VisitAll(func(*pflag.Flag) {})
	})
	if commands < 50 {
		t.Errorf("only %d commands were built; the tree looks truncated", commands)
	}
}

func TestAskingForHelpNeverNeedsRoot(t *testing.T) {
	// `<cmd> --help` short circuits inside cobra, but `ratline help <cmd>` is a real
	// command and went through the privilege check — so it answered "ratline must run
	// as root" to someone who was asking what the command does. That is the spelling
	// people reach for before they know the flag exists.
	for _, args := range [][]string{
		{"help"},
		{"help", "user", "add"},
		{"help", "site", "deploy"},
		{"help", "cert", "issue"},
		{"user", "add", "--help"},
		{"site", "add", "--help"},
	} {
		code, out, errOut := harness(t, args...)
		if code != 0 {
			t.Errorf("%v exited %d, want 0\nstderr: %s", args, code, errOut.String())
		}
		if strings.Contains(errOut.String(), "must run as root") {
			t.Errorf("%v refused for want of root; help is not a privileged operation", args)
		}
		if out.Len() == 0 && errOut.Len() == 0 {
			t.Errorf("%v printed nothing at all", args)
		}
	}
}

func TestARealCommandStillNeedsRoot(t *testing.T) {
	// The other half of the pair: the help exemption must not have opened the door.
	code, _, errOut := harness(t, "user", "add", "bob")
	if code != 3 {
		t.Errorf("exit code = %d, want 3 (needs root)", code)
	}
	if !strings.Contains(errOut.String(), "root") {
		t.Errorf("it should still refuse without root, got:\n%s", errOut.String())
	}
}
