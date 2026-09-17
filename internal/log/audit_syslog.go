package log

import (
	"encoding/json"
	"fmt"
	"log/syslog"
	"time"
)

// SyslogAudit sends entries to the local syslog socket, for a process that cannot open
// the audit file.
//
// ratline-shell runs as the tenant sshd has already switched to, and
// /var/log/ratline/audit.log is root's, so its open fails on every scoped-key session.
// The fallback used to be to discard the entry — leaving exactly the sessions worth a
// trail unrecorded, while the docs said each one was logged. journald accepts a line from
// any uid and stamps it with the uid that sent it, so a tenant can add noise to the
// journal but cannot forge who they are.
func SyslogAudit(tag string) (Auditor, error) {
	w, err := syslog.New(syslog.LOG_AUTHPRIV|syslog.LOG_INFO, tag)
	if err != nil {
		return nil, fmt.Errorf("connecting to syslog: %w", err)
	}
	return &syslogAuditor{w: w}, nil
}

type syslogAuditor struct {
	w *syslog.Writer
}

func (a *syslogAuditor) Write(e Entry) error {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	e.Argv = Argv(e.Argv)
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encoding audit entry: %w", err)
	}
	return a.w.Info(string(b))
}

func (a *syslogAuditor) Note(command string, fields map[string]string) error {
	return a.Write(Entry{Command: command, Result: "note", Fields: fields})
}

func (a *syslogAuditor) Close() error { return a.w.Close() }
