package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

func newConfigCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show, check and describe the panel's configuration",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "Print the effective configuration",
			Args:  cobra.NoArgs,
			Long: "The values in force, not the file: an absent key shows the default it fell\n" +
				"back to, which is the thing somebody debugging actually wants to know.",
			RunE: func(cmd *cobra.Command, _ []string) error {
				out, err := yaml.Marshal(app.Cfg)
				if err != nil {
					return rlerr.Wrap(err, rlerr.CodeGeneric, "rendering the configuration")
				}
				fmt.Fprintf(app.Stdout, "# effective configuration (source: %s, loaded: %t)\n%s",
					app.Cfg.SourcePath, app.Cfg.Loaded, out)
				return nil
			},
		},
		&cobra.Command{
			Use:   "path",
			Short: "Print the configuration file's path",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				fmt.Fprintln(app.Stdout, app.Cfg.SourcePath)
				return nil
			},
		},
		&cobra.Command{
			Use:   "validate",
			Short: "Check the configuration and say what is wrong",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				if err := app.Cfg.Validate(); err != nil {
					return err
				}
				app.printf("%s is valid.\n", app.Cfg.SourcePath)
				return nil
			},
		},
		&cobra.Command{
			Use:   "reference",
			Short: "Print a commented reference of every setting",
			Args:  cobra.NoArgs,
			Long: "The defaults with the reasoning attached. Redirect it into panel.yaml to\n" +
				"start from a file that explains itself.",
			RunE: func(cmd *cobra.Command, _ []string) error {
				fmt.Fprint(app.Stdout, configReference)
				return nil
			},
		},
	)
	return cmd
}

// configReference is written rather than generated, because the value of it is the
// commentary and a generator cannot write that.
const configReference = `# /etc/ratline/panel.yaml
#
# ratline-panel is a caller of the ratline binary. Everything it does on this server,
# it does by running 'ratline … --json' and reading the envelope — so what is
# configured here is the front door, not the behaviour of any operation.

version: 1

listen:
  # 127.0.0.1 unless you have a reason. The panel is a root-equivalent surface:
  # anybody who signs in can provision services on this machine. Binding to the
  # loopback and putting nginx in front means what faces the internet is nginx, with
  # a certificate and logs, rather than this process.
  address: 127.0.0.1
  port: 8420
  # Set by 'ratline-panel domain set'. The panel needs its own name to decide
  # whether a session cookie may be marked Secure and whether a state-changing
  # request came from itself.
  domain: ""
  # Believe X-Forwarded-For and X-Forwarded-Proto. True because nginx on this host
  # sets them. If the panel is ever reachable directly, set it to false — otherwise
  # every client can claim any address, and the per-address rate limit counts them
  # separately.
  trust_proxy: true

ratline:
  binary: /usr/local/bin/ratline
  # Passed through as --config. Empty means ratline finds its own, which is what a
  # normal install wants.
  config: ""
  # A listing is a process spawn and a SQLite read; a deploy is npm and a build.
  read_timeout: 45s
  write_timeout: 5m
  job_timeout: 45m

paths:
  # Password hashes, TOTP secrets and live session hashes. 0600 root:root — the one
  # file on this server that, read, lets somebody become an administrator of it.
  state_db: /var/lib/ratline/panel.db
  audit_log: /var/log/ratline/panel-audit.log
  nginx_vhost: /etc/nginx/sites-available/ratline-panel.conf

session:
  # The absolute lifetime. A session ends this long after it began however active it
  # has been, so a stolen cookie has a horizon.
  ttl: 12h
  # Ends a session that has gone quiet. Refreshing it never pushes the absolute
  # expiry past its original ceiling.
  idle_timeout: 2h
  # auto | always | never. auto marks the cookie Secure when the request arrived
  # over HTTPS — which is what lets the first sign-in happen through an SSH tunnel
  # on http://localhost without shipping a cookie in the clear once there is a
  # domain.
  secure_cookie: auto
  cookie_name: ratline_panel

security:
  # Refuse to let an account do anything until it has enrolled a second factor.
  # Off by default because a panel nobody can sign in to is broken rather than
  # secure. On is the right answer for a panel on a public domain.
  require_totp: false
  invite_ttl: 72h
  # Counted per account and per source address independently: per account alone
  # lets one password be tried against every address, per address alone lets a
  # distributed attempt through.
  max_failed_logins: 8
  login_window: 15m
  # When set, every request from outside these blocks is refused before its body is
  # read. A second lock for a panel that is reachable from the internet and only
  # ever used from one place.
  allow_from: []

jobs:
  retain: 300
  output_limit_bytes: 1048576
  # One. ratline takes a global lock for every mutation, so a second job would sit
  # inside ratline waiting for the first and eventually report exit 5. Queueing here
  # turns that into a position in a line somebody can watch.
  concurrency: 1

logging:
  level: info
  json: false
`
