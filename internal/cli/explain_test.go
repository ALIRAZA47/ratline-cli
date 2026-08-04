package cli

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/docs"
)

func TestExplainListsEveryTopicWithASummary(t *testing.T) {
	code, out, _ := harness(t, "explain")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	s := out.String()

	// Every embedded topic must appear, so adding a page cannot leave it
	// undiscoverable.
	entries, err := fs.ReadDir(docs.Topics, "topics")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no topics are embedded, so `explain` has nothing to show")
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".md")
		if name == topicIndex {
			continue
		}
		if !strings.Contains(s, name) {
			t.Errorf("the topic %q is not listed:\n%s", name, s)
		}
	}

	// And the count matches, so a page cannot be embedded and silently skipped by
	// the listing rather than merely absent from it.
	names, err := topicNames()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != len(entries)-1 {
		t.Errorf("%d topics listed but %d markdown files are embedded (one is the index)",
			len(names), len(entries))
	}
}

func TestTheSectionIndexIsNotATopic(t *testing.T) {
	// topics/README.md exists so the directory reads correctly when browsed on the
	// repository host. It must not be offered as something to explain.
	names, err := topicNames()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if n == topicIndex {
			t.Fatalf("%q is the section index, not a topic", n)
		}
	}
	if _, err := readTopic(topicIndex); err == nil {
		t.Errorf("`ratline explain %s` should not resolve", topicIndex)
	}
}

func TestEveryTopicHasATitleAndASummary(t *testing.T) {
	names, err := topicNames()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		body, err := readTopic(name)
		if err != nil {
			t.Fatalf("readTopic(%q) = %v", name, err)
		}
		title, summary := topicHeader(string(body))
		// The list is built from these two lines, so a page without them appears
		// blank in the index rather than failing loudly.
		if title == "" {
			t.Errorf("%s.md has no '# ' heading", name)
		}
		if summary == "" {
			t.Errorf("%s.md has no '> ' summary line", name)
		}
		if len(summary) > 200 {
			t.Errorf("%s.md's summary is %d characters, which will not fit a table row",
				name, len(summary))
		}
	}
}

func TestExplainNeedsNeitherRootNorConfiguration(t *testing.T) {
	// Documentation has to be readable before `ratline init` has run and by a
	// tenant with an account but no privileges. The harness runs as the test user,
	// so a non-zero exit here means the command demanded root.
	for _, args := range [][]string{{"explain"}, {"explain", "sockets"}, {"explain", "node", "--raw"}} {
		code, out, _ := harness(t, args...)
		if code != 0 {
			t.Errorf("%v exited %d, want 0", args, code)
		}
		if out.Len() == 0 {
			t.Errorf("%v printed nothing", args)
		}
	}
}

func TestExplainUnknownTopicIsAUsageErrorWithASuggestion(t *testing.T) {
	code, _, errOut := harness(t, "explain", "socket")
	// Exit 2 is the usage-error contract, so a script can tell a typo from a
	// missing file.
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "sockets") {
		t.Errorf("a near-miss should be suggested:\n%s", errOut.String())
	}
}

func TestExplainRawIsTheSourceAndFormattedIsNot(t *testing.T) {
	_, rawOut, _ := harness(t, "explain", "sockets", "--raw")
	_, fmtOut, _ := harness(t, "explain", "sockets")

	if !strings.Contains(rawOut.String(), "# Sockets") {
		t.Error("--raw should hand back the markdown source")
	}
	// Formatted output strips the markers: they are the source's syntax, not the
	// reader's, and a path is easier to copy without backticks around it.
	if strings.Contains(fmtOut.String(), "# Sockets") {
		t.Error("the formatted output should not show the markdown heading marker")
	}
	if strings.Contains(fmtOut.String(), "```") {
		t.Error("the formatted output should not show fence markers")
	}
	// The content itself has to survive both ways.
	for _, want := range []string{"connect(2)", "0640", "EACCES"} {
		if !strings.Contains(fmtOut.String(), want) {
			t.Errorf("the formatted output lost %q", want)
		}
	}
}

func TestExplainKeepsCommandLinesCopyable(t *testing.T) {
	// The one thing a terminal renderer must not do to a technical document is
	// reflow it: a wrapped command line cannot be pasted.
	_, out, _ := harness(t, "explain", "node")
	for _, want := range []string{
		"ratline runtime install node 22 --with-pm2",
		"ratline site runtime app.example.com --daemon direct",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the command %q was not emitted intact:\n%s", want, out.String())
		}
	}
}

func TestExplainJSONCarriesTheWholePage(t *testing.T) {
	code, out, _ := harness(t, "--json", "explain", "sockets")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var env struct {
		Data struct {
			Topic, Title, Summary, Body string
		}
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, out.String())
	}
	if env.Data.Topic != "sockets" || env.Data.Title == "" || env.Data.Body == "" {
		t.Errorf("incomplete payload: %+v", env.Data)
	}
}

func TestRenderMarkdownDecoratesOnlyWithColour(t *testing.T) {
	const src = "# Title\n\n> Summary line.\n\nA `path` here.\n"
	plain := renderMarkdown(src, false)
	if strings.Contains(plain, "\x1b[") {
		t.Errorf("no ANSI without colour:\n%q", plain)
	}
	// The backticks come off either way, so a path can be copied without them.
	if strings.Contains(plain, "`path`") {
		t.Errorf("backticks should be stripped:\n%q", plain)
	}
	if !strings.Contains(plain, "path") {
		t.Errorf("the content inside the backticks was lost:\n%q", plain)
	}

	colour := renderMarkdown(src, true)
	if !strings.Contains(colour, "\x1b[") {
		t.Errorf("colour output should be decorated:\n%q", colour)
	}
	// Every escape sequence must be closed, or the terminal stays bold after the
	// command exits.
	if strings.Count(colour, "\x1b[0m") == 0 {
		t.Error("the decoration is never reset")
	}
}

func TestInlineCodeHandlesAnUnpairedBacktick(t *testing.T) {
	// A stray backtick in prose must not swallow the rest of the line.
	got := inlineCode("an unpaired ` backtick here", false)
	if !strings.Contains(got, "backtick here") {
		t.Errorf("content after an unpaired backtick was lost: %q", got)
	}
}

func TestTopicTraversalCannotEscapeTheEmbeddedFS(t *testing.T) {
	for _, name := range []string{"../../etc/passwd", "../embed", "/etc/passwd", "topics/../embed"} {
		if _, err := readTopic(name); err == nil {
			t.Errorf("readTopic(%q) resolved, which it must not", name)
		}
	}
}

func TestClosestTopicResolvesTheObviousMisses(t *testing.T) {
	names, err := topicNames()
	if err != nil {
		t.Fatal(err)
	}
	for input, want := range map[string]string{
		"socket":      "sockets",
		"502":         "diagnose",
		"pm2":         "node",
		"cert":        "tls",
		"certificate": "tls",
		"depl":        "deploys",
	} {
		if got := closestTopic(input, names); got != want {
			t.Errorf("closestTopic(%q) = %q, want %q", input, got, want)
		}
	}
	if got := closestTopic("zzzzz", names); got != "" {
		t.Errorf("closestTopic on nonsense = %q, want no suggestion", got)
	}
}
