package mongo

import (
	"strings"
	"testing"
)

// The file is one an operator writes by hand — that is what `db connect --from-file` is
// for — so the format has to survive what a person naturally puts in a credentials file
// in /etc: a line at the top saying what it is.
func TestTheURIFileToleratesCommentsAndBlankLines(t *testing.T) {
	const want = "mongodb://admin:pass@127.0.0.1:27017/?authSource=admin"
	for name, body := range map[string]string{
		"bare":                  want,
		"trailing newline":      want + "\n",
		"leading comment":       "# MongoDB admin credentials for ratline\n" + want + "\n",
		"comment and blanks":    "\n# what this is\n\n" + want + "\n\n",
		"indented comment":      "   # indented\n  " + want + "  \n",
		"comment after the uri": want + "\n# rotated 2026-08-06\n",
		"crlf":                  "# windows wrote this\r\n" + want + "\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseURIFile(body, "/etc/ratline/db/mongodb.uri")
			if err != nil {
				t.Fatalf("ParseURIFile(%q) = %v", body, err)
			}
			if got != want {
				t.Errorf("ParseURIFile(%q) = %q, want %q", body, got, want)
			}
		})
	}
}

func TestTheURIFileRefusesWhatItCannotResolve(t *testing.T) {
	const uri = "mongodb://admin:pass@127.0.0.1:27017/?authSource=admin"
	for name, tc := range map[string]struct{ body, want string }{
		"empty":         {"", "no connection string"},
		"only comments": {"# nothing here\n\n# really\n", "no connection string"},
		// Two credentials for every database on the server is not a coin toss.
		"two strings": {uri + "\n" + uri + "\n", "will not guess"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseURIFile(tc.body, "/etc/ratline/db/mongodb.uri")
			if err == nil {
				t.Fatalf("ParseURIFile(%q) was accepted", tc.body)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ParseURIFile(%q) said %q, want it to mention %q", tc.body, err, tc.want)
			}
		})
	}
}
