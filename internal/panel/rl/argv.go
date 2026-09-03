package rl

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// Request is one thing somebody asked the panel to run.
type Request struct {
	// Verb is the command path without the tool name: "site deploy".
	Verb string
	// Args are the positional arguments, in order.
	Args []string
	// Flags are flag values by flag name, without the leading dashes. A bool is a
	// bool, a repeatable flag is a []string; everything else is a string.
	Flags map[string]any
	// Secret travels on stdin and never appears in argv.
	Secret string
	// SecretKey is the name that goes with it, for the one command whose stdin is
	// an assignment rather than a bare value.
	SecretKey string
	// DryRun asks ratline to describe the mutation without making it.
	DryRun bool
	// Confirmed records that a human typed the target's name back. It is what
	// --yes is standing in for, and a destructive request without it is refused
	// before an argv is built.
	Confirmed bool
}

// maxValueLength bounds a single argument well below the 4096 bytes execve accepts,
// because nothing ratline takes as a flag is a kilobyte long and a request that says
// otherwise is not a mistake somebody made in a form.
const maxValueLength = 2048

// BuildArgv turns a request into the argv slice to execute.
//
// Every element is constructed here, one at a time, from a value that has been
// checked against the flag the binary says it has. There is no string that gets split
// and no shell to split it, so the classes of bug that produce command injection have
// nowhere to occur — the worst a hostile value can do is fail validation.
//
// Three details are doing real work:
//
//   - Flags are emitted as a single --name=value element rather than two. The two-
//     element form lets a value that begins with a dash be read as the next flag; the
//     joined form cannot be, whatever the value is.
//   - Positional arguments come last, after a bare --. Without it, `site show` given
//     a "domain" of --config=/tmp/mine.yaml would be handed a different configuration
//     file to run against.
//   - --json --no-input are always present. The panel has no terminal to answer a
//     prompt with, and a command that decided to ask one would otherwise hang holding
//     the global lock.
func BuildArgv(cat *Catalogue, policy Policy, req Request) ([]string, error) {
	cmd, ok := cat.Leaves[req.Verb]
	if !ok {
		return nil, rlerr.Usagef("%q is not a ratline command", req.Verb).
			WithHint("the installed ratline may be older or newer than this panel expects")
	}
	if policy.Denied {
		return nil, rlerr.Usagef("%q cannot be run from the panel: %s", req.Verb, policy.DeniedWhy)
	}
	if policy.Destructive && !req.Confirmed && !req.DryRun {
		return nil, rlerr.Preconditionf("%q needs the target's name typed back before it runs", req.Verb)
	}

	argv := strings.Fields(req.Verb)

	// The globals, first and unconditionally.
	argv = append(argv, "--json", "--no-input")
	if req.DryRun {
		argv = append(argv, "--dry-run")
	} else if cmd.Mutates {
		// Honest rather than convenient: --yes suppresses a prompt, and the prompt
		// has already happened — in the browser, where the operator confirmed the
		// action and, for a destructive one, typed the name back.
		argv = append(argv, "--yes")
	}

	// A secret is never a flag value. --stdin is how ratline is told to read one,
	// and it is refused for a command that has no such flag rather than passed and
	// ignored — the second of which would run the command without the value and
	// report success.
	if req.Secret != "" {
		if policy.Stdin == nil {
			return nil, rlerr.Usagef("%q does not read a value from standard input", req.Verb).
				WithHint("this is a panel bug: the action was not declared as carrying one")
		}
		if _, ok := cat.Flag(req.Verb, "stdin"); !ok {
			return nil, rlerr.Usagef("ratline %s has no --stdin flag", req.Verb).
				WithHint("the installed ratline may be older than this panel expects")
		}
		argv = append(argv, "--stdin")
	}

	// Sorted, so the same request always produces the same argv. The audit record
	// and the job transcript are read side by side with what somebody ran by hand,
	// and a map's iteration order would make two identical actions look different.
	names := make([]string, 0, len(req.Flags))
	for name := range req.Flags {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if isGlobalFlagName(name) {
			// The panel decides these, not the request. A caller that could set
			// --config could point ratline at a configuration file naming different
			// paths for everything it manages.
			return nil, rlerr.Usagef("--%s is set by the panel and cannot be supplied", name)
		}
		flag, ok := cat.Flag(req.Verb, name)
		if !ok {
			return nil, rlerr.Usagef("ratline %s has no --%s flag", req.Verb, name).
				WithHint("the panel builds its forms from 'ratline schema'; reload the page")
		}
		rendered, err := renderFlag(flag, req.Flags[name])
		if err != nil {
			return nil, err
		}
		argv = append(argv, rendered...)
	}

	if len(req.Args) > 0 {
		argv = append(argv, "--")
		for i, a := range req.Args {
			if err := checkValue(fmt.Sprintf("argument %d", i+1), a); err != nil {
				return nil, err
			}
			argv = append(argv, a)
		}
	}

	// The same check ratline applies to everything it executes. Reached only by a
	// value that got past the per-field checks above, so it is a backstop rather
	// than the guard — but a backstop on the boundary to execve is worth having.
	if err := system.ValidateArgv(argv); err != nil {
		return nil, err
	}
	return argv, nil
}

// renderFlag turns one typed value into zero, one or several argv elements.
func renderFlag(flag SchemaFlag, value any) ([]string, error) {
	switch flag.Type {
	case "bool":
		on, err := asBool(flag.Name, value)
		if err != nil {
			return nil, err
		}
		switch {
		case on:
			return []string{"--" + flag.Name}, nil
		case flag.Default == "true":
			// A flag that defaults to on has to be turned off explicitly, or the
			// form's unticked box would silently do nothing.
			return []string{"--" + flag.Name + "=false"}, nil
		default:
			return nil, nil
		}

	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		n, err := asInt(flag.Name, value)
		if err != nil {
			return nil, err
		}
		return []string{"--" + flag.Name + "=" + strconv.FormatInt(n, 10)}, nil

	case "stringArray", "stringSlice":
		items, err := asStrings(flag.Name, value)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(items))
		for _, item := range items {
			if item == "" {
				continue
			}
			if err := checkValue("--"+flag.Name, item); err != nil {
				return nil, err
			}
			out = append(out, "--"+flag.Name+"="+item)
		}
		return out, nil

	default:
		s, err := asString(flag.Name, value)
		if err != nil {
			return nil, err
		}
		if s == "" {
			// An empty box means "leave it alone", not "set it to nothing".
			return nil, nil
		}
		if err := checkValue("--"+flag.Name, s); err != nil {
			return nil, err
		}
		return []string{"--" + flag.Name + "=" + s}, nil
	}
}

// checkValue is the blanket check every value passes before it can become argv.
//
// It is not domain knowledge — ratline holds that, and applies it again where the
// value enters a manager. This is the part that has to be true of *any* value
// whatever it means: no control characters, nothing long enough to be an attack on
// the argument list.
func checkValue(field, v string) error {
	if err := validate.NoControlChars(field, v); err != nil {
		return err
	}
	if len(v) > maxValueLength {
		return rlerr.Usagef("%s is %d bytes long; the limit is %d", field, len(v), maxValueLength)
	}
	return nil
}

func asString(name string, v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(t), nil
	case bool:
		return strconv.FormatBool(t), nil
	case float64:
		// Every number in a JSON body arrives as a float64.
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10), nil
		}
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	default:
		return "", rlerr.Usagef("--%s expects text", name)
	}
}

func asBool(name string, v any) (bool, error) {
	switch t := v.(type) {
	case nil:
		return false, nil
	case bool:
		return t, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "", "false", "0", "no", "off":
			return false, nil
		case "true", "1", "yes", "on":
			return true, nil
		}
		return false, rlerr.Usagef("--%s expects true or false", name)
	default:
		return false, rlerr.Usagef("--%s expects true or false", name)
	}
}

func asInt(name string, v any) (int64, error) {
	switch t := v.(type) {
	case nil:
		return 0, nil
	case float64:
		if t != float64(int64(t)) {
			return 0, rlerr.Usagef("--%s expects a whole number", name)
		}
		return int64(t), nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		if err != nil {
			return 0, rlerr.Usagef("--%s expects a whole number", name)
		}
		return n, nil
	default:
		return 0, rlerr.Usagef("--%s expects a whole number", name)
	}
}

func asStrings(name string, v any) ([]string, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(t) == "" {
			return nil, nil
		}
		return []string{strings.TrimSpace(t)}, nil
	case []string:
		return t, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, err := asString(name, item)
			if err != nil {
				return nil, err
			}
			if s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	default:
		return nil, rlerr.Usagef("--%s expects a list of values", name)
	}
}

// globalFlagNames are the flags the panel sets for itself. A request that could set
// any of them could change what the command means: --config points ratline at a
// different configuration, --dry-run turns a real operation into a rehearsal (and,
// worse, the reverse), and --yes is the confirmation the browser already collected.
var globalFlagNames = map[string]bool{
	"json": true, "no-input": true, "yes": true, "y": true, "dry-run": true,
	"config": true, "interactive": true, "i": true, "quiet": true, "q": true,
	"verbose": true, "v": true, "stdin": true, "help": true, "h": true,
}

func isGlobalFlagName(name string) bool { return globalFlagNames[name] }

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
