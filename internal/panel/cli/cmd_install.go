package cli

import (
	"context"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/panel/auth"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/install"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/rl"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// installOptions is one `install` invocation.
type installOptions struct {
	domain  string
	email   string
	staging bool
	noStart bool

	adminEmail    string
	adminName     string
	adminPassword bool // read it from stdin
	noAdmin       bool
}

func newInstallCommand(app *App) *cobra.Command {
	var opts installOptions

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Set the panel up: configuration, database, first super admin, service",
		Args:  cobra.NoArgs,
		Long: "Everything a fresh install needs, and nothing that cannot be undone.\n\n" +
			"It writes /etc/ratline/panel.yaml if it is not there, creates the panel's\n" +
			"database, creates the first super admin, installs and enables the systemd\n" +
			"unit, and starts it. With --domain it also writes an nginx vhost and obtains\n" +
			"a certificate.\n\n" +
			"The account is created here rather than by whoever opens the panel first,\n" +
			"because 'the first visitor becomes the administrator' is a window, and a\n" +
			"window on a machine nobody is watching is how a server is lost. There is no\n" +
			"default password: one is generated and printed once, or you are asked for\n" +
			"one, or it is read from stdin.\n\n" +
			"Safe to run twice. An existing configuration is kept, an existing database is\n" +
			"reused, and an existing account is left alone.",
		Example: "  # on a server already running ratline\n" +
			"  ratline-panel install --admin-email you@example.com\n\n" +
			"  # and put it straight onto a domain\n" +
			"  ratline-panel install --admin-email you@example.com \\\n" +
			"      --domain panel.example.com --email you@example.com\n\n" +
			"  # from a provisioning script, with the password piped in\n" +
			"  printf '%s' \"$PANEL_PASSWORD\" | ratline-panel install \\\n" +
			"      --admin-email ops@example.com --admin-password-stdin",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.install(cmd.Context(), opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.domain, "domain", "", "Also put the panel on this domain")
	f.StringVar(&opts.email, "email", "", "ACME contact address, required to issue a certificate")
	f.BoolVar(&opts.staging, "staging", false, "Use Let's Encrypt staging: no rate limit spent, no browser trust")
	f.BoolVar(&opts.noStart, "no-start", false, "Install without starting the service")
	f.StringVar(&opts.adminEmail, "admin-email", "", "Address of the first super admin")
	f.StringVar(&opts.adminName, "admin-name", "", "Display name for that account")
	f.BoolVar(&opts.adminPassword, "admin-password-stdin", false,
		"Read that account's password from stdin instead of generating one")
	f.BoolVar(&opts.noAdmin, "no-admin", false,
		"Do not create an account; the first person to reach the panel becomes the super admin")
	return cmd
}

func (app *App) install(ctx context.Context, opts installOptions) error {
	// The ratline binary is checked before anything is written. A panel installed
	// against a ratline that is not there is a service that starts, fails, and
	// restarts five times before systemd gives up — with the reason three levels
	// down in the journal.
	if _, err := rl.NewClient(app.Cfg.Ratline.Binary, app.Runner, app.Log); err != nil {
		return err
	}

	if !app.Cfg.Loaded {
		if app.DryRun {
			app.Log.Info("would write the configuration", "path", app.Cfg.SourcePath)
		} else {
			if err := app.Cfg.Write(app.Cfg.SourcePath); err != nil {
				return err
			}
			app.printf("Wrote %s\n", app.Cfg.SourcePath)
		}
	} else {
		app.printf("Keeping the existing %s\n", app.Cfg.SourcePath)
	}

	// Created before the service starts, so there is never a moment in which the
	// panel is answering and unclaimed.
	var created *createdAdmin
	if !app.DryRun {
		st, err := store.Open(app.Cfg.Paths.StateDB)
		if err != nil {
			return err
		}
		version, _ := st.SchemaVersion(ctx)
		app.printf("Panel database at %s (schema %d)\n", app.Cfg.Paths.StateDB, version)

		created, err = app.ensureFirstAdmin(ctx, st, opts)
		if cerr := st.Close(); cerr != nil {
			app.Log.Debug("could not close the panel database", "err", cerr)
		}
		if err != nil {
			return err
		}
	} else if !opts.noAdmin {
		app.Log.Info("would create the first super admin", "email", opts.adminEmail)
	}

	mgr := &install.Manager{
		Cfg: app.Cfg, Log: app.Log, Runner: app.Runner,
		SelfPath: selfPath(), DryRun: app.DryRun,
	}
	if err := mgr.EnsureUnit(ctx); err != nil {
		return err
	}
	app.printf("Installed %s\n", install.UnitPath)

	if opts.domain != "" {
		if err := mgr.SetDomain(ctx, install.DomainOptions{
			Domain: opts.domain, Email: opts.email, Staging: opts.staging,
		}); err != nil {
			return err
		}
	}

	if opts.noStart {
		app.printf("\nNot started, as asked. Start it with:\n  systemctl start %s\n", install.UnitName)
		return nil
	}
	if app.DryRun {
		app.printf("\nDry run: nothing was written.\n")
		return nil
	}
	if err := mgr.Restart(ctx); err != nil {
		return err
	}
	app.printf("\nThe panel is running.\n")
	app.reportAdmin(created)
	app.reportHowToReachIt(opts.domain)
	return nil
}

// createdAdmin is what to tell the operator about the account just made.
type createdAdmin struct {
	Email string
	// Password is set only when the installer generated it. A password the
	// operator supplied is theirs and is not echoed back.
	Password string
	Existing int
}

// ensureFirstAdmin creates the panel's first super admin, or explains why it did not.
//
// Idempotent, like everything else here: an install run twice reports the accounts
// that exist rather than making another. That matters because the natural way to add
// the panel to a server that already has ratline is to run this, and the natural way
// to fix a mistake in it is to run it again.
func (app *App) ensureFirstAdmin(ctx context.Context, st *store.Store, opts installOptions) (*createdAdmin, error) {
	n, err := st.CountAccounts(ctx)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		return &createdAdmin{Existing: n}, nil
	}
	if opts.noAdmin {
		app.Log.Warn("no account was created, as asked",
			"note", "the first person to reach the panel becomes its super admin")
		return nil, nil
	}

	email := strings.TrimSpace(opts.adminEmail)
	if email == "" {
		if !app.canPrompt() {
			return nil, rlerr.InputRequiredf("no address was given for the first super admin").
				WithHint("pass --admin-email you@example.com, or --no-admin to leave the " +
					"panel unclaimed (which means whoever reaches it first becomes one)")
		}
		if email, err = app.ask("Email address for the first super admin: "); err != nil {
			return nil, err
		}
	}

	var password string
	switch {
	case opts.adminPassword:
		if password, err = app.readSecret(""); err != nil {
			return nil, err
		}
	case app.canPrompt():
		if password, err = app.readSecret("Password (leave empty to generate one): "); err != nil {
			return nil, err
		}
		if strings.TrimSpace(password) == "" {
			password = ""
		} else {
			confirm, cerr := app.readSecret("Repeat: ")
			if cerr != nil {
				return nil, cerr
			}
			if password != confirm {
				return nil, rlerr.Usagef("the two passwords do not match")
			}
		}
	}

	generated := password == ""
	if generated {
		if password, err = auth.GeneratePassword(); err != nil {
			return nil, err
		}
	}

	account, err := app.addAccount(ctx, st, email, opts.adminName,
		store.RoleSuperAdmin, password, "ratline-panel install")
	if err != nil {
		return nil, err
	}
	// Never logged. The logger goes to the journal, which is readable by anybody in
	// systemd-journal and kept for weeks; the password belongs on this terminal and
	// nowhere else.
	app.Log.Info("created the first super admin", "email", account.Email)

	out := &createdAdmin{Email: account.Email}
	if generated {
		out.Password = password
	}
	return out, nil
}

func (app *App) reportAdmin(created *createdAdmin) {
	switch {
	case created == nil:
		app.printf("\nNo account exists yet, as asked. Whoever reaches the panel first\n")
		app.printf("becomes its super admin, so do not leave it reachable and unclaimed.\n")
	case created.Existing > 0:
		app.printf("\nKeeping the %d account(s) that already exist.\n", created.Existing)
		app.printf("Forgotten the password? ratline-panel account password <email>\n")
	case created.Password != "":
		app.printf("\n  Sign in as   %s\n", created.Email)
		app.printf("  Password     %s\n\n", created.Password)
		app.printf("That password is shown once and is not stored anywhere in the clear.\n")
		app.printf("Change it after you sign in, or set a new one with:\n")
		app.printf("  ratline-panel account password %s\n", created.Email)
	default:
		app.printf("\n  Sign in as   %s, with the password you set.\n", created.Email)
	}
}

func (app *App) reportHowToReachIt(domain string) {
	if domain != "" {
		app.printf("\nOpen %s\n", app.Cfg.PublicURL())
		return
	}
	app.printf("\nIt is listening on %s, which is not reachable from anywhere else.\n",
		app.Cfg.PublicURL())
	app.printf("From your own machine:\n\n")
	app.printf("  ssh -L %d:%s:%d <this-server>\n\n",
		app.Cfg.Listen.Port, app.Cfg.Listen.Address, app.Cfg.Listen.Port)
	app.printf("then open http://localhost:%d\n\n", app.Cfg.Listen.Port)
	app.printf("To put it on a domain, once DNS points here:\n")
	app.printf("  ratline-panel domain set panel.example.com --email you@example.com\n")
}

func newUninstallCommand(app *App) *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop the panel and remove its service and vhost",
		Args:  cobra.NoArgs,
		Long: "Removes the systemd unit and the nginx vhost, and stops the service.\n\n" +
			"The panel's database is kept unless --purge is given, because it holds the\n" +
			"accounts: reinstalling without it means claiming the panel again from\n" +
			"scratch. Nothing ratline manages is touched — no tenant, no site, no\n" +
			"certificate — because the panel never owned any of it.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.uninstall(cmd.Context(), purge)
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "Also delete the panel's database, and with it every account")
	return cmd
}

func (app *App) uninstall(ctx context.Context, purge bool) error {
	if purge && !app.Yes {
		return rlerr.Preconditionf("--purge deletes every panel account").
			WithHint("pass --yes if that is what you mean")
	}
	mgr := &install.Manager{Cfg: app.Cfg, Log: app.Log, Runner: app.Runner, DryRun: app.DryRun}

	if !app.DryRun {
		if _, err := app.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{"disable", "--now", install.UnitName},
			Mutates: true, OKExit: []int{1, 5},
		}); err != nil {
			app.Log.Warn("could not stop the service", "err", err)
		}
		if err := system.RemoveManaged(install.UnitPath); err != nil {
			app.Log.Warn("could not remove the unit", "err", err)
		}
		if _, err := app.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{"daemon-reload"}, Mutates: true,
		}); err != nil {
			app.Log.Warn("could not reload systemd", "err", err)
		}
		// systemd remembers that a unit failed after its file is gone, and the
		// leftover entry sits in `systemctl --failed` for ever — which is what
		// monitoring watches.
		if _, err := app.Runner.Run(ctx, system.Cmd{
			Name: "systemctl", Args: []string{"reset-failed", install.UnitName},
			Mutates: true, OKExit: []int{1, 4, 5},
		}); err != nil {
			app.Log.Debug("could not reset the unit's failed state", "err", err)
		}
	}
	if system.Exists(app.Cfg.Paths.NginxVhost) {
		if err := mgr.ClearDomain(ctx); err != nil {
			app.Log.Warn("could not remove the nginx vhost", "err", err)
		}
	}
	if purge && !app.DryRun {
		if err := os.Remove(app.Cfg.Paths.StateDB); err != nil && !os.IsNotExist(err) {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the panel database")
		}
		app.printf("Removed %s\n", app.Cfg.Paths.StateDB)
	}
	app.printf("The panel is uninstalled. %s was left alone.\n", app.Cfg.SourcePath)
	return nil
}

// selfPath resolves this binary, so a unit written by a panel installed somewhere
// unusual starts the binary that wrote it rather than one that may not be there.
func selfPath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	resolved, err := os.Readlink("/proc/self/exe")
	if err == nil && resolved != "" {
		return resolved
	}
	return p
}
