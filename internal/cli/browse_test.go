package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The menu is generated from the command tree, and the whole reason for that is the last
// one: the previous hand-written menu reached about a dozen of the eighty-six commands and
// nothing made that visible. A test that only checked "the menu renders" would have passed
// on the broken version too.

// scriptedPrompter returns a prompter reading canned answers, with everything it writes
// captured — enough to drive the menu without a terminal.
func scriptedPrompter(answers ...string) (*prompter, *bytes.Buffer) {
	out := &bytes.Buffer{}
	g := &Globals{
		Stdin:    strings.NewReader(strings.Join(answers, "\n") + "\n"),
		Stderr:   out,
		Stdout:   out,
		StdinTTY: true, StderrTTY: true,
		Width: 80,
	}
	p := newPrompter(g)
	p.out = out
	return p, out
}

func TestEveryCommandIsReachableFromTheMenu(t *testing.T) {
	root := NewRootCommand(&Globals{})

	// Walk the tree the way browse does, and the way cobra does, and compare. A command
	// the menu cannot reach is a command nobody discovers.
	var unreachable []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		offered := map[string]bool{}
		for _, child := range runnableChildren(c) {
			offered[child.Name()] = true
		}
		for _, child := range c.Commands() {
			if child.Hidden || !child.IsAvailableCommand() {
				continue
			}
			switch child.Name() {
			case "help", "completion", "man":
				continue // deliberately not in the menu; reachable by name
			}
			if !offered[child.Name()] {
				unreachable = append(unreachable, child.CommandPath())
				continue
			}
			walk(child)
		}
	}
	walk(root)

	if len(unreachable) > 0 {
		t.Errorf("the menu cannot reach %d command(s): %s",
			len(unreachable), strings.Join(unreachable, ", "))
	}

	// And the count itself, because "reachable" is vacuous if the tree is empty.
	total := 0
	var count func(c *cobra.Command)
	count = func(c *cobra.Command) {
		for _, child := range runnableChildren(c) {
			if len(runnableChildren(child)) == 0 {
				total++
			}
			count(child)
		}
	}
	count(root)
	if total < 50 {
		t.Errorf("only %d runnable commands found; the walk is not reaching the tree", total)
	}
	t.Logf("%d runnable commands reachable from the menu", total)
}

func TestPositionalNamesComeFromTheUseLine(t *testing.T) {
	for _, tc := range []struct {
		use  string
		want []string
	}{
		{"add <username>", []string{"<username>"}},
		{"show <fingerprint|label>", []string{"<fingerprint|label>"}},
		{"connect", nil},
		{"deploy <domain> [ref]", []string{"<domain>", "[ref]"}},
		// The verb is never an argument, even when it looks like one.
		{"list", nil},
		// A command with no Use at all is malformed, but the menu must not panic on it.
		{"", nil},
	} {
		got := positionalNames(tc.use)
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("positionalNames(%q) = %v, want %v", tc.use, got, tc.want)
		}
	}
}

func TestCommandWordsDropTheBinaryName(t *testing.T) {
	root := NewRootCommand(&Globals{})
	target, _, err := root.Find([]string{"db", "user", "add"})
	if err != nil {
		t.Fatalf("finding db user add: %v", err)
	}
	got := strings.Join(commandWords(target), " ")
	if got != "db user add" {
		t.Errorf("commandWords = %q, want %q", got, "db user add")
	}
}

// A bool is spelled `--force`, not `--force true`, and getting that wrong produces a
// command that fails on an unexpected positional argument.
func TestBoolFlagsAreSpelledWithoutAValue(t *testing.T) {
	root := NewRootCommand(&Globals{})
	cmd, _, err := root.Find([]string{"db", "connect"})
	if err != nil {
		t.Fatalf("finding db connect: %v", err)
	}
	// Choose --force, then Run it.
	p, _ := scriptedPrompter("force", "y", "..done")
	got, err := askOptions(p, cmd)
	if err != nil {
		t.Fatalf("askOptions: %v", err)
	}
	if strings.Join(got, " ") != "--force" {
		t.Errorf("askOptions = %v, want [--force] with no value", got)
	}
}

func TestAValuedFlagCarriesItsValue(t *testing.T) {
	root := NewRootCommand(&Globals{})
	cmd, _, err := root.Find([]string{"db", "connect"})
	if err != nil {
		t.Fatalf("finding db connect: %v", err)
	}
	p, _ := scriptedPrompter("from-file", "/root/atlas.uri", "..done")
	got, err := askOptions(p, cmd)
	if err != nil {
		t.Fatalf("askOptions: %v", err)
	}
	if strings.Join(got, " ") != "--from-file /root/atlas.uri" {
		t.Errorf("askOptions = %v, want [--from-file /root/atlas.uri]", got)
	}
}

// Taking the defaults must produce a bare command. The menu offering twenty options does
// not mean an operator has to answer twenty questions.
func TestRunningWithNoOptionsProducesNoFlags(t *testing.T) {
	root := NewRootCommand(&Globals{})
	cmd, _, err := root.Find([]string{"db", "connect"})
	if err != nil {
		t.Fatalf("finding db connect: %v", err)
	}
	p, _ := scriptedPrompter("..done")
	got, err := askOptions(p, cmd)
	if err != nil {
		t.Fatalf("askOptions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("askOptions = %v, want nothing", got)
	}
}

// The menu must not offer the global flags: --json and --config belong to the invocation,
// not to the command, and setting them from inside the menu would be a surprise.
func TestTheMenuOffersOnlyTheCommandsOwnFlags(t *testing.T) {
	root := NewRootCommand(&Globals{})
	cmd, _, err := root.Find([]string{"db", "connect"})
	if err != nil {
		t.Fatalf("finding db connect: %v", err)
	}
	// Force cobra to merge the parents' persistent flags into this command, which is what
	// has happened by the time a real invocation reaches RunE. Without this the test
	// passes whether the code filters them or not, because they are not there yet — the
	// first version of this test proved nothing for exactly that reason.
	_ = cmd.InheritedFlags()
	if cmd.Flags().Lookup("json") == nil {
		t.Fatal("setup: the global flags are not merged, so this test cannot detect them")
	}

	p, buf := scriptedPrompter("..done")
	if _, err := askOptions(p, cmd); err != nil {
		t.Fatalf("askOptions: %v", err)
	}
	for _, global := range []string{"--json", "--config", "--quiet", "--dry-run"} {
		if strings.Contains(buf.String(), global) {
			t.Errorf("the options list offers the global flag %s:\n%s", global, buf.String())
		}
	}
	// It should still be offering the command's own.
	if !strings.Contains(buf.String(), "--from-file") {
		t.Errorf("the options list is missing --from-file:\n%s", buf.String())
	}
}

// The whole flow, driven the way an operator would: root menu, into a group, onto a
// command, take the defaults, read the summary, cancel. Cancelling at the summary is what
// makes this safe to run — nothing is executed, and that is also the property being
// checked, because a menu that acts before it confirms is worse than no menu.
func TestBrowsingFromTheRootReachesACommandAndShowsWhatItWouldRun(t *testing.T) {
	out := &bytes.Buffer{}
	g := &Globals{
		Stdin: strings.NewReader(strings.Join([]string{
			"db",      // group
			"connect", // command
			"..done",  // no options
			"c",       // cancel at the summary
			"..back",  // leave the db menu
			"..back",  // leave the root menu
		}, "\n") + "\n"),
		Stdout: out, Stderr: out,
		StdinTTY: true, StderrTTY: true, Width: 80,
	}
	p := newPrompter(g)
	p.out = out

	err := browse(t.Context(), g, p, NewRootCommand(g))
	if err != ErrCancelled {
		t.Fatalf("browse returned %v, want ErrCancelled after backing out", err)
	}

	got := out.String()
	for _, want := range []string{
		"Point ratline at a MongoDB server", // the command's own Short, from cobra
		"ratline db connect",                // the equivalent command, before running it
		"[R]un",                             // it asked before doing anything
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the transcript never showed %q:\n%s", want, got)
		}
	}
}
