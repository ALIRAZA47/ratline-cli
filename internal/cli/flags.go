package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// RequireFlags checks that every named flag was supplied.
//
// On a terminal the error points at the wizard, because an operator who forgot
// --runtime does not want to read a usage page — they want to be asked. Without
// a terminal it is a plain exit-2 listing every missing flag at once, so a CI
// job's log shows the whole problem after one run.
func RequireFlags(cmd *cobra.Command, g *Globals, names ...string) error {
	var missing []string
	for _, n := range names {
		f := cmd.Flags().Lookup(n)
		if f == nil {
			return rlerr.Genericf("internal error: %q has no flag named --%s", cmd.CommandPath(), n)
		}
		if !f.Changed {
			missing = append(missing, "--"+n)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	err := rlerr.Usagef("missing %s", joinHuman(missing))
	if g.CanPrompt() {
		return err.WithHint("run with -i for a guided setup, or see '%s --help'", cmd.CommandPath())
	}
	return err.WithHint("see '%s --help'", cmd.CommandPath())
}

// ExclusiveFlags rejects flags that contradict each other.
func ExclusiveFlags(cmd *cobra.Command, names ...string) error {
	var set []string
	for _, n := range names {
		if f := cmd.Flags().Lookup(n); f != nil && f.Changed {
			set = append(set, "--"+n)
		}
	}
	if len(set) > 1 {
		return rlerr.Usagef("%s contradict each other", joinHuman(set))
	}
	return nil
}

// RequireOneOf insists that exactly one of a set of alternatives is present.
func RequireOneOf(cmd *cobra.Command, names ...string) (string, error) {
	var set []string
	for _, n := range names {
		if f := cmd.Flags().Lookup(n); f != nil && f.Changed {
			set = append(set, n)
		}
	}
	switch len(set) {
	case 1:
		return set[0], nil
	case 0:
		return "", rlerr.Usagef("one of %s is required", joinHuman(dashed(names)))
	default:
		return "", rlerr.Usagef("%s contradict each other", joinHuman(dashed(set)))
	}
}

func dashed(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "--" + n
	}
	return out
}

// joinHuman renders a list the way a person would read it aloud.
func joinHuman(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}
