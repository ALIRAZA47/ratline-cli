package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/nginx"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/site"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
)

func newSiteCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "site",
		Short:   "Create and manage sites",
		GroupID: GroupSites,
		Long: "A site is one domain owned by one user, served by nginx from inside that user's\n" +
			"home. There are three runtimes:\n\n" +
			"  static  nginx serves files directly; nothing runs\n" +
			"  node    nginx proxies to a Node server under its own systemd unit\n" +
			"  python  nginx proxies to Gunicorn in a per-site virtualenv\n\n" +
			"TLS is managed separately with 'ratline cert', so a site can be created and\n" +
			"serving before DNS has been pointed at this server.",
	}
	cmd.AddCommand(
		newSiteAddCommand(g),
		newSiteListCommand(g),
		newSiteShowCommand(g),
		newSiteEnableCommand(g, true),
		newSiteEnableCommand(g, false),
		newSiteControlCommand(g, "start"),
		newSiteControlCommand(g, "stop"),
		newSiteControlCommand(g, "restart"),
		newSiteReloadCommand(g),
		newSiteStatusCommand(g),
		newSiteScaleCommand(g),
		newSiteDeleteCommand(g),
		newSiteAliasCommand(g),
		newSiteLogsCommand(g),
		newSiteEnvCommand(g),
		newSiteDeployCommand(g),
		newSiteRuntimeCommand(g),
		newSiteDeployKeyCommand(g),
	)
	return cmd
}

func (g *Globals) siteManager(ctx context.Context) (*site.Manager, error) {
	st, err := g.Store(ctx)
	if err != nil {
		return nil, err
	}
	return &site.Manager{
		Cfg:     g.Cfg,
		Log:     g.Log,
		Runner:  g.Runner,
		State:   st,
		Nginx:   &nginx.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, DryRun: g.DryRun},
		Unit:    &unit.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, DryRun: g.DryRun},
		Invoker: g.Invoked(),
		DryRun:  g.DryRun,
	}, nil
}

func newSiteAddCommand(g *Globals) *cobra.Command {
	var (
		opts  site.AddOptions
		asgi  bool
		wsgi  bool
		ssl   string
		email string
		relax []string
	)
	cmd := &cobra.Command{
		Use:   "add <domain>",
		Short: "Provision a site: directories, vhost, service and TLS",
		Args:  cobra.MaximumNArgs(1),
		Example: "  ratline site add static.example.com --user acme --runtime static --spa\n\n" +
			"  ratline site add api.example.com --user acme --runtime python \\\n" +
			"      --app-module app.main:app --workers 3\n\n" +
			"  ratline site add app.example.com --user acme --runtime node \\\n" +
			"      --entry server.js --node 22",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.Domain = args[0]
			}
			// The wizard runs when asked for, and is offered rather than a usage
			// page when required flags are missing on a terminal.
			needsWizard := g.Interactive ||
				(g.CanPrompt() && (opts.Domain == "" || opts.Owner == "" || opts.Runtime == ""))
			if needsWizard {
				resolved, err := wizardSiteAdd(g, cmd.Context(), opts)
				if err != nil {
					return errCancelledToNil(err)
				}
				opts = resolved
			}
			if opts.Domain == "" {
				return rlerr.Usagef("a domain is required")
			}
			if err := RequireFlags(cmd, g, "user", "runtime"); err != nil {
				return err
			}
			if asgi && wsgi {
				return rlerr.Usagef("--asgi and --wsgi contradict each other")
			}
			// Validated here so an unknown directive is a usage error rather than
			// one that silently stays enabled.
			if len(relax) > 0 {
				if err := validateRelaxNames(relax); err != nil {
					return err
				}
				opts.Relaxed = relax
			}
			if asgi {
				t := true
				opts.ASGI = &t
			} else if wsgi {
				f := false
				opts.ASGI = &f
			}

			mgr, err := g.siteManager(cmd.Context())
			if err != nil {
				return err
			}
			res, err := mgr.Add(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if len(relax) > 0 {
				g.Log.Warn("systemd hardening relaxed for this site",
					"directives", strings.Join(relax, ", "),
					"note", "the generated unit records which directives are off")
			}

			// A certificate failure never fails the site: it is already serving
			// over HTTP, and the operator is told the one command to finish it.
			certNote := ""
			if ssl != "none" && res.Created {
				certNote = "certificates are managed separately in this build"
			}

			if g.JSON {
				return g.EmitJSON(map[string]any{"site": res.Site, "created": res.Created,
					"health": res.Health, "port": res.Port, "steps": res.Steps, "tls": certNote})
			}
			if !res.Created {
				g.Printf("%s is already configured; nothing changed.\n", res.Site.Domain)
				return nil
			}
			g.Printf("Created %s\n", res.Site.Domain)
			pairs := [][2]string{
				{"owner", res.Site.Owner},
				{"runtime", res.Site.Runtime},
				{"root", g.Cfg.SiteDir(res.Site.Owner, res.Site.Domain)},
			}
			if res.Site.Dynamic() {
				pairs = append(pairs, [2]string{"unit", mgr.UnitName(res.Site)})
				if res.Port > 0 {
					pairs = append(pairs, [2]string{"listen", fmt.Sprintf("127.0.0.1:%d", res.Port)})
				} else {
					pairs = append(pairs, [2]string{"socket", g.Cfg.SocketPath(res.Site.Owner, res.Site.Domain)})
				}
				if res.Health != "" {
					pairs = append(pairs, [2]string{"health", res.Health})
				}
			}
			if err := g.Fields(pairs...); err != nil {
				return err
			}
			g.Printf("\nServing over HTTP. To add TLS once DNS points here:\n  ratline cert issue %s", res.Site.Domain)
			if email != "" {
				g.Printf(" --email %s", email)
			}
			g.Printf("\n")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Owner, "user", "", "Owning tenant (required)")
	f.StringVar(&opts.Runtime, "runtime", "", "static, node or python (required)")
	f.StringArrayVar(&opts.Aliases, "alias", nil, "Additional server name (repeatable)")
	f.StringVar(&ssl, "ssl", "letsencrypt", "letsencrypt, selfsigned or none")
	f.StringVar(&email, "email", "", "ACME contact address")
	f.StringVar(&opts.WWWRedirect, "www-redirect", "none", "Canonical host: apex, www or none")
	f.BoolVar(&opts.NoEnable, "no-enable", false, "Write the configuration without enabling or starting it")
	f.StringVar(&opts.Repo, "repo", "", "Clone this repository into the application directory")
	f.StringVar(&opts.Branch, "branch", "main", "Branch to clone")

	f.StringVar(&opts.DocRoot, "root", "", "static: document root under the site directory (default public)")
	f.BoolVar(&opts.SPA, "spa", false, "static: serve the index document for unmatched paths")
	f.StringVar(&opts.IndexFile, "index", "index.html", "static: index document")

	f.StringVar(&opts.Entry, "entry", "", "node: the file that starts the server")
	f.StringVar(&opts.NodeVersion, "node", "", "node: managed Node version, e.g. 22")
	f.StringVar(&opts.PackageManager, "package-manager", "", "node: npm, pnpm, yarn or bun (detected from the lockfile)")
	f.StringVar(&opts.Listen, "listen", "socket", "node: socket or port")
	f.StringVar(&opts.ProcessManager, "daemon", "",
		"node: pm2 (default, reloads without dropping requests) or direct (node straight under systemd)")
	f.IntVar(&opts.Instances, "instances", 1, "Run this many instances behind an nginx upstream pool")

	f.StringVar(&opts.AppModule, "app-module", "", "python: import path of the callable, e.g. app.main:app")
	f.StringVar(&opts.PythonVersion, "python", "", "python: managed Python version, e.g. 3.12")
	f.BoolVar(&asgi, "asgi", false, "python: treat the application as ASGI")
	f.BoolVar(&wsgi, "wsgi", false, "python: treat the application as WSGI")
	f.StringVar(&opts.AppServer, "server", "", "python: gunicorn or uvicorn (default gunicorn)")
	f.IntVar(&opts.Workers, "workers", 0, "python: worker processes (default (2 x cores) + 1, capped)")
	f.StringVar(&opts.Requirements, "requirements", "", "python: requirements file (detected by default)")
	f.StringVar(&opts.ManagePy, "manage-py", "", "python: Django manage.py, enabling --migrate and --collectstatic")
	f.StringVar(&opts.StaticURL, "static-url", "", "python: URL prefix nginx serves from disk, e.g. /static")
	f.StringVar(&opts.StaticDir, "static-dir", "", "python: directory behind --static-url")

	f.StringVar(&opts.StartCommand, "start-command", "", "Start command, as an argv (no shell)")
	f.StringVar(&opts.InstallCommand, "install-command", "", "Dependency install command")
	f.StringVar(&opts.BuildCommand, "build-command", "", "Build command")
	f.StringVar(&opts.BuildOutput, "build-output", "", "Directory the build writes, published as the document root")
	f.StringVar(&opts.PublicDir, "public", "", "Directory nginx serves directly, bypassing the application")

	f.StringVar(&opts.MemoryMax, "memory-max", "", "Memory ceiling, e.g. 512M")
	f.StringVar(&opts.CPUQuota, "cpu-quota", "", "CPU ceiling, e.g. 100%")
	f.StringVar(&opts.ClientMaxBodySize, "client-max-body-size", "", "Upload limit, e.g. 20M")
	f.BoolVar(&opts.HSTS, "hsts", false, "Send Strict-Transport-Security (only with a trusted certificate)")
	f.StringSliceVar(&relax, "relax", nil, "Turn off a named systemd hardening directive for this site")
	return Mutating(cmd)
}

func newSiteListCommand(g *Globals) *cobra.Command {
	var owner, rt string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sites",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, err := g.siteManager(cmd.Context())
			if err != nil {
				return err
			}
			sites, err := mgr.List(cmd.Context(), state.SiteFilter{Owner: owner, Runtime: rt})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"sites": sites})
			}
			if len(sites) == 0 {
				g.Println("No sites yet. Create one with: ratline site add <domain> --user <user> --runtime static")
				return nil
			}
			tbl := g.Table("domain", "user", "runtime", "enabled", "service", "aliases")
			for _, s := range sites {
				svc := "-"
				if s.Dynamic() {
					if st, err := mgr.Unit.Status(cmd.Context(), s); err == nil {
						svc = st.Active
					}
				}
				tbl.Row(s.Domain, s.Owner, s.Runtime, yesNo(s.Enabled), svc, fmt.Sprint(len(s.Aliases)))
			}
			return tbl.Render()
		},
	}
	cmd.Flags().StringVar(&owner, "user", "", "Only this tenant's sites")
	cmd.Flags().StringVar(&rt, "runtime", "", "Only this runtime")
	return cmd
}

func newSiteShowCommand(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "show <domain>",
		Short: "Show a site's runtime, service state, socket, certificate and last deploy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.siteManager(cmd.Context())
			if err != nil {
				return err
			}
			info, err := mgr.Show(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(info)
			}
			pairs := [][2]string{
				{"domain", info.Domain},
				{"user", info.Owner},
				{"runtime", info.Runtime},
				{"enabled", yesNo(info.Site.Enabled)},
				{"root", g.Cfg.SiteDir(info.Owner, info.Domain)},
				{"disk", info.DiskHuman},
			}
			if len(info.Aliases) > 0 {
				pairs = append(pairs, [2]string{"aliases", strings.Join(info.Aliases, ", ")})
			}
			if info.DocRootAbs != "" {
				pairs = append(pairs, [2]string{"document root", info.DocRootAbs})
			}
			if info.Unit != nil {
				state := info.Unit.Active
				if info.Unit.Sub != "" {
					state += " (" + info.Unit.Sub + ")"
				}
				pairs = append(pairs,
					[2]string{"unit", info.Unit.Unit},
					[2]string{"service", state},
				)
				if info.Unit.MemoryHuman != "" {
					pairs = append(pairs, [2]string{"memory", info.Unit.MemoryHuman})
				}
				if info.Unit.NRestarts != "" && info.Unit.NRestarts != "0" {
					pairs = append(pairs, [2]string{"restarts", info.Unit.NRestarts})
				}
				pairs = append(pairs, [2]string{"socket", info.Socket + socketNote(info.SocketOK)})
			}
			if info.Cert != nil {
				pairs = append(pairs, [2]string{"certificate",
					fmt.Sprintf("%s (%s), %d days remaining", info.Cert.Name, info.Cert.Source, info.CertDays)})
			} else {
				pairs = append(pairs, [2]string{"certificate", "none — run 'ratline cert issue " + info.Domain + "'"})
			}
			if info.LastDeploy != nil {
				status := "ok"
				if !info.LastDeploy.OK {
					status = "failed"
				}
				pairs = append(pairs, [2]string{"last deploy",
					fmt.Sprintf("%s %s %s", info.LastDeploy.StartedAt.Format("2006-01-02 15:04"), status, info.LastDeploy.GitSHA)})
			}
			if !info.VhostOK {
				pairs = append(pairs, [2]string{"warning", "no nginx configuration on disk — run 'ratline reconcile --fix'"})
			}
			return g.Fields(pairs...)
		},
	}
}

func socketNote(ok bool) string {
	if ok {
		return " (accepting connections)"
	}
	return " (not answering)"
}

func newSiteEnableCommand(g *Globals, enable bool) *cobra.Command {
	verb, short := "enable", "Enable a site and start its service"
	if !enable {
		verb, short = "disable", "Take a site offline, returning 503 while keeping certificate renewal working"
	}
	cmd := &cobra.Command{
		Use:   verb + " <domain>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.siteManager(cmd.Context())
			if err != nil {
				return err
			}
			if enable {
				err = mgr.Enable(cmd.Context(), args[0])
			} else {
				err = mgr.Disable(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"domain": args[0], "enabled": enable})
			}
			g.Printf("%s %sd\n", args[0], verb)
			return nil
		},
	}
	return Mutating(cmd)
}

func newSiteControlCommand(g *Globals, verb string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   verb + " <domain>",
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " a site's service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.siteManager(cmd.Context())
			if err != nil {
				return err
			}
			health, err := mgr.Control(cmd.Context(), args[0], verb)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"domain": args[0], "action": verb, "health": health})
			}
			if health != "" {
				g.Printf("%s: %s (%s)\n", args[0], verb+"ed", health)
			} else {
				g.Printf("%s %sped\n", args[0], verb)
			}
			return nil
		},
	}
	return Mutating(cmd)
}

func newSiteReloadCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reload <domain>",
		Short: "Reload a site's workers without dropping requests, where the runtime allows",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.siteManager(cmd.Context())
			if err != nil {
				return err
			}
			health, err := mgr.Reload(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"domain": args[0], "health": health})
			}
			g.Printf("%s reloaded (%s)\n", args[0], health)
			return nil
		},
	}
	return Mutating(cmd)
}

func newSiteStatusCommand(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "status <domain>",
		Short: "Show a site's service state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.siteManager(cmd.Context())
			if err != nil {
				return err
			}
			info, err := mgr.Show(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			// Asked for unconditionally so the reported restart count is the real
			// one: under PM2 systemd's counter stays at zero because PM2 restarts
			// the workers itself. A failure here is not the operator's problem —
			// the systemd view is still worth printing.
			report, _ := mgr.ProcessReport(cmd.Context(), info.Site)

			if g.JSON {
				return g.EmitJSON(map[string]any{"domain": info.Domain, "unit": info.Unit,
					"socket": info.Socket, "socket_ok": info.SocketOK, "enabled": info.Site.Enabled,
					"process_manager": processManagerLabel(info.Site, report != nil), "pm2": report})
			}
			if info.Unit == nil {
				g.Printf("%s is a static site: nginx serves it directly and there is no service.\n", info.Domain)
				return nil
			}
			pairs := [][2]string{
				{"unit", info.Unit.Unit},
				{"active", info.Unit.Active},
				{"sub", info.Unit.Sub},
				{"enabled", info.Unit.Enabled},
				{"main pid", info.Unit.MainPID},
				{"memory", info.Unit.MemoryHuman},
			}
			if report != nil {
				pairs = append(pairs,
					[2]string{"supervisor", "pm2 (" + orDefault2(report.Mode, "cluster") + ")"},
					[2]string{"workers", fmt.Sprintf("%d online of %d", report.Online, report.Instances)},
					// PM2's counter, labelled as PM2's, so it is never confused with
					// systemd's zero.
					[2]string{"restarts", fmt.Sprintf("%d (as counted by pm2)", report.Restarts)},
				)
			} else {
				pairs = append(pairs, [2]string{"restarts", info.Unit.NRestarts})
			}
			pairs = append(pairs, [2]string{"socket", info.Socket + socketNote(info.SocketOK)})
			return g.Fields(pairs...)
		},
	}
}

func newSiteScaleCommand(g *Globals) *cobra.Command {
	var opts site.ScaleOptions
	cmd := &cobra.Command{
		Use:   "scale <domain>",
		Short: "Change workers, instances or resource ceilings",
		Args:  cobra.ExactArgs(1),
		Example: "  ratline site scale api.example.com --workers 6\n" +
			"  ratline site scale api.example.com --memory-max 1G --cpu-quota 200%",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.siteManager(cmd.Context())
			if err != nil {
				return err
			}
			s, err := mgr.Scale(cmd.Context(), args[0], opts)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"site": s})
			}
			return g.Fields(
				[2]string{"domain", s.Domain},
				[2]string{"workers", fmt.Sprint(s.Workers)},
				[2]string{"instances", fmt.Sprint(s.Instances)},
				[2]string{"memory max", orDash(s.MemoryMax)},
				[2]string{"cpu quota", orDash(s.CPUQuota)},
			)
		},
	}
	f := cmd.Flags()
	f.IntVar(&opts.Workers, "workers", 0, "Worker processes")
	f.IntVar(&opts.Instances, "instances", 0, "Instances behind the upstream pool")
	f.StringVar(&opts.MemoryMax, "memory-max", "", "Memory ceiling, e.g. 512M")
	f.StringVar(&opts.CPUQuota, "cpu-quota", "", "CPU ceiling, e.g. 100%")
	return Mutating(cmd)
}

func newSiteDeleteCommand(g *Globals) *cobra.Command {
	var (
		purge     bool
		backupDir string
	)
	cmd := &cobra.Command{
		Use:     "delete <domain>",
		Aliases: []string{"rm"},
		Short:   "Delete a site, its vhost, its service and its logs",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.siteManager(cmd.Context())
			if err != nil {
				return err
			}
			inv, err := mgr.InventoryFor(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !g.JSON {
				g.Printf("This will delete:\n")
				for _, p := range inv.Paths {
					g.Printf("  %s\n", p)
				}
				if !purge {
					g.Printf("  (the site directory is kept unless you pass --purge)\n")
				}
				g.Printf("  %s on disk, %d state row types", inv.DiskHuman, len(inv.StateRows))
				if inv.Port > 0 {
					g.Printf(", port %d", inv.Port)
				}
				if inv.KeyCount > 0 {
					g.Printf(", %d site-scoped SSH key(s)", inv.KeyCount)
				}
				if inv.Cert != "" {
					g.Printf(", and the attachment to %s", inv.Cert)
				}
				g.Printf("\n")
			}
			if err := g.ConfirmTyped(inv.Domain, fmt.Sprintf("Delete %s?", inv.Domain)); err != nil {
				return err
			}
			if err := mgr.Delete(cmd.Context(), args[0], purge, backupDir); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"domain": inv.Domain, "deleted": true, "purged": purge})
			}
			g.Printf("Deleted %s\n", inv.Domain)
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "Also delete the site directory and its contents")
	cmd.Flags().StringVar(&backupDir, "backup", "", "Archive the site into this directory first")
	return Mutating(cmd)
}

func newSiteAliasCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{Use: "alias", Short: "Add or remove a site's additional server names"}
	for _, spec := range []struct {
		verb  string
		short string
		add   bool
	}{
		{"add", "Add an alias and re-render the vhost", true},
		{"remove", "Remove an alias and re-render the vhost", false},
	} {
		verb, add := spec.verb, spec.add
		sub := &cobra.Command{
			Use:   verb + " <domain> <alias>",
			Short: spec.short,
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				mgr, err := g.siteManager(cmd.Context())
				if err != nil {
					return err
				}
				var s *state.Site
				if add {
					s, err = mgr.AddAlias(cmd.Context(), args[0], args[1])
				} else {
					s, err = mgr.RemoveAlias(cmd.Context(), args[0], args[1])
				}
				if err != nil {
					return err
				}
				if g.JSON {
					return g.EmitJSON(map[string]any{"site": s})
				}
				g.Printf("%s now serves: %s\n", s.Domain, strings.Join(s.ServerNames(), " "))
				if add {
					g.Printf("\nThe certificate does not cover the new name yet. To add it:\n  ratline cert issue %s --force\n", s.Domain)
				}
				return nil
			},
		}
		cmd.AddCommand(Mutating(sub))
	}
	return cmd
}

// processManagerLabel names the supervisor for machine-readable output.
func processManagerLabel(s *state.Site, pm2 bool) string {
	switch {
	case !s.Dynamic():
		return ""
	case pm2:
		return "pm2"
	case s.Runtime == "node":
		return "direct"
	default:
		// A python site is gunicorn under systemd; there is no choice to report.
		return "systemd"
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
