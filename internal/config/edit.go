package config

import (
	"strconv"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// Surgical edits to the configuration file, by line.
//
// The obvious implementation is to unmarshal, change the field, and re-encode. That is
// what Save did, and it destroys every comment in the file — including on the first
// `ratline init`, which records the ACME email and in doing so flattened the commented
// reference the documentation calls "the reference". An operator who then opens the file
// finds a bare list of values and no explanation of any of them.
//
// So this edits the text instead: it finds the line the key is on and replaces the value
// after the colon, leaving the indentation, the ordering, the blank lines and every
// comment exactly as they were — including a trailing comment on the line it changed.
//
// It is not a general YAML editor and does not try to be. The configuration is a map of
// scalars nested at most three deep, with no lists of maps and no anchors, and the parser
// below assumes exactly that. Anything it cannot place, it refuses rather than guesses:
// writing a key into the wrong section would be worse than not writing it.

// maxKeyDepth is what the file actually uses: databases.mongodb.default_role.
const maxKeyDepth = 3

// indentWidth matches the encoder's SetIndent(2) and the shipped file.
const indentWidth = 2

// SetValue returns body with key set to value, preserving comments and layout.
//
// key is a dotted path — "acme.email", "features.db_provisioning",
// "databases.mongodb.default_role". value is written verbatim, so the caller is
// responsible for quoting anything YAML would misread; FormatScalar does that.
func SetValue(body []byte, key, value string) ([]byte, error) {
	parts, err := splitKey(key)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(body), "\n")

	idx, parentIdx, err := findKey(lines, parts)
	if err != nil {
		return nil, err
	}

	if idx >= 0 {
		lines[idx] = replaceValue(lines[idx], value)
		return []byte(strings.Join(lines, "\n")), nil
	}

	// The key is absent. It can only be added under a parent that exists, because
	// inventing a section means guessing where it belongs and what else is missing.
	if len(parts) > 1 && parentIdx < 0 {
		return nil, rlerr.Usagef("there is no %q section in the configuration",
			strings.Join(parts[:len(parts)-1], ".")).
			WithHint("add the section by hand, or run 'ratline config reference' to see " +
				"the shipped file and copy the block you need")
	}

	indent := strings.Repeat(" ", (len(parts)-1)*indentWidth)
	line := indent + parts[len(parts)-1] + ": " + value

	if len(parts) == 1 {
		// A top-level key goes at the end, after any trailing blank lines.
		return []byte(strings.Join(appendBeforeTrailingBlanks(lines, line), "\n")), nil
	}
	// Immediately after the parent, so it lands inside the section rather than after
	// whatever follows it.
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:parentIdx+1]...)
	out = append(out, line)
	out = append(out, lines[parentIdx+1:]...)
	return []byte(strings.Join(out, "\n")), nil
}

// GetValue returns the value of key as it appears in the file, without quotes.
//
// Reported as "not set" rather than as an empty string when the key is absent: the
// difference matters, because an absent key means the built-in default applies and an
// empty one means the operator deliberately blanked it.
func GetValue(body []byte, key string) (string, bool, error) {
	parts, err := splitKey(key)
	if err != nil {
		return "", false, err
	}
	lines := strings.Split(string(body), "\n")
	idx, _, err := findKey(lines, parts)
	if err != nil {
		return "", false, err
	}
	if idx < 0 {
		return "", false, nil
	}
	_, after, _ := strings.Cut(lines[idx], ":")
	return strings.TrimSpace(stripComment(after)), true, nil
}

// UnsetValue removes key's line, so the built-in default applies again.
func UnsetValue(body []byte, key string) ([]byte, bool, error) {
	parts, err := splitKey(key)
	if err != nil {
		return nil, false, err
	}
	lines := strings.Split(string(body), "\n")
	idx, _, err := findKey(lines, parts)
	if err != nil {
		return nil, false, err
	}
	if idx < 0 {
		return body, false, nil
	}
	// Any comment lines immediately above go with it: they describe the setting, and
	// leaving them orphaned above an unrelated key is worse than losing them.
	start := idx
	for start > 0 {
		prev := strings.TrimSpace(lines[start-1])
		if prev == "" || !strings.HasPrefix(prev, "#") {
			break
		}
		start--
	}
	out := append(append([]string{}, lines[:start]...), lines[idx+1:]...)
	return []byte(strings.Join(out, "\n")), true, nil
}

// findKey locates the line holding the last part of the path.
//
// Returns the line index, the index of the immediate parent's line, and -1 for either
// when not found. Matching is by indentation depth, so a key named the same as one in
// another section is not confused for it.
func findKey(lines []string, parts []string) (idx, parentIdx int, err error) {
	depth := 0
	parentIdx = -1

	for i, raw := range lines {
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent%indentWidth != 0 {
			// Not something this editor understands. Refusing here rather than
			// mis-parsing is the point: a wrong edit to a configuration file is worse
			// than no edit.
			return -1, -1, rlerr.Preconditionf(
				"line %d of the configuration is indented by %d spaces, which ratline cannot edit safely",
				i+1, indent).
				WithHint("edit the file by hand, or re-seed it with 'ratline config reference'")
		}
		level := indent / indentWidth

		// Anything at or above the depth we are inside ends that section.
		if level < depth {
			depth = level
		}
		if level != depth {
			continue
		}

		name, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name != parts[depth] {
			continue
		}
		if depth == len(parts)-1 {
			return i, parentIdx, nil
		}
		// Matched an intermediate section; descend into it.
		parentIdx = i
		depth++
	}
	return -1, parentIdx, nil
}

// replaceValue swaps the scalar after the colon, keeping the indentation and any
// trailing comment.
func replaceValue(line, value string) string {
	before, after, ok := strings.Cut(line, ":")
	if !ok {
		return line
	}
	comment := ""
	if _, c, found := cutComment(after); found {
		// Two spaces before a trailing comment, which is what the shipped file uses.
		comment = "  " + c
	}
	return before + ": " + value + comment
}

// splitKey validates a dotted path.
func splitKey(key string) ([]string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, rlerr.Usagef("no setting was named").
			WithHint("a dotted path, for example acme.email or features.db_provisioning")
	}
	parts := strings.Split(key, ".")
	if len(parts) > maxKeyDepth {
		return nil, rlerr.Usagef("%q is nested %d deep; the configuration goes at most %d",
			key, len(parts), maxKeyDepth)
	}
	for _, p := range parts {
		if p == "" {
			return nil, rlerr.Usagef("%q has an empty component", key)
		}
		// A key with a space or a colon in it would produce a file that no longer
		// parses, and the failure would surface on the next command rather than here.
		if strings.ContainsAny(p, " \t:#\"'") {
			return nil, rlerr.Usagef("%q is not a valid setting name", key)
		}
	}
	return parts, nil
}

// stripComment removes a trailing comment, respecting quotes.
func stripComment(s string) string {
	out, _, _ := cutComment(s)
	return out
}

// cutComment splits a line at a trailing comment that is not inside quotes.
func cutComment(s string) (before, comment string, found bool) {
	var quote rune
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '#':
			// A # only starts a comment at the start of the line or after whitespace,
			// so a value like an anchor or a colour code survives.
			if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' {
				return s[:i], s[i:], true
			}
		}
	}
	return s, "", false
}

// appendBeforeTrailingBlanks inserts a line after the last non-blank one, so a file
// ending in a newline does not grow a blank line every time something is set.
func appendBeforeTrailingBlanks(lines []string, line string) []string {
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:last+1]...)
	out = append(out, line)
	out = append(out, lines[last+1:]...)
	return out
}

// FormatScalar renders a Go value the way the configuration file writes it.
//
// Quoting matters more than it looks: an unquoted `yes`, `no`, `on`, `off` or `null` is a
// boolean or a null to a YAML parser, so a password or a label that happens to read that
// way would come back as the wrong type. A leading zero makes an octal. Anything
// ambiguous is quoted.
func FormatScalar(v string) string {
	if v == "" {
		return `""`
	}
	lower := strings.ToLower(v)
	switch lower {
	case "true", "false", "yes", "no", "on", "off", "null", "~":
		// A real boolean is written bare; the words YAML also treats as booleans are
		// quoted so they survive as the strings they were typed as.
		if lower == "true" || lower == "false" {
			return lower
		}
		return strconv.Quote(v)
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		// A number, but a leading zero would be read as octal and a leading + is not
		// valid YAML.
		if strings.HasPrefix(v, "0") && v != "0" && !strings.Contains(v, ".") {
			return strconv.Quote(v)
		}
		if strings.HasPrefix(v, "+") {
			return strconv.Quote(v)
		}
		return v
	}
	// Anything a parser could take for structure rather than text. @ and ` are reserved
	// only as the *first* character of a scalar, so an email address stays unquoted —
	// which is how the shipped file writes acme.email, and quoting it would make a
	// hand-edited file and a ratline-edited one differ for no reason.
	if strings.ContainsAny(v, ":#{}[],&*!|>'\"%") || strings.TrimSpace(v) != v {
		return strconv.Quote(v)
	}
	if first := v[0]; first == '@' || first == '`' || first == '-' || first == '?' {
		return strconv.Quote(v)
	}
	return v
}

// ParseBool accepts what an operator types for a boolean setting.
func ParseBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "on", "1", "enabled":
		return true, nil
	case "false", "no", "off", "0", "disabled":
		return false, nil
	}
	return false, rlerr.Usagef("%q is not a yes or a no", v).
		WithHint("true or false")
}

// Redacted is what a value is replaced with when it should not be printed.
const Redacted = "«redacted»"

// SecretKeys are settings whose values must not be printed by `config show`.
//
// The configuration deliberately holds no passwords — the MongoDB URI and the DNS
// credentials live in their own 0600 files — but a webhook URL usually carries a token in
// its path, and printing one into a terminal or a support ticket leaks it.
var SecretKeys = map[string]bool{
	"acme.alerts.webhook_url": true,
}

// IsSecret reports whether a dotted key's value should be redacted when displayed.
func IsSecret(key string) bool { return SecretKeys[key] }

// KeyExists reports whether a dotted key is one the Config struct actually has.
//
// Checked against the shipped defaults rather than by reflection, because the defaults are
// the file's own definition of what exists — and a typo like paths.systemdir must be a
// refusal rather than a key written into the file and never read.
func KeyExists(key string) bool {
	parts, err := splitKey(key)
	if err != nil {
		return false
	}
	_, found, err := GetValue(DefaultYAML(), strings.Join(parts, "."))
	return err == nil && found
}

// KnownKeys lists every dotted key in the shipped defaults, for completion and for
// `config show`.
func KnownKeys() []string {
	var out []string
	var section []string
	for _, raw := range strings.Split(string(DefaultYAML()), "\n") {
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent%indentWidth != 0 {
			continue
		}
		level := indent / indentWidth
		name, rest, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if level > len(section) {
			continue
		}
		section = section[:level]
		if strings.TrimSpace(rest) == "" {
			// A section header: nothing after the colon.
			section = append(section, name)
			continue
		}
		out = append(out, strings.Join(append(append([]string{}, section...), name), "."))
	}
	return out
}
