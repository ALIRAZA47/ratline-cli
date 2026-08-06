package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// A required flag the interactive layer does not know about is a trap: the menu offers it
// among the optional extras, the operator takes the defaults, confirms a summary, and the
// command refuses for something it never asked about. `db create` did that without
// --owner and `db user add` without --database.
//
// Enforcement is hand-written, because the messages are worth writing. This is what stops
// the hand-written half and the declared half drifting apart.

var (
	// `if x == "" { return rlerr.Usagef("--owner is required")`
	inlineRequired = regexp.MustCompile(`rlerr\.Usagef\("--([a-z][a-z-]*) is required"\)`)
	// `RequireFlags(cmd, g, "user", "runtime")`
	requireFlagsCall = regexp.MustCompile(`RequireFlags\(cmd, g,\s*((?:"[a-z][a-z-]*",?\s*)+)\)`)
	quoted           = regexp.MustCompile(`"([a-z][a-z-]*)"`)
)

// enforcedFlagNames reads the source for every flag a command refuses to run without.
//
// Reading the source rather than the command tree because that is where the two forms of
// enforcement live, and a test that only walked the tree could not see either.
func enforcedFlagNames(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "cmd_") || !strings.HasSuffix(e.Name(), ".go") ||
			strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Clean(e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		src := string(body)
		for _, m := range inlineRequired.FindAllStringSubmatch(src, -1) {
			out[m[1]] = true
		}
		for _, m := range requireFlagsCall.FindAllStringSubmatch(src, -1) {
			for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
				out[q[1]] = true
			}
		}
	}
	if len(out) < 8 {
		t.Fatalf("only found %d enforced flags (%v); the patterns have stopped matching and "+
			"this test would pass on anything", len(out), out)
	}
	return out
}

// markedFlagNames walks the command tree for everything Required() marked.
func markedFlagNames(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
			if requiredFlag(f) {
				out[f.Name] = true
			}
		})
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(NewRootCommand(&Globals{}))
	return out
}

func TestEveryEnforcedFlagIsMarkedRequired(t *testing.T) {
	enforced := enforcedFlagNames(t)
	marked := markedFlagNames(t)

	var missing []string
	for name := range enforced {
		if !marked[name] {
			missing = append(missing, "--"+name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("these flags are enforced as required but not marked with Required(), so the "+
			"menu offers them as optional extras and only refuses after the operator "+
			"confirms: %s", strings.Join(missing, ", "))
	}
	t.Logf("%d enforced flags, %d marked", len(enforced), len(marked))
}

// The menu must ask for a required flag rather than listing it, which is the behaviour
// that was missing.
func TestTheMenuAsksForRequiredFlagsBeforeOfferingTheRest(t *testing.T) {
	root := NewRootCommand(&Globals{})
	cmd, _, err := root.Find([]string{"db", "create"})
	if err != nil {
		t.Fatalf("finding db create: %v", err)
	}

	// Answer the required --owner, then leave the picker immediately.
	p, buf := scriptedPrompter("acme", "..done")
	got, err := askOptions(p, cmd)
	if err != nil {
		t.Fatalf("askOptions: %v", err)
	}
	if strings.Join(got, " ") != "--owner acme" {
		t.Errorf("askOptions = %v, want [--owner acme]: a required flag must be collected "+
			"even when the operator sets no optional extras", got)
	}
	// It has to be asked before the list, or the operator can run without answering it.
	transcript := buf.String()
	ownerAt := strings.Index(transcript, "--owner")
	listAt := strings.Index(transcript, "Run it")
	if ownerAt < 0 || listAt < 0 || ownerAt > listAt {
		t.Errorf("--owner was not asked before the options list:\n%s", transcript)
	}
}
