package sshkey

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// acceptedRe matches sshd's accepted-publickey line, which is where the only
// durable record of a key actually being used lives.
//
//	Accepted publickey for alice from 203.0.113.19 port 54321 ssh2: ED25519 SHA256:x9K…
var acceptedRe = regexp.MustCompile(
	`Accepted\s+(publickey|publickey/\S+)\s+for\s+(\S+)\s+from\s+(\S+)\s+port\s+\d+\s+\S+:\s+\S+\s+(SHA256:[A-Za-z0-9+/=]+)`)

// journalTimeRe matches the ISO timestamp journalctl emits with --output=short-iso.
var journalTimeRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2})`)

// ScanUsage reads the auth log and records when each key was last used.
//
// This runs opportunistically on `key list` and on a low-frequency timer, because
// logs rotate: a contractor's key last used four months ago leaves no trace in
// today's journal, and the whole point of `key list --unused 90` is to find
// exactly that key. Recording it as it goes means the data outlives the logs.
func (m *Manager) ScanUsage(ctx context.Context, since time.Time) (int, error) {
	if !m.Cfg.SSH.UsageScanEnabled {
		return 0, nil
	}
	lines, err := m.readAuthLog(ctx, since)
	if err != nil {
		// A server without journald or a readable auth log is a normal state,
		// not a failure: usage data is a convenience, not a correctness concern.
		m.Log.Debug("could not read the authentication log", "err", err)
		return 0, nil
	}

	recorded := 0
	latest := since
	for _, line := range lines {
		match := acceptedRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		fingerprint := match[4]
		ip := match[3]
		at := parseLogTime(line, time.Now())
		if at.After(latest) {
			latest = at
		}
		if err := m.State.RecordKeyUsage(ctx, fingerprint, at, ip, "publickey"); err != nil {
			m.Log.Debug("could not record key usage", "fingerprint", fingerprint, "err", err)
			continue
		}
		recorded++
	}
	if !latest.IsZero() {
		if err := m.State.SetLastKeyUsageScan(ctx, latest); err != nil {
			m.Log.Debug("could not record the scan watermark", "err", err)
		}
	}
	if recorded > 0 {
		m.Log.Debug("recorded key usage", "observations", recorded)
	}
	return recorded, nil
}

func (m *Manager) readAuthLog(ctx context.Context, since time.Time) ([]string, error) {
	if m.Runner == nil {
		return nil, nil
	}
	if m.Cfg == nil {
		return nil, nil
	}
	// journald first, falling back to /var/log/auth.log for hosts that still
	// write one.
	if m.hasBinary("journalctl") {
		args := []string{"--unit=ssh", "--unit=sshd", "--output=short-iso", "--no-pager", "--quiet"}
		if !since.IsZero() {
			args = append(args, "--since="+since.UTC().Format("2006-01-02 15:04:05"))
		} else {
			args = append(args, "--since=-30d")
		}
		res, err := m.Runner.Run(ctx, system.Cmd{Name: "journalctl", Args: args, OKExit: []int{1}})
		if err == nil && res != nil && strings.TrimSpace(res.Stdout) != "" {
			return strings.Split(res.Stdout, "\n"), nil
		}
	}
	data, err := system.ReadFileLimit("/var/log/auth.log", 64<<20)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(data), "\n"), nil
}

func (m *Manager) hasBinary(name string) bool {
	type available interface{ Available(string) bool }
	if b, ok := any(m.Runner).(available); ok {
		return b.Available(name)
	}
	return true
}

// parseLogTime reads the timestamp from a log line, falling back to now.
//
// syslog's traditional format omits the year, which makes a December-to-January
// boundary ambiguous. journalctl --output=short-iso does not, which is why it is
// preferred; the fallback accepts the imprecision rather than guessing wrongly.
func parseLogTime(line string, fallback time.Time) time.Time {
	if m := journalTimeRe.FindStringSubmatch(line); m != nil {
		for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
			if t, err := time.Parse(layout, m[1]); err == nil {
				return t.UTC()
			}
		}
	}
	return fallback.UTC()
}

// AuditFinding is one problem `ratline key audit` reports.
type AuditFinding struct {
	Severity string `json:"severity"` // warning or problem
	Kind     string `json:"kind"`
	Key      string `json:"key,omitempty"`
	Label    string `json:"label,omitempty"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix,omitempty"`
}
