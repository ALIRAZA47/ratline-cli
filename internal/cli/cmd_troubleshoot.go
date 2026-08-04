package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/diag"
	"github.com/ALIRAZA47/ratline-cli/internal/nginx"
	"github.com/ALIRAZA47/ratline-cli/internal/sshkey"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
)

// `troubleshoot` is the causal half of the diagnostics, and `doctor` is the sweep.
//
// doctor runs every check across the server and reports what is wrong, in whatever
// order the checks happen to run. That is the right shape for a cron job and the
// wrong shape for a human with something broken in front of them, who gets a list
// and has to rank it themselves.
//
// troubleshoot takes one subject, walks its preconditions in dependency order, and
// stops at the first failure — which, because the order is the order things depend
// on each other, is the cause. What it broke is reported as not-checked rather than
// as more problems. It works on anything ratline manages, because a site, a tenant,
// a key, a certificate, nginx, sshd and the host all have that same shape.

func newTroubleshootCommand(g *Globals) *cobra.Command {
	var (
		kind    string
		all     bool
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:     "troubleshoot [subject]",
		Short:   "Find why something is broken, in the order things depend on each other",
		GroupID: GroupOps,
		Args:    cobra.MaximumNArgs(1),
		Long: "Diagnoses anything ratline manages. The subject is worked out from the\n" +
			"argument — a domain is a site, a name is a tenant, SHA256:… is a key — and\n" +
			"'nginx', 'ssh' and 'server' name the subsystems. With no argument it\n" +
			"diagnoses the server.\n\n" +
			"Checks run in dependency order and stop at the first failure, so the first\n" +
			"failure is the cause: a socket nginx cannot open explains the 502, and the\n" +
			"502 is not reported as a second problem. Steps that depended on it are\n" +
			"marked as not checked rather than guessed at.\n\n" +
			"Read-only. It never takes the lock, so it is safe against a site that is\n" +
			"currently on fire.",
		Example: "  ratline troubleshoot                          # the server\n" +
			"  ratline troubleshoot app.example.com          # one site's request path\n" +
			"  ratline troubleshoot acme                     # a tenant: account, home, keys, sites\n" +
			"  ratline troubleshoot SHA256:AbC…              # can this key log in, and to what\n" +
			"  ratline troubleshoot nginx\n" +
			"  ratline troubleshoot ssh                      # including the lockout guard\n" +
			"  ratline troubleshoot app.example.com --json | jq -r .data.likely_cause",
		ValidArgsFunction: g.completeSubjects,
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			env, err := g.diagEnv(cmd.Context())
			if err != nil {
				return err
			}
			env.ProbeTimeout = timeout

			var subject *diag.Subject
			if kind != "" {
				subject, err = diag.ResolveKind(cmd.Context(), env, arg, diag.Kind(kind))
			} else {
				subject, err = diag.Resolve(cmd.Context(), env, arg)
			}
			if err != nil {
				return err
			}

			report, err := diag.Diagnose(cmd.Context(), env, subject)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(report)
			}
			return g.printDiagnosis(report, all)
		},
	}
	f := cmd.Flags()
	f.StringVar(&kind, "kind", "",
		"Say what the subject is when the name is ambiguous: "+strings.Join(kindValues(), ", "))
	f.BoolVar(&all, "all", false, "Show every step, not only the ones that need attention")
	f.DurationVar(&timeout, "probe-timeout", 0,
		"How long any single network probe may take (default 3s)")
	_ = cmd.RegisterFlagCompletionFunc("kind", completeFixed(kindValues()...))
	// Read-only, so it is not Mutating and never takes the lock. It does need root:
	// the socket, the unit and the state database are not readable otherwise, and a
	// check that silently could not look would be worse than one that refuses.
	return cmd
}

func kindValues() []string {
	out := make([]string, 0, len(diag.Kinds))
	for _, k := range diag.Kinds {
		out = append(out, string(k))
	}
	return out
}

// diagEnv assembles everything the check sets need.
func (g *Globals) diagEnv(ctx context.Context) (*diag.Env, error) {
	st, err := g.Store(ctx)
	if err != nil {
		return nil, err
	}
	siteMgr, err := g.siteManager(ctx)
	if err != nil {
		return nil, err
	}
	certMgr, err := g.certManager(ctx)
	if err != nil {
		// A missing certificate manager makes the TLS checks skip rather than the
		// whole diagnosis fail: everything else is still worth knowing.
		g.Log.Debug("the certificate inventory is unavailable", "err", err)
	}
	return &diag.Env{
		Cfg:    g.Cfg,
		Log:    g.Log,
		Runner: g.Runner,
		Bins:   g.Bins,
		State:  st,
		OS:     g.OS,
		Site:   siteMgr,
		Nginx:  &nginx.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, DryRun: true},
		Unit:   &unit.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, DryRun: true},
		TLS:    certMgr,
		Keys:   &sshkey.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, State: st, DryRun: true},
	}, nil
}

// completeSubjects offers everything that can be diagnosed.
func (g *Globals) completeSubjects(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	// The subsystems first: they are always valid and need no database, so they are
	// still offered on a server where completion cannot read state.
	out := []string{
		"server\tthe host and everything ratline needs from it",
		"nginx\tthe web server and its generated configuration",
		"ssh\tsshd, the drop-in, and the lockout guard",
	}
	filtered := make([]string, 0, len(out))
	for _, c := range out {
		if strings.HasPrefix(c, prefix) {
			filtered = append(filtered, c)
		}
	}
	domains, _ := g.completeDomains(cmd, args, prefix)
	users, _ := g.completeUsers(cmd, args, prefix)
	certs, _ := g.completeCertificates(cmd, args, prefix)
	filtered = append(filtered, domains...)
	filtered = append(filtered, users...)
	filtered = append(filtered, certs...)
	return filtered, cobra.ShellCompDirectiveNoFileComp
}

// printDiagnosis renders a report.
//
// Everything that needs attention is always shown. Passing steps are folded into a
// count unless --all, because on a healthy subject the interesting output is the
// last line and twelve `ok` rows push it off the screen.
func (g *Globals) printDiagnosis(r *diag.Report, all bool) error {
	header := r.Kind
	if r.Subject != "" {
		header = r.Subject
	}
	g.Printf("%s", header)
	if r.Summary != "" {
		g.Printf("  —  %s", r.Summary)
	}
	g.Printf("\n\n")

	hidden := 0
	for _, s := range r.Steps {
		if !all && s.Verdict == diag.OK {
			hidden++
			continue
		}
		g.Printf("%s\n", diagLine(s))
	}
	if hidden > 0 {
		g.Printf("  %s  %s passed\n", mark(diag.OK), plural(hidden, "check"))
	}

	g.Printf("\n")
	switch {
	case r.Cause != "":
		g.Printf("Likely cause: %s\n", r.Cause)
		if r.Fix != "" {
			g.Printf("Try:          %s\n", r.Fix)
		}
		if r.Topic != "" {
			g.Printf("Background:   ratline explain %s\n", r.Topic)
		}
	case r.Warnings > 0:
		// Said plainly, so a warning is never mistaken for the diagnosis of whatever
		// is actually being investigated.
		g.Printf("Nothing has failed. %s above %s worth reading.\n",
			plural(r.Warnings, "warning"), verbFor(r.Warnings))
	default:
		g.Printf("Nothing is wrong with %s.\n", header)
	}
	return nil
}

// diagLine renders one step.
func diagLine(s diag.Step) string {
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(mark(s.Verdict))
	b.WriteString("  ")
	b.WriteString(s.Title)
	if s.Detail != "" {
		b.WriteString("  —  ")
		b.WriteString(s.Detail)
	}
	return b.String()
}

// mark is the fixed-width verdict column. Words rather than symbols: this output
// gets pasted into issues and chat, where a coloured glyph survives neither.
func mark(v diag.Verdict) string {
	switch v {
	case diag.Failed:
		return "FAIL"
	case diag.Warning:
		return "warn"
	case diag.Skipped:
		return "--  "
	default:
		return "ok  "
	}
}

func verbFor(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// newSiteTroubleshootCommand keeps `ratline site troubleshoot` working.
//
// The same engine and the same check list — it exists because that is where someone
// looks for it when the thing that is broken is a site, and because it was the
// documented spelling before the general command existed.
func newSiteTroubleshootCommand(g *Globals) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:               "troubleshoot <domain>",
		Short:             "Walk one site's request path and find where it breaks",
		Args:              cobra.ExactArgs(1),
		Long:              "The site-scoped spelling of 'ratline troubleshoot', which diagnoses anything.",
		Example:           "  ratline site troubleshoot app.example.com",
		ValidArgsFunction: g.completeDomains,
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := g.diagEnv(cmd.Context())
			if err != nil {
				return err
			}
			subject, err := diag.ResolveKind(cmd.Context(), env, args[0], diag.KindSite)
			if err != nil {
				return err
			}
			report, err := diag.Diagnose(cmd.Context(), env, subject)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(report)
			}
			return g.printDiagnosis(report, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Show every step, not only the ones that need attention")
	return cmd
}
