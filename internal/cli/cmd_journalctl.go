package cli

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// `ratline journalctl <site|unit> [-- <args>]` — a unit's journal, read from the
// namespace it actually logs into.
//
// This is the command CLAUDE.md's journald gotcha asks for. Every unit of a site carries
// LogNamespace=<slug>, so `journalctl -u ratline-acme-app_example_com.service` reads the
// shared journal, finds nothing, and says so at exactly the moment somebody is debugging.
// The filter here is the one ratline's own hints use (unit.Manager.JournalFilter), read
// from the unit file on disk: the unit, and --namespace=+<slug> when it names one, which
// merges the namespace with the shared journal so lines from before the site moved into
// its namespace still show.

// journalctlRefusedLong are journalctl's long options that write, redirect what is read,
// or replace the scope ratline set. Matched exactly and as prefixes, because getopt_long
// takes any unambiguous prefix: --rot is --rotate.
var journalctlRefusedLong = map[string]string{
	"vacuum-size":          "journalWrites",
	"vacuum-time":          "journalWrites",
	"vacuum-files":         "journalWrites",
	"rotate":               "journalWrites",
	"flush":                "journalWrites",
	"sync":                 "journalWrites",
	"relinquish-var":       "journalWrites",
	"smart-relinquish-var": "journalWrites",
	"setup-keys":           "journalWrites",
	"update-catalog":       "journalWrites",
	"cursor-file":          "journalWrites",
	"directory":            "journalSource",
	"file":                 "journalSource",
	"root":                 "journalSource",
	"image":                "journalSource",
	"namespace":            "journalSource",
	"machine":              "journalSource",
	"merge":                "journalMerge",
	"unit":                 "journalUnit",
	"user-unit":            "journalUnit",
}

// journalctlRefusedShort are the short spellings of the same: -D --directory, -i --file,
// -M --machine, -m --merge, -u --unit.
var journalctlRefusedShort = map[rune]string{
	'D': "journalSource", 'i': "journalSource", 'M': "journalSource",
	'm': "journalMerge", 'u': "journalUnit",
}

// journalctlArgShorts are the short options that take a value. In a cluster such as
// -n50 or -gfoo, everything after one of them is its value, not more options.
const journalctlArgShorts = "bcDFgiIMnopStTuU"

// journalctlLongPrefixOK are exact long options that happen to be a prefix of a refused
// one, and are not themselves refused: --user (the invoking user's journal) is a prefix
// of --user-unit, and --cursor (start at a cursor, which only reads) of --cursor-file.
var journalctlLongPrefixOK = map[string]bool{"user": true, "cursor": true}

func journalRefusal(kind, flag string) *rlerr.Error {
	switch kind {
	case "journalWrites":
		return rlerr.Usagef("ratline journalctl only reads; %s changes the journal or writes a file", flag).
			WithHint("a site's journal is capped by defaults.journal_max_use in /etc/ratline/config.yaml, which journald enforces itself")
	case "journalSource":
		return rlerr.Usagef("ratline journalctl chooses which journal to read; %s would choose another", flag).
			WithHint("the first argument decides it: a site reads its own namespace, merged with the shared journal")
	case "journalMerge":
		return rlerr.Usagef("%s reads every journal on the machine, which defeats naming one site or unit", flag).
			WithHint("for the whole machine, run journalctl directly")
	default:
		return rlerr.Usagef("ratline journalctl sets the unit itself; %s would add another", flag).
			WithHint("name the site or the unit as the first argument: 'ratline journalctl app.example.com -- -n 100'")
	}
}

// checkJournalctlArgs refuses the options that write, redirect or widen, and reports
// whether the arguments ask to follow.
//
// Every element is checked on its own, values included. A value that happens to look like
// a refused option (`-g -u`) is refused too; `--grep=-u` says the same thing unambiguously.
// The opposite mistake — skipping an element as a value when it was really an option —
// is the one that would let --vacuum-size through, so this never skips.
func checkJournalctlArgs(args []string) (follow bool, err error) {
	for _, a := range args {
		switch {
		case a == "--" || a == "-" || !strings.HasPrefix(a, "-"):
			continue
		case strings.HasPrefix(a, "--"):
			name, _, _ := strings.Cut(strings.TrimPrefix(a, "--"), "=")
			if kind, ok := journalctlRefusedLong[name]; ok {
				return false, journalRefusal(kind, a)
			}
			if !journalctlLongPrefixOK[name] && name != "" {
				var could []string
				for full := range journalctlRefusedLong {
					if strings.HasPrefix(full, name) {
						could = append(could, "--"+full)
					}
				}
				if len(could) > 0 {
					sort.Strings(could)
					return false, journalRefusal(journalctlRefusedLong[strings.TrimPrefix(could[0], "--")], a).
						WithHint("journalctl takes %s as an abbreviation, and it could mean %s; spell the option out",
							a, strings.Join(could, " or "))
				}
			}
			if len(name) >= 2 && strings.HasPrefix("follow", name) {
				follow = true
			}
		default:
			for _, c := range a[1:] {
				if kind, ok := journalctlRefusedShort[c]; ok {
					return false, journalRefusal(kind, a)
				}
				if c == 'f' {
					follow = true
				}
				if strings.ContainsRune(journalctlArgShorts, c) {
					break
				}
			}
		}
	}
	return follow, nil
}

// journalUnitRe is a unit name as journalctl -u takes it here: no patterns, no leading
// dash, no separator.
var journalUnitRe = regexp.MustCompile(`^[A-Za-z0-9:_.\\@-]+$`)

// journalTarget resolves the first argument: a site's domain to its service unit, or a
// unit name checked as one.
type journalTarget struct {
	Unit   string
	Domain string
}

func (g *Globals) resolveJournalTarget(ctx context.Context, name string) (*journalTarget, error) {
	if name == "" || strings.HasPrefix(name, "-") {
		return nil, rlerr.Usagef("which site or unit?").
			WithHint("ratline journalctl app.example.com -- -n 100")
	}
	if !hasUnitSuffix(name) && strings.Contains(name, ".") {
		s, err := g.lookupSite(ctx, name)
		if err != nil {
			return nil, err
		}
		if s != nil {
			if !s.Dynamic() {
				return nil, rlerr.Usagef("%s is a %s site, which has no unit and no journal: nginx serves it from disk", s.Domain, s.Runtime).
					WithHint("its logs are nginx's: 'ratline logs %s --access' or '--error'", s.Domain)
			}
			return &journalTarget{Unit: validate.UnitName(s.Owner, s.Domain), Domain: s.Domain}, nil
		}
		// A name that reads as a hostname and is not a site is far more likely a typo of
		// one than a unit, and journalctl would answer "-- No entries --" either way —
		// which is the empty screen this command exists to prevent. A unit with a dot in
		// it (php8.2-fpm) has a last label that is not a word, and passes.
		if looksLikeHostname(name) {
			return nil, rlerr.Usagef("there is no site called %s", name).
				WithHint("'ratline site list' shows them; a unit is named with its suffix, as in %s.service", name)
		}
	}
	if hasGlob(name) {
		return nil, rlerr.Usagef("ratline journalctl reads one unit, not a pattern (%s)", name).
			WithHint("name the site or the unit")
	}
	if len(name) > 255 || !journalUnitRe.MatchString(name) {
		return nil, rlerr.Usagef("invalid unit name %q", name).
			WithHint("a site's domain, or a unit such as nginx.service")
	}
	return &journalTarget{Unit: name}, nil
}

// looksLikeHostname reports a dotted name whose last label is letters only, the shape of
// a domain rather than of a versioned unit name.
func looksLikeHostname(name string) bool {
	if _, err := validate.Domain(name); err != nil {
		return false
	}
	last := name[strings.LastIndex(name, ".")+1:]
	if len(last) < 2 {
		return false
	}
	for _, c := range last {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// journalctlArgv builds the argv: the filter ratline's own hints use, --no-pager, and
// the operator's own arguments last.
func (g *Globals) journalctlArgv(t *journalTarget, extra []string) []string {
	mgr := &unit.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner}
	argv := append(mgr.JournalFilter(t.Unit), "--no-pager")
	return append(argv, extra...)
}

func newJournalctlCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "journalctl <domain|unit> -- [args...]",
		Short:   "Read a site's or a unit's journal, from the namespace it actually logs into",
		GroupID: GroupOps,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return rlerr.Usagef("which site or unit?").
					WithHint("ratline journalctl app.example.com -- -n 100 (journalctl's own options go after --)")
			}
			return nil
		},
		Long: "Every unit ratline runs for a site logs into the site's own journal namespace,\n" +
			"so 'journalctl -u ratline-…' on its own reads the shared journal and finds\n" +
			"nothing. This builds the filter ratline's own hints use: the site's unit, and\n" +
			"--namespace=+<slug> when the unit on disk names one, which merges the site's\n" +
			"namespace with the shared journal so lines from before it moved still show.\n" +
			"--no-pager is always passed.\n\n" +
			"The first argument is a site's domain (or alias), which reads the site's\n" +
			"service, or a unit name such as nginx.service or a site's worker unit, which\n" +
			"reads that unit — from its namespace, if its unit file names one. A static\n" +
			"site has no unit; its logs are nginx's, under 'ratline logs --access'.\n\n" +
			"journalctl's own options go after --: -n 200, --since '1 hour ago', -p err,\n" +
			"-g <pattern>, -o json, -f to follow (which runs until Ctrl-C, with no\n" +
			"timeout, and cannot be combined with --json). Refused, because they write,\n" +
			"read some other journal, or replace the scope: --vacuum-*, --rotate, --flush,\n" +
			"--sync, --relinquish-var, --smart-relinquish-var, --setup-keys,\n" +
			"--update-catalog, --cursor-file, -D/--directory, -i/--file, --root, --image,\n" +
			"--namespace, -M/--machine, -m/--merge and -u/--unit. An abbreviation of any\n" +
			"of them is refused too, since journalctl would accept it.\n\n" +
			"Read-only, so it never takes the server lock. Root only: a tenant reads their\n" +
			"own site's journal with 'ratline logs <domain> --journal'.",
		Example: "  ratline journalctl app.example.com\n" +
			"  ratline journalctl app.example.com -- -n 200 -p warning\n" +
			"  ratline journalctl app.example.com -- -f\n" +
			"  ratline journalctl app.example.com -- --since '10 min ago' -o cat\n" +
			"  ratline journalctl nginx.service -- -n 50",
		ValidArgsFunction: func(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return g.completeDomains(cmd, args, prefix)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.runJournalctl(cmd.Context(), args[0], args[1:])
		},
	}
	ProgramArgvExample(cmd, "ratline journalctl app.example.com -- -n 100 -f")
	// Not Mutating: it reads, so it takes no lock and runs under --dry-run as it would
	// without it.
	return ProgramArgv(cmd)
}

// runJournalctl is everything after cobra: separated so a test reaches it without root.
func (g *Globals) runJournalctl(ctx context.Context, target string, rest []string) error {
	extra, err := passthroughArgs(rest)
	if err != nil {
		return err
	}
	follow, err := checkJournalctlArgs(extra)
	if err != nil {
		return err
	}
	if follow && g.JSON {
		return rlerr.Usagef("--follow runs until it is interrupted, so there is no one envelope to write under --json").
			WithHint("drop --json to follow, or pass -n to read a fixed number of lines as JSON")
	}
	t, err := g.resolveJournalTarget(ctx, target)
	if err != nil {
		return err
	}
	info := map[string]any{"unit": t.Unit}
	if t.Domain != "" {
		info["domain"] = t.Domain
	}
	return g.passthroughRun(ctx, system.Cmd{
		Name: "journalctl", Args: g.journalctlArgv(t, extra), Label: "journalctl",
	}, follow, info, nil)
}
