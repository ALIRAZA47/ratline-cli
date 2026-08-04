package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ALIRAZA47/ratline-cli/internal/buildinfo"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// manSection is 8 because ratline is a system administration tool.
const manSection = "8"

func newManCommand(g *Globals) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "man",
		Short: "Write man pages for every command",
		Long: "Generates one roff page per command. With no --dir the top-level page is\n" +
			"written to stdout, which is handy for previewing:\n\n" +
			"  ratline man | man -l -",
		GroupID: GroupOps,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := cmd.Root()
			if dir == "" {
				_, err := fmt.Fprint(g.Stdout, renderMan(root))
				return err
			}
			if _, err := system.EnsureDir(dir, 0o755, system.KeepUnchanged, system.KeepUnchanged); err != nil {
				return err
			}
			count := 0
			var walk func(*cobra.Command) error
			walk = func(c *cobra.Command) error {
				if c.Hidden || c.Name() == "help" {
					return nil
				}
				name := strings.ReplaceAll(c.CommandPath(), " ", "-") + "." + manSection
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte(renderMan(c)), 0o644); err != nil {
					return rlerr.Wrap(err, rlerr.CodeGeneric, "writing %s", path)
				}
				count++
				for _, sub := range c.Commands() {
					if err := walk(sub); err != nil {
						return err
					}
				}
				return nil
			}
			if err := walk(root); err != nil {
				return err
			}
			g.Log.Info("wrote man pages", "count", count, "dir", dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "Directory to write pages into (default: stdout)")
	return NonRoot(cmd)
}

// renderMan produces a roff page from cobra's own metadata.
//
// Hand-rolled rather than pulling in a markdown-to-roff dependency: the output
// is entirely mechanical, and the page layout is worth controlling directly.
func renderMan(cmd *cobra.Command) string {
	var b strings.Builder
	name := cmd.CommandPath()
	dashed := strings.ReplaceAll(name, " ", "-")
	date := buildinfo.Date
	if date == "unknown" {
		date = time.Now().UTC().Format("2006-01-02")
	}

	fmt.Fprintf(&b, ".TH %s %s \"%s\" \"ratline %s\" \"System Administration\"\n",
		roffEscape(strings.ToUpper(dashed)), manSection, date, roffEscape(buildinfo.Version))

	b.WriteString(".SH NAME\n")
	fmt.Fprintf(&b, "%s \\- %s\n", roffEscape(dashed), roffEscape(cmd.Short))

	b.WriteString(".SH SYNOPSIS\n")
	fmt.Fprintf(&b, ".B %s\n", roffEscape(name))
	if cmd.HasAvailableSubCommands() {
		b.WriteString(".RI [ command ]\n")
	}
	if cmd.Runnable() {
		b.WriteString(".RI [ options ]\n")
	}

	if long := cmd.Long; long != "" {
		b.WriteString(".SH DESCRIPTION\n")
		b.WriteString(roffParagraphs(long))
	} else if cmd.Short != "" {
		b.WriteString(".SH DESCRIPTION\n")
		fmt.Fprintf(&b, "%s\n", roffEscape(cmd.Short))
	}

	if cmd.HasAvailableSubCommands() {
		b.WriteString(".SH COMMANDS\n")
		for _, sub := range cmd.Commands() {
			if sub.Hidden || sub.Name() == "help" {
				continue
			}
			fmt.Fprintf(&b, ".TP\n.B %s\n%s\n", roffEscape(sub.Name()), roffEscape(sub.Short))
		}
	}

	if flags := cmd.NonInheritedFlags(); flags.HasAvailableFlags() {
		b.WriteString(".SH OPTIONS\n")
		writeFlags(&b, flags)
	}
	if flags := cmd.InheritedFlags(); flags.HasAvailableFlags() {
		b.WriteString(".SH GLOBAL OPTIONS\n")
		writeFlags(&b, flags)
	}

	if cmd.Example != "" {
		b.WriteString(".SH EXAMPLES\n")
		for _, line := range strings.Split(cmd.Example, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			fmt.Fprintf(&b, ".PP\n%s\n", roffEscape(strings.TrimSpace(line)))
		}
	}

	b.WriteString(".SH EXIT STATUS\n")
	for _, e := range []struct {
		code int
		desc string
	}{
		{0, "Success."},
		{1, "An unclassified failure."},
		{2, "Bad flags, bad arguments, or input that failed validation."},
		{3, "A precondition was not met."},
		{4, "An external command failed."},
		{5, "Another ratline invocation holds the lock."},
		{6, "The operation failed and so did its rollback; a human is needed."},
		{7, "The application started but never became healthy."},
		{8, "An ACME challenge failed."},
		{9, "The request would exceed a certificate authority rate limit."},
		{10, "A prompt was required but no input was available."},
	} {
		fmt.Fprintf(&b, ".TP\n.B %d\n%s\n", e.code, roffEscape(e.desc))
	}

	b.WriteString(".SH FILES\n")
	for _, f := range []struct{ path, desc string }{
		{"/etc/ratline/config.yaml", "Configuration."},
		{"/var/lib/ratline/state.db", "State index and audit history."},
		{"/var/log/ratline/audit.log", "One JSON record per mutating invocation."},
		{"/etc/nginx/ratline/custom/", "Operator additions, included and never regenerated."},
	} {
		fmt.Fprintf(&b, ".TP\n.B %s\n%s\n", roffEscape(f.path), roffEscape(f.desc))
	}

	if cmd.HasParent() {
		b.WriteString(".SH SEE ALSO\n")
		fmt.Fprintf(&b, ".BR %s (%s)\n", roffEscape(strings.ReplaceAll(cmd.Root().CommandPath(), " ", "-")), manSection)
	}
	return b.String()
}

func writeFlags(b *strings.Builder, flags *pflag.FlagSet) {
	flags.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		b.WriteString(".TP\n")
		if f.Shorthand != "" {
			fmt.Fprintf(b, ".BR \\-%s \", \" \\-\\-%s\n", roffEscape(f.Shorthand), roffEscape(f.Name))
		} else {
			fmt.Fprintf(b, ".B \\-\\-%s\n", roffEscape(f.Name))
		}
		usage := f.Usage
		if f.DefValue != "" && f.DefValue != "false" && f.Value.Type() != "bool" {
			usage += fmt.Sprintf(" (default %s)", f.DefValue)
		}
		fmt.Fprintf(b, "%s\n", roffEscape(usage))
	})
}

// roffEscape neutralises the four sequences roff treats specially.
func roffEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\e`)
	s = strings.ReplaceAll(s, "-", `\-`)
	// A leading dot or apostrophe would start a roff request.
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "'") {
		s = `\&` + s
	}
	return s
}

func roffParagraphs(s string) string {
	var b strings.Builder
	for _, para := range strings.Split(strings.TrimSpace(s), "\n\n") {
		para = strings.Join(strings.Fields(para), " ")
		if para == "" {
			continue
		}
		fmt.Fprintf(&b, ".PP\n%s\n", roffEscape(para))
	}
	return b.String()
}
