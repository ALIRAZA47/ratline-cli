package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// The interactive browser, generated from the command tree rather than written out.
//
// There was a menu here before, and it listed five groups with two or three verbs each —
// about a dozen of the eighty-six commands. The rest were unreachable without knowing they
// existed, which is the opposite of what a menu is for, and every command added since had
// to be remembered into it by hand. Nothing reminded anybody, so nothing was.
//
// So it walks cobra. A command that exists is in the menu because it exists; a command
// added tomorrow is in the menu tomorrow. The flags come from the same flagset the command
// parses, with the same help text, so the menu cannot describe an option the command does
// not have.

// browse walks down the command tree from cmd, asking at each level, and runs the leaf the
// operator lands on. Returning ErrCancelled means "go back up", not "fail".
func browse(ctx context.Context, g *Globals, p *prompter, cmd *cobra.Command) error {
	for {
		children := runnableChildren(cmd)
		if len(children) == 0 {
			return runLeaf(ctx, g, p, cmd)
		}

		options := make([]choice, 0, len(children)+1)
		for _, c := range children {
			label := c.Name()
			if len(c.Commands()) > 0 {
				// A group rather than a verb; say so, or the operator cannot tell why
				// choosing it asks another question instead of doing something.
				label += " …"
			}
			options = append(options, choice{Value: c.Name(), Label: label, Note: c.Short})
		}
		options = append(options, choice{Value: "..back", Label: "Back"})

		title := "ratline"
		if cmd.HasParent() {
			title = cmd.CommandPath()
		}
		picked, err := p.pick(title, options, options[0].Value)
		if err != nil {
			return err
		}
		if picked == "..back" {
			return ErrCancelled
		}
		for _, c := range children {
			if c.Name() == picked {
				// A cancelled child returns here rather than unwinding, so "Back" from a
				// verb lands on the list it was chosen from.
				if err := browse(ctx, g, p, c); err != nil && !errors.Is(err, ErrCancelled) {
					return err
				}
				break
			}
		}
	}
}

// runnableChildren is what the menu should offer: not help, not completion, not the
// hidden ones, and not a command that only exists to hang subcommands off.
func runnableChildren(cmd *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, c := range cmd.Commands() {
		if c.Hidden || c.Deprecated != "" || !c.IsAvailableCommand() {
			continue
		}
		switch c.Name() {
		case "help", "completion", "man":
			// Reachable by name; in a menu they are noise.
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// runLeaf collects a command's arguments and options, shows what it is about to run, and
// runs it.
func runLeaf(ctx context.Context, g *Globals, p *prompter, cmd *cobra.Command) error {
	p.heading(cmd.CommandPath())
	if cmd.Short != "" {
		p.note("%s", cmd.Short)
	}
	if cmd.Long != "" && cmd.Long != cmd.Short {
		// The first paragraph only. The full text is a page; this is a prompt.
		if para := strings.SplitN(strings.TrimSpace(cmd.Long), "\n\n", 2)[0]; para != "" {
			p.note("%s", para)
		}
	}
	fmt.Fprintln(p.out)

	args, err := askPositionals(p, cmd)
	if err != nil {
		return err
	}
	flags, err := askOptions(p, cmd)
	if err != nil {
		return err
	}

	argv := append(commandWords(cmd), args...)
	argv = append(argv, flags...)

	fields := [][2]string{{"command", "ratline " + strings.Join(argv, " ")}}
	switch action, err := p.summary("About to run", fields, append([]string{"ratline"}, argv...)); {
	case err != nil:
		return err
	case action == actionCancel:
		return ErrCancelled
	case action == actionEdit:
		// Edit means "ask me again", which is the whole loop.
		return runLeaf(ctx, g, p, cmd)
	}

	return g.runArgv(ctx, argv)
}

// commandWords is the path from the root, without the binary name.
func commandWords(cmd *cobra.Command) []string {
	var parts []string
	for c := cmd; c != nil && c.HasParent(); c = c.Parent() {
		parts = append([]string{c.Name()}, parts...)
	}
	return parts
}

// askPositionals reads the argument names out of the command's Use line and asks for each.
//
// `Use` is the only declaration of them cobra has — "add <username>", "show
// <fingerprint|label>" — so the menu asks for exactly what the help page promises, and an
// argument renamed in help is renamed in the menu.
func askPositionals(p *prompter, cmd *cobra.Command) ([]string, error) {
	var out []string
	for _, name := range positionalNames(cmd.Use) {
		optional := strings.HasPrefix(name, "[")
		clean := strings.Trim(name, "<>[]")
		label := clean
		if optional {
			label += " (optional)"
		}
		v, err := p.ask(label+":", "", func(s string) error {
			if s == "" && !optional {
				return rlerr.Usagef("%s is required", clean)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if v != "" {
			out = append(out, v)
		}
	}
	return out, nil
}

// positionalNames pulls <these> and [these] out of a Use line.
//
// The bracket is what identifies an argument, so the verb needs no special case — it was
// skipped with a [1:] here, which did nothing except panic on an empty Use line. A command
// with no Use is malformed, but a menu is the wrong place to discover that.
func positionalNames(use string) []string {
	var out []string
	for _, field := range strings.Fields(use) {
		if strings.HasPrefix(field, "<") || strings.HasPrefix(field, "[") {
			out = append(out, field)
		}
	}
	return out
}

// askOptions offers the command's own flags, one at a time, until the operator is done.
//
// A list rather than a march through all of them: `site add` has twenty, and being asked
// twenty questions to create a static site is worse than reading the help. Every one is
// still reachable — which is the point — but the default is to set none and take the
// defaults the command already documents.
func askOptions(p *prompter, cmd *cobra.Command) ([]string, error) {
	var available []*pflag.Flag
	cmd.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Deprecated != "" {
			return
		}
		available = append(available, f)
	})
	if len(available) == 0 {
		return nil, nil
	}
	sort.Slice(available, func(i, j int) bool { return available[i].Name < available[j].Name })

	set := map[string]string{}
	for {
		options := []choice{{Value: "..done", Label: "Run it", Note: describeChosen(set)}}
		for _, f := range available {
			note := f.Usage
			if v, ok := set[f.Name]; ok {
				note = "currently " + v
			} else if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "[]" {
				note += "  (default " + f.DefValue + ")"
			}
			options = append(options, choice{Value: f.Name, Label: "--" + f.Name, Note: note})
		}
		picked, err := p.pick("Options", options, "..done")
		if err != nil {
			return nil, err
		}
		if picked == "..done" {
			break
		}
		f := cmd.NonInheritedFlags().Lookup(picked)
		if f == nil {
			continue
		}
		v, err := askFlag(p, f)
		if err != nil {
			return nil, err
		}
		if v == "" {
			delete(set, f.Name)
			continue
		}
		set[f.Name] = v
	}

	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names)*2)
	for _, n := range names {
		if isBoolFlag(cmd.NonInheritedFlags().Lookup(n)) {
			// A bool's value is in the flag itself; `--force true` is not how it is spelled.
			out = append(out, "--"+n)
			continue
		}
		out = append(out, "--"+n, set[n])
	}
	return out, nil
}

func describeChosen(set map[string]string) string {
	if len(set) == 0 {
		return "with no options"
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, "--"+n)
	}
	sort.Strings(names)
	return "with " + strings.Join(names, " ")
}

// askFlag asks for one flag's value, in the shape its type calls for.
func askFlag(p *prompter, f *pflag.Flag) (string, error) {
	if isBoolFlag(f) {
		on, err := p.confirm("--"+f.Name+"?  "+f.Usage, f.DefValue == "true")
		if err != nil {
			return "", err
		}
		if !on {
			return "", nil
		}
		return "true", nil
	}
	def := ""
	if f.DefValue != "[]" {
		def = f.DefValue
	}
	// Validated by the command itself when it runs. Re-implementing each flag's rule here
	// would be a second copy of every validator, and they would disagree.
	return p.ask("--"+f.Name+"  "+p.dim(f.Usage)+"\n  value:", def, nil)
}

func isBoolFlag(f *pflag.Flag) bool {
	return f != nil && f.Value.Type() == "bool"
}

// runArgv executes a fully-formed argument list against a fresh root command.
//
// The same trick runFromMenu used: setup has already run for this process, so the second
// root must not take the lock again or it deadlocks against itself.
func (g *Globals) runArgv(ctx context.Context, argv []string) error {
	root := NewRootCommand(g)
	root.SetArgs(argv)
	root.SetOut(g.Stdout)
	root.SetErr(g.Stderr)
	root.PersistentPreRunE = nil
	return root.ExecuteContext(ctx)
}
