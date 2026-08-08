package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/ALIRAZA47/ratline-cli/internal/buildinfo"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// Envelope is the single object --json emits. One shape for success and
// failure means a caller can branch on ok without special cases.
type Envelope struct {
	OK      bool          `json:"ok"`
	Command string        `json:"command"`
	Version string        `json:"version"`
	Data    any           `json:"data,omitempty"`
	Error   *ErrorPayload `json:"error,omitempty"`
}

// ErrorPayload is the machine-readable half of an error.
type ErrorPayload struct {
	Code    int               `json:"code"`
	Name    string            `json:"name"`
	Message string            `json:"message"`
	Hint    string            `json:"hint,omitempty"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// EmitJSON writes the success envelope to stdout.
func (g *Globals) EmitJSON(data any) error {
	// Recorded so a command that emits its result and *then* fails does not produce a
	// second envelope. The contract is exactly one object on stdout per invocation, and
	// `doctor --json` broke it the moment it started exiting non-zero: callers piping it
	// into jq got two documents, and a filter that had worked for a year began returning
	// two answers.
	g.jsonEmitted = true
	return g.writeEnvelope(Envelope{
		OK:      true,
		Command: g.CmdPath,
		Version: buildinfo.Version,
		Data:    data,
	})
}

// EmitJSONError writes the failure envelope to stdout.
func (g *Globals) EmitJSONError(err error) error {
	code := rlerr.CodeOf(err)
	return g.writeEnvelope(Envelope{
		OK:      false,
		Command: g.CmdPath,
		Version: buildinfo.Version,
		Error: &ErrorPayload{
			Code:    int(code),
			Name:    code.Name(),
			Message: err.Error(),
			Hint:    rlerr.Hint(err),
			Fields:  rlerr.Fields(err),
		},
	})
}

func (g *Globals) writeEnvelope(e Envelope) error {
	enc := json.NewEncoder(g.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(e); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "writing JSON output")
	}
	return nil
}

// writeJSON writes a value to stdout without the envelope.
//
// Used only by `ratline schema`, which *is* the description of the envelope: wrapping it
// in the thing it documents would make a caller unwrap one before it could read what the
// wrapper looks like.
func (g *Globals) writeJSON(v any) error {
	enc := json.NewEncoder(g.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "writing JSON output")
	}
	return nil
}

// Table renders aligned columns for human output. Piped output gets the same
// text without colour, so `ratline site list | grep` behaves.
type Table struct {
	g       *Globals
	headers []string
	rows    [][]string
}

// Table starts a table with the given column headers.
func (g *Globals) Table(headers ...string) *Table {
	return &Table{g: g, headers: headers}
}

// Row appends a row. Cells beyond the header count are ignored, missing ones
// render empty, so a caller cannot produce a misaligned table.
func (t *Table) Row(cells ...string) *Table {
	row := make([]string, len(t.headers))
	for i := range row {
		if i < len(cells) {
			row[i] = cells[i]
		}
	}
	t.rows = append(t.rows, row)
	return t
}

// Len reports how many rows the table holds.
func (t *Table) Len() int { return len(t.rows) }

// Render writes the table to stdout. Under --json it does nothing: those
// commands emit an envelope instead.
func (t *Table) Render() error {
	if t.g.JSON {
		return nil
	}
	if len(t.rows) == 0 {
		return nil
	}
	w := tabwriter.NewWriter(t.g.Stdout, 0, 0, 2, ' ', 0)
	head := make([]string, len(t.headers))
	for i, h := range t.headers {
		head[i] = strings.ToUpper(h)
		if t.g.Color {
			head[i] = "\033[1m" + head[i] + "\033[0m"
		}
	}
	if _, err := fmt.Fprintln(w, strings.Join(head, "\t")); err != nil {
		return err
	}
	for _, r := range t.rows {
		if _, err := fmt.Fprintln(w, strings.Join(r, "\t")); err != nil {
			return err
		}
	}
	return w.Flush()
}

// Fields renders a label/value block, the shape used by every `show` command.
func (g *Globals) Fields(pairs ...[2]string) error {
	if g.JSON {
		return nil
	}
	w := tabwriter.NewWriter(g.Stdout, 0, 0, 2, ' ', 0)
	for _, p := range pairs {
		label := p[0]
		if g.Color {
			label = "\033[2m" + label + "\033[0m"
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\n", label, p[1]); err != nil {
			return err
		}
	}
	return w.Flush()
}

// reportError prints a failure for humans: what happened, what caused it, and
// what to do next, each on its own line.
func (g *Globals) reportError(err error) {
	if g.JSON {
		// A command that already wrote its envelope has said what it has to say; the
		// error still sets the exit code, which is how a caller learns of it. Writing a
		// second object here would break the one-object contract for the sake of
		// repeating something the exit code already carries.
		if g.jsonEmitted {
			return
		}
		if jerr := g.EmitJSONError(err); jerr != nil {
			fmt.Fprintf(g.Stderr, "error: %v\n", err)
		}
		return
	}
	prefix, reset := "", ""
	if g.Color {
		prefix, reset = "\033[31m", "\033[0m"
	}
	fmt.Fprintf(g.Stderr, "%serror%s: %s\n", prefix, reset, err.Error())
	if hint := rlerr.Hint(err); hint != "" {
		fmt.Fprintf(g.Stderr, "  hint: %s\n", hint)
	}
	if code := rlerr.CodeOf(err); code == rlerr.CodeUsage && g.CmdPath != "" {
		fmt.Fprintf(g.Stderr, "  see:  %s --help\n", g.CmdPath)
	}
}
