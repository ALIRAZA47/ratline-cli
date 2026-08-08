package cli

import (
	"context"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

// printServerSummary draws the live header of the main menu: what is on this box
// and what needs attention, so the first thing an operator sees is the truth
// rather than a menu.
func printServerSummary(ctx context.Context, g *Globals, p *prompter) error {
	p.heading("ratline " + shortVersion())
	st, err := g.Store(ctx)
	if err != nil {
		return err
	}
	users, err := st.ListUsers(ctx)
	if err != nil {
		return err
	}
	sites, err := st.ListSites(ctx, state.SiteFilter{})
	if err != nil {
		return err
	}
	byRuntime := map[string]int{}
	for _, s := range sites {
		byRuntime[s.Runtime]++
	}
	p.note("%d user(s), %d site(s): %d static, %d node, %d python",
		len(users), len(sites), byRuntime["static"], byRuntime["node"], byRuntime["python"])

	// Failed services first: that is the thing most likely to be why someone
	// opened this menu.
	mgr, err := g.siteManager(ctx)
	if err != nil {
		return err
	}
	var failed []string
	for _, s := range sites {
		if !s.Dynamic() {
			continue
		}
		if status, err := mgr.Unit.Status(ctx, s); err == nil && status.Active == "failed" {
			failed = append(failed, s.Domain)
		}
	}
	if len(failed) > 0 {
		p.note("failed services: %v", failed)
	}

	certs, err := st.ListCertificates(ctx)
	if err != nil {
		return err
	}
	soon, expired := 0, 0
	now := time.Now()
	for _, c := range certs {
		switch d := c.DaysRemaining(now); {
		case d < 0:
			expired++
		case d < 21:
			soon++
		}
	}
	if len(certs) == 0 {
		p.note("no certificates recorded")
	} else {
		p.note("%d certificate(s), %d expiring within 21 days, %d expired", len(certs), soon, expired)
	}
	if !g.Cfg.Loaded {
		p.note("no configuration file at %s — run 'ratline init'", g.Cfg.SourcePath)
	}
	return nil
}

func shortVersion() string {
	v := versionString()
	if v == "" {
		return ""
	}
	return v
}

// doctorOptions configures a diagnostic run.
type doctorOptions struct {
	Fix bool
}

// runDoctor runs the sweep and exits non-zero when it found a problem.
//
// The exit code is the whole reason this command claims to be usable from cron, and for a
// long time it returned nil unconditionally: every server, however broken, exited 0. Anyone
// who had wired `ratline doctor` into a monitor was being told everything was fine, and the
// suite's own "doctor is clean" assertions passed whatever it printed, because exit 0 was
// all they could ever see.
//
// Problems fail; warnings do not. A warning is something worth reading — an orphaned unit,
// a certificate 20 days out, a database server that blipped — and paging somebody for one
// is how a check gets muted, after which the problems go unread too.
func runDoctor(ctx context.Context, g *Globals, opts doctorOptions) error {
	findings, err := g.diagnose(ctx, opts)
	if err != nil {
		return err
	}
	problems := 0
	for _, f := range findings {
		if f.Severity == "problem" {
			problems++
		}
	}
	failed := func() error {
		if problems == 0 {
			return nil
		}
		// The same code `site health` uses, and for the same reason: the server is not
		// well. Automation branching on 7 gets one meaning from both.
		return rlerr.New(rlerr.CodeUnhealthy, "%s on this server", plural(problems, "problem")).
			WithHint("each one is listed above with the command that addresses it")
	}
	if g.JSON {
		// Deliberately the same shape as before. The exit code is the fix; adding a field
		// here broke a caller that walks this object, and the count is derivable from the
		// findings it already carries.
		if err := g.EmitJSON(map[string]any{
			"findings": findings, "healthy": len(findings) == 0,
		}); err != nil {
			return err
		}
		return failed()
	}
	if len(findings) == 0 {
		g.Println("Everything checks out.")
		return nil
	}
	tbl := g.Table("severity", "check", "subject", "detail")
	for _, f := range findings {
		tbl.Row(f.Severity, f.Check, f.Subject, f.Detail)
	}
	if err := tbl.Render(); err != nil {
		return err
	}
	g.Println()
	for _, f := range findings {
		if f.Fix != "" {
			g.Printf("  %s: %s\n", f.Check, f.Fix)
		}
	}

	// The sweep names what is wrong; it does not rank it. When the findings point at
	// one resource, say which command explains *why* — otherwise an operator with a
	// list of five findings has to guess which is the cause and which are its
	// consequences.
	if subject := dominantSubject(findings); subject != "" {
		g.Printf("\nMost of that is about %s. For the cause rather than the symptoms:\n"+
			"  ratline troubleshoot %s\n", subject, subject)
	}
	return failed()
}

// dominantSubject finds the one resource most of the findings concern.
//
// Reported only when it is genuinely dominant: on a server with problems spread
// across three tenants there is no single cause to point at, and offering one would
// be a guess dressed up as a diagnosis.
func dominantSubject(findings []Finding) string {
	counts := map[string]int{}
	total := 0
	for _, f := range findings {
		if f.Severity != "problem" || f.Subject == "" {
			continue
		}
		// A path is not a subject anything can be troubleshooted by name.
		if strings.HasPrefix(f.Subject, "/") {
			continue
		}
		counts[f.Subject]++
		total++
	}
	if total < 2 {
		return ""
	}
	for subject, n := range counts {
		if n*2 > total {
			return subject
		}
	}
	return ""
}

func versionString() string {
	return buildVersion
}
