package cli

import (
	"context"
	"strings"
)

// Running a sequence of ratline commands as one transaction.
//
// Two commands compose others: `ratline new` builds a stack, `ratline import` rebuilds a
// server from an export. Both want the same three properties, and each of them is a lesson
// that cost something to learn:
//
//   - Decide everything first, execute second. A composite cannot rehearse itself by running
//     its steps with --dry-run, because each step preconditions on the previous one having
//     really happened — the second is told "no such user" and the preview reports a failure
//     for something perfectly buildable. Resolving the plan without executing it is the only
//     honest answer, and it is also what makes the closing summary the commands that ran
//     rather than a second derivation that drifts from them.
//   - Carry the global flags explicitly. Each step binds a fresh root command, and binding
//     writes the flag defaults back over the fields, so --dry-run silently becomes a real run
//     part-way through if it is assumed to survive.
//   - Undo what failed, in reverse, loudly. A compensation that fails leaves something
//     behind, and the operator needs to know which thing.
//
// They compose the commands rather than the managers. Every step is the code path an
// operator gets by typing it — same validation, same refusals, same messages — so a
// composite cannot develop its own opinion about what a site is, and a flag added to
// `site add` tomorrow is available here tomorrow.

// step is one command to run, and how to take it back.
type step struct {
	what string
	argv []string
	// undo is nil for a step that must not be undone: one that built something we did not
	// create, or one whose cost has already been paid. kept says why, in a sentence the
	// preview can print — a step that is not taken back is exactly what somebody reading a
	// preview needs told, and leaving it implicit is how it gets missed.
	undo []string
	kept string
}

// plan is everything a run has decided, before any of it happens.
type plan struct {
	steps []step
	notes []string
}

func (p *plan) add(st step) { p.steps = append(p.steps, st) }
func (p *plan) note(s string) {
	p.notes = append(p.notes, s)
}

// composer executes a plan, remembering what to undo.
type composer struct {
	g *Globals
	// done records what to undo, innermost last.
	done []step
}

// inherited carries the global flags into each step.
func (c *composer) inherited() []string {
	var out []string
	if c.g.DryRun {
		out = append(out, "--dry-run")
	}
	if c.g.Yes {
		out = append(out, "--yes")
	}
	if c.g.Quiet {
		out = append(out, "--quiet")
	}
	if c.g.NoInput {
		out = append(out, "--no-input")
	}
	return out
}

// run executes one step and records how to undo it.
func (c *composer) run(ctx context.Context, st step) error {
	full := append(append([]string{}, st.argv...), c.inherited()...)
	c.g.Printf("\n→ %s\n", st.what)
	if err := c.g.runArgv(ctx, full); err != nil {
		return err
	}
	if st.undo != nil && !c.g.DryRun {
		c.done = append(c.done, st)
	}
	return nil
}

// unwind undoes what was built, most recent first.
//
// Best effort, and loud about it: a compensation that fails leaves something behind, and
// the operator needs to know which thing rather than being told the whole command failed.
func (c *composer) unwind(ctx context.Context) {
	for i := len(c.done) - 1; i >= 0; i-- {
		st := c.done[i]
		c.g.Log.Warn("undoing", "step", st.what)
		argv := append(append([]string{}, st.undo...), "--yes")
		if err := c.g.runArgv(ctx, argv); err != nil {
			c.g.Log.Error("could not undo a step; it is still there",
				"step", st.what, "fix", strings.Join(st.undo, " "), "err", err)
		}
	}
}

// execute runs the plan, unwinding everything it created on the first failure.
func (c *composer) execute(ctx context.Context, p plan) (err error) {
	defer func() {
		if err != nil && len(c.done) > 0 {
			c.g.Log.Warn("a step failed, so everything this command created is being removed",
				"created", len(c.done))
			c.unwind(ctx)
		}
	}()
	for _, st := range p.steps {
		if err = c.run(ctx, st); err != nil {
			return err
		}
	}
	return nil
}

// rehearse prints the plan for --dry-run.
//
// The steps are not run, not even with --dry-run passed down. See the note at the top of
// this file for why there is no version of that which works.
func (c *composer) rehearse(p plan, closing string) {
	if c.g.Quiet {
		return
	}
	if len(p.steps) == 0 {
		c.g.Printf("\nThere is nothing to do.\n")
		return
	}
	c.g.Printf("\nThis would run %s:\n\n", plural(len(p.steps), "command"))
	for _, st := range p.steps {
		c.g.Printf("    %s\n", commandLine(st.argv))
	}
	// Deliberately not a count. How many things come back depends on which step fails, so
	// any number printed here is wrong for every case but one — and the first version said
	// "the 3 things before it would be removed" about a three-step plan, where the most that
	// can ever be removed is two.
	if undoable(p) > 0 {
		c.g.Printf("\nIf any of them failed, everything created before it would be removed.\n")
	}
	for _, st := range p.steps {
		if st.kept != "" {
			c.g.Printf("%s\n", st.kept)
		}
	}
	c.g.Printf("\nNothing was written. %s\n", closing)
}

// commandLine renders a step for a human to copy.
//
// Only for display: nothing here is ever parsed back into a command. Quoting is applied to
// arguments containing a space so that copying the line into a shell runs what it appears to
// run, which matters for a value like --install-command 'npm ci --omit=dev'.
func commandLine(argv []string) string {
	out := make([]string, 0, len(argv)+1)
	out = append(out, "ratline")
	for _, a := range argv {
		if strings.ContainsAny(a, " \t'\"") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		out = append(out, a)
	}
	return strings.Join(out, " ")
}

func undoable(p plan) int {
	n := 0
	for _, st := range p.steps {
		if st.undo != nil {
			n++
		}
	}
	return n
}
