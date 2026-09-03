package cli

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/ALIRAZA47/ratline-cli/internal/panel/auth"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// The account commands exist for one reason: the panel can lock you out of itself.
//
// A super admin who loses their second factor, or who is the only one and gets
// disabled, cannot fix it from a browser they cannot reach. Every recovery path
// therefore has a command here, runnable over SSH by whoever already has root — which
// is the honest boundary, because somebody with root on this machine can read the
// panel's database anyway.
func newAccountCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "Manage panel accounts from the command line",
		Long: "The recovery path. A panel that has locked out its last super admin is fixed\n" +
			"from here, over SSH, by whoever has root — which is not a new privilege,\n" +
			"because root can read the panel's database in any case.",
	}
	cmd.AddCommand(
		newAccountListCommand(app),
		newAccountCreateCommand(app),
		newAccountRoleCommand(app),
		newAccountPasswordCommand(app),
		newAccountDisableCommand(app),
		newAccountTOTPResetCommand(app),
		newAccountDeleteCommand(app),
	)
	return cmd
}

func newAccountListCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List panel accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := app.openStore()
			if err != nil {
				return err
			}
			defer st.Close() //nolint:errcheck // a read-only command exiting
			accounts, err := st.ListAccounts(cmd.Context())
			if err != nil {
				return err
			}
			if len(accounts) == 0 {
				app.printf("No accounts. The first person to reach %s becomes the super admin.\n",
					app.Cfg.PublicURL())
				return nil
			}
			w := tabwriter.NewWriter(app.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "EMAIL\tROLE\t2FA\tSTATE\tLAST SIGN-IN")
			for _, a := range accounts {
				state := "active"
				if a.Disabled {
					state = "disabled"
				}
				totp := "no"
				if a.TOTPEnabled {
					totp = "yes"
				}
				last := "never"
				if !a.LastLoginAt.IsZero() {
					last = a.LastLoginAt.Format("2006-01-02 15:04")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", a.Email, a.Role, totp, state, last)
			}
			return w.Flush()
		},
	}
}

func newAccountCreateCommand(app *App) *cobra.Command {
	var role, name string
	cmd := &cobra.Command{
		Use:   "create <email>",
		Short: "Create an account, reading the password from a terminal or stdin",
		Args:  cobra.ExactArgs(1),
		Long: "The password is never a flag. /proc/PID/cmdline is world-readable, so a\n" +
			"password on a command line is a password every account on this machine can\n" +
			"read while the command runs — and it is in the shell history afterwards.\n\n" +
			"On a terminal it is prompted for without echo; otherwise it is read from\n" +
			"stdin, which is what a provisioning script should do.",
		Example: "  ratline-panel account create you@example.com --role superadmin\n\n" +
			"  # from a script\n" +
			"  printf '%s' \"$PANEL_PASSWORD\" | ratline-panel account create ops@example.com",
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.createAccount(cmd.Context(), args[0], name, role)
		},
	}
	cmd.Flags().StringVar(&role, "role", store.RoleAdmin, "superadmin or admin")
	cmd.Flags().StringVar(&name, "name", "", "Display name")
	return cmd
}

func (app *App) createAccount(ctx context.Context, email, name, role string) error {
	password, err := app.readSecret("Password: ")
	if err != nil {
		return err
	}
	st, err := app.openStore()
	if err != nil {
		return err
	}
	defer st.Close() //nolint:errcheck // the process is exiting
	if _, err := app.addAccount(ctx, st, email, name, role, password, "command line"); err != nil {
		return err
	}
	app.printf("Created %s as %s.\n", store.NormalizeEmail(email), role)
	return nil
}

// addAccount validates, hashes and inserts.
//
// One function for the three places that create an account — `account create`, the
// installer, and the panel's own setup page — because they must not be able to
// disagree about what a valid address is, how strong a password has to be, or which
// roles exist. The third of those lives in the HTTP layer and calls its own copy of
// the same checks; this is the command-line half.
func (app *App) addAccount(ctx context.Context, st *store.Store,
	email, name, role, password, by string) (*store.Account, error) {
	email = store.NormalizeEmail(email)
	if err := validate.Email(email); err != nil {
		return nil, err
	}
	if !store.ValidRole(role) {
		return nil, rlerr.Usagef("a role must be %q or %q", store.RoleSuperAdmin, store.RoleAdmin)
	}
	if err := validate.NoControlChars("name", name); err != nil {
		return nil, err
	}
	if err := auth.CheckPasswordStrength(password); err != nil {
		return nil, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	id, err := auth.NewID()
	if err != nil {
		return nil, err
	}
	account := &store.Account{
		ID: id, Email: email, Name: name, Role: role,
		PasswordHash: hash, CreatedBy: by, CreatedAt: time.Now().UTC(),
	}
	if err := st.CreateAccount(ctx, account); err != nil {
		return nil, err
	}
	return account, nil
}

func newAccountRoleCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "role <email> <role>",
		Short: "Change an account's role",
		Args:  cobra.ExactArgs(2),
		Long: "This is how a panel with no super admin gets one back. It refuses to remove\n" +
			"the last active super admin, here as in the browser: the check is in the\n" +
			"store, so both paths get the same answer.",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := app.openStore()
			if err != nil {
				return err
			}
			defer st.Close() //nolint:errcheck // the process is exiting
			account, err := st.FindAccountByEmail(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := st.SetAccountRole(cmd.Context(), account.ID, args[1]); err != nil {
				return err
			}
			app.printf("%s is now %s.\n", account.Email, args[1])
			return nil
		},
	}
}

func newAccountPasswordCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "password <email>",
		Short: "Set an account's password, signing it out everywhere",
		Args:  cobra.ExactArgs(1),
		Long: "Every session for the account ends, which is the point: a password reset\n" +
			"that leaves the old sessions signed in has not taken the access back.",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := app.openStore()
			if err != nil {
				return err
			}
			defer st.Close() //nolint:errcheck // the process is exiting
			account, err := st.FindAccountByEmail(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			password, err := app.readSecret("New password: ")
			if err != nil {
				return err
			}
			if err := auth.CheckPasswordStrength(password); err != nil {
				return err
			}
			hash, err := auth.HashPassword(password)
			if err != nil {
				return err
			}
			if err := st.SetPassword(cmd.Context(), account.ID, hash); err != nil {
				return err
			}
			app.printf("Set. %s has been signed out everywhere.\n", account.Email)
			return nil
		},
	}
}

func newAccountDisableCommand(app *App) *cobra.Command {
	var enable bool
	cmd := &cobra.Command{
		Use:   "disable <email>",
		Short: "Disable an account and end its sessions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := app.openStore()
			if err != nil {
				return err
			}
			defer st.Close() //nolint:errcheck // the process is exiting
			account, err := st.FindAccountByEmail(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := st.SetAccountDisabled(cmd.Context(), account.ID, !enable); err != nil {
				return err
			}
			state := "disabled"
			if enable {
				state = "enabled"
			}
			app.printf("%s is %s.\n", account.Email, state)
			return nil
		},
	}
	cmd.Flags().BoolVar(&enable, "enable", false, "Re-enable instead")
	return cmd
}

func newAccountTOTPResetCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "totp-reset <email>",
		Short: "Remove an account's second factor so it can enrol again",
		Args:  cobra.ExactArgs(1),
		Long: "For the phone that was lost or wiped. It removes the second factor and\n" +
			"nothing else — the password still applies — so the account can sign in and\n" +
			"enrol a new one.",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := app.openStore()
			if err != nil {
				return err
			}
			defer st.Close() //nolint:errcheck // the process is exiting
			account, err := st.FindAccountByEmail(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := st.SetTOTP(cmd.Context(), account.ID, "", false); err != nil {
				return err
			}
			if err := st.DeleteSessionsFor(cmd.Context(), account.ID); err != nil {
				return err
			}
			app.printf("The second factor for %s has been removed. It can be enrolled again after signing in.\n",
				account.Email)
			return nil
		},
	}
}

func newAccountDeleteCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <email>",
		Short: "Remove an account's access to the panel",
		Args:  cobra.ExactArgs(1),
		Long: "It removes access to the panel and nothing else. Tenants, sites, keys and\n" +
			"certificates belong to the server, not to the person who administered them.",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := app.openStore()
			if err != nil {
				return err
			}
			defer st.Close() //nolint:errcheck // the process is exiting
			account, err := st.FindAccountByEmail(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !app.Yes {
				if err := app.confirmTyped(account.Email,
					fmt.Sprintf("This removes %s's access to the panel.", account.Email)); err != nil {
					return err
				}
			}
			if err := st.DeleteAccount(cmd.Context(), account.ID); err != nil {
				return err
			}
			app.printf("Removed %s.\n", account.Email)
			return nil
		},
	}
}

// canPrompt reports whether there is a terminal on both ends to ask a question with.
//
// Both, not one: reading an answer needs stdin, and showing the question needs a
// terminal to show it on. An installer piped from curl has neither on its own
// streams, which is why it passes the answers as flags instead.
func (app *App) canPrompt() bool {
	return term.IsTerminal(int(app.Stdin.Fd())) && term.IsTerminal(int(app.Stderr.Fd()))
}

// ask puts a question on stderr and reads one line from stdin.
//
// stderr rather than stdout, so that a command whose output is being captured is not
// polluted by its own prompts.
func (app *App) ask(question string) (string, error) {
	if !app.canPrompt() {
		return "", rlerr.InputRequiredf("%s", strings.TrimSpace(question))
	}
	fmt.Fprint(app.Stderr, question)
	line, err := bufio.NewReader(app.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", rlerr.InputRequiredf("could not read a reply from the terminal")
	}
	return strings.TrimSpace(line), nil
}

// readSecret reads a password without echoing it, or from stdin when there is no
// terminal — which is what a provisioning script needs and what makes the password
// never a flag.
func (app *App) readSecret(prompt string) (string, error) {
	fd := int(app.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(app.Stderr, prompt)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(app.Stderr)
		if err != nil {
			return "", rlerr.Wrap(err, rlerr.CodeInputRequired, "reading the password")
		}
		return string(b), nil
	}
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := app.Stdin.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
		if sb.Len() > 4096 {
			break
		}
	}
	secret := strings.TrimRight(sb.String(), "\r\n")
	if secret == "" {
		return "", rlerr.InputRequiredf("no password was supplied").
			WithHint("pipe it in: printf '%%s' \"$PASSWORD\" | ratline-panel account …")
	}
	return secret, nil
}

// confirmTyped requires the exact name back, the same discipline ratline uses.
func (app *App) confirmTyped(expect, question string) error {
	fd := int(app.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return rlerr.InputRequiredf("%s", question).
			WithHint("pass --yes to confirm without a terminal")
	}
	fmt.Fprintf(app.Stderr, "%s\nType %q to confirm: ", question, expect)
	var line string
	if _, err := fmt.Fscanln(app.Stdin, &line); err != nil {
		return rlerr.InputRequiredf("could not read a reply")
	}
	if strings.TrimSpace(line) != expect {
		return rlerr.Usagef("confirmation did not match %q; nothing was changed", expect)
	}
	return nil
}
