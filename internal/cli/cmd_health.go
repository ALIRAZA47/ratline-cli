package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

// `ratline site health` — is it answering, right now.
//
// `doctor` could already tell you a service had failed or a socket was missing, which is
// the configuration being wrong. It could not tell you that a site was returning 500 to
// every request, because nothing ever asked it. A unit can be perfectly active while the
// application inside it is broken, and that is the state nobody notices: systemd is happy,
// nginx is happy, and every visitor gets an error page.
//
// The probe is the same one a deploy already runs before declaring itself successful —
// an HTTP request through the site's own socket or port. Reusing it means "healthy" means
// the same thing continuously as it does at deploy time, rather than two definitions that
// can disagree.

func (g *Globals) checkHealth(ctx context.Context, domains []string, quiet bool) (int, error) {
	st, err := g.Store(ctx)
	if err != nil {
		return 0, err
	}
	mgr, err := g.siteManager(ctx)
	if err != nil {
		return 0, err
	}

	var sites []*state.Site
	if len(domains) == 0 {
		all, err := st.ListSites(ctx, state.SiteFilter{})
		if err != nil {
			return 0, err
		}
		sites = all
	} else {
		for _, d := range domains {
			s, err := st.FindSiteByName(ctx, d)
			if err != nil {
				return 0, err
			}
			sites = append(sites, s)
		}
	}

	failed := 0
	rows := make([]map[string]any, 0, len(sites))
	for _, s := range sites {
		// A static site has nothing listening: nginx serves the files and there is no
		// application to ask. Checking one would mean probing nginx, which is a different
		// question, and reporting a static site as unhealthy because it has no socket
		// would be noise on every server that has one.
		if !s.Dynamic() {
			continue
		}
		// A site somebody deliberately disabled is meant to be returning 503. Reporting it
		// as unhealthy every interval would train people to ignore this command.
		if !s.Enabled {
			continue
		}

		h := &state.Health{Domain: s.Domain, CheckedAt: time.Now().UTC()}
		started := time.Now()
		code, perr := mgr.Unit.Probe(ctx, s)
		h.LatencyMS = int(time.Since(started).Milliseconds())
		h.StatusCode = code

		switch {
		case perr != nil:
			h.OK = false
			h.Detail = firstLine(perr.Error())
		case code >= 500:
			// A 5xx is the application saying it is broken. A 4xx is not: a site whose
			// root path legitimately 404s or 401s is answering correctly, and treating
			// that as down would make this useless for anything behind auth.
			h.OK = false
			h.Detail = fmt.Sprintf("HTTP %d", code)
		default:
			h.OK = true
		}

		if !g.DryRun {
			if err := st.RecordHealth(ctx, h); err != nil {
				return 0, err
			}
			// Re-read so the streak this run produced is what gets reported, rather than
			// the value this function happened to start with.
			if stored, gerr := st.GetHealth(ctx, s.Domain); gerr == nil {
				h = stored
			}
		}
		if !h.OK {
			failed++
		}
		rows = append(rows, map[string]any{
			"domain": h.Domain, "ok": h.OK, "status_code": h.StatusCode,
			"latency_ms": h.LatencyMS, "detail": h.Detail,
			"consecutive_failures": h.ConsecutiveFailures,
		})

		if quiet || g.JSON {
			continue
		}
		if h.OK {
			g.Printf("ok       %-34s HTTP %d in %dms\n", h.Domain, h.StatusCode, h.LatencyMS)
			continue
		}
		since := ""
		if !h.FailingSince.IsZero() {
			since = fmt.Sprintf(", failing since %s", h.FailingSince.Format("2006-01-02 15:04"))
		}
		g.Printf("FAILING  %-34s %s%s\n", h.Domain, h.Detail, since)
	}

	if g.JSON {
		return failed, g.EmitJSON(map[string]any{"checked": len(rows), "failing": failed, "sites": rows})
	}
	if len(rows) == 0 && !quiet {
		g.Printf("Nothing to check: a health check asks a running application, and there are\n" +
			"no enabled dynamic sites on this server.\n")
	}
	return failed, nil
}

func newSiteHealthCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health [domain...]",
		Short: "Ask each site whether it is actually answering",
		Args:  cobra.ArbitraryArgs,
		Long: "Makes an HTTP request through the site's own socket or port and records the\n" +
			"result, so 'is it up' has an answer that does not depend on somebody watching.\n\n" +
			"This is a different question from the one the rest of doctor asks. A unit can be\n" +
			"perfectly active while the application inside it returns 500 to every request:\n" +
			"systemd is happy, nginx is happy, and every visitor gets an error page. Nothing\n" +
			"noticed that before, because nothing asked.\n\n" +
			"A 5xx counts as failing. A 4xx does not — a site whose root path legitimately\n" +
			"401s or 404s is answering correctly, and treating that as down would make this\n" +
			"useless for anything behind authentication.\n\n" +
			"Static sites and disabled sites are skipped: the first has no application to\n" +
			"ask, and the second is meant to be returning 503.\n\n" +
			"Exits non-zero when a site is failing, so it is usable from a timer or a monitor.",
		Example: "  ratline site health                     # every site\n" +
			"  ratline site health app.example.com\n" +
			"  ratline site health --quiet             # exit code only, for a monitor",
		RunE: func(cmd *cobra.Command, args []string) error {
			failed, err := g.checkHealth(cmd.Context(), args, g.Quiet)
			if err != nil {
				return err
			}
			if failed > 0 {
				// Unhealthy is its own exit code, and it already means exactly this: it
				// started but never answered. Automation branching on 7 gets the same
				// meaning here as it does from a deploy.
				return rlerr.New(rlerr.CodeUnhealthy,
					"%s failing its health check", plural(failed, "site"))
			}
			return nil
		},
	}
	return Mutating(cmd)
}

// healthSummary is the one-line form used on the status screen.
func healthSummary(h *state.Health) string {
	if h == nil {
		return ""
	}
	if h.OK {
		return fmt.Sprintf("healthy (%dms)", h.LatencyMS)
	}
	if h.ConsecutiveFailures > 1 {
		return fmt.Sprintf("FAILING x%d — %s", h.ConsecutiveFailures, h.Detail)
	}
	return "FAILING — " + h.Detail
}

// staleHealth reports whether a check is old enough that it should not be believed.
//
// A recorded "healthy" from four days ago on a server whose timer has stopped is worse
// than no answer at all, because it reads as current.
func staleHealth(h *state.Health, now time.Time) bool {
	if h == nil || h.CheckedAt.IsZero() {
		return true
	}
	return now.Sub(h.CheckedAt) > 24*time.Hour
}
