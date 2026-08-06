package cli

import (
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/docs"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// `explain` exists because of where this tool is used. An operator meets ratline
// over SSH on a server they just built: no browser, no manual pages beyond the one
// ratline installs, and the documentation site is on a machine they are not looking
// at. `--help` answers "what are the flags"; it is the wrong shape for "why does my
// socket 502" or "what does PM2 buy me".
//
// The topics are the same markdown files the documentation site renders, embedded
// at build time, so there is exactly one source of truth.

// topicSummary is a topic's name, title and one-line summary.
type topicSummary struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

func newExplainCommand(g *Globals) *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:     "explain [topic]",
		Short:   "Explain how part of ratline works",
		GroupID: GroupOps,
		Args:    cobra.MaximumNArgs(1),
		Long: "Longer-form answers than a help page can carry: how sites are laid out on\n" +
			"disk, why a node site is supervised by PM2, what turns a working application\n" +
			"into a silent 502, what happens when a deploy fails halfway.\n\n" +
			"Run without a topic to list them. The pages are built into the binary, so\n" +
			"this works on a server with no browser and no network.",
		Example: "  ratline explain\n" +
			"  ratline explain sockets\n" +
			"  ratline explain node | less",
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			names, _ := topicNames()
			return names, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return g.listTopics()
			}
			return g.showTopic(args[0], raw)
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "Print the markdown source without terminal formatting")
	// No root, no configuration file, no state database: reading documentation must
	// work before `ratline init` has ever run, and for a tenant who has an account
	// on the box but no privileges.
	return NonRoot(cmd)
}

// topicIndex is the section index that lives beside the topics so the directory
// reads correctly when browsed. It is not itself a topic.
const topicIndex = "README"

// topicNames lists the embedded topics in alphabetical order.
func topicNames() ([]string, error) {
	entries, err := fs.ReadDir(docs.Topics, "topics")
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the embedded topics")
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".md")
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && name != topicIndex {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// readTopic returns one topic's markdown.
func readTopic(name string) ([]byte, error) {
	// path.Clean plus the Base call keeps a name like "../../etc/passwd" from
	// reaching the embedded filesystem. An embedded FS is read-only and contains
	// nothing secret, but a traversal that resolves is still a bug worth not having.
	clean := path.Base(path.Clean("/" + name))
	if clean != topicIndex {
		if body, err := docs.Topics.ReadFile(path.Join("topics", clean+".md")); err == nil {
			return body, nil
		}
	}
	names, _ := topicNames()
	e := rlerr.Usagef("there is no topic called %q", name)
	if near := closestTopic(name, names); near != "" {
		return nil, e.WithHint("did you mean 'ratline explain %s'?", near)
	}
	return nil, e.WithHint("the topics are: %s", strings.Join(names, ", "))
}

// closestTopic finds a plausible correction for a mistyped topic.
//
// Prefix and substring matching rather than an edit distance: the realistic typos
// here are abbreviations and partial words ("cert" for "tls", "socket" for
// "sockets"), which prefix matching catches and a distance threshold does not.
func closestTopic(want string, names []string) string {
	want = strings.ToLower(want)
	for _, n := range names {
		if strings.HasPrefix(n, want) || strings.HasPrefix(want, n) {
			return n
		}
	}
	for _, n := range names {
		if strings.Contains(n, want) || strings.Contains(want, n) {
			return n
		}
	}
	// A handful of names an operator is likely to reach for that are not the name
	// of the page that answers them.
	for alias, topic := range map[string]string{
		"502":         "diagnose",
		"pm2":         "node",
		"certificate": "tls",
		"cert":        "tls",
		"https":       "tls",
		"acme":        "tls",
		"socket":      "sockets",
		"permissions": "layout",
		"paths":       "layout",
		"files":       "layout",
		"secrets":     "deploys",
		"env":         "deploys",
		"mongo":       "databases",
		"mongodb":     "databases",
		"database":    "databases",
		"db":          "databases",
		"memory":      "limits",
		"cgroup":      "limits",
		"hardening":   "limits",
		"backup":      "state",
		"audit":       "state",
		"rollback":    "safety",
		"idempotent":  "safety",
		"keys":        "ssh",
		"broken":      "diagnose",
		"debug":       "diagnose",
		"gunicorn":    "python",
		"django":      "python",
		"spa":         "static",
	} {
		if want == alias {
			return topic
		}
	}
	return ""
}

// listTopics prints the topic index.
func (g *Globals) listTopics() error {
	names, err := topicNames()
	if err != nil {
		return err
	}
	summaries := make([]topicSummary, 0, len(names))
	for _, n := range names {
		body, err := readTopic(n)
		if err != nil {
			continue
		}
		title, summary := topicHeader(string(body))
		summaries = append(summaries, topicSummary{Name: n, Title: title, Summary: summary})
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"topics": summaries})
	}

	table := g.Table("TOPIC", "WHAT IT COVERS")
	for _, s := range summaries {
		table.Row(s.Name, s.Summary)
	}
	if err := table.Render(); err != nil {
		return err
	}
	g.Printf("\nRead one with 'ratline explain <topic>'.\n")
	return nil
}

// topicHeader pulls the title and the summary line out of a topic.
//
// The convention is a level-one heading followed by a blockquote, which reads
// correctly as markdown on the documentation site and gives this command something
// to put in a table without a second copy of the text.
func topicHeader(body string) (title, summary string) {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case title == "" && strings.HasPrefix(line, "# "):
			title = strings.TrimSpace(line[2:])
		case strings.HasPrefix(line, "> "):
			summary += strings.TrimSpace(line[2:]) + " "
		case summary != "" && line == "":
			return title, strings.TrimSpace(summary)
		}
	}
	return title, strings.TrimSpace(summary)
}

// showTopic prints one topic, lightly formatted for a terminal.
func (g *Globals) showTopic(name string, raw bool) error {
	body, err := readTopic(name)
	if err != nil {
		return err
	}
	if g.JSON {
		title, summary := topicHeader(string(body))
		return g.EmitJSON(map[string]any{"topic": name, "title": title,
			"summary": summary, "body": string(body)})
	}
	if raw {
		g.Printf("%s", body)
		return nil
	}
	// Formatted even when piped: the markdown markers are the source's syntax, not
	// the reader's, and `ratline explain node | less` is the expected way to read a
	// long page. Only the ANSI decoration is conditional.
	g.Printf("%s", renderMarkdown(string(body), g.Color))
	return nil
}

// ANSI sequences for the small amount of structure worth showing in a terminal.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiUnder  = "\x1b[4m"
	ansiYellow = "\x1b[33m"
)

// renderMarkdown formats a topic for a terminal.
//
// Deliberately not a markdown implementation. It handles the four constructs the
// topics use — headings, blockquotes, indented blocks and inline code — and passes
// everything else through untouched. A real renderer would reflow paragraphs, and
// reflowing is what breaks a command line an operator wants to copy: it is the one
// thing in a technical document that must survive verbatim.
func renderMarkdown(body string, color bool) string {
	var out strings.Builder
	inFence := false

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			// The fence markers themselves are noise on a terminal; the indentation
			// of what they contain already reads as a block.
			inFence = !inFence
			continue
		}
		if inFence {
			out.WriteString("    " + line + "\n")
			continue
		}

		// An indented line is a code block, and its contents are not markup.
		//
		// The switch below matches on the *trimmed* line, so without this an indented
		// `# comment` in a shell example renders as an UPPERCASED HEADING and a `>` as a
		// blockquote. Documenting a file that contains comments made that unmissable: the
		// example became six headings and one line of content.
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			out.WriteString(line + "\n")
			continue
		}

		switch {
		case strings.HasPrefix(trimmed, "# "):
			out.WriteString(decorate(strings.ToUpper(trimmed[2:]), color, ansiBold+ansiUnder) + "\n")
		case strings.HasPrefix(trimmed, "## "):
			out.WriteString("\n" + decorate(trimmed[3:], color, ansiBold) + "\n")
		case strings.HasPrefix(trimmed, "### "):
			out.WriteString("\n" + decorate(trimmed[4:], color, ansiBold) + "\n")
		case strings.HasPrefix(trimmed, "> "):
			out.WriteString(decorate(trimmed[2:], color, ansiDim) + "\n")
		case trimmed == ">":
			out.WriteString("\n")
		default:
			out.WriteString(inlineCode(line, color) + "\n")
		}
	}
	return out.String()
}

// decorate wraps text in an ANSI sequence, or returns it unchanged without colour.
func decorate(s string, color bool, seq string) string {
	if !color {
		return s
	}
	return seq + s + ansiReset
}

// inlineCode highlights `backticked` spans and strips the backticks.
//
// The backticks come off either way: they are markdown's syntax, not the reader's,
// and a path that has to be typed back is easier to copy without them.
func inlineCode(line string, color bool) string {
	if !strings.Contains(line, "`") {
		return line
	}
	parts := strings.Split(line, "`")
	var b strings.Builder
	for i, p := range parts {
		// Odd indices are the spans between a pair of backticks. An unpaired
		// trailing backtick leaves the last part on an even index, so it is
		// written plainly rather than left half-decorated.
		if i%2 == 1 && i < len(parts)-1 {
			b.WriteString(decorate(p, color, ansiYellow))
			continue
		}
		if i%2 == 1 {
			b.WriteString("`")
		}
		b.WriteString(p)
	}
	return b.String()
}
