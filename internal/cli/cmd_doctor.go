package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/buildinfo"
	"github.com/ALIRAZA47/ratline-cli/internal/diag"
	"github.com/ALIRAZA47/ratline-cli/internal/mongo"
	"github.com/ALIRAZA47/ratline-cli/internal/nginx"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/tls"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// buildVersion is the version shown in the menu header.
var buildVersion = buildinfo.Version

// Finding is one thing `doctor` noticed.
type Finding struct {
	Severity string `json:"severity"` // problem or warning
	Check    string `json:"check"`
	Subject  string `json:"subject,omitempty"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix,omitempty"`
}

func newDoctorCommand(g *Globals) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "doctor [subject]",
		Short:   "Check the server, or diagnose one thing on it",
		GroupID: GroupOps,
		Args:    cobra.MaximumNArgs(1),
		Long: "With no argument, runs every check ratline knows how to run: the nginx\n" +
			"configuration, failed services, dead sockets, certificate expiry, orphaned\n" +
			"configuration, drift between state and the filesystem, permission anomalies,\n" +
			"allocated but unused ports, and the SSH key audit. Exit code 0 means healthy,\n" +
			"which makes it usable from cron.\n\n" +
			"With a subject — a domain, a tenant, a key fingerprint, a certificate, or\n" +
			"'nginx', 'ssh' or 'server' — it diagnoses that one thing instead, walking its\n" +
			"preconditions in order and stopping at the first failure. That is the same as\n" +
			"'ratline troubleshoot <subject>', which is the explicit spelling.\n\n" +
			"The difference is worth knowing: the sweep tells you what is wrong across the\n" +
			"server, and the walk tells you why one thing is.",
		Example: "  ratline doctor                       # everything, as a cron job would\n" +
			"  ratline doctor app.example.com       # why is this site broken\n" +
			"  ratline doctor ssh                   # including the lockout guard",
		ValidArgsFunction: g.completeSubjects,
		RunE: func(cmd *cobra.Command, args []string) error {
			// A named subject is the causal walk rather than the sweep. Same engine as
			// `troubleshoot`, because two diagnostics that could disagree about the
			// same server would be worse than one.
			if len(args) == 1 {
				env, err := g.diagEnv(cmd.Context())
				if err != nil {
					return err
				}
				subject, err := diag.Resolve(cmd.Context(), env, args[0])
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
			}
			return runDoctor(cmd.Context(), g, doctorOptions{})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false,
		"With a subject: show every step, not only the ones that need attention")
	return cmd
}

// diagnose runs every check and returns what it found.
func (g *Globals) diagnose(ctx context.Context, opts doctorOptions) ([]Finding, error) {
	var findings []Finding
	add := func(severity, check, subject, detail, fix string) {
		findings = append(findings, Finding{Severity: severity, Check: check, Subject: subject, Detail: detail, Fix: fix})
	}

	if !g.Cfg.Loaded {
		add("warning", "config", g.Cfg.SourcePath,
			"no configuration file, so the built-in defaults are in use",
			"ratline init")
	}
	if !g.OS.Supported() {
		add("warning", "platform", g.OS.PrettyName,
			"ratline targets Debian and Ubuntu; other hosts may have a different filesystem layout", "")
	}

	// Tooling. A missing certbot is only a problem once a certificate is wanted,
	// so it is a warning rather than a failure.
	for _, bin := range []string{"nginx", "systemctl", "ssh-keygen"} {
		if !g.Bins.Available(bin) {
			add("problem", "tooling", bin, "not installed, so ratline cannot manage this server",
				"apt-get install "+bin)
		}
	}
	if !g.Bins.Available("certbot") {
		add("warning", "tooling", "certbot", "not installed, so certificates cannot be issued",
			"apt-get install certbot")
	}

	nginxMgr := &nginx.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, DryRun: true}
	if g.Bins.Available("nginx") {
		if err := nginxMgr.Test(ctx); err != nil {
			add("problem", "nginx", "configuration", firstLine(err.Error()),
				"nginx -t shows the detail; the failing file is named in its output")
		}
	}

	st, err := g.Store(ctx)
	if err != nil {
		return nil, err
	}
	sites, err := st.ListSites(ctx, state.SiteFilter{})
	if err != nil {
		return nil, err
	}
	users, err := st.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	mgr, err := g.siteManager(ctx)
	if err != nil {
		return nil, err
	}

	// Drift between the state index and the system it describes. The filesystem
	// wins in every case; state is only an index.
	for _, u := range users {
		if !system.UserExists(u.Name) {
			add("problem", "drift", u.Name,
				"recorded in state but has no system account",
				"ratline reconcile --fix, or 'ratline user delete "+u.Name+"' if it is gone for good")
			continue
		}
		if !system.Exists(u.Home) {
			add("problem", "drift", u.Name, "the home directory "+u.Home+" is missing", "ratline reconcile --fix")
		}
	}

	for _, s := range sites {
		siteDir := g.Cfg.SiteDir(s.Owner, s.Domain)
		vhost := g.Cfg.VhostPath(s.Domain)
		if !system.Exists(vhost) {
			add("problem", "drift", s.Domain, "no nginx configuration at "+vhost, "ratline reconcile --fix")
		}
		if s.Enabled && !system.IsSymlink(g.Cfg.VhostLink(s.Domain)) {
			add("problem", "drift", s.Domain, "enabled in state but not linked into sites-enabled", "ratline reconcile --fix")
		}
		if !system.Exists(siteDir) {
			add("problem", "drift", s.Domain, "the site directory "+siteDir+" is missing",
				"ratline site delete "+s.Domain+", then create it again")
			continue
		}

		// Permissions. A home at 0755 exposes every tenant's files to every other
		// tenant, which is the single worst permission mistake on a shared box.
		if fi, err := os.Stat(g.Cfg.HomeDir(s.Owner)); err == nil {
			if fi.Mode().Perm()&0o007 != 0 {
				add("problem", "permissions", g.Cfg.HomeDir(s.Owner),
					fmt.Sprintf("mode %04o gives access to every other tenant", fi.Mode().Perm()),
					"chmod 0750 "+g.Cfg.HomeDir(s.Owner))
			}
		}
		envPath := filepath.Join(siteDir, ".env")
		if fi, err := os.Stat(envPath); err == nil && fi.Mode().Perm()&0o077 != 0 {
			add("problem", "permissions", envPath,
				fmt.Sprintf("mode %04o, but it holds secrets", fi.Mode().Perm()),
				"chmod 0600 "+envPath)
		}

		if !s.Dynamic() {
			continue
		}
		status, err := mgr.Unit.Status(ctx, s)
		if err != nil {
			continue
		}
		switch {
		case status.Active == "failed":
			add("problem", "service", s.Domain, "the service has failed",
				"journalctl -u "+mgr.UnitName(s)+" -n 50 --no-pager")
		case s.Enabled && status.Active != "active":
			add("problem", "service", s.Domain, "enabled but "+status.Active,
				"ratline site start "+s.Domain)
		case status.NRestarts != "" && status.NRestarts != "0":
			add("warning", "service", s.Domain, "restarted "+status.NRestarts+" time(s), which suggests a crash loop",
				"ratline site logs "+s.Domain)
		}

		// systemd's NRestarts above is zero on a PM2-supervised site even while the
		// application crash-loops, because PM2 is the one doing the restarting. The
		// check would be quietly useless on the default node setup without this.
		if report, rerr := mgr.ProcessReport(ctx, s); rerr == nil && report != nil {
			switch {
			case report.Restarts >= 10:
				add("problem", "service", s.Domain,
					fmt.Sprintf("PM2 has restarted a worker %d times, which is a crash loop", report.Restarts),
					"ratline site logs "+s.Domain)
			case report.Restarts > 0:
				add("warning", "service", s.Domain,
					fmt.Sprintf("PM2 has restarted a worker %d time(s)", report.Restarts),
					"ratline site logs "+s.Domain)
			}
			// Fewer workers online than configured means some died and did not come
			// back, which nothing else on this page would reveal.
			if s.Enabled && status.Active == "active" && report.Instances > 0 && report.Online < report.Instances {
				add("problem", "service", s.Domain,
					fmt.Sprintf("%d of %d PM2 workers are online", report.Online, report.Instances),
					"ratline site logs "+s.Domain)
			}
		}

		// A socket file left behind by a crashed process still exists, so the
		// only meaningful check is whether it accepts a connection.
		if s.Enabled && status.Active == "active" {
			if s.Listen == "port" {
				if err := system.ProbeTCP(ctx, fmt.Sprintf("127.0.0.1:%d", s.Port), 2*time.Second); err != nil {
					add("problem", "socket", s.Domain,
						fmt.Sprintf("the service is active but nothing answers on port %d", s.Port),
						"ratline site restart "+s.Domain)
				}
			} else {
				sock := g.Cfg.SocketPath(s.Owner, s.Domain)
				if err := system.ProbeUnix(ctx, sock, 2*time.Second); err != nil {
					detail := "the service is active but the socket does not accept connections"
					fix := "ratline site restart " + s.Domain
					if fi, serr := os.Stat(sock); serr == nil && fi.Mode().Perm()&0o060 != 0o060 {
						// This is the classic silent 502: connect(2) needs write
						// permission on the socket inode.
						detail = fmt.Sprintf("the socket is mode %04o; nginx needs 0660 to connect, so every request is a 502",
							fi.Mode().Perm())
						fix = "ratline site restart " + s.Domain + "; the full story is in 'ratline explain sockets'"
					}
					add("problem", "socket", s.Domain, detail, fix)
				}
			}
		}
	}

	// Configuration on disk that state knows nothing about — the residue left
	// when someone edits nginx by hand.
	if entries, err := os.ReadDir(g.Cfg.Paths.NginxSitesAvailable); err == nil {
		known := map[string]bool{}
		for _, s := range sites {
			known[s.Domain+".conf"] = true
		}
		for _, e := range entries {
			if e.IsDir() || known[e.Name()] {
				continue
			}
			path := filepath.Join(g.Cfg.Paths.NginxSitesAvailable, e.Name())
			managed, _ := system.HasManagedHeader(path)
			if managed {
				add("warning", "orphan", e.Name(),
					"a ratline-generated vhost with no matching site",
					"ratline reconcile --fix, or remove "+path)
			}
		}
	}

	// Units on disk with no site.
	if entries, err := os.ReadDir(g.Cfg.Paths.SystemdDir); err == nil {
		known := map[string]bool{}
		for _, s := range sites {
			known[validate.UnitName(s.Owner, s.Domain)] = true
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, "ratline-") || !strings.HasSuffix(name, ".service") || known[name] {
				continue
			}
			// ratline's own units are not site units, and there is no site they could
			// match. Reporting them as orphans handed the operator a "fix" that deletes
			// their certificate renewal — after which nothing renews and the first sign
			// is an expired certificate weeks later. Now that `init` installs these on
			// every server, every server would have seen it.
			if unit.IsOwnUnit(name) {
				continue
			}
			add("warning", "orphan", name, "a ratline unit with no matching site",
				"systemctl disable --now "+name+" && rm "+filepath.Join(g.Cfg.Paths.SystemdDir, name))
		}
	}

	// MongoDB, when provisioning is on. A database server that has become unreachable
	// is invisible otherwise: the sites keep serving, their connection strings keep
	// looking correct, and the first sign is an application error nobody attributes to
	// the database until they read its logs.
	if g.Cfg.Features.DBProvisioning {
		g.diagnoseMongo(ctx, st, add)
	}

	// Ports allocated in state but not used by any site.
	ports, err := st.ListPorts(ctx)
	if err != nil {
		return nil, err
	}
	byDomain := map[string]*state.Site{}
	for _, s := range sites {
		byDomain[s.Domain] = s
	}
	for _, p := range ports {
		s, ok := byDomain[p.Domain]
		if !ok {
			add("warning", "ports", fmt.Sprint(p.Port), "allocated to "+p.Domain+", which no longer exists",
				"ratline reconcile --fix")
			continue
		}
		if s.Listen != "port" {
			add("warning", "ports", fmt.Sprint(p.Port), p.Domain+" listens on a socket, so this allocation is unused",
				"ratline reconcile --fix")
		}
	}

	// Certificates.
	certs, err := st.ListCertificates(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for _, c := range certs {
		days := c.DaysRemaining(now)
		switch {
		case days < 0:
			add("problem", "certificate", c.Name, fmt.Sprintf("expired %d day(s) ago", -days),
				"ratline cert issue "+c.Name+" --force")
		case days < 7:
			add("problem", "certificate", c.Name, fmt.Sprintf("%d day(s) remaining", days),
				"ratline cert renew "+c.Name+" --force")
		case days < 21:
			add("warning", "certificate", c.Name, fmt.Sprintf("%d day(s) remaining", days), "")
		}
		if c.ConsecutiveFailures > 0 {
			add("problem", "certificate", c.Name,
				fmt.Sprintf("the last %d renewal(s) failed: %s", c.ConsecutiveFailures, firstLine(c.LastRenewalError)),
				"ratline cert renew "+c.Name+" --dry-run to see why")
		}
		if !c.Trusted() {
			add("warning", "certificate", c.Name,
				"a "+c.Source+" certificate, which browsers will not trust",
				"ratline cert issue "+c.Name)
		}
		if c.Source == state.CertSourceImported && days < 45 {
			// Nothing renews an imported certificate automatically.
			add("warning", "certificate", c.Name,
				fmt.Sprintf("imported, so nothing will renew it, and it expires in %d day(s)", days),
				"import a fresh one, or switch to Let's Encrypt with 'ratline cert issue "+c.Name+"'")
		}
		if len(c.Attached) == 0 {
			add("warning", "certificate", c.Name, "not attached to any site",
				"ratline cert attach <domain> --cert "+c.Name+", or delete it")
		}
		// A private ACME CA with no trust store configured. Every check above waits
		// for something to have gone wrong — an expiry, a failure count. This one is
		// visible the day the server is set up, and it is the cause of those: certbot
		// verifies the ACME directory against certifi's bundled roots rather than the
		// system trust store, so a private CA needs acme.ca_bundle, and without it
		// every renewal fails months later with a TLS error that reads as a network
		// problem.
		if server := tls.RenewalServer(g.Cfg, c.Name); server != "" && !tls.IsPublicACME(server) {
			switch bundle := g.Cfg.ACME.CABundle; {
			case bundle == "":
				add("problem", "certificate", c.Name,
					"renews from "+server+" and acme.ca_bundle is not set, so certbot cannot verify it",
					"set acme.ca_bundle in "+g.Cfg.SourcePath+" to that CA's root")
			case !system.Exists(bundle):
				add("problem", "certificate", c.Name,
					"acme.ca_bundle points at "+bundle+", which does not exist",
					"correct acme.ca_bundle in "+g.Cfg.SourcePath)
			}
		}
		// A certificate whose SANs no longer cover the site it serves means
		// browsers see a name mismatch.
		for _, domain := range c.Attached {
			if s, ok := byDomain[domain]; ok {
				for _, name := range s.ServerNames() {
					if !c.Covers(name) {
						add("problem", "certificate", c.Name,
							"attached to "+domain+" but does not cover "+name,
							"ratline cert issue "+domain+" --force")
						break
					}
				}
			}
		}
	}

	// The SSH key audit is part of a health check, not a separate concern.
	keyFindings, err := g.auditKeys(ctx)
	if err != nil {
		g.Log.Debug("the key audit failed", "err", err)
	}
	for _, kf := range keyFindings {
		subject := kf.Label
		if subject == "" {
			subject = kf.Key
		}
		findings = append(findings, Finding{
			Severity: kf.Severity, Check: "ssh-key:" + kf.Kind,
			Subject: subject, Detail: kf.Detail, Fix: kf.Fix,
		})
	}

	// Disk. A full disk breaks everything and gives no useful error anywhere else.
	if free, err := system.FreeBytes(g.Cfg.Paths.HomeBase); err == nil {
		if free < 512<<20 {
			add("problem", "disk", g.Cfg.Paths.HomeBase,
				"only "+validate.FormatSize(int64(free))+" free", "free some space before deploying anything")
		} else if free < 2<<30 {
			add("warning", "disk", g.Cfg.Paths.HomeBase, "only "+validate.FormatSize(int64(free))+" free", "")
		}
	}
	return findings, nil
}

func newReconcileCommand(g *Globals) *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:     "reconcile",
		Short:   "Report or repair drift between state and the system",
		GroupID: GroupOps,
		Args:    cobra.NoArgs,
		Long: "The filesystem, the systemd units and /etc/passwd are the source of truth; the\n" +
			"state database is an index. This command compares the two, and with --fix\n" +
			"re-renders every configuration file from state.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			sites, err := st.ListSites(cmd.Context(), state.SiteFilter{})
			if err != nil {
				return err
			}
			findings, err := g.diagnose(cmd.Context(), doctorOptions{Fix: fix})
			if err != nil {
				return err
			}
			drift := 0
			for _, f := range findings {
				if f.Check == "drift" || f.Check == "orphan" || f.Check == "ports" {
					drift++
				}
			}
			if !fix {
				if g.JSON {
					return g.EmitJSON(map[string]any{"drift": drift, "findings": findings})
				}
				if drift == 0 {
					g.Println("No drift found.")
					return nil
				}
				g.Printf("%d item(s) of drift. Re-run with --fix to repair.\n\n", drift)
				for _, f := range findings {
					if f.Check == "drift" || f.Check == "orphan" || f.Check == "ports" {
						g.Printf("  %s %s: %s\n", f.Severity, f.Subject, f.Detail)
					}
				}
				return nil
			}

			mgr, err := g.siteManager(cmd.Context())
			if err != nil {
				return err
			}
			keyMgr, err := g.keyManager(cmd.Context())
			if err != nil {
				return err
			}

			repaired := 0
			if err := mgr.Nginx.EnsureSnippets(cmd.Context()); err != nil {
				return err
			}
			if err := mgr.Unit.EnsureTarget(cmd.Context()); err != nil {
				return err
			}
			for _, s := range sites {
				cert, _ := st.CertificateForSite(cmd.Context(), s.Domain)
				rb := system.NewRollback(g.Log)
				if err := mgr.Nginx.Apply(cmd.Context(), s, cert, rb); err != nil {
					g.Log.Error("could not re-render a site", "domain", s.Domain, "err", err)
					continue
				}
				rb.Commit()
				if err := mgr.Unit.InstallLogrotate(cmd.Context(), s); err != nil {
					g.Log.Warn("could not write the logrotate policy", "domain", s.Domain, "err", err)
				}
				repaired++
			}
			// Release port allocations no site uses any more.
			ports, err := st.ListPorts(cmd.Context())
			if err != nil {
				return err
			}
			known := map[string]*state.Site{}
			for _, s := range sites {
				known[s.Domain] = s
			}
			for _, p := range ports {
				s, ok := known[p.Domain]
				if !ok || s.Listen != "port" {
					if err := st.ReleasePort(cmd.Context(), p.Domain); err != nil {
						return err
					}
					g.Log.Info("released a stale port allocation", "port", p.Port, "domain", p.Domain)
				}
			}
			if _, err := keyMgr.SyncAll(cmd.Context()); err != nil {
				return err
			}
			if err := keyMgr.ApplyDropIn(cmd.Context()); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"sites_rerendered": repaired, "fixed": true})
			}
			g.Printf("Re-rendered %d site(s) and every authorized_keys file.\n", repaired)
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "Repair what can be repaired, rather than only reporting")
	return Mutating(cmd)
}

func newExportCommand(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:     "export",
		Short:   "Dump the full state as JSON, for migration",
		GroupID: GroupOps,
		Args:    cobra.NoArgs,
		Long: "Contains no private key material: public key blobs and fingerprints only.\n" +
			"Certificate private keys are never read, let alone exported.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			export, err := st.Export(cmd.Context())
			if err != nil {
				return err
			}
			// Always JSON: this command exists to be piped.
			was := g.JSON
			g.JSON = true
			defer func() { g.JSON = was }()
			return g.EmitJSON(export)
		},
	}
}

// diagnoseMongo checks the database server and the credentials pointing at it.
//
// Deliberately a warning rather than a problem when the server is simply unreachable:
// that may be a network blip, and `doctor` exits non-zero on problems, which would make
// a cron job page somebody for something that fixed itself. A missing admin file, or one
// that other accounts can read, is a different matter.
func (g *Globals) diagnoseMongo(ctx context.Context, st *state.Store, add func(severity, check, subject, detail, fix string)) {
	if !g.Bins.Available("mongosh") {
		add("warning", "mongodb", "mongosh", "not installed, so ratline cannot manage databases",
			"apt-get install mongodb-mongosh")
		return
	}
	mgr := &mongo.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, Bins: g.Bins, State: st, DryRun: true}

	if _, err := mgr.AdminURI(); err != nil {
		// The file's mode is the part worth being firm about: a URI other accounts can
		// read is the admin password for every database on the server.
		severity := "warning"
		if strings.Contains(err.Error(), "mode") {
			severity = "problem"
		}
		add(severity, "mongodb", g.Cfg.Paths.MongoURIFile, firstLine(err.Error()),
			"see 'ratline db --help' for the two commands that write it")
		return
	}

	info, err := mgr.Ping(ctx)
	if err != nil {
		add("warning", "mongodb", "server", firstLine(err.Error()),
			"ratline db ping shows the detail")
		return
	}
	if !info.AuthEnabled {
		add("problem", "mongodb", "server",
			"the server does not appear to enforce authentication, so any process that "+
				"can reach the port has full access to every database",
			"start mongod with --auth, or security.authorization: enabled in mongod.conf")
	}

	// A database recorded here but gone from the server, or the other way round. Both
	// matter: the first breaks an application that still holds a connection string, and
	// the second is a database nothing will clean up when its tenant is deleted.
	recorded, err := st.ListDatabases(ctx, "")
	if err != nil {
		return
	}
	live, err := mgr.LiveDatabases(ctx)
	if err != nil {
		return
	}
	onServer := map[string]bool{}
	for _, d := range live {
		onServer[d.Name] = true
	}
	for _, d := range recorded {
		if !onServer[d.Name] {
			add("problem", "mongodb", d.Name,
				"recorded as "+d.Owner+"'s database but the server does not have it",
				"ratline db drop "+d.Name+" --keep-database, if it is gone for good")
		}
	}
	// Users whose credentials a site holds, but which the server no longer has: the
	// application is failing to authenticate right now.
	for _, d := range recorded {
		for _, u := range d.Users {
			if len(u.Attachments) == 0 {
				continue
			}
			users, err := mgr.LiveUsers(ctx, u.AuthDB)
			if err != nil {
				continue
			}
			var present bool
			for _, lu := range users {
				if lu.Username == u.Username {
					present = true
					break
				}
			}
			if !present {
				var sites []string
				for _, a := range u.Attachments {
					sites = append(sites, a.Domain)
				}
				add("problem", "mongodb", u.Username,
					"gone from the server, but "+strings.Join(sites, ", ")+" still has its "+
						"connection string, so that site cannot authenticate",
					"ratline db user add "+u.Username+" --database "+u.Database+
						" --attach "+sites[0])
			}
		}
	}
}
