package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/user"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

func newUserCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "user",
		Short:   "Create and manage tenant accounts",
		GroupID: GroupUsers,
		Long: "Each user is a sandbox: its own system account, group, home tree and SSH keys.\n" +
			"Making one is cheap on purpose — one user per site is the recommended pattern\n" +
			"whenever a site is run by someone you do not fully trust.",
	}
	cmd.AddCommand(
		newUserAddCommand(g),
		newUserListCommand(g),
		newUserShowCommand(g),
		newUserEnableCommand(g, false),
		newUserEnableCommand(g, true),
		newUserDeleteCommand(g),
		newUserPasswordCommand(g),
		newUserSudoCommand(g),
		newUserKeyAliasCommand(g),
	)
	return cmd
}

// manager builds a user.Manager from the resolved globals.
func (g *Globals) userManager(ctx context.Context) (*user.Manager, error) {
	st, err := g.Store(ctx)
	if err != nil {
		return nil, err
	}
	return &user.Manager{
		Cfg:     g.Cfg,
		Log:     g.Log,
		Runner:  g.Runner,
		State:   st,
		Invoker: g.Invoked(),
		DryRun:  g.DryRun,
	}, nil
}

func newUserAddCommand(g *Globals) *cobra.Command {
	var (
		sshKeys       []string
		passwordLogin bool
		shell         string
		sftpOnly      bool
		quota         string
		memoryMax     string
		comment       string
	)
	cmd := &cobra.Command{
		Use:   "add <username>",
		Short: "Create a tenant account with a home tree and key-only SSH",
		Args:  cobra.MaximumNArgs(1),
		Example: "  ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub\n" +
			"  ratline user add contractor --sftp-only --quota 5G\n" +
			"  cat key.pub | ratline user add ci --ssh-key -",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := user.AddOptions{
				Shell:         shell,
				Comment:       comment,
				PasswordLogin: passwordLogin,
				SFTPOnly:      sftpOnly,
				Quota:         quota,
				MemoryMax:     memoryMax,
			}
			if len(args) == 1 {
				opts.Name = args[0]
			}
			if g.Interactive || (opts.Name == "" && g.CanPrompt()) {
				var err error
				if opts, sshKeys, err = wizardUserAdd(g, opts, sshKeys); err != nil {
					return errCancelledToNil(err)
				}
			}
			if opts.Name == "" {
				return rlerr.Usagef("a username is required").
					WithHint("run 'ratline user add <username>', or -i for a guided setup")
			}

			mgr, err := g.userManager(cmd.Context())
			if err != nil {
				return err
			}
			u, err := mgr.Add(cmd.Context(), opts)
			if err != nil {
				return err
			}

			// Keys are added after the account exists, because the key manager
			// writes into the home tree the account owns.
			var added int
			if len(sshKeys) > 0 {
				added, err = g.addUserKeys(cmd.Context(), u.Name, sshKeys)
				if err != nil {
					return err
				}
			}

			if g.JSON {
				return g.EmitJSON(map[string]any{"user": u, "keys_added": added})
			}
			g.Printf("Created %s\n", u.Name)
			pairs := [][2]string{
				{"home", u.Home},
				{"shell", u.Shell},
				{"login", loginDescription(u)},
				{"keys", fmt.Sprint(added)},
			}
			if u.Quota != "" {
				pairs = append(pairs, [2]string{"quota", u.Quota})
			}
			if err := g.Fields(pairs...); err != nil {
				return err
			}
			if added == 0 && !u.PasswordLogin {
				g.Printf("\nThis account cannot log in yet. Add a key:\n  ratline key add --scope user --user %s --label \"…\" --key ~/.ssh/id_ed25519.pub\n", u.Name)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&sshKeys, "ssh-key", nil, "Public key: the key itself, a path, an https URL, or - for stdin (repeatable)")
	f.BoolVar(&passwordLogin, "password-login", false, "Allow password login (default: keys only)")
	f.StringVar(&shell, "shell", "", "Login shell (default from config; /usr/sbin/nologin to disable)")
	f.BoolVar(&sftpOnly, "sftp-only", false, "SFTP only, chrooted to the home directory, with no shell")
	f.StringVar(&quota, "quota", "", "Disk quota, e.g. 20G (needs filesystem quota support)")
	f.StringVar(&memoryMax, "memory-max", "", "Default memory ceiling inherited by this user's sites, e.g. 512M")
	f.StringVar(&comment, "comment", "", "Description recorded in /etc/passwd")
	return OwnWizard(Mutating(cmd))
}

func loginDescription(u *state.User) string {
	switch {
	case u.SFTPOnly:
		return "sftp only, no shell"
	case u.PasswordLogin:
		return "password and keys"
	default:
		return "keys only, password locked"
	}
}

func newUserListCommand(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List tenant accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			users, err := st.ListUsers(cmd.Context())
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"users": users})
			}
			if len(users) == 0 {
				g.Println("No users yet. Create one with: ratline user add <username>")
				return nil
			}
			tbl := g.Table("user", "uid", "sites", "login", "status")
			for _, u := range users {
				n, err := st.CountSitesForUser(cmd.Context(), u.Name)
				if err != nil {
					return err
				}
				status := "active"
				if u.Disabled {
					status = "disabled"
				}
				tbl.Row(u.Name, fmt.Sprint(u.UID), fmt.Sprint(n), loginDescription(u), status)
			}
			return tbl.Render()
		},
	}
}

func newUserShowCommand(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "show <username>",
		Short: "Show a user's home, sites, keys, disk usage and services",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.userManager(cmd.Context())
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
				{"user", info.Name},
				{"uid/gid", fmt.Sprintf("%d/%d", info.UID, info.GID)},
				{"home", info.Home},
				{"shell", info.Shell},
				{"login", loginDescription(info.User)},
				{"disk", info.DiskHuman},
				{"ssh keys", fmt.Sprint(info.KeyCount)},
				{"sites", fmt.Sprint(len(info.Sites))},
			}
			if info.Quota != "" {
				pairs = append(pairs, [2]string{"quota", info.Quota})
			}
			if info.Disabled {
				pairs = append(pairs, [2]string{"status", "disabled"})
			}
			if !info.OnSystem {
				// State and reality disagree, which is exactly what doctor and
				// reconcile exist for.
				pairs = append(pairs, [2]string{"warning", "recorded in state but no system account exists — run 'ratline doctor'"})
			}
			if err := g.Fields(pairs...); err != nil {
				return err
			}
			if len(info.Sites) > 0 {
				g.Println()
				tbl := g.Table("domain", "runtime", "enabled", "service")
				for _, s := range info.Sites {
					active := "-"
					for _, u := range info.Units {
						if u.Domain == s.Domain {
							active = u.Active
						}
					}
					tbl.Row(s.Domain, s.Runtime, yesNo(s.Enabled), active)
				}
				return tbl.Render()
			}
			return nil
		},
	}
}

func newUserEnableCommand(g *Globals, disable bool) *cobra.Command {
	verb, short := "enable", "Re-enable a user and restart their sites"
	if disable {
		verb, short = "disable", "Lock a user's login and stop all their sites"
	}
	cmd := &cobra.Command{
		Use:   verb + " <username>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.userManager(cmd.Context())
			if err != nil {
				return err
			}
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			sites, err := st.ListSites(cmd.Context(), state.SiteFilter{Owner: args[0]})
			if err != nil {
				return err
			}
			if err := mgr.SetDisabled(cmd.Context(), args[0], disable); err != nil {
				return err
			}
			// Sites follow the account: a disabled tenant should not still be
			// serving traffic, and an enabled one should not stay dark.
			sm, err := g.siteManager(cmd.Context())
			if err != nil {
				return err
			}
			var touched int
			for _, s := range sites {
				if disable {
					err = sm.Disable(cmd.Context(), s.Domain)
				} else {
					err = sm.Enable(cmd.Context(), s.Domain)
				}
				if err != nil {
					g.Log.Warn("could not update a site", "domain", s.Domain, "err", err)
					continue
				}
				touched++
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"user": args[0], "disabled": disable, "sites_updated": touched})
			}
			g.Printf("%s %sd (%d site(s) updated)\n", args[0], verb, touched)
			return nil
		},
	}
	return Mutating(cmd)
}

func newUserDeleteCommand(g *Globals) *cobra.Command {
	var (
		purge     bool
		backupDir string
	)
	cmd := &cobra.Command{
		Use:     "delete <username>",
		Aliases: []string{"rm"},
		Short:   "Delete a user, refusing while they still own sites unless --purge",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			u, err := st.GetUser(cmd.Context(), name)
			if err != nil {
				return err
			}
			sites, err := st.ListSites(cmd.Context(), state.SiteFilter{Owner: name})
			if err != nil {
				return err
			}

			// A precise inventory before a destructive step: an operator should
			// never be surprised by what a delete took with it.
			mgr, err := g.userManager(cmd.Context())
			if err != nil {
				return err
			}
			info, err := mgr.Show(cmd.Context(), name)
			if err != nil {
				return err
			}
			if !g.JSON {
				g.Printf("This will permanently delete:\n")
				g.Printf("  the system account %s (uid %d) and its group\n", name, u.UID)
				g.Printf("  %s (%s on disk)\n", u.Home, info.DiskHuman)
				g.Printf("  %d SSH key(s)\n", info.KeyCount)
				for _, s := range sites {
					g.Printf("  the site %s, its vhost, unit, logs and certificate attachment\n", s.Domain)
				}
			}
			if err := g.ConfirmTyped(name, fmt.Sprintf("Delete %s and everything above?", name)); err != nil {
				return err
			}

			// Sites are torn down first: removing a home from under a running
			// service leaves nginx serving 502s and a unit that cannot start.
			if len(sites) > 0 {
				if !purge {
					names := make([]string, 0, len(sites))
					for _, s := range sites {
						names = append(names, s.Domain)
					}
					return rlerr.Preconditionf("%s still owns %d site(s): %s", name, len(sites), strings.Join(names, ", ")).
						WithHint("pass --purge to delete them too")
				}
				sm, err := g.siteManager(cmd.Context())
				if err != nil {
					return err
				}
				for _, s := range sites {
					if err := sm.Delete(cmd.Context(), s.Domain, true, backupDir); err != nil {
						return err
					}
				}
			}

			if err := mgr.Delete(cmd.Context(), user.DeleteOptions{
				Name: name, Purge: purge, BackupDir: backupDir,
			}); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"user": name, "deleted": true, "sites_deleted": len(sites)})
			}
			g.Printf("Deleted %s\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "Also delete every site the user owns")
	cmd.Flags().StringVar(&backupDir, "backup", "", "Archive the home directory into this directory first")
	return Mutating(cmd)
}

func newUserPasswordCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "password",
		Short: "Manage passwords (keys are preferred)",
	}
	var fromStdin bool
	set := &cobra.Command{
		Use:   "set <username>",
		Short: "Set a password, read from stdin",
		Args:  cobra.ExactArgs(1),
		Long: "The password is never taken from the command line, where it would appear in\n" +
			"the process table, the shell history and the audit log.",
		Example: "  ratline user password set alice --stdin < password.txt",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.userManager(cmd.Context())
			if err != nil {
				return err
			}
			var password string
			switch {
			case fromStdin:
				b, err := io.ReadAll(io.LimitReader(g.Stdin, 4096))
				if err != nil {
					return rlerr.Wrap(err, rlerr.CodeUsage, "reading the password from stdin")
				}
				password = strings.TrimRight(string(b), "\r\n")
			case g.CanPrompt():
				if password, err = g.readSecret("New password: "); err != nil {
					return err
				}
				confirm, err := g.readSecret("Repeat: ")
				if err != nil {
					return err
				}
				if password != confirm {
					return rlerr.Usagef("the two passwords do not match")
				}
			default:
				return rlerr.InputRequiredf("no password was supplied").
					WithHint("pass --stdin and pipe the password in")
			}
			if err := mgr.SetPassword(cmd.Context(), args[0], password); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"user": args[0], "password_set": true})
			}
			g.Printf("Password set for %s\n", args[0])
			return nil
		},
	}
	set.Flags().BoolVar(&fromStdin, "stdin", false, "Read the password from stdin")
	cmd.AddCommand(Mutating(set))
	return cmd
}

// newUserKeyAliasCommand keeps `ratline user key …` working as documented, by
// forwarding to the key command with --scope user pre-set.
func newUserKeyAliasCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "key",
		Short:  "Manage a user's SSH keys (an alias for 'ratline key … --scope user')",
		Hidden: true,
	}
	cmd.AddCommand(
		aliasToKeyCommand(g, "add"),
		aliasToKeyCommand(g, "list"),
		aliasToKeyCommand(g, "remove"),
	)
	return cmd
}

func aliasToKeyCommand(g *Globals, verb string) *cobra.Command {
	return &cobra.Command{
		Use:                verb + " <username> [flags]",
		Short:              "Alias for 'ratline key " + verb + " --scope user --user <username>'",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			forwarded := append([]string{"key", verb, "--scope", "user", "--user", args[0]}, args[1:]...)
			g.Log.Debug("forwarding a compatibility alias", "argv", strings.Join(forwarded, " "))
			root := cmd.Root()
			root.SetArgs(forwarded)
			return root.Execute()
		},
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

var _ = validate.Username
