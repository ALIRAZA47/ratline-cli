package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// RequireFlags resolves every named flag, asking for any that is missing.
//
// On a terminal it asks. An operator who forgot --runtime does not want a usage page and
// does not want to retype the whole command; they want to be asked the one question. The
// answer is written back into the flagset, so the command that runs afterwards is exactly
// the command that would have run had the flag been typed — there is no second path
// through the logic, which is the property that keeps the wizard honest.
//
// Without a terminal it is unchanged: a plain exit-2 naming every missing flag at once, so
// one CI run shows the whole problem. --no-input and --json take that path too, because a
// script that starts asking questions is a script that hangs.
func RequireFlags(cmd *cobra.Command, g *Globals, names ...string) error {
	var missing []string
	for _, n := range names {
		f := cmd.Flags().Lookup(n)
		if f == nil {
			return rlerr.Genericf("internal error: %q has no flag named --%s", cmd.CommandPath(), n)
		}
		if !f.Changed {
			missing = append(missing, n)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)

	if g.CanPrompt() {
		p := newPrompter(g)
		p.note("%s needs %s.", cmd.CommandPath(), joinHuman(dashed(missing)))
		for _, n := range missing {
			f := cmd.Flags().Lookup(n)
			v, err := askFlag(p, f)
			if err != nil {
				return err
			}
			if v == "" {
				return rlerr.Usagef("--%s is required", n).
					WithHint("see '%s --help'", cmd.CommandPath())
			}
			if err := cmd.Flags().Set(n, v); err != nil {
				return rlerr.Wrap(err, rlerr.CodeUsage, "--%s", n)
			}
		}
		return nil
	}
	return rlerr.Usagef("missing %s", joinHuman(dashed(missing))).
		WithHint("see '%s --help'", cmd.CommandPath())
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
