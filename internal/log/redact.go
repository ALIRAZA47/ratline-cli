package log

import (
	"regexp"
	"strings"
)

// Placeholders. Redacted goes to files and logs; Masked is for the interactive
// summary panel, where a fixed-width dot run reads better.
const (
	Redacted = "[redacted]"
	Masked   = "•••••"
)

// secretNameRe matches flag and variable names whose *values* must never be
// written to the audit log. It errs on the side of over-redaction: losing a
// debugging detail is cheaper than leaking a credential into a log file.
var secretNameRe = regexp.MustCompile(`(?i)(pass(word|phrase)?|secret|token|api[-_]?key|private[-_]?key|credential|bearer|session|cookie|salt|signature|webhook)`)

// neverRedact lists flags whose values are paths, URLs or public keys. They
// match secretNameRe by accident and are worth keeping legible.
var neverRedact = map[string]bool{
	"dns-credentials": true,
	"ssh-key":         true,
	"key":             true,
	"cert":            true,
	"chain":           true,
	"config":          true,
	"from-github":     true,
	"from-gitlab":     true,
	"key-type":        true,
	"password-login":  true, // a boolean flag, not a value
}

var envAssignRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

// IsSecretName reports whether a flag or variable name denotes a secret.
func IsSecretName(name string) bool {
	name = strings.TrimLeft(name, "-")
	if neverRedact[strings.ToLower(name)] {
		return false
	}
	return secretNameRe.MatchString(name)
}

// Value replaces a secret with the file-safe placeholder.
func Value(string) string { return Redacted }

// Mask replaces a secret with the display placeholder.
func Mask(string) string { return Masked }

// Argv redacts an argument vector for logging.
//
// Three cases are handled: --flag=value and --flag value where the flag name
// denotes a secret, and bare NAME=VALUE arguments (`site env set`), whose
// values are always treated as secrets.
func Argv(argv []string) []string {
	out := make([]string, 0, len(argv))
	redactNext := false
	for _, a := range argv {
		if redactNext {
			out = append(out, Redacted)
			redactNext = false
			continue
		}
		switch {
		case strings.HasPrefix(a, "-"):
			if i := strings.IndexByte(a, '='); i > 0 {
				if IsSecretName(a[:i]) {
					out = append(out, a[:i+1]+Redacted)
					continue
				}
				out = append(out, a)
				continue
			}
			if IsSecretName(a) {
				redactNext = true
			}
			out = append(out, a)
		default:
			if m := envAssignRe.FindStringSubmatch(a); m != nil {
				out = append(out, m[1]+"="+Redacted)
				continue
			}
			out = append(out, a)
		}
	}
	return out
}

// ArgvString renders a redacted argv as a single readable line.
func ArgvString(argv []string) string { return strings.Join(Argv(argv), " ") }
