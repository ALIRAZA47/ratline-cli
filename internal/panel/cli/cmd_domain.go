package cli

import (
	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/panel/install"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

func newDomainCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "domain",
		Short: "Put the panel behind a domain, with a certificate",
		Long: "The panel listens on the loopback. This is what makes it reachable: an nginx\n" +
			"vhost that proxies to it, and a certificate for the name.\n\n" +
			"The vhost is written by the panel rather than by ratline, because the panel is\n" +
			"not a ratline site — it has no tenant and no unit running as one — and\n" +
			"registering a fake one to borrow the renderer would be a lie the model would\n" +
			"then have to keep. It is staged, checked with nginx -t and rolled back on\n" +
			"failure exactly as ratline's own are.",
	}
	cmd.AddCommand(newDomainSetCommand(app), newDomainShowCommand(app), newDomainClearCommand(app))
	return cmd
}

func newDomainSetCommand(app *App) *cobra.Command {
	var email string
	var staging, noTLS bool
	cmd := &cobra.Command{
		Use:   "set <domain>",
		Short: "Serve the panel on a domain",
		Args:  cobra.ExactArgs(1),
		Long: "Writes the vhost over plain HTTP first, then obtains a certificate, then\n" +
			"rewrites it with TLS. That order is not incidental: the ACME challenge is\n" +
			"answered over port 80, and a TLS vhost naming a certificate that does not\n" +
			"exist yet fails nginx -t — so the reload never happens and the challenge is\n" +
			"never served.\n\n" +
			"Point DNS at this server first. An attempt against a name that does not\n" +
			"resolve here spends one of five validations per hour and cannot succeed.",
		Example: "  ratline-panel domain set panel.example.com --email you@example.com\n\n" +
			"  # prove the plumbing without spending a rate limit\n" +
			"  ratline-panel domain set panel.example.com --email you@example.com --staging",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr := &install.Manager{
				Cfg: app.Cfg, Log: app.Log, Runner: app.Runner,
				SelfPath: selfPath(), DryRun: app.DryRun,
			}
			if err := mgr.SetDomain(cmd.Context(), install.DomainOptions{
				Domain: args[0], Email: email, Staging: staging, NoTLS: noTLS,
			}); err != nil {
				return err
			}
			if app.DryRun {
				return nil
			}
			// The panel has to be told its own name: it decides whether a cookie
			// may be Secure and whether an Origin belongs to it by comparing
			// against this. Restarting is how the running process learns.
			app.printf("Restarting the panel so it knows its own name…\n")
			return mgr.Restart(cmd.Context())
		},
	}
	f := cmd.Flags()
	f.StringVar(&email, "email", "", "ACME contact address (required for a certificate)")
	f.BoolVar(&staging, "staging", false, "Let's Encrypt staging: untrusted certificate, no rate limit spent")
	f.BoolVar(&noTLS, "no-tls", false, "Write the HTTP vhost only, for TLS terminated elsewhere")
	return cmd
}

func newDomainShowCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the domain the panel is serving",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if app.Cfg.Listen.Domain == "" {
				app.printf("No domain. The panel is on %s only.\n", app.Cfg.PublicURL())
				return nil
			}
			app.printf("%s\n  vhost: %s\n  upstream: %s:%d\n",
				app.Cfg.PublicURL(), app.Cfg.Paths.NginxVhost,
				app.Cfg.Listen.Address, app.Cfg.Listen.Port)
			return nil
		},
	}
}

func newDomainClearCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove the vhost and take the panel off its domain",
		Args:  cobra.NoArgs,
		Long: "The certificate is left alone. Deleting a lineage because the panel moved\n" +
			"would spend a rate limit to get it back; certbot delete is the tool for\n" +
			"actually wanting it gone.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if app.Cfg.Listen.Domain == "" {
				return rlerr.Preconditionf("the panel is not on a domain")
			}
			mgr := &install.Manager{Cfg: app.Cfg, Log: app.Log, Runner: app.Runner, DryRun: app.DryRun}
			if err := mgr.ClearDomain(cmd.Context()); err != nil {
				return err
			}
			app.printf("The panel is on %s only.\n", app.Cfg.PublicURL())
			return nil
		},
	}
}

func newNginxCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nginx",
		Short: "nginx operations for the panel's own vhost",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "reload",
		Short: "Check and reload nginx",
		Args:  cobra.NoArgs,
		Long: "This is what certbot's deploy hook calls after renewing the panel's\n" +
			"certificate. A renewal that does not reload nginx changes the file on disk\n" +
			"and leaves the old certificate being served until it expires.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr := &install.Manager{Cfg: app.Cfg, Log: app.Log, Runner: app.Runner, DryRun: app.DryRun}
			return mgr.ReloadNginx(cmd.Context())
		},
	})
	return cmd
}
