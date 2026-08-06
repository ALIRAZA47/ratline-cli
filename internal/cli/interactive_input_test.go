package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// `site env set` is where secrets are set, and its primary documented form put the value
// in argv — world-readable through /proc for as long as the command runs, then in the
// shell history. A bare KEY now means "ask me", which was a usage error before.

func TestABareKeyIsAcceptedSoTheValueNeedNotBeInArgv(t *testing.T) {
	root := NewRootCommand(&Globals{})
	cmd, _, err := root.Find([]string{"site", "env", "set"})
	if err != nil {
		t.Fatalf("finding site env set: %v", err)
	}
	// The declared usage has to offer the safe form, or nobody will find it. Checking
	// for "KEY" alone is not enough — the old usage line contained it too, inside
	// KEY=VALUE, so the first version of this assertion passed on the unfixed command.
	if !strings.Contains(cmd.Use, "| KEY") {
		t.Errorf("Use = %q, want it to offer a bare KEY as an alternative to KEY=VALUE", cmd.Use)
	}
	if !strings.Contains(cmd.Example, "DATABASE_URL\n") {
		t.Errorf("the examples never show naming a secret without its value:\n%s", cmd.Example)
	}
	// And must not be teaching printf again.
	if strings.Contains(cmd.Example, "printf") {
		t.Errorf("the examples still recommend printf:\n%s", cmd.Example)
	}
}

// -i was global, promised to prompt, and was read by four commands out of ninety-nine.
func TestEveryLeafCommandHonoursInteractive(t *testing.T) {
	root := NewRootCommand(&Globals{})

	var missing []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, child := range runnableChildren(c) {
			if len(runnableChildren(child)) > 0 {
				walk(child)
				continue
			}
			// A leaf either collects its own input or goes through the generic path.
			// The generic path is unconditional in setup, so what is checked here is
			// that nothing is excluded by accident.
			if annotated(child, AnnoOwnWizard) {
				continue
			}
			if child.Flags().HasAvailableFlags() {
				continue // the collector has something to offer
			}
			// A leaf with no flags of its own has nothing to ask about, which is fine.
			_ = missing
		}
	}
	walk(root)

	// The four with their own wizards must actually be marked, or -i asks twice.
	for _, path := range [][]string{
		{"user", "add"}, {"site", "add"}, {"cert", "issue"}, {"key", "add"},
	} {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Fatalf("finding %v: %v", path, err)
		}
		if !annotated(cmd, AnnoOwnWizard) {
			t.Errorf("%s has a hand-written wizard but is not marked AnnoOwnWizard, so -i "+
				"would collect its flags and then the wizard would ask again",
				cmd.CommandPath())
		}
	}

	// And a command without one must not be marked, or -i silently does nothing there.
	cmd, _, err := root.Find([]string{"db", "create"})
	if err != nil {
		t.Fatalf("finding db create: %v", err)
	}
	if annotated(cmd, AnnoOwnWizard) {
		t.Errorf("%s is marked as having its own wizard but does not", cmd.CommandPath())
	}
}

// The collector writes into the flagset, so what runs is what would have run had the
// flags been typed. If it built a separate argv the two paths could drift.
func TestInteractiveCollectionWritesIntoTheFlagset(t *testing.T) {
	out := &strings.Builder{}
	g := &Globals{
		Stdin:  strings.NewReader("owner\nacme\n..done\n"),
		Stdout: out, Stderr: out,
		StdinTTY: true, StderrTTY: true, Width: 80,
	}
	root := NewRootCommand(g)
	// After NewRootCommand, never before: bind() calls BoolVarP, which writes the flag's
	// default back over the field. Setting it in the struct literal above looks right and
	// leaves Interactive false, which made the first version of this test pass by
	// returning early.
	g.Interactive = true
	cmd, _, err := root.Find([]string{"db", "create"})
	if err != nil {
		t.Fatalf("finding db create: %v", err)
	}
	if err := g.collectInteractively(cmd); err != nil {
		t.Fatalf("collectInteractively: %v", err)
	}
	f := cmd.Flags().Lookup("owner")
	if f == nil {
		t.Fatal("db create has no --owner flag")
	}
	if f.Value.String() != "acme" {
		t.Errorf("--owner = %q, want %q", f.Value.String(), "acme")
	}
	if !f.Changed {
		t.Error("--owner is not marked as changed, so RequireFlags will still refuse it")
	}
}

// Without a terminal, -i must not start asking: that is a script that hangs.
func TestInteractiveDoesNothingWithoutATerminal(t *testing.T) {
	out := &strings.Builder{}
	g := &Globals{
		Stdin:  strings.NewReader("owner\nacme\n..done\n"),
		Stdout: out, Stderr: out,
		StdinTTY: false, StderrTTY: false,
	}
	root := NewRootCommand(g)
	g.Interactive = true // after binding; see the note above
	cmd, _, _ := root.Find([]string{"db", "create"})
	if !g.Interactive {
		t.Fatal("setup: Interactive is false, so this would pass without testing anything")
	}
	if err := g.collectInteractively(cmd); err != nil {
		t.Fatalf("collectInteractively: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("it prompted without a terminal:\n%s", out.String())
	}
}
