package log

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHumanHandlerFormat(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Out: &buf, Level: LevelDebug})
	l.Debug("resolving", "bin", "nginx")
	l.Info("wrote the vhost", "path", "/etc/nginx/sites-available/example.com.conf")
	l.Warn("no configuration file")
	l.Error("nginx -t failed", "exit", 1)

	out := buf.String()
	for _, want := range []string{
		"· resolving bin=nginx",
		"→ wrote the vhost path=/etc/nginx/sites-available/example.com.conf",
		"! no configuration file",
		"✗ nginx -t failed exit=1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\033[") {
		t.Error("colour escapes appeared with Color disabled")
	}
}

func TestHumanHandlerQuotesValuesNeedingIt(t *testing.T) {
	var buf bytes.Buffer
	New(Options{Out: &buf, Level: LevelInfo}).Info("ran", "cmd", "npm run build", "empty", "")
	out := buf.String()
	if !strings.Contains(out, `cmd="npm run build"`) {
		t.Errorf("a value containing a space was not quoted:\n%s", out)
	}
	if !strings.Contains(out, `empty=""`) {
		t.Errorf("an empty value was not quoted:\n%s", out)
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Out: &buf, Level: LevelError})
	l.Debug("d")
	l.Info("i")
	l.Warn("w")
	l.Error("e")
	if got := buf.String(); strings.Count(got, "\n") != 1 || !strings.Contains(got, "e") {
		t.Errorf("at error level the output was %q", got)
	}
}

func TestJSONHandler(t *testing.T) {
	var buf bytes.Buffer
	New(Options{Out: &buf, Level: LevelInfo, JSON: true}).Info("issued", "domain", "example.com")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("JSON logs do not parse: %v\n%s", err, buf.String())
	}
	if rec["msg"] != "issued" || rec["domain"] != "example.com" {
		t.Errorf("record = %v", rec)
	}
}

func TestWithAddsAttributesToEveryRecord(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Out: &buf, Level: LevelInfo}).With("site", "example.com")
	l.Info("first")
	l.Info("second")
	if n := strings.Count(buf.String(), "site=example.com"); n != 2 {
		t.Errorf("the attribute appeared %d times, want 2:\n%s", n, buf.String())
	}
}

func TestStreamEmitsWholeLines(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Out: &buf, Level: LevelInfo})
	w := l.Stream(LevelInfo, "npm")

	// Child output arrives in arbitrary chunks; each complete line must be
	// logged once and the trailing partial line flushed on Close.
	w.Write([]byte("added 12 pack"))
	w.Write([]byte("ages\nfound 0 vulnerabilities\npartial"))
	if err := w.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}

	out := buf.String()
	for _, want := range []string{"added 12 packages", "found 0 vulnerabilities", "partial"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "source=npm") {
		t.Errorf("the stream prefix is missing:\n%s", out)
	}
	if strings.Contains(out, "added 12 pack\n") {
		t.Error("a partial line was emitted before its newline arrived")
	}
}

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]Level{"": LevelInfo, "info": LevelInfo, "DEBUG": LevelDebug, "warn": LevelWarn, "warning": LevelWarn, "error": LevelError} {
		got, err := ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseLevel("trace"); err == nil {
		t.Error("ParseLevel accepted an unknown level")
	}
}

func TestAuditWritesJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "audit.log")
	a, err := OpenAudit(path)
	if err != nil {
		t.Fatalf("OpenAudit = %v", err)
	}
	if err := a.Write(Entry{
		Command:  "ratline site add",
		Argv:     []string{"ratline", "site", "env", "set", "example.com", "DATABASE_URL=postgres://u:pw@h/db"},
		UID:      0,
		SudoUser: "ali",
		Result:   "ok",
		ExitCode: 0,
	}); err != nil {
		t.Fatalf("Write = %v", err)
	}
	if err := a.Write(Entry{Command: "ratline user add", Result: "error", ExitCode: 3, Error: "already exists"}); err != nil {
		t.Fatalf("Write = %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines, want 2:\n%s", len(lines), data)
	}
	var e Entry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("audit line does not parse: %v", err)
	}
	if e.Time.IsZero() {
		t.Error("the entry has no timestamp")
	}
	// The audit trail must never become a place secrets are archived.
	if strings.Contains(lines[0], "postgres://") || strings.Contains(lines[0], "pw@h") {
		t.Errorf("an environment value was written to the audit log:\n%s", lines[0])
	}
	if !strings.Contains(lines[0], "DATABASE_URL=") {
		t.Errorf("the variable name was redacted along with its value:\n%s", lines[0])
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o640 {
		t.Errorf("audit log mode = %04o, want 0640", got)
	}
}

func TestNopAudit(t *testing.T) {
	a := NopAudit()
	if err := a.Write(Entry{}); err != nil {
		t.Errorf("Write = %v", err)
	}
	if err := a.Note("x", nil); err != nil {
		t.Errorf("Note = %v", err)
	}
	if err := a.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
}
