package system

import (
	"strings"
	"unicode"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// Limits on operator-supplied command strings (--start-command, --build-command).
const (
	maxCommandBytes = 4096
	maxCommandWords = 128
	maxWordBytes    = 1024
)

// shellOperators are the constructs that only a shell can interpret. ratline
// execs argv slices directly, so a command containing any of these would either
// be silently passed as a literal argument (confusing) or, if we ever handed it
// to a shell, become command injection (dangerous). Refuse both outcomes and
// point the operator at a script in their own repository.
var shellOperators = []struct{ token, why string }{
	{"$(", "command substitution"},
	{"${", "variable expansion"},
	{"`", "command substitution"},
	{"&&", "command chaining"},
	{"||", "command chaining"},
	{">>", "output redirection"},
	{"<<", "here-document"},
	{";", "command separator"},
	{"|", "pipe"},
	{"&", "backgrounding"},
	{">", "output redirection"},
	{"<", "input redirection"},
	{"$", "variable expansion"},
	{"\n", "newline"},
	{"\r", "carriage return"},
	{"\x00", "NUL byte"},
}

// shellFirstWords must never be the program: they exist to reinterpret their
// arguments, which would defeat argv-only execution.
var shellFirstWords = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "csh": true,
	"tcsh": true, "fish": true, "env": true, "eval": true, "exec": true,
	"nohup": true, "setsid": true, "sudo": true, "su": true, "doas": true,
	"xargs": true, "time": true, "watch": true, "script": true,
}

// glob characters pass through literally because there is no shell to expand
// them. That is usually a mistake in the operator's command, so warn.
const globChars = "*?[]{}~"

// ParsedCommand is an operator command string turned into an argv slice.
type ParsedCommand struct {
	Raw      string
	Argv     []string
	Warnings []string
}

// Program is the first word of the command.
func (p *ParsedCommand) Program() string {
	if len(p.Argv) == 0 {
		return ""
	}
	return p.Argv[0]
}

// ParseCommand splits an operator-supplied command into argv, refusing anything
// that would need a shell.
//
// Quoting is supported so that arguments may contain spaces: 'single' quotes
// and "double" quotes are literal (there is no expansion to suppress), and a
// backslash escapes the next character outside single quotes.
func ParseCommand(s string) (*ParsedCommand, error) {
	if strings.TrimSpace(s) == "" {
		return nil, rlerr.Usagef("command is empty")
	}
	if len(s) > maxCommandBytes {
		return nil, rlerr.Usagef("command is %d bytes long, which exceeds the %d-byte limit", len(s), maxCommandBytes)
	}
	for _, op := range shellOperators {
		if i := strings.Index(s, op.token); i >= 0 {
			return nil, rlerr.Usagef("command contains %q (%s) at position %d, which needs a shell", op.token, op.why, i+1).
				WithHint("put the pipeline in a script inside your repository and reference that script instead, " +
					"for example --start-command \"./bin/start\"")
		}
	}
	for _, r := range s {
		if r != '\t' && unicode.IsControl(r) {
			return nil, rlerr.Usagef("command contains the control character %q", r)
		}
	}

	argv, err := splitWords(s)
	if err != nil {
		return nil, err
	}
	if len(argv) == 0 {
		return nil, rlerr.Usagef("command is empty")
	}
	if len(argv) > maxCommandWords {
		return nil, rlerr.Usagef("command has %d words, which exceeds the limit of %d", len(argv), maxCommandWords)
	}
	for i, w := range argv {
		if len(w) > maxWordBytes {
			return nil, rlerr.Usagef("word %d of the command is %d bytes long, which exceeds the %d-byte limit", i+1, len(w), maxWordBytes)
		}
	}

	prog := argv[0]
	if strings.HasPrefix(prog, "-") {
		return nil, rlerr.Usagef("the command starts with %q, which looks like a flag rather than a program", prog)
	}
	base := prog
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	if shellFirstWords[base] {
		return nil, rlerr.Usagef("%q may not be the command's program", prog).
			WithHint("ratline executes commands directly rather than through a shell; " +
				"reference the real program, or a script in your repository")
	}

	p := &ParsedCommand{Raw: s, Argv: argv}
	if strings.ContainsAny(s, globChars) {
		p.Warnings = append(p.Warnings,
			"the command contains glob or brace characters; they are passed through literally because no shell expands them")
	}
	return p, nil
}

// splitWords implements the quoting rules described on ParseCommand.
func splitWords(s string) ([]string, error) {
	var (
		out     []string
		cur     strings.Builder
		started bool
	)
	const (
		plain = iota
		single
		double
	)
	state := plain
	escaped := false

	flush := func() {
		if started {
			out = append(out, cur.String())
			cur.Reset()
			started = false
		}
	}

	for i, r := range s {
		if escaped {
			cur.WriteRune(r)
			started = true
			escaped = false
			continue
		}
		switch state {
		case plain:
			switch r {
			case '\\':
				escaped = true
			case '\'':
				state = single
				started = true
			case '"':
				state = double
				started = true
			case ' ', '\t':
				flush()
			default:
				cur.WriteRune(r)
				started = true
			}
		case single:
			if r == '\'' {
				state = plain
			} else {
				cur.WriteRune(r)
			}
		case double:
			switch r {
			case '"':
				state = plain
			case '\\':
				escaped = true
			default:
				cur.WriteRune(r)
			}
		}
		_ = i
	}
	if escaped {
		return nil, rlerr.Usagef("command ends with a trailing backslash")
	}
	if state != plain {
		return nil, rlerr.Usagef("command has an unterminated quote")
	}
	flush()
	return out, nil
}
