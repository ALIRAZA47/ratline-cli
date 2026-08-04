package diag

import (
	"strconv"
	"strings"
)

// firstLine is the first line of a possibly multi-line error.
//
// An external command's error carries its whole output, which is the right thing in
// a log and the wrong thing on a line of a diagnostic table. The fix on the same
// step says where to read the rest.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// orDefault returns v, or fallback when v is empty.
func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// plural formats a count with its noun. "1 sites" reads as a bug in the tool rather
// than as a count of one, and a diagnostic is read closely.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
