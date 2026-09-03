package rl

import (
	"encoding/json"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// Envelope is the single object every `ratline --json` invocation writes to stdout.
//
// Declared here rather than imported from internal/cli on purpose. The panel is a
// consumer of a published contract; reading it through the producer's own structs
// would mean a change in the envelope's shape compiles cleanly and breaks at runtime
// on somebody's server. A test marshals the real cli.Envelope and unmarshals it into
// this one, so the two are checked against each other rather than assumed identical.
type Envelope struct {
	OK      bool            `json:"ok"`
	Command string          `json:"command"`
	Version string          `json:"version"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   *ErrorPayload   `json:"error,omitempty"`
}

// ErrorPayload is the machine-readable half of a failure.
type ErrorPayload struct {
	Code    int               `json:"code"`
	Name    string            `json:"name"`
	Message string            `json:"message"`
	Hint    string            `json:"hint,omitempty"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// ParseEnvelope reads what a command wrote to stdout.
//
// Exactly one object, which is the contract. Anything else — empty output, a stray
// line, two objects — is reported as the contract being broken rather than papered
// over, because the alternative is the panel silently reporting success for a command
// whose output it could not read.
func ParseEnvelope(stdout string) (*Envelope, error) {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return nil, rlerr.Externalf("ratline produced no output").
			WithHint("run the same command over SSH with --json to see what it printed")
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	var env Envelope
	if err := dec.Decode(&env); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeExternal, "reading ratline's JSON output").
			WithField("output", firstLine(trimmed))
	}
	if dec.More() {
		return nil, rlerr.Externalf("ratline wrote more than one JSON object").
			WithHint("the --json contract is one object per invocation; this is a ratline bug")
	}
	return &env, nil
}

// Err turns a failure envelope back into a ratline error, with its code and hint
// intact, so the panel's HTTP layer can map it the same way the CLI maps an exit
// status. A caller branching on rlerr.CodeLocked gets the same answer whether the
// command ran here or in a terminal.
func (e *Envelope) Err() error {
	if e == nil || e.OK || e.Error == nil {
		return nil
	}
	err := rlerr.New(rlerr.Code(e.Error.Code), "%s", e.Error.Message)
	if e.Error.Hint != "" {
		err = err.WithHint("%s", e.Error.Hint)
	}
	for k, v := range e.Error.Fields {
		err = err.WithField(k, v)
	}
	return err
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
