package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// The interactive layer is a flag collector, never a second implementation:
// every wizard resolves values and then calls the same code the flags reach.
// That is enforced structurally — a wizard's only output is an argument list,
// which it prints before it runs so an operator can graduate to scripting it.
//
// Prompts are plain and line-based rather than a full-screen form. The
// requirements demand a working experience over a bare SSH session, inside tmux,
// with NO_COLOR, with TERM=dumb and in a 60-column window, so the simple path
// has to exist in any case; having only one path means the degraded case is the
// tested case.

// prompter reads answers from a terminal, validating each field where it stands.
type prompter struct {
	g   *Globals
	in  *bufio.Reader
	out io.Writer
}

func newPrompter(g *Globals) *prompter {
	return &prompter{g: g, in: bufio.NewReader(g.Stdin), out: g.Stderr}
}

// ErrCancelled is returned when an operator abandons a wizard. Nothing has been
// mutated at that point, by construction: the wizard collects everything before
// the first system call.
var ErrCancelled = rlerr.New(rlerr.CodeOK, "cancelled; nothing was changed")

func (p *prompter) dim(s string) string {
	if !p.g.Color {
		return s
	}
	return "\033[2m" + s + "\033[0m"
}

func (p *prompter) bold(s string) string {
	if !p.g.Color {
		return s
	}
	return "\033[1m" + s + "\033[0m"
}

func (p *prompter) heading(title string) {
	fmt.Fprintf(p.out, "\n%s\n%s\n", p.bold(title), p.dim(strings.Repeat("─", min(len(title), p.width()))))
}

func (p *prompter) note(format string, a ...any) {
	fmt.Fprintf(p.out, "%s\n", p.dim(fmt.Sprintf(format, a...)))
}

func (p *prompter) width() int {
	if p.g.Width > 0 {
		return p.g.Width
	}
	return 72
}

// readLine reads one answer. An EOF means the terminal went away, which is a
// cancellation rather than an error to report as a failure.
func (p *prompter) readLine() (string, error) {
	line, err := p.in.ReadString('\n')
	if err != nil && line == "" {
		return "", ErrCancelled
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// ask prompts for a single value, re-asking until validate accepts it. The
// validator's message is shown next to the field, not after the whole form.
func (p *prompter) ask(label, def string, validate func(string) error) (string, error) {
	for {
		if def != "" {
			fmt.Fprintf(p.out, "%s %s ", label, p.dim("["+def+"]"))
		} else {
			fmt.Fprintf(p.out, "%s ", label)
		}
		answer, err := p.readLine()
		if err != nil {
			return "", err
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			answer = def
		}
		if answer == "" && validate == nil {
			return "", nil
		}
		if validate != nil {
			if err := validate(answer); err != nil {
				fmt.Fprintf(p.out, "  %s\n", p.problem(err))
				continue
			}
		}
		return answer, nil
	}
}

func (p *prompter) problem(err error) string {
	msg := err.Error()
	if hint := rlerr.Hint(err); hint != "" {
		msg += " — " + hint
	}
	if !p.g.Color {
		return msg
	}
	return "\033[33m" + msg + "\033[0m"
}

// confirm asks a yes/no question with an explicit default.
func (p *prompter) confirm(question string, def bool) (bool, error) {
	suffix := "[y/N]"
	if def {
		suffix = "[Y/n]"
	}
	for {
		fmt.Fprintf(p.out, "%s %s ", question, p.dim(suffix))
		answer, err := p.readLine()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Fprintln(p.out, "  answer y or n")
		}
	}
}

// choice is one option in a picker.
type choice struct {
	Value string
	Label string
	Note  string
}

// pick presents a numbered list. Numbers rather than arrow keys, so it behaves
// identically over a raw SSH session and inside tmux.
func (p *prompter) pick(label string, options []choice, def string) (string, error) {
	if len(options) == 0 {
		return "", rlerr.Preconditionf("there is nothing to choose from for %q", label)
	}
	defIndex := 0
	for i, o := range options {
		if o.Value == def {
			defIndex = i
		}
	}
	fmt.Fprintf(p.out, "%s\n", label)
	for i, o := range options {
		line := fmt.Sprintf("  %d) %s", i+1, o.Label)
		if o.Note != "" {
			line += "  " + p.dim(o.Note)
		}
		fmt.Fprintln(p.out, line)
	}
	for {
		fmt.Fprintf(p.out, "choose %s ", p.dim(fmt.Sprintf("[%d]", defIndex+1)))
		answer, err := p.readLine()
		if err != nil {
			return "", err
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			return options[defIndex].Value, nil
		}
		if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(options) {
			return options[n-1].Value, nil
		}
		// Accept the value itself too, so a wizard transcript can be replayed.
		for _, o := range options {
			if strings.EqualFold(o.Value, answer) {
				return o.Value, nil
			}
		}
		fmt.Fprintf(p.out, "  enter a number between 1 and %d\n", len(options))
	}
}

// askList collects repeatable values, one per line, until a blank line.
func (p *prompter) askList(label string, validate func(string) error) ([]string, error) {
	p.note("%s — one per line, blank line to finish", label)
	var out []string
	for {
		fmt.Fprint(p.out, "  > ")
		answer, err := p.readLine()
		if err != nil {
			return nil, err
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			return out, nil
		}
		if validate != nil {
			if err := validate(answer); err != nil {
				fmt.Fprintf(p.out, "  %s\n", p.problem(err))
				continue
			}
		}
		out = append(out, answer)
	}
}

// summaryAction is what an operator chooses at the confirmation step.
type summaryAction int

const (
	actionRun summaryAction = iota
	actionEdit
	actionCancel
)

// summary shows every resolved value and the exact non-interactive command that
// reproduces it, then asks what to do.
//
// Echoing the command is a required feature rather than a nicety: it is how an
// operator moves from the wizard to a script, and it is what lands in the audit
// log.
func (p *prompter) summary(title string, fields [][2]string, argv []string) (summaryAction, error) {
	p.heading(title)
	width := 0
	for _, f := range fields {
		if len(f[0]) > width {
			width = len(f[0])
		}
	}
	for _, f := range fields {
		fmt.Fprintf(p.out, "  %-*s  %s\n", width, p.dim(f[0]), f[1])
	}
	fmt.Fprintf(p.out, "\n%s\n  %s\n\n", p.dim("equivalent command:"), strings.Join(argv, " "))

	for {
		fmt.Fprintf(p.out, "%s ", "[R]un, [E]dit a field, [C]ancel?")
		answer, err := p.readLine()
		if err != nil {
			return actionCancel, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "r", "run":
			return actionRun, nil
		case "e", "edit":
			return actionEdit, nil
		case "c", "cancel", "q", "quit":
			return actionCancel, nil
		default:
			fmt.Fprintln(p.out, "  answer r, e or c")
		}
	}
}

// mainMenu is the bare-`ratline` entry point.
func mainMenu(ctx context.Context, g *Globals) error {
	p := newPrompter(g)
	for {
		if err := printServerSummary(ctx, g, p); err != nil {
			return err
		}
		what, err := p.pick("What would you like to do?", []choice{
			{Value: "user", Label: "Users", Note: "add, list, show, disable, delete"},
			{Value: "site", Label: "Sites", Note: "add, deploy, scale, logs, delete"},
			{Value: "key", Label: "SSH keys", Note: "add, list, test, audit, revoke"},
			{Value: "cert", Label: "Certificates", Note: "issue, list, renew, import"},
			{Value: "doctor", Label: "Diagnostics", Note: "run ratline doctor"},
			{Value: "quit", Label: "Quit", Note: ""},
		}, "site")
		if err != nil {
			return errCancelledToNil(err)
		}

		switch what {
		case "quit":
			return nil
		case "doctor":
			if err := runDoctor(ctx, g, doctorOptions{}); err != nil {
				return err
			}
		default:
			if err := menuGroup(ctx, g, p, what); err != nil {
				if err == ErrCancelled {
					continue
				}
				return err
			}
		}
	}
}

func errCancelledToNil(err error) error {
	if err == ErrCancelled {
		return nil
	}
	return err
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
