package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

func testCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "add", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().String("user", "", "owner")
	cmd.Flags().String("runtime", "", "runtime")
	cmd.Flags().String("entry", "", "entry point")
	cmd.Flags().String("start-command", "", "start command")
	return cmd
}

func TestRequireFlagsNamesEveryMissingFlagAtOnce(t *testing.T) {
	cmd := testCommand()
	g := NewGlobals()
	g.Stdout, g.Stderr, g.Stdin = &bytes.Buffer{}, &bytes.Buffer{}, strings.NewReader("")
	if err := g.resolve(); err != nil {
		t.Fatal(err)
	}

	err := RequireFlags(cmd, g, "user", "runtime")
	if err == nil {
		t.Fatal("RequireFlags accepted a command with no flags set")
	}
	if !rlerr.Is(err, rlerr.CodeUsage) {
		t.Errorf("code = %v, want usage (exit 2)", rlerr.CodeOf(err))
	}
	// A CI log should show the whole problem after one run, not one flag per run.
	msg := err.Error()
	if !strings.Contains(msg, "--user") || !strings.Contains(msg, "--runtime") {
		t.Errorf("the error does not name both flags: %q", msg)
	}
	if !strings.Contains(msg, "and") {
		t.Errorf("the error does not read as a sentence: %q", msg)
	}
	// Without a terminal it must not suggest the wizard, which cannot run.
	if strings.Contains(rlerr.Hint(err), "-i") {
		t.Errorf("the hint offers the wizard without a terminal: %q", rlerr.Hint(err))
	}

	if err := cmd.Flags().Set("user", "alice"); err != nil {
		t.Fatal(err)
	}
	err = RequireFlags(cmd, g, "user", "runtime")
	if err == nil || strings.Contains(err.Error(), "--user") {
		t.Errorf("a satisfied flag is still reported missing: %v", err)
	}
	if err := cmd.Flags().Set("runtime", "python"); err != nil {
		t.Fatal(err)
	}
	if err := RequireFlags(cmd, g, "user", "runtime"); err != nil {
		t.Errorf("RequireFlags = %v, want nil once both are set", err)
	}
}

func TestRequireFlagsRejectsAnUnknownFlagName(t *testing.T) {
	// A typo in a call to RequireFlags is a programming bug, not a usage error.
	g := NewGlobals()
	err := RequireFlags(testCommand(), g, "nonesuch")
	if err == nil || rlerr.Is(err, rlerr.CodeUsage) {
		t.Errorf("err = %v, want an internal error", err)
	}
}

func TestExclusiveFlags(t *testing.T) {
	cmd := testCommand()
	if err := ExclusiveFlags(cmd, "entry", "start-command"); err != nil {
		t.Errorf("ExclusiveFlags with neither set = %v", err)
	}
	if err := cmd.Flags().Set("entry", "server.js"); err != nil {
		t.Fatal(err)
	}
	if err := ExclusiveFlags(cmd, "entry", "start-command"); err != nil {
		t.Errorf("ExclusiveFlags with one set = %v", err)
	}
	if err := cmd.Flags().Set("start-command", "npm start"); err != nil {
		t.Fatal(err)
	}
	err := ExclusiveFlags(cmd, "entry", "start-command")
	if err == nil {
		t.Fatal("ExclusiveFlags accepted both")
	}
	if !strings.Contains(err.Error(), "--entry") || !strings.Contains(err.Error(), "--start-command") {
		t.Errorf("the error does not name both: %q", err)
	}
}

func TestRequireOneOf(t *testing.T) {
	cmd := testCommand()
	if _, err := RequireOneOf(cmd, "entry", "start-command"); err == nil {
		t.Error("RequireOneOf accepted neither")
	}
	if err := cmd.Flags().Set("entry", "server.js"); err != nil {
		t.Fatal(err)
	}
	got, err := RequireOneOf(cmd, "entry", "start-command")
	if err != nil || got != "entry" {
		t.Errorf("RequireOneOf = %q, %v", got, err)
	}
	if err := cmd.Flags().Set("start-command", "npm start"); err != nil {
		t.Fatal(err)
	}
	if _, err := RequireOneOf(cmd, "entry", "start-command"); err == nil {
		t.Error("RequireOneOf accepted both")
	}
}

func TestJoinHuman(t *testing.T) {
	cases := map[string][]string{
		"":                 {},
		"--a":              {"--a"},
		"--a and --b":      {"--a", "--b"},
		"--a, --b and --c": {"--a", "--b", "--c"},
	}
	for want, in := range cases {
		if got := joinHuman(in); got != want {
			t.Errorf("joinHuman(%v) = %q, want %q", in, got, want)
		}
	}
}
