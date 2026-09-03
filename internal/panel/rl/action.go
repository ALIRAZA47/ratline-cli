package rl

import (
	"sort"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
)

// Action is one thing the panel can do, as the browser needs to understand it.
//
// It is the schema and the policy joined: the flags come from the binary, so a form
// can never offer one that does not exist, and the classification comes from the
// table, so the same form knows whether to ask for a typed confirmation first. The
// front end renders these; it holds no list of its own.
type Action struct {
	ID          string       `json:"id"`
	Verb        string       `json:"verb"`
	Title       string       `json:"title"`
	Summary     string       `json:"summary"`
	Description string       `json:"description,omitempty"`
	Group       string       `json:"group"`
	Args        []ActionArg  `json:"args,omitempty"`
	Flags       []ActionFlag `json:"flags,omitempty"`
	Mutates     bool         `json:"mutates"`
	Destructive bool         `json:"destructive"`
	Long        bool         `json:"long"`
	MinRole     string       `json:"min_role"`
	// Stdin describes the value this action reads from standard input, if any.
	// The form renders it as a password field and the value never reaches argv.
	Stdin    *StdinSpec `json:"stdin,omitempty"`
	Examples []string   `json:"examples,omitempty"`
}

// ActionArg is one positional argument.
type ActionArg struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

// ActionFlag is one flag, as a form field.
type ActionFlag struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Usage      string `json:"usage"`
	Default    string `json:"default,omitempty"`
	Required   bool   `json:"required"`
	Repeatable bool   `json:"repeatable,omitempty"`
	// Runtime marks a flag that only applies to one runtime, read out of the
	// usage text ratline already writes: "node, bun: the file that starts the
	// server". Without it a form for a static site offers --app-module, and the
	// operator has to know which of forty flags are theirs.
	Runtime []string `json:"runtime,omitempty"`
}

// ID is the verb with spaces replaced, so it survives a URL: "site.deploy".
func actionID(verb string) string { return strings.ReplaceAll(verb, " ", ".") }

// VerbFromID is the inverse.
func VerbFromID(id string) string { return strings.ReplaceAll(id, ".", " ") }

// Actions returns every action the given role may run, in a stable order.
//
// Filtered by role rather than returned-and-hidden: an admin's browser should not be
// holding the list of super-admin operations, and a UI that hides a button is a UI
// that shows it to anybody who opens the developer tools.
func Actions(cat *Catalogue, role string) []*Action {
	verbs := make([]string, 0, len(cat.Leaves))
	for verb := range cat.Leaves {
		verbs = append(verbs, verb)
	}
	sort.Strings(verbs)

	out := make([]*Action, 0, len(verbs))
	for _, verb := range verbs {
		cmd := cat.Leaves[verb]
		policy, _ := PolicyFor(verb, cmd)
		if policy.Denied || !store.AtLeast(role, policy.MinRole) {
			continue
		}
		out = append(out, buildAction(verb, cmd, policy))
	}
	return out
}

// Lookup resolves one action by id, for a role. The second result is false when it
// does not exist, is denied, or is above the role — one answer for all three, because
// telling somebody an action exists but is not theirs is telling them the shape of
// the surface above them.
func Lookup(cat *Catalogue, id, role string) (*Action, Policy, bool) {
	verb := VerbFromID(id)
	cmd, ok := cat.Leaves[verb]
	if !ok {
		return nil, Policy{}, false
	}
	policy, _ := PolicyFor(verb, cmd)
	if policy.Denied || !store.AtLeast(role, policy.MinRole) {
		return nil, Policy{}, false
	}
	return buildAction(verb, cmd, policy), policy, true
}

func buildAction(verb string, cmd *SchemaCommand, policy Policy) *Action {
	a := &Action{
		ID:          actionID(verb),
		Verb:        verb,
		Title:       titleFor(verb),
		Summary:     cmd.Summary,
		Description: cmd.Description,
		Group:       policy.Group,
		Mutates:     cmd.Mutates,
		Destructive: policy.Destructive,
		Long:        policy.Long,
		MinRole:     policy.MinRole,
		Examples:    cmd.Examples,
	}
	a.Stdin = policy.Stdin
	// The policy's list wins where it has one: a usage line that is prose rather
	// than a list of placeholders parses into nonsense, and a form built from that
	// would invite somebody to type a secret into a positional argument.
	names := cmd.Args
	if len(policy.Args) > 0 {
		names = policy.Args
	}
	for _, name := range names {
		a.Args = append(a.Args, ActionArg{
			Name:     strings.Trim(name, "<>[]"),
			Required: strings.HasPrefix(name, "<"),
		})
	}
	for _, f := range cmd.Flags {
		if isGlobalFlagName(f.Name) {
			continue
		}
		a.Flags = append(a.Flags, ActionFlag{
			Name:       f.Name,
			Type:       f.Type,
			Usage:      f.Usage,
			Default:    f.Default,
			Required:   f.Required,
			Repeatable: f.Repeatable,
			Runtime:    runtimesIn(f.Usage),
		})
	}
	return a
}

// titleFor turns "site deploy-key rotate" into "Rotate deploy key".
//
// Derived rather than written down, for the same reason everything else here is: a
// hand-kept title table would be one more thing to forget when a command is renamed,
// and the verb is already the clearest description of what the button does.
func titleFor(verb string) string {
	parts := strings.Fields(verb)
	if len(parts) == 0 {
		return verb
	}
	action := strings.ReplaceAll(parts[len(parts)-1], "-", " ")
	subject := strings.Join(parts[:len(parts)-1], " ")
	subject = strings.ReplaceAll(subject, "-", " ")
	title := strings.TrimSpace(action + " " + subject)
	return strings.ToUpper(title[:1]) + title[1:]
}

// knownRuntimes are the prefixes ratline uses to mark a runtime-specific flag.
var knownRuntimes = []string{"static", "node", "bun", "python"}

// runtimesIn reads the runtimes a flag applies to out of its own help text.
//
// ratline already writes "node, bun: the file that starts the server", so the fact is
// there and does not need a second list to fall out of step with it. A form for a
// static site can then leave out the thirty flags that belong to the other three.
func runtimesIn(usage string) []string {
	prefix, _, found := strings.Cut(usage, ":")
	if !found || len(prefix) > 32 {
		return nil
	}
	var out []string
	for _, part := range strings.Split(prefix, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		for _, rt := range knownRuntimes {
			if part == rt {
				out = append(out, rt)
			}
		}
	}
	// Every runtime is the same as none: a flag that says "static, node, bun,
	// python:" is not runtime-specific, it is just documented oddly.
	if len(out) == len(knownRuntimes) {
		return nil
	}
	return out
}
