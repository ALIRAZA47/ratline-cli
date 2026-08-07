package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// `ratline site cron` and `ratline site worker`.
//
// Two faces on one mechanism, because a scheduled job and a long-running worker differ
// only in what starts them. They are systemd units rather than crontab lines and rather
// than a supervisor of ratline's own, for one reason: a crontab line runs outside every
// limit the site is held to, and nothing in `status`, `doctor` or `reconcile` knows it is
// there. The thing on a server most likely to be quietly broken should not also be the
// thing nothing watches.

// siteUnitOpts is what both `add` commands collect.
type siteUnitOpts struct {
	command     string
	schedule    string
	description string
	timeout     string
	memoryMax   string
	persistent  bool
	disabled    bool
}

func (g *Globals) siteUnitManager(ctx context.Context) (*unit.Manager, *state.Store, error) {
	st, err := g.Store(ctx)
	if err != nil {
		return nil, nil, err
	}
	return &unit.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, DryRun: g.DryRun}, st, nil
}

// addSiteUnit is the shared body of `cron add` and `worker add`.
func (g *Globals) addSiteUnit(cmd *cobra.Command, kind, domain, name string, o siteUnitOpts) error {
	ctx := cmd.Context()
	if err := validate.JobName(name); err != nil {
		return err
	}
	if o.command == "" {
		return rlerr.Usagef("--command is required").
			WithHint("what to run, as an absolute path and its arguments. systemd parses " +
				"this itself, so it is not a shell line: anything needing a pipe belongs " +
				"in a script")
	}
	// systemd's ExecStart is argv, not a shell line, and quietly accepting `a | b` would
	// pass "|" and "b" to a as arguments. Saying so is better than the confusing failure.
	for _, meta := range []string{"|", "&&", "||", ";", ">", "<", "$(", "`"} {
		if strings.Contains(o.command, meta) {
			return rlerr.Usagef("--command contains %q, which systemd will not interpret", meta).
				WithHint("ExecStart is an argv, not a shell line. Put it in a script and " +
					"run that: --command '/home/…/app/bin/nightly'")
		}
	}

	mgr, st, err := g.siteUnitManager(ctx)
	if err != nil {
		return err
	}
	site, err := st.GetSite(ctx, domain)
	if err != nil {
		return err
	}

	u := &state.SiteUnit{
		Domain: domain, Name: name, Kind: kind,
		Command: o.command, Description: o.description,
		Timeout: o.timeout, MemoryMax: o.memoryMax,
		Persistent: o.persistent, Enabled: !o.disabled,
		CreatedBy: g.Invoked(),
	}

	if kind == state.UnitJob {
		if o.schedule == "" {
			return rlerr.Usagef("--schedule is required for a job").
				WithHint("cron works: '0 3 * * *'. So does systemd's own: 'daily', '03:00'")
		}
		calendar, err := validate.Schedule(o.schedule)
		if err != nil {
			return err
		}
		// Verified by systemd itself before anything is written, and the operator is shown
		// what it decided. A translated cron expression the operator cannot see is one
		// they cannot check, and a schedule that is silently wrong runs at the wrong time
		// for months before anybody notices.
		next, err := mgr.VerifySchedule(ctx, calendar)
		if err != nil {
			return err
		}
		u.Schedule = calendar
		if !g.Quiet && !g.JSON {
			if validate.LooksLikeCron(o.schedule) {
				g.Printf("%s becomes %s\n", o.schedule, calendar)
			}
			for i, n := range next {
				if i == 0 {
					g.Printf("next runs:\n")
				}
				g.Printf("    %s\n", n)
			}
		}
	}

	if existing, gerr := st.GetSiteUnit(ctx, domain, name); gerr == nil {
		// Safe to run twice, like everything else — but say what changed rather than
		// reporting success for a no-op the operator may not have intended.
		if existing.Command == u.Command && existing.Schedule == u.Schedule {
			g.Printf("%s %s on %s is already exactly this\n", kind, name, domain)
			return g.emitSiteUnit(u, nil)
		}
		g.Printf("replacing the existing %s %s on %s\n", kind, name, domain)
	}

	service, timer, err := mgr.RenderSiteUnit(site, u)
	if err != nil {
		return err
	}

	rb := system.NewRollback(g.Log)
	defer rb.UnwindOn(ctx, &err)

	if err = mgr.InstallSiteUnit(ctx, site, u, service, timer, rb); err != nil {
		return err
	}
	if !g.DryRun {
		if err = st.PutSiteUnit(ctx, u); err != nil {
			return err
		}
		rb.Push("recorded the "+kind, func(c context.Context) error {
			return st.DeleteSiteUnit(c, domain, name)
		})
	}
	if err = mgr.EnableSiteUnit(ctx, site, u); err != nil {
		return err
	}
	rb.Commit()

	g.Printf("\n%s %s is set up on %s\n", kind, name, domain)
	if kind == state.UnitJob {
		g.Printf("    ratline site cron run %s %s      # run it now, without waiting\n", domain, name)
		g.Printf("    ratline site cron logs %s %s\n", domain, name)
	} else {
		g.Printf("    ratline site worker logs %s %s\n", domain, name)
	}
	return g.emitSiteUnit(u, nil)
}

func (g *Globals) emitSiteUnit(u *state.SiteUnit, s *unit.SiteUnitStatus) error {
	if !g.JSON {
		return nil
	}
	out := map[string]any{
		"domain": u.Domain, "name": u.Name, "kind": u.Kind,
		"command": u.Command, "enabled": u.Enabled,
	}
	if u.Schedule != "" {
		out["schedule"] = u.Schedule
	}
	if s != nil {
		out["active"] = s.Active
		out["next_run"] = s.NextRun
	}
	return g.EmitJSON(out)
}

func (g *Globals) listSiteUnits(cmd *cobra.Command, kind, domain string) error {
	ctx := cmd.Context()
	mgr, st, err := g.siteUnitManager(ctx)
	if err != nil {
		return err
	}
	units, err := st.ListSiteUnits(ctx, domain, kind)
	if err != nil {
		return err
	}
	if g.JSON {
		rows := make([]map[string]any, 0, len(units))
		for _, u := range units {
			rows = append(rows, map[string]any{
				"domain": u.Domain, "name": u.Name, "kind": u.Kind,
				"command": u.Command, "schedule": u.Schedule, "enabled": u.Enabled,
			})
		}
		return g.EmitJSON(map[string]any{"units": rows})
	}
	if len(units) == 0 {
		noun := "scheduled jobs"
		if kind == state.UnitWorker {
			noun = "workers"
		}
		g.Printf("No %s on %s.\n", noun, domain)
		return nil
	}
	for _, u := range units {
		site, serr := st.GetSite(ctx, u.Domain)
		status := ""
		if serr == nil {
			s := mgr.SiteUnitStatusOf(ctx, site, u)
			status = s.Active
			if s.NextRun != "" && s.NextRun != "n/a" {
				status += ", next " + s.NextRun
			}
		}
		g.Printf("%-20s %-24s %s\n", u.Name, u.Schedule, status)
		g.Printf("  %s\n", u.Command)
	}
	return nil
}

func (g *Globals) removeSiteUnit(cmd *cobra.Command, kind, domain, name string) error {
	ctx := cmd.Context()
	mgr, st, err := g.siteUnitManager(ctx)
	if err != nil {
		return err
	}
	u, err := st.GetSiteUnit(ctx, domain, name)
	if err != nil {
		return err
	}
	if u.Kind != kind {
		return rlerr.Usagef("%s on %s is a %s, not a %s", name, domain, u.Kind, kind)
	}
	site, err := st.GetSite(ctx, domain)
	if err != nil {
		return err
	}
	if err := mgr.RemoveSiteUnit(ctx, site, u); err != nil {
		return err
	}
	if !g.DryRun {
		if err := st.DeleteSiteUnit(ctx, domain, name); err != nil {
			return err
		}
	}
	g.Printf("Removed the %s %s from %s\n", kind, name, domain)
	return nil
}

func newSiteCronCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Scheduled jobs for a site",
		Long: "A job runs on a schedule as the site's tenant, in the site's directory, with the\n" +
			"site's .env and the site's sandbox and memory ceiling.\n\n" +
			"These are systemd timers rather than crontab lines. A crontab line runs outside\n" +
			"every limit the site is held to — no memory ceiling, no filesystem protection, no\n" +
			"cgroup — and nothing in status, doctor or reconcile knows it is there.\n\n" +
			"Schedules may be written as cron or in systemd's own syntax. A cron expression is\n" +
			"translated, and either way systemd is asked to confirm it before anything is\n" +
			"written; the next few run times are printed so you can check it means what you\n" +
			"intended.",
	}
	cmd.AddCommand(
		newSiteCronAddCommand(g),
		newSiteUnitListCommand(g, state.UnitJob),
		newSiteUnitRemoveCommand(g, state.UnitJob),
		newSiteCronRunCommand(g),
		newSiteUnitLogsCommand(g, state.UnitJob),
	)
	return cmd
}

func newSiteWorkerCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Long-running background processes for a site",
		Long: "A worker runs alongside the site's own service, as the same tenant, with the same\n" +
			"directory, .env, sandbox and ceiling — a queue consumer, a websocket process, a\n" +
			"scheduler daemon.\n\n" +
			"It is bound to the site: stopping the site stops its workers, and deleting the\n" +
			"site removes them. A worker left running against a half-removed site is how a\n" +
			"queue gets consumed by a process nobody remembers starting.",
	}
	cmd.AddCommand(
		newSiteWorkerAddCommand(g),
		newSiteUnitListCommand(g, state.UnitWorker),
		newSiteUnitRemoveCommand(g, state.UnitWorker),
		newSiteUnitLogsCommand(g, state.UnitWorker),
	)
	return cmd
}

func newSiteCronAddCommand(g *Globals) *cobra.Command {
	var o siteUnitOpts
	cmd := &cobra.Command{
		Use:   "add <domain> <name>",
		Short: "Add a scheduled job",
		Args:  cobra.ExactArgs(2),
		Example: "  ratline site cron add app.example.com nightly \\\n" +
			"      --schedule '0 3 * * *' --command '/home/acme/app.example.com/app/bin/nightly'\n\n" +
			"  # systemd's own syntax works too\n" +
			"  ratline site cron add app.example.com digest \\\n" +
			"      --schedule 'Mon *-*-* 09:00' --command '…/bin/digest' --persistent",
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.addSiteUnit(cmd, state.UnitJob, args[0], args[1], o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.schedule, "schedule", "", "When to run: cron ('0 3 * * *') or systemd ('daily') (required)")
	f.StringVar(&o.command, "command", "", "What to run, as a path and arguments (required)")
	f.StringVar(&o.description, "description", "", "What this job is for")
	f.StringVar(&o.timeout, "timeout", "", "Give up after this long, e.g. 30m")
	f.StringVar(&o.memoryMax, "memory-max", "", "Memory ceiling for this job (default: the site's)")
	f.BoolVar(&o.persistent, "persistent", false, "Run a firing missed while the server was off")
	f.BoolVar(&o.disabled, "disabled", false, "Create it without arming the timer")
	Required(cmd, "schedule")
	Required(cmd, "command")
	return Mutating(cmd)
}

func newSiteWorkerAddCommand(g *Globals) *cobra.Command {
	var o siteUnitOpts
	cmd := &cobra.Command{
		Use:   "add <domain> <name>",
		Short: "Add a long-running worker",
		Args:  cobra.ExactArgs(2),
		Example: "  ratline site worker add app.example.com queue \\\n" +
			"      --command '/home/acme/app.example.com/app/bin/worker'",
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.addSiteUnit(cmd, state.UnitWorker, args[0], args[1], o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.command, "command", "", "What to run, as a path and arguments (required)")
	f.StringVar(&o.description, "description", "", "What this worker is for")
	f.StringVar(&o.memoryMax, "memory-max", "", "Memory ceiling for this worker (default: the site's)")
	f.BoolVar(&o.disabled, "disabled", false, "Create it without starting it")
	Required(cmd, "command")
	return Mutating(cmd)
}

func newSiteUnitListCommand(g *Globals, kind string) *cobra.Command {
	what := "jobs"
	if kind == state.UnitWorker {
		what = "workers"
	}
	cmd := &cobra.Command{
		Use:   "list <domain>",
		Short: "List a site's " + what,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.listSiteUnits(cmd, kind, args[0])
		},
	}
	return NonRoot(cmd)
}

func newSiteUnitRemoveCommand(g *Globals, kind string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "remove <domain> <name>",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove a " + kind,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.removeSiteUnit(cmd, kind, args[0], args[1])
		},
	}
	return Mutating(cmd)
}

func newSiteCronRunCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <domain> <name>",
		Short: "Run a scheduled job now, without waiting for its timer",
		Args:  cobra.ExactArgs(2),
		Long: "Starts the job's service directly. The timer is untouched, so this does not\n" +
			"affect when it next runs on its own.\n\n" +
			"This is the command for finding out whether a job works, rather than waiting\n" +
			"until 3am to discover it does not.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			domain, name := args[0], args[1]
			st, err := g.Store(ctx)
			if err != nil {
				return err
			}
			u, err := st.GetSiteUnit(ctx, domain, name)
			if err != nil {
				return err
			}
			site, err := st.GetSite(ctx, domain)
			if err != nil {
				return err
			}
			unitName := unit.SiteUnitName(site.Slug, u.Kind, u.Name)
			if g.DryRun {
				g.Printf("would run %s\n", unitName)
				return nil
			}
			if _, err := g.Runner.Run(ctx, system.Cmd{
				Name: "systemctl", Args: []string{"start", unitName},
				Mutates: true, Label: "run the job",
			}); err != nil {
				return err
			}
			_ = st.RecordSiteUnitRun(ctx, domain, name, time.Now())
			g.Printf("Started %s. Its output:\n    ratline site cron logs %s %s\n",
				name, domain, name)
			return nil
		},
	}
	return Mutating(cmd)
}

func newSiteUnitLogsCommand(g *Globals, kind string) *cobra.Command {
	var lines int
	cmd := &cobra.Command{
		Use:   "logs <domain> <name>",
		Short: "Show what a " + kind + " last printed",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			mgr, st, err := g.siteUnitManager(ctx)
			if err != nil {
				return err
			}
			u, err := st.GetSiteUnit(ctx, args[0], args[1])
			if err != nil {
				return err
			}
			site, err := st.GetSite(ctx, args[0])
			if err != nil {
				return err
			}
			// The unit writes to a file under the site's logs directory rather than to the
			// journal, so that the tenant can read their own job's output and logrotate
			// can age it out with everything else. Reading the journal instead was the
			// first version, and it reported "Nothing logged yet" for a job that had just
			// run perfectly well.
			path := unit.SiteUnitLogPath(g.Cfg, site, u)
			if system.Exists(path) {
				return printTail(g, path, lines)
			}
			// Nothing there yet is the ordinary case for a job that has never fired. But a
			// unit that failed before it could open its log file leaves its complaint in
			// the journal, and that is exactly when somebody runs this command.
			out := mgr.Logs(ctx, unit.SiteUnitName(site.Slug, u.Kind, u.Name), lines)
			if strings.TrimSpace(out) == "" {
				g.Printf("Nothing logged yet.\n")
				return nil
			}
			g.Printf("%s\n", out)
			return nil
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "How many lines")
	return NonRoot(cmd)
}
