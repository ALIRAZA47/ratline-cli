package log

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one line of /var/log/ratline/audit.log.
//
// The audit log answers "who changed what on this box, when, and did it work".
// It is JSON lines so it can be shipped or grepped with jq, and it is written
// for every mutating invocation, successful or not.
type Entry struct {
	Time       time.Time         `json:"time"`
	Version    string            `json:"version,omitempty"`
	Command    string            `json:"command"`
	Argv       []string          `json:"argv"`
	UID        int               `json:"uid"`
	User       string            `json:"user,omitempty"`
	SudoUser   string            `json:"sudo_user,omitempty"`
	DryRun     bool              `json:"dry_run,omitempty"`
	Result     string            `json:"result"`
	ExitCode   int               `json:"exit_code"`
	DurationMS int64             `json:"duration_ms"`
	Error      string            `json:"error,omitempty"`
	Fields     map[string]string `json:"fields,omitempty"`
}

// Auditor appends entries to the audit trail.
type Auditor interface {
	Write(Entry) error
	// Note records a free-form event outside the command lifecycle, such as a
	// ratline-shell dispatch or an expired-key pruning.
	Note(command string, fields map[string]string) error
	Close() error
}

// OpenAudit opens (creating if needed) the append-only audit log.
//
// The file is 0640 so that members of the log-reading group can audit without
// write access. A caller that cannot open it should fall back to NopAudit and
// warn rather than refusing to work — losing the trail is bad, refusing to
// manage the server is worse.
func OpenAudit(path string) (Auditor, error) {
	if path == "" {
		return NopAudit(), nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("creating audit log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, fmt.Errorf("opening audit log %s: %w", path, err)
	}
	return &fileAuditor{f: f}, nil
}

type fileAuditor struct {
	mu sync.Mutex
	f  *os.File
}

func (a *fileAuditor) Write(e Entry) error {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	e.Argv = Argv(e.Argv)
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encoding audit entry: %w", err)
	}
	b = append(b, '\n')
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.f.Write(b); err != nil {
		return fmt.Errorf("writing audit entry: %w", err)
	}
	return a.f.Sync()
}

func (a *fileAuditor) Note(command string, fields map[string]string) error {
	return a.Write(Entry{Command: command, Result: "note", Fields: fields})
}

func (a *fileAuditor) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		return nil
	}
	err := a.f.Close()
	a.f = nil
	return err
}

// NopAudit discards entries. Used in tests and when the trail cannot be opened.
func NopAudit() Auditor { return nopAuditor{} }

type nopAuditor struct{}

func (nopAuditor) Write(Entry) error                    { return nil }
func (nopAuditor) Note(string, map[string]string) error { return nil }
func (nopAuditor) Close() error                         { return nil }
