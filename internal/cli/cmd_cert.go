package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/nginx"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/tls"
)

func newCertCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cert",
		Short:   "Issue, attach, renew and import TLS certificates",
		GroupID: GroupCerts,
		Long: "Certificates are a resource with their own lifecycle, not a flag on 'site add'.\n\n" +
			"That is deliberate: a site can be created and serving HTTP before DNS has been\n" +
			"pointed at this server, and have a real certificate issued and attached later —\n" +
			"which is the normal order of operations when a client is still moving a domain.",
	}
	cmd.AddCommand(
		newCertIssueCommand(g),
		newCertAttachCommand(g),
		newCertDetachCommand(g),
		newCertListCommand(g),
		newCertShowCommand(g),
		newCertRenewCommand(g),
		newCertRevokeCommand(g),
		newCertDeleteCommand(g),
		newCertImportCommand(g),
		newCertSelfSignCommand(g),
		newCertAutoRenewCommand(g),
		newCertTestRenewalCommand(g),
		newCertAccountCommand(g),
		newCertDeployHookCommand(g),
	)
	return cmd
}

func (g *Globals) certManager(ctx context.Context) (*tls.Manager, error) {
	st, err := g.Store(ctx)
	if err != nil {
		return nil, err
	}
	return &tls.Manager{
		Cfg:     g.Cfg,
		Log:     g.Log,
		Runner:  g.Runner,
		State:   st,
		Nginx:   &nginx.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, DryRun: g.DryRun},
		Invoker: g.Invoked(),
		DryRun:  g.DryRun,
	}, nil
}

func newCertIssueCommand(g *Globals) *cobra.Command {
	var (
		opts     tls.IssueOptions
		noAttach bool
	)
	cmd := &cobra.Command{
		Use:   "issue <domain>",
		Short: "Obtain a certificate and attach it to the site",
		Args:  cobra.MaximumNArgs(1),
		Long: "Runs every preflight check first and reports all the problems at once, because\n" +
			"fixing one per attempt is a poor way to spend a rate-limit budget and the\n" +
			"certificate authority counts failed validations.\n\n" +
			"The result is verified over the network: a certificate that exists on disk but is\n" +
			"not being served is a failure, not a success.",
		Example: "  ratline cert issue example.com --email admin@example.com\n\n" +
			"  # test without spending an attempt\n" +
			"  ratline cert issue example.com --dry-run\n\n" +
			"  # a wildcard, which requires DNS-01\n" +
			"  ratline cert issue '*.example.com' --challenge dns \\\n" +
			"      --dns-provider cloudflare --dns-credentials /etc/ratline/dns/cloudflare.ini",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.Domain = args[0]
			}
			opts.Attach = !noAttach

			mgr, err := g.certManager(cmd.Context())
			if err != nil {
				return err
			}
			if g.Interactive || (opts.Domain == "" && g.CanPrompt()) {
				resolved, err := wizardCertIssue(g, cmd.Context(), mgr, opts)
				if err != nil {
					return errCancelledToNil(err)
				}
				opts = resolved
			}
			if opts.Domain == "" {
				return rlerr.Usagef("a domain is required")
			}

			res, err := mgr.Issue(cmd.Context(), opts)
			if err != nil {
				// The preflight detail is what makes the failure actionable, so it
				// is printed even though the command failed.
				if res != nil && len(res.Preflight) > 0 && !g.JSON {
					g.printPreflight(res.Preflight)
				}
				return err
			}
			if g.JSON {
				return g.EmitJSON(res)
			}
			if len(res.Preflight) > 0 && g.Verbose {
				g.printPreflight(res.Preflight)
			}
			if res.DryRun {
				g.Printf("Dry run succeeded: the challenge for %s would have completed.\n", opts.Domain)
				g.Printf("Nothing was issued, and no rate-limit budget was spent.\n")
				return nil
			}
			if !res.Issued {
				g.Printf("%s already has a valid certificate covering those names; nothing was done.\n", opts.Domain)
				return nil
			}
			c := res.Certificate
			g.Printf("Issued a certificate for %s\n", c.Name)
			pairs := [][2]string{
				{"names", strings.Join(c.SANs, ", ")},
				{"issuer", c.Issuer},
				{"key type", c.KeyType},
				{"expires", fmt.Sprintf("%s (%d days)", c.NotAfter.Format("2006-01-02"), c.DaysRemaining(time.Now()))},
				{"attached", yesNo(res.Attached)},
			}
			if res.Verified != "" {
				pairs = append(pairs, [2]string{"verified", res.Verified})
			}
			if err := g.Fields(pairs...); err != nil {
				return err
			}
			if opts.Staging {
				g.Printf("\nThis is a STAGING certificate. Browsers will reject it.\n" +
					"Re-run without --staging when you are ready for a real one.\n")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&opts.Aliases, "alias", nil, "SAN to include, replacing the site's own aliases (repeatable)")
	f.StringArrayVar(&opts.ExtraSANs, "san", nil, "Extra SAN not registered as a site alias (repeatable)")
	f.StringVar(&opts.Challenge, "challenge", "http", "http (webroot) or dns; a wildcard forces dns")
	f.StringVar(&opts.DNSProvider, "dns-provider", "", "certbot DNS plugin, e.g. cloudflare or route53")
	f.StringVar(&opts.DNSCredentials, "dns-credentials", "", "Credentials file for the DNS plugin, which must be 0600")
	f.IntVar(&opts.DNSPropagation, "dns-propagation", 0, "Seconds to wait for the TXT record before validating")
	f.StringVar(&opts.Email, "email", "", "ACME contact address")
	f.StringVar(&opts.DirectoryURL, "acme-directory", "",
		"ACME directory URL, for a private CA such as step-ca (default: the configured one)")
	f.StringVar(&opts.CABundle, "acme-ca-bundle", "",
		"Trust store for a private ACME server (default: the system one, when --acme-directory is set)")
	f.BoolVar(&opts.Staging, "staging", false, "Use the staging endpoint: real exchange, untrusted certificate, generous limits")
	f.StringVar(&opts.KeyType, "key-type", "", "ecdsa or rsa (default from config)")
	f.BoolVar(&opts.Force, "force", false, "Re-issue even if a valid certificate exists, and proceed past preflight")
	f.BoolVar(&noAttach, "no-attach", false, "Obtain the certificate without pointing the vhost at it")
	f.BoolVar(&opts.CertbotDryRun, "dry-run", false, "Validate fully without issuing, and without spending budget")
	return Mutating(cmd)
}

// printPreflight shows the checks, failures first.
func (g *Globals) printPreflight(results []tls.PreflightResult) {
	g.Printf("\nPreflight:\n")
	for _, r := range results {
		if r.OK {
			continue
		}
		g.Printf("  ✗ %-18s %s\n", r.Check, r.Detail)
		if r.Fix != "" {
			g.Printf("    %s\n", r.Fix)
		}
	}
	for _, r := range results {
		if r.OK {
			g.Printf("  ✓ %-18s %s\n", r.Check, r.Detail)
		}
	}
	g.Printf("\n")
}

func newCertAttachCommand(g *Globals) *cobra.Command {
	var certName string
	cmd := &cobra.Command{
		Use:   "attach <domain>",
		Short: "Point a site's vhost at an existing certificate",
		Args:  cobra.ExactArgs(1),
		Long: "How one SAN certificate serves several vhosts, and how an imported or\n" +
			"already-issued certificate is put to use without a new ACME exchange.",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.certManager(cmd.Context())
			if err != nil {
				return err
			}
			if certName == "" {
				certName = args[0]
			}
			if err := mgr.Attach(cmd.Context(), args[0], certName); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"domain": args[0], "certificate": certName, "attached": true})
			}
			g.Printf("Attached %s to %s\n", certName, args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&certName, "cert", "", "Certificate to attach (default: one named after the domain)")
	return Mutating(cmd)
}

func newCertDetachCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "detach <domain>",
		Short: "Revert a site to plain HTTP",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.certManager(cmd.Context())
			if err != nil {
				return err
			}
			if err := mgr.Detach(cmd.Context(), args[0]); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"domain": args[0], "detached": true})
			}
			g.Printf("%s now serves plain HTTP.\n", args[0])
			return nil
		},
	}
	return Mutating(cmd)
}

func newCertListCommand(g *Globals) *cobra.Command {
	var (
		expiring int
		orphaned bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every certificate on this server, including ones issued by hand",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, err := g.certManager(cmd.Context())
			if err != nil {
				return err
			}
			rows, err := mgr.List(cmd.Context(), expiring, orphaned)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"certificates": rows})
			}
			if len(rows) == 0 {
				g.Println("No certificates. Issue one with: ratline cert issue <domain>")
				return nil
			}
			tbl := g.Table("domain", "sans", "issuer", "key", "expires", "days", "status", "sites", "auto-renew")
			for _, r := range rows {
				expires := "-"
				if !r.NotAfter.IsZero() {
					expires = r.NotAfter.Format("2006-01-02")
				}
				tbl.Row(r.Name, tls.SANSummary(r.SANs, 2), orDash(r.Issuer), orDash(r.KeyType),
					expires, fmt.Sprint(r.Days), g.colourStatus(r.Status),
					fmt.Sprint(len(r.Attached)), yesNo(r.AutoRenew))
			}
			if err := tbl.Render(); err != nil {
				return err
			}
			// The statuses that need an action get a line explaining it.
			for _, r := range rows {
				switch r.Status {
				case tls.StatusStaging:
					g.Printf("\n%s is a STAGING certificate — browsers reject it. Replace it with:\n  ratline cert issue %s\n", r.Name, r.Name)
				case tls.StatusSelfSigned:
					g.Printf("\n%s is self-signed — browsers reject it. Replace it with:\n  ratline cert issue %s\n", r.Name, r.Name)
				case tls.StatusMismatch:
					g.Printf("\n%s does not cover every name its site serves:\n  ratline cert issue %s --force\n", r.Name, r.Name)
				case tls.StatusDegraded:
					g.Printf("\n%s failed to renew %d time(s):\n  ratline cert renew %s --dry-run\n", r.Name, r.ConsecutiveFailures, r.Name)
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&expiring, "expiring", 0, "Only certificates expiring within this many days")
	cmd.Flags().BoolVar(&orphaned, "orphaned", false, "Only certificates no site uses")
	return cmd
}

func (g *Globals) colourStatus(s tls.Status) string {
	if !g.Color {
		return string(s)
	}
	switch s {
	case tls.StatusValid:
		return "\033[32m" + string(s) + "\033[0m"
	case tls.StatusExpiring, tls.StatusStaging, tls.StatusSelfSigned, tls.StatusOrphaned:
		return "\033[33m" + string(s) + "\033[0m"
	case tls.StatusCritical, tls.StatusExpired, tls.StatusDegraded, tls.StatusMismatch:
		return "\033[31m" + string(s) + "\033[0m"
	default:
		return string(s)
	}
}

func newCertShowCommand(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "show <domain>",
		Short: "Show a certificate in full",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.certManager(cmd.Context())
			if err != nil {
				return err
			}
			s, err := mgr.Show(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(s)
			}
			pairs := [][2]string{
				{"name", s.Name},
				{"status", g.colourStatus(s.Status)},
				{"source", s.Source},
				{"trusted by browsers", yesNo(s.Trusted)},
				{"sans", strings.Join(s.SANs, ", ")},
				{"issuer", orDash(s.Issuer)},
				{"serial", orDash(s.Serial)},
				{"fingerprint", orDash(s.Fingerprint)},
				{"key type", orDash(s.KeyType)},
			}
			if !s.NotBefore.IsZero() {
				pairs = append(pairs, [2]string{"valid from", s.NotBefore.Format("2006-01-02 15:04")})
			}
			if !s.NotAfter.IsZero() {
				pairs = append(pairs,
					[2]string{"expires", s.NotAfter.Format("2006-01-02 15:04")},
					[2]string{"days remaining", fmt.Sprint(s.Days)})
			}
			pairs = append(pairs,
				[2]string{"attached to", orDash(strings.Join(s.Attached, ", "))},
				[2]string{"auto-renew", yesNo(s.AutoRenew)})
			if s.Challenge != "" {
				pairs = append(pairs, [2]string{"challenge", s.Challenge})
			}
			if s.DNSProvider != "" {
				pairs = append(pairs, [2]string{"dns provider", s.DNSProvider})
			}
			if !s.LastRenewalAt.IsZero() {
				pairs = append(pairs, [2]string{"last renewal",
					s.LastRenewalAt.Format("2006-01-02 15:04") + " " + s.LastRenewalStatus})
			}
			if s.NextRenew != "" {
				pairs = append(pairs, [2]string{"next renewal", s.NextRenew})
			}
			if s.ConsecutiveFailures > 0 {
				pairs = append(pairs, [2]string{"consecutive failures", fmt.Sprint(s.ConsecutiveFailures)})
			}
			pairs = append(pairs, [2]string{"certificate", s.CertPath})
			if err := g.Fields(pairs...); err != nil {
				return err
			}
			if s.RenewalLog != "" {
				g.Printf("\nLast renewal error:\n  %s\n", strings.ReplaceAll(s.RenewalLog, "\n", "\n  "))
			}
			return nil
		},
	}
}

func newCertRenewCommand(g *Globals) *cobra.Command {
	var opts tls.RenewOptions
	cmd := &cobra.Command{
		Use:   "renew [<domain>]",
		Short: "Renew certificates that are due",
		Args:  cobra.MaximumNArgs(1),
		Long: "Run twice daily by ratline-cert-renew.timer.\n\n" +
			"A failure is not an emergency: the existing certificate is valid for weeks yet,\n" +
			"which is why the window is 30 days. One certificate failing never stops the\n" +
			"others; the failure is recorded and surfaced by 'ratline doctor'.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.Name = args[0]
			}
			if opts.Name == "" && !opts.All {
				opts.All = true
			}
			mgr, err := g.certManager(cmd.Context())
			if err != nil {
				return err
			}
			outcomes, err := mgr.Renew(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"outcomes": outcomes})
			}
			if len(outcomes) == 0 {
				g.Println("No certificates to renew.")
				return nil
			}
			var failed int
			tbl := g.Table("certificate", "action", "detail")
			for _, o := range outcomes {
				if o.Action == "failed" {
					failed++
				}
				tbl.Row(o.Name, o.Action, firstLine(o.Detail))
			}
			if err := tbl.Render(); err != nil {
				return err
			}
			if failed > 0 {
				// Not a non-zero exit: the previous certificates are still valid, and
				// the timer must not report a hard failure for something recoverable.
				g.Printf("\n%d renewal(s) failed. The existing certificates are still valid.\n"+
					"See why with:  ratline cert renew <domain> --dry-run\n", failed)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.All, "all", false, "Every certificate")
	f.BoolVar(&opts.Force, "force", false, "Renew even if not due")
	f.BoolVar(&opts.DryRun, "dry-run", false, "Exercise the challenge without replacing anything")
	return Mutating(cmd)
}

func newCertRevokeCommand(g *Globals) *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "revoke <domain>",
		Short: "Ask the certificate authority to revoke a certificate",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.certManager(cmd.Context())
			if err != nil {
				return err
			}
			if err := g.ConfirmTyped(args[0],
				fmt.Sprintf("Revoke the certificate for %s? This cannot be undone.", args[0])); err != nil {
				return err
			}
			if err := mgr.Revoke(cmd.Context(), args[0], reason); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"certificate": args[0], "revoked": true, "reason": reason})
			}
			g.Printf("Revoked %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "",
		"keycompromise, superseded or cessationofoperation")
	return Mutating(cmd)
}

func newCertDeleteCommand(g *Globals) *cobra.Command {
	var keepFiles bool
	cmd := &cobra.Command{
		Use:     "delete <domain>",
		Aliases: []string{"rm"},
		Short:   "Delete a certificate, refusing while a site still uses it",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.certManager(cmd.Context())
			if err != nil {
				return err
			}
			if err := mgr.Delete(cmd.Context(), args[0], keepFiles); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"certificate": args[0], "deleted": true})
			}
			g.Printf("Deleted %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepFiles, "keep-files", false, "Remove the state record but leave the files on disk")
	return Mutating(cmd)
}

func newCertImportCommand(g *Globals) *cobra.Command {
	var (
		opts     tls.ImportOptions
		noAttach bool
	)
	cmd := &cobra.Command{
		Use:   "import <domain>",
		Short: "Install a third-party certificate",
		Args:  cobra.ExactArgs(1),
		Long: "For a Cloudflare Origin certificate, ZeroSSL, or a corporate CA.\n\n" +
			"Everything is validated before anything is written: the PEM parses, the private\n" +
			"key matches the certificate, the chain builds, the dates are sane, and the SANs\n" +
			"cover the site's names. Each failure names its own reason.\n\n" +
			"Nothing renews an imported certificate. 'ratline doctor' warns as expiry\n" +
			"approaches, because nothing else will.",
		Example: "  ratline cert import example.com --cert origin.pem --key origin.key",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := RequireFlags(cmd, g, "cert", "key"); err != nil {
				return err
			}
			opts.Domain = args[0]
			opts.Attach = !noAttach
			mgr, err := g.certManager(cmd.Context())
			if err != nil {
				return err
			}
			cert, err := mgr.Import(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"certificate": cert})
			}
			g.Printf("Imported a certificate for %s\n", cert.Name)
			if err := g.Fields(
				[2]string{"issuer", cert.Issuer},
				[2]string{"sans", strings.Join(cert.SANs, ", ")},
				[2]string{"expires", fmt.Sprintf("%s (%d days)",
					cert.NotAfter.Format("2006-01-02"), cert.DaysRemaining(time.Now()))},
				[2]string{"auto-renew", "no — imported certificates are not renewed automatically"},
			); err != nil {
				return err
			}
			g.Printf("\nSet a reminder: nothing will renew this. 'ratline doctor' warns from 45 days out.\n")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.CertPath, "cert", "", "Certificate, ideally the full chain (required)")
	f.StringVar(&opts.KeyPath, "key", "", "Private key, not passphrase-encrypted (required)")
	f.StringVar(&opts.ChainPath, "chain", "", "Intermediates, if not already in --cert")
	f.BoolVar(&noAttach, "no-attach", false, "Install without pointing the vhost at it")
	return Mutating(cmd)
}

func newCertSelfSignCommand(g *Globals) *cobra.Command {
	var (
		days     int
		noAttach bool
	)
	cmd := &cobra.Command{
		Use:   "selfsign <domain>",
		Short: "Generate an untrusted placeholder so a site can serve HTTPS immediately",
		Args:  cobra.ExactArgs(1),
		Long: "So a site can serve HTTPS the moment it is created, before DNS is pointed.\n\n" +
			"Recorded distinctly, never counted as valid, always flagged in 'cert list' and\n" +
			"'doctor', and replaced cleanly by 'cert issue' later. HSTS is refused on one.",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.certManager(cmd.Context())
			if err != nil {
				return err
			}
			cert, err := mgr.SelfSign(cmd.Context(), args[0], days, !noAttach)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"certificate": cert})
			}
			g.Printf("Generated a self-signed placeholder for %s, valid until %s.\n",
				cert.Name, cert.NotAfter.Format("2006-01-02"))
			g.Printf("\nBrowsers will show a warning. Replace it once DNS points here:\n  ratline cert issue %s\n", cert.Name)
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 365, "Validity in days")
	cmd.Flags().BoolVar(&noAttach, "no-attach", false, "Generate without pointing the vhost at it")
	return Mutating(cmd)
}

func newCertAutoRenewCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auto-renew",
		Short: "Inspect or change automatic renewal",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Report whether renewal is actually wired up",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, err := g.certManager(cmd.Context())
			if err != nil {
				return err
			}
			s, err := mgr.AutoRenewState(cmd.Context())
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(s)
			}
			pairs := [][2]string{
				{"timer installed", yesNo(s.TimerInstalled)},
				{"timer active", yesNo(s.TimerActive)},
				{"certbot's own timer", s.CertbotTimer},
				{"auto-renew on", orDash(strings.Join(s.Enabled, ", "))},
				{"auto-renew off", orDash(strings.Join(s.Disabled, ", "))},
			}
			if s.NextRun != "" {
				pairs = append(pairs, [2]string{"next run", s.NextRun})
			}
			if err := g.Fields(pairs...); err != nil {
				return err
			}
			if !s.TimerInstalled {
				g.Printf("\nThe renewal timer is not installed. Nothing will renew automatically.\n" +
					"Install it from packaging/systemd/, or re-run the installer.\n")
			}
			if strings.Contains(s.CertbotTimer, "race") {
				g.Printf("\ncertbot.timer is enabled and will race ratline's timer: each reloads nginx\n" +
					"from under the other, and only ratline's runs the deploy hook that reloads\n" +
					"just the affected sites. Disable it:\n" +
					"  systemctl disable --now certbot.timer\n")
			}
			return nil
		},
	})
	for _, spec := range []struct {
		verb string
		on   bool
	}{{"enable", true}, {"disable", false}} {
		verb, on := spec.verb, spec.on
		sub := &cobra.Command{
			Use:   verb + " <domain>",
			Short: strings.ToUpper(verb[:1]) + verb[1:] + " automatic renewal for one certificate",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				mgr, err := g.certManager(cmd.Context())
				if err != nil {
					return err
				}
				if err := mgr.SetAutoRenew(cmd.Context(), args[0], on); err != nil {
					return err
				}
				if g.JSON {
					return g.EmitJSON(map[string]any{"certificate": args[0], "auto_renew": on})
				}
				g.Printf("Automatic renewal %sd for %s\n", verb, args[0])
				return nil
			},
		}
		cmd.AddCommand(Mutating(sub))
	}
	return cmd
}

func newCertTestRenewalCommand(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "test-renewal",
		Short: "Dry-run every certificate, to find breakage before it matters",
		Args:  cobra.NoArgs,
		Long: "Exercises the real challenge for every managed certificate without replacing\n" +
			"anything and without spending rate-limit budget.\n\n" +
			"Worth a monthly cron: it finds a closed port 80 or a moved DNS record weeks\n" +
			"before the certificate would actually expire.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, err := g.certManager(cmd.Context())
			if err != nil {
				return err
			}
			outcomes, err := mgr.TestRenewal(cmd.Context())
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"outcomes": outcomes})
			}
			if len(outcomes) == 0 {
				g.Println("No certificates to test.")
				return nil
			}
			failed := 0
			tbl := g.Table("certificate", "result", "detail")
			for _, o := range outcomes {
				if o.Action == "failed" {
					failed++
				}
				tbl.Row(o.Name, o.Action, firstLine(o.Detail))
			}
			if err := tbl.Render(); err != nil {
				return err
			}
			if failed > 0 {
				return rlerr.ACMEf("%d certificate(s) would fail to renew", failed).
					WithHint("fix these now; they have weeks of validity left, which is the point of testing early")
			}
			g.Printf("\nEvery certificate would renew successfully.\n")
			return nil
		},
	}
}

func newCertAccountCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{Use: "account", Short: "Inspect the ACME account"}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show the ACME account state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, err := g.certManager(cmd.Context())
			if err != nil {
				return err
			}
			info := mgr.Account(cmd.Context())
			if g.JSON {
				return g.EmitJSON(info)
			}
			return g.Fields(
				[2]string{"contact", orDash(info.Email)},
				[2]string{"directory", info.Directory},
				[2]string{"terms accepted", yesNo(info.TOSAgreed)},
				[2]string{"registered", yesNo(info.Registered)},
			)
		},
	})
	var email string
	register := &cobra.Command{
		Use:   "register",
		Short: "Record the ACME contact address and accept the terms",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := RequireFlags(cmd, g, "email"); err != nil {
				return err
			}
			// Registration proper happens on the first issuance; what this records
			// is the contact address and the agreement, which are ratline's to keep.
			g.Cfg.ACME.Email = email
			g.Cfg.ACME.TOSAgreed = true
			if err := g.Cfg.Save(g.configPath()); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"email": email, "tos_agreed": true})
			}
			g.Printf("Recorded %s as the ACME contact, and accepted the subscriber agreement.\n", email)
			return nil
		},
	}
	register.Flags().StringVar(&email, "email", "", "Contact address (required)")
	cmd.AddCommand(Mutating(register))
	return cmd
}

func newCertDeployHookCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "deploy-hook",
		Short:  "Reload the sites affected by a renewal (invoked by certbot)",
		Args:   cobra.NoArgs,
		Hidden: true,
		Long: "certbot sets RENEWED_LINEAGE and RENEWED_DOMAINS. This maps them back to sites\n" +
			"through state, tests the nginx configuration, and reloads only the affected\n" +
			"site — never a blanket restart of everything.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, err := g.certManager(cmd.Context())
			if err != nil {
				return err
			}
			reloaded, err := mgr.DeployHook(cmd.Context())
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"reloaded": reloaded})
			}
			g.Printf("Reloaded %d site(s): %s\n", len(reloaded), strings.Join(reloaded, ", "))
			return nil
		},
	}
	// The hook runs inside a `cert renew` that already holds the global lock, so
	// taking it again would deadlock against ourselves.
	return SkipLock(Mutating(cmd))
}
