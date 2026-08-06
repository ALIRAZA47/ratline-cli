package cli

import (
	"strings"
	"testing"
)

// A shell example is not markup. The topics document files that contain # comments and
// commands that contain > redirections, and the renderer matches on the trimmed line — so
// without the indent check every comment in an example became an UPPERCASE HEADING. The
// databases topic turned into six headings and one line of content before this was caught.
func TestIndentedExamplesAreNotTreatedAsMarkup(t *testing.T) {
	body := strings.Join([]string{
		"# A heading",
		"",
		"Some prose.",
		"",
		"    # managed-by: ratline",
		"    # a comment in a file",
		"    mongodb://admin:pass@127.0.0.1:27017/?authSource=admin",
		"    ratline export > inventory.json",
		"",
		"## A real subheading",
	}, "\n")

	got := renderMarkdown(body, false)

	for _, want := range []string{
		"    # managed-by: ratline",
		"    # a comment in a file",
		"    ratline export > inventory.json",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the indented line %q did not survive:\n%s", want, got)
		}
	}
	if strings.Contains(got, "A COMMENT IN A FILE") {
		t.Errorf("an indented # comment was rendered as a heading:\n%s", got)
	}
	// The real headings must still be headings.
	if !strings.Contains(got, "A HEADING") {
		t.Errorf("the level-one heading stopped rendering:\n%s", got)
	}
	if !strings.Contains(got, "A real subheading") {
		t.Errorf("the level-two heading stopped rendering:\n%s", got)
	}
}
