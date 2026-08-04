package cli

import (
	"context"
	"fmt"
	"time"

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

// menuGroup dispatches from the main menu into a group's actions.
//
// Each branch resolves to the same command an operator would type, so the menu is
// a way of discovering the CLI rather than a parallel implementation of it.
func menuGroup(ctx context.Context, g *Globals, p *prompter, group string) error {
	switch group {
	case "user":
		what, err := p.pick("Users", []choice{
			{Value: "add", Label: "Create a tenant"},
			{Value: "list", Label: "List tenants"},
			{Value: "back", Label: "Back"},
		}, "add")
		if err != nil || what == "back" {
			return ErrCancelled
		}
		return g.runFromMenu(ctx, "user", what)

	case "site":
		what, err := p.pick("Sites", []choice{
			{Value: "add", Label: "Create a site"},
			{Value: "list", Label: "List sites"},
			{Value: "back", Label: "Back"},
		}, "add")
		if err != nil || what == "back" {
			return ErrCancelled
		}
		return g.runFromMenu(ctx, "site", what)

	case "key":
		what, err := p.pick("SSH keys", []choice{
			{Value: "list", Label: "List keys"},
			{Value: "audit", Label: "Audit keys", Note: "duplicates, weak, stale, unmanaged"},
			{Value: "back", Label: "Back"},
		}, "list")
		if err != nil || what == "back" {
			return ErrCancelled
		}
		return g.runFromMenu(ctx, "key", what)

	case "cert":
		what, err := p.pick("Certificates", []choice{
			{Value: "list", Label: "List certificates"},
			{Value: "issue", Label: "Issue a certificate"},
			{Value: "test-renewal", Label: "Test renewal", Note: "dry-run everything, spend nothing"},
			{Value: "back", Label: "Back"},
		}, "list")
		if err != nil || what == "back" {
			return ErrCancelled
		}
		return g.runFromMenu(ctx, "cert", what)

	default:
		return ErrCancelled
	}
}

// runFromMenu re-enters the command tree, which keeps the menu from becoming a
// second implementation of anything.
func (g *Globals) runFromMenu(ctx context.Context, group, verb string) error {
	root := NewRootCommand(g)
	root.SetArgs([]string{group, verb})
	root.SetOut(g.Stdout)
	root.SetErr(g.Stderr)
	// setup has already run for this process, so skip it rather than taking the
	// lock a second time and deadlocking against ourselves.
	root.PersistentPreRunE = nil
	return root.ExecuteContext(ctx)
}

// doctorOptions configures a diagnostic run.
type doctorOptions struct {
	Fix bool
}

// runDoctor performs the health checks.
func runDoctor(ctx context.Context, g *Globals, opts doctorOptions) error {
	findings, err := g.diagnose(ctx, opts)
	if err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"findings": findings, "healthy": len(findings) == 0})
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
	return nil
}

func versionString() string {
	return fmt.Sprintf("%s", buildVersion)
}
