package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

func newInitCommand(g *Globals) *cobra.Command {
	var (
		writeConfigOnly bool
		email           string
		adminUser       string
		agreeTOS        bool
	)
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Set up this server: configuration, directories and defaults",
		GroupID: GroupOps,
		Args:    cobra.NoArgs,
		Long: "Seeds /etc/ratline/config.yaml, creates the directories ratline needs, and\n" +
			"records the ACME contact address and the administrator account.\n\n" +
			"Safe to re-run: it reviews and updates settings rather than starting over, and\n" +
			"never overwrites a configuration file you have edited.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			created, err := config.Seed(g.configPath())
			if err != nil {
				return err
			}
			if created {
				g.Log.Info("wrote the configuration", "path", g.configPath())
			}
			if err := g.ensureDirectories(); err != nil {
				return err
			}
			// The timers ratline runs for itself. They come out of the embedded
			// templates, so a server that only ever received the binary — which is what
			// a one-command install leaves — still gets certificate renewal. Before
			// this, only install.sh and the .deb postinstall placed them, and both
			// copied from a directory that had to be sitting next to the installer.
			if err := g.ensureOwnTimers(cmd.Context()); err != nil {
				// Not fatal: the configuration and directories are already in place, and
				// a server with no systemd is a supported oddity. doctor reports it.
				g.Log.Warn("could not install ratline's own timers", "err", err,
					"fix", "run 'ratline init' again once systemd is available")
			}

			// --write-config-only is what the package postinstall calls: it wants
			// the file and the directories, with no questions.
			if writeConfigOnly {
				if g.JSON {
					return g.EmitJSON(map[string]any{"config": g.configPath(), "created": created})
				}
				g.Printf("Configuration at %s\n", g.configPath())
				return nil
			}

			cfg, err := config.LoadOrDefault(g.configPath())
			if err != nil {
				return err
			}
			p := newPrompter(g)
			p.heading("ratline setup")
			p.note("Host: %s", g.OS)
			if !g.OS.Supported() {
				p.note("ratline targets Debian and Ubuntu; on this host the layout may differ")
			}

			// Report what is present rather than installing anything silently.
			for _, bin := range []string{"nginx", "certbot", "systemctl", "ssh-keygen", "git"} {
				if g.Bins.Available(bin) {
					p.note("%-12s found", bin)
				} else {
					p.note("%-12s missing — apt-get install %s", bin, bin)
				}
			}

			if email == "" && g.CanPrompt() {
				answer, err := p.ask("ACME contact address (expiry warnings go here):", cfg.ACME.Email, func(s string) error {
					if s == "" {
						return nil
					}
					return validate.Email(s)
				})
				if err != nil {
					return errCancelledToNil(err)
				}
				email = answer
			}
			if email != "" {
				if err := validate.Email(email); err != nil {
					return err
				}
				cfg.ACME.Email = email
			}

			if adminUser == "" {
				// Under sudo the operator's own account is the sensible default:
				// they are already logging in as it.
				adminUser = g.Invoker.SudoUser
				if adminUser == "" {
					adminUser = g.Invoker.Name
				}
				if g.CanPrompt() {
					answer, err := p.ask("Account that holds global-scope SSH keys:", adminUser, func(s string) error {
						if s == "" {
							return nil
						}
						if !system.UserExists(s) {
							return rlerr.Preconditionf("%s is not an account on this system", s)
						}
						return nil
					})
					if err != nil {
						return errCancelledToNil(err)
					}
					adminUser = answer
				}
			}
			if adminUser != "" && system.UserExists(adminUser) {
				cfg.Server.AdminUser = adminUser
			}

			if !cfg.ACME.TOSAgreed && cfg.ACME.Email != "" {
				if agreeTOS {
					cfg.ACME.TOSAgreed = true
				} else if g.CanPrompt() {
					p.note("Let's Encrypt requires you to accept its subscriber agreement:")
					p.note("  https://letsencrypt.org/repository/")
					ok, err := p.confirm("Do you accept it?", false)
					if err != nil {
						return errCancelledToNil(err)
					}
					cfg.ACME.TOSAgreed = ok
				}
			}

			if err := cfg.Save(g.configPath()); err != nil {
				return err
			}

			// The public addresses are needed by every certificate preflight, and
			// detecting them costs a network round trip, so they are cached.
			if st, err := g.Store(ctx); err == nil {
				if err := st.SetServerValue(ctx, "initialised_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
					g.Log.Debug("could not record the setup time", "err", err)
				}
			}

			if g.JSON {
				return g.EmitJSON(map[string]any{
					"config": g.configPath(), "acme_email": cfg.ACME.Email,
					"admin_user": cfg.Server.AdminUser, "tos_agreed": cfg.ACME.TOSAgreed,
				})
			}
			g.Println()
			if err := g.Fields(
				[2]string{"configuration", g.configPath()},
				[2]string{"acme contact", orDash(cfg.ACME.Email)},
				[2]string{"admin account", orDash(cfg.Server.AdminUser)},
				[2]string{"terms accepted", yesNo(cfg.ACME.TOSAgreed)},
			); err != nil {
				return err
			}
			g.Printf("\nratline does not change your firewall. These ports need to be reachable:\n" +
				"    22    SSH\n" +
				"    80    HTTP, and the ACME challenge — renewal fails without it\n" +
				"    443   HTTPS\n" +
				"\nNext:\n" +
				"    ratline runtime install node 22       if you will host Node sites\n" +
				"    ratline runtime install python 3.12   if you will host Python sites\n" +
				"    ratline user add <name>               create your first tenant\n" +
				"    ratline doctor                        confirm the server is healthy\n")
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&writeConfigOnly, "write-config-only", false, "Seed the configuration and directories, then stop")
	f.StringVar(&email, "email", "", "ACME contact address")
	f.StringVar(&adminUser, "admin-user", "", "Account that holds global-scope SSH keys")
	f.BoolVar(&agreeTOS, "agree-tos", false, "Accept the certificate authority's subscriber agreement")
	return Mutating(cmd)
}

// ensureDirectories creates ratline's own directories with the modes it needs.
//
// The credential directories are 0700: /etc/ratline/dns holds DNS provider API
// tokens, which are as good as the domain itself.
func (g *Globals) ensureDirectories() error {
	dirs := []struct {
		path string
		mode uint32
	}{
		{filepath.Dir(g.Cfg.Paths.StateDB), 0o750},
		{filepath.Dir(g.Cfg.Paths.AuditLog), 0o750},
		{g.Cfg.Paths.SSHDir, 0o700},
		{g.Cfg.Paths.DNSCredentials, 0o700},
		{g.Cfg.Paths.ImportedCerts, 0o700},
		{g.Cfg.Paths.RuntimesDir, 0o755},
		{g.Cfg.Paths.NginxSnippets, 0o755},
		{g.Cfg.Paths.NginxCustom, 0o755},
		{g.Cfg.Paths.ACMEWebroot, 0o755},
		// Every level has to be listed. The leaf was 0755 and its parent was created
		// implicitly under the 0027 provisioning umask, landing at 0750 — so nginx,
		// running as www-data, could not traverse into .well-known and every HTTP-01
		// challenge returned 404. A fresh install could therefore never renew a
		// certificate, and nothing short of a real request would have said so.
		{filepath.Join(g.Cfg.Paths.ACMEWebroot, ".well-known"), 0o755},
		{filepath.Join(g.Cfg.Paths.ACMEWebroot, ".well-known", "acme-challenge"), 0o755},
		{g.Cfg.Paths.BackupDir, 0o700},
		{filepath.Dir(g.Cfg.Paths.ShellWrapper), 0o755},
	}
	for _, d := range dirs {
		if g.DryRun {
			g.Log.Info("would create", "path", d.path, "mode", fmt.Sprintf("%04o", d.mode))
			continue
		}
		if err := ensureDirAll(d.path, d.mode); err != nil {
			return err
		}
	}
	return nil
}

// ensureDirAll creates a directory and its parents, then sets the exact mode on
// the leaf. The leaf's mode is what matters: /etc/ratline/dns holds DNS provider
// API tokens, which are as good as the domain itself.
func ensureDirAll(path string, mode uint32) error {
	return system.MkdirAllMode(path, os.FileMode(mode))
}

// ensureOwnTimers installs the renewal and key-pruning units from the embedded
// templates and starts their timers.
func (g *Globals) ensureOwnTimers(ctx context.Context) error {
	if !g.Bins.Available("systemctl") {
		return rlerr.Preconditionf("systemd is not available on this host")
	}
	mgr := &unit.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, DryRun: g.DryRun}
	return mgr.EnsureTimers(ctx)
}
