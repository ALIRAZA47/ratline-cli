package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// `ratline db user` manages the credentials applications actually connect with.
//
// A password is generated, shown once, and never stored. MongoDB keeps a hash and will
// not return it, so ratline could not display it later even if it wanted to — which is
// the right shape rather than a limitation: there is no credential store here to be
// stolen, and a lost password is rotated rather than recovered.

func newDBUserCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Add, inspect, re-role and remove MongoDB users",
		Long: "Users are scoped to one database. A password is shown once — MongoDB stores a\n" +
			"hash, so nothing can print it again — and --attach writes it straight into a\n" +
			"site's .env instead, which keeps it out of your shell history entirely.",
	}
	cmd.AddCommand(
		newDBUserAddCommand(g),
		newDBUserListCommand(g),
		newDBUserPasswordCommand(g),
		newDBUserGrantCommand(g),
		newDBUserDeleteCommand(g),
	)
	return cmd
}

func newDBUserAddCommand(g *Globals) *cobra.Command {
	var (
		database string
		role     string
		attach   string
		envKey   string
	)
	cmd := &cobra.Command{
		Use:   "add <username>",
		Short: "Create a MongoDB user scoped to one database",
		Args:  cobra.ExactArgs(1),
		Long: "Creates a user whose only role is on the named database, and prints the\n" +
			"connection string once.\n\n" +
			"A second user on the same database is the way to give something narrower access\n" +
			"than the application has: a reporting job with 'read', a migration tool with\n" +
			"'dbAdmin', each with its own credential that can be revoked on its own.",
		Example: "  ratline db user add reports --database shop --role read\n" +
			"  ratline db user add worker --database shop --attach worker.example.com",
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]
			if err := validate.DatabaseUsername(username); err != nil {
				return err
			}
			if database == "" {
				return rlerr.Usagef("--database is required").
					WithHint("a user is scoped to one database; that is what makes it least-privilege")
			}
			mgr, st, err := g.dbManager(cmd.Context())
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if _, err := st.GetDatabase(ctx, database); err != nil {
				return err
			}
			if role == "" {
				role = g.Cfg.Databases.MongoDB.DefaultRole
			}
			if err := validate.DatabaseRole(role); err != nil {
				return err
			}
			if existing, gerr := st.GetDatabaseUser(ctx, username, database); gerr == nil {
				return rlerr.Preconditionf("%s already exists in %s with the role %s",
					username, database, existing.Role).
					WithHint("change its password with 'ratline db user password %s', or its "+
						"role with 'ratline db user grant %s --role …'", username, username)
			}
			if g.DryRun {
				g.Log.Info("would create the database user", "user", username,
					"database", database, "role", role)
				return nil
			}

			password, err := mgr.CreateUser(ctx, database, username, role, "")
			if err != nil {
				return err
			}
			adminURI, err := mgr.AdminURI()
			if err != nil {
				return err
			}
			uri, err := mgr.ConnectionURI(adminURI, database, username, password)
			if err != nil {
				return err
			}
			if err := st.PutDatabaseUser(ctx, &state.DatabaseUser{
				Username: username, AuthDB: database, Database: database,
				Role: role, CreatedBy: g.Invoked(),
			}); err != nil {
				return err
			}

			var attached string
			if attach != "" {
				if attached, err = g.dbAttach(ctx, st, attach, username, database, envKey, uri); err != nil {
					return err
				}
			}
			return g.dbPrintCredential(dbCredential{
				Verb: "Created", Username: username, Database: database, Role: role,
				URI: uri, AttachedTo: attach, EnvKey: attached,
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&database, "database", "", "Database this user has a role on (required)")
	f.StringVar(&role, "role", "", "Role to grant (default: databases.mongodb.default_role)")
	f.StringVar(&attach, "attach", "", "Write the connection string into this site's .env")
	f.StringVar(&envKey, "env-key", "", "Variable name for --attach")
	_ = cmd.RegisterFlagCompletionFunc("database", g.completeDatabases)
	_ = cmd.RegisterFlagCompletionFunc("role", completeDBRoles)
	_ = cmd.RegisterFlagCompletionFunc("attach", g.completeDomains)
	return Mutating(cmd)
}

func newDBUserListCommand(g *Globals) *cobra.Command {
	var (
		database string
		live     bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List MongoDB users",
		Args:  cobra.NoArgs,
		Long: "By default this lists the users ratline created, with the sites holding their\n" +
			"credentials.\n\n" +
			"--live asks the server, which needs --database: MongoDB stores users per database\n" +
			"and there is no server-wide listing that does not require reading the admin\n" +
			"database directly. Anything the server has that ratline does not is worth\n" +
			"knowing — it will survive a tenant deletion.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, st, err := g.dbManager(cmd.Context())
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			if live {
				if database == "" {
					return rlerr.Usagef("--live needs --database").
						WithHint("MongoDB keeps users per database, so there is nothing " +
							"server-wide to list")
				}
				users, err := mgr.LiveUsers(ctx, database)
				if err != nil {
					return err
				}
				recorded := map[string]bool{}
				if known, err := st.ListDatabaseUsers(ctx, database); err == nil {
					for _, u := range known {
						recorded[u.Username] = true
					}
				}
				if g.JSON {
					return g.EmitJSON(map[string]any{"users": users, "source": "server"})
				}
				if len(users) == 0 {
					g.Printf("The server reports no users in %s.\n", database)
					return nil
				}
				tbl := g.Table("user", "auth db", "roles", "managed")
				for _, u := range users {
					tbl.Row(u.Username, u.AuthDB, strings.Join(u.Roles, " "), yesNo(recorded[u.Username]))
				}
				return tbl.Render()
			}

			users, err := st.ListDatabaseUsers(ctx, database)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"users": users})
			}
			if len(users) == 0 {
				g.Println("No database users recorded.")
				return nil
			}
			tbl := g.Table("user", "database", "role", "attached to", "rotated")
			for _, u := range users {
				var to []string
				for _, a := range u.Attachments {
					to = append(to, a.Domain+":"+a.EnvKey)
				}
				rotated := "never"
				if !u.RotatedAt.IsZero() {
					rotated = u.RotatedAt.Format("2006-01-02")
				}
				tbl.Row(u.Username, u.Database, u.Role, orDash(strings.Join(to, " ")), rotated)
			}
			return tbl.Render()
		},
	}
	cmd.Flags().StringVar(&database, "database", "", "Only users of this database")
	cmd.Flags().BoolVar(&live, "live", false, "Ask the server rather than reading ratline's index")
	_ = cmd.RegisterFlagCompletionFunc("database", g.completeDatabases)
	return NonRoot(cmd)
}

func newDBUserPasswordCommand(g *Globals) *cobra.Command {
	var (
		authDB string
		attach string
		envKey string
		all    bool
	)
	cmd := &cobra.Command{
		Use:   "password <username>",
		Short: "Rotate a user's password",
		Args:  cobra.ExactArgs(1),
		Long: "Generates a new password, sets it on the server, and prints it once.\n\n" +
			"The old password stops working immediately, so anything still using it fails\n" +
			"until it gets the new one. --all-sites updates every site recorded as holding this\n" +
			"user's credentials, which is usually what you want and is the difference between\n" +
			"a rotation and an outage. The applications still need restarting: an environment\n" +
			"variable is read at startup.",
		Example: "  ratline db user password shop_app --all-sites\n" +
			"  ratline db user password shop_app --attach shop.example.com",
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]
			mgr, st, err := g.dbManager(cmd.Context())
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			u, err := st.GetDatabaseUser(ctx, username, authDB)
			if err != nil {
				return err
			}
			if g.DryRun {
				g.Log.Info("would rotate the password", "user", username, "auth_db", u.AuthDB)
				return nil
			}

			password, err := mgr.SetPassword(ctx, u.AuthDB, username, "")
			if err != nil {
				return err
			}
			adminURI, err := mgr.AdminURI()
			if err != nil {
				return err
			}
			uri, err := mgr.ConnectionURI(adminURI, u.Database, username, password)
			if err != nil {
				return err
			}
			u.RotatedAt = time.Now().UTC()
			if err := st.PutDatabaseUser(ctx, u); err != nil {
				return err
			}

			// Every site already holding this credential, so a rotation does not silently
			// break the applications using it.
			var updated []string
			if all {
				for _, a := range u.Attachments {
					key, aerr := g.dbAttach(ctx, st, a.Domain, username, u.Database, a.EnvKey, uri)
					if aerr != nil {
						// Named rather than swallowed: the password has already changed on
						// the server, so a site that could not be updated is now broken and
						// the operator has to know which one.
						g.Log.Error("could not update a site's .env after rotating the password",
							"domain", a.Domain, "key", a.EnvKey, "err", aerr,
							"note", "the old password no longer works, so this site is down "+
								"until its .env is corrected")
						continue
					}
					updated = append(updated, a.Domain+" ("+key+")")
				}
			}
			var attached string
			if attach != "" {
				if attached, err = g.dbAttach(ctx, st, attach, username, u.Database, envKey, uri); err != nil {
					return err
				}
				updated = append(updated, attach+" ("+attached+")")
			}

			if g.JSON {
				out := map[string]any{
					"user": username, "database": u.Database, "rotated": true,
					"sites_updated": updated,
				}
				if len(updated) == 0 {
					out["connection_uri"] = uri
				}
				return g.EmitJSON(out)
			}
			g.Printf("Rotated the password for %s.\n", username)
			if len(updated) > 0 {
				g.Printf("\nUpdated: %s\n", strings.Join(updated, ", "))
				g.Printf("\nEach application reads its environment at startup, so restart them:\n")
				for _, a := range u.Attachments {
					g.Printf("    ratline site restart %s\n", a.Domain)
				}
				return nil
			}
			if len(u.Attachments) > 0 {
				g.Printf("\n%s still hold the old password and will fail until updated.\n"+
					"Re-run with --all-sites to fix them:\n", plural(len(u.Attachments), "site"))
				for _, a := range u.Attachments {
					g.Printf("    %s (%s)\n", a.Domain, a.EnvKey)
				}
			}
			g.Printf("\nThe new connection string, shown once:\n\n    %s\n", uri)
			return nil
		},
		ValidArgsFunction: g.completeDatabaseUsers,
	}
	f := cmd.Flags()
	f.StringVar(&authDB, "auth-db", "", "Authentication database, when the username is ambiguous")
	f.StringVar(&attach, "attach", "", "Also write the new string into this site's .env")
	f.StringVar(&envKey, "env-key", "", "Variable name for --attach")
	f.BoolVar(&all, "all-sites", false, "Update every site already holding this credential")
	_ = cmd.RegisterFlagCompletionFunc("attach", g.completeDomains)
	return Mutating(cmd)
}

func newDBUserGrantCommand(g *Globals) *cobra.Command {
	var (
		role   string
		authDB string
	)
	cmd := &cobra.Command{
		Use:   "grant <username>",
		Short: "Change a user's role on its database",
		Args:  cobra.ExactArgs(1),
		Long: "Replaces the user's roles with exactly the one named.\n\n" +
			"Replaced rather than added to, deliberately: 'grant' means \"this user should have\n" +
			"exactly this access\", and accumulating roles quietly is how a read-only user ends\n" +
			"up able to write.",
		Example: "  ratline db user grant reports --role read\n" +
			"  ratline db user grant shop_app --role readWrite",
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]
			if role == "" {
				return rlerr.Usagef("--role is required").
					WithHint("'ratline db roles' lists what can be granted")
			}
			if err := validate.DatabaseRole(role); err != nil {
				return err
			}
			mgr, st, err := g.dbManager(cmd.Context())
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			u, err := st.GetDatabaseUser(ctx, username, authDB)
			if err != nil {
				return err
			}
			if u.Role == role {
				g.Printf("%s already has the role %s on %s.\n", username, role, u.Database)
				return nil
			}
			if g.DryRun {
				g.Log.Info("would change the role", "user", username, "from", u.Role, "to", role)
				return nil
			}
			if err := mgr.SetRole(ctx, u.AuthDB, username, role); err != nil {
				return err
			}
			previous := u.Role
			u.Role = role
			if err := st.PutDatabaseUser(ctx, u); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{
					"user": username, "database": u.Database, "from": previous, "to": role,
				})
			}
			g.Printf("%s now has %s on %s, was %s.\n", username, role, u.Database, previous)
			// The credential is unchanged, so nothing needs redeploying — worth saying,
			// because the instinct after a permission change is to restart everything.
			g.Printf("\nThe password is unchanged, so nothing needs restarting.\n")
			return nil
		},
		ValidArgsFunction: g.completeDatabaseUsers,
	}
	cmd.Flags().StringVar(&role, "role", "", "Role to grant, replacing any existing (required)")
	cmd.Flags().StringVar(&authDB, "auth-db", "", "Authentication database, when the username is ambiguous")
	_ = cmd.RegisterFlagCompletionFunc("role", completeDBRoles)
	return Mutating(cmd)
}

func newDBUserDeleteCommand(g *Globals) *cobra.Command {
	var (
		authDB string
		force  bool
	)
	cmd := &cobra.Command{
		Use:     "delete <username>",
		Aliases: []string{"remove", "rm"},
		Short:   "Remove a MongoDB user",
		Args:    cobra.ExactArgs(1),
		Long: "Removes the user from the server. Its data is untouched — a user is a credential,\n" +
			"not a container.\n\n" +
			"If any site holds this user's connection string, that is named before anything\n" +
			"happens: removing the user takes the site's database access with it.",
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]
			mgr, st, err := g.dbManager(cmd.Context())
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			u, err := st.GetDatabaseUser(ctx, username, authDB)
			if err != nil {
				return err
			}

			if len(u.Attachments) > 0 && !force {
				g.Printf("%s is used by:\n", username)
				for _, a := range u.Attachments {
					g.Printf("    %s (%s)\n", a.Domain, a.EnvKey)
				}
				g.Printf("\nRemoving it will stop %s connecting to %s.\n",
					plural(len(u.Attachments), "site"), u.Database)
				ok, cerr := g.Confirm("Remove it anyway?")
				if cerr != nil {
					return cerr
				}
				if !ok {
					return ErrCancelled
				}
			}
			if g.DryRun {
				g.Log.Info("would remove the database user", "user", username, "auth_db", u.AuthDB)
				return nil
			}

			if err := mgr.DropUser(ctx, u.AuthDB, username); err != nil {
				return err
			}
			if err := st.DeleteDatabaseUser(ctx, username, u.AuthDB); err != nil {
				return err
			}

			if g.JSON {
				return g.EmitJSON(map[string]any{
					"user": username, "database": u.Database, "removed": true,
					"sites_affected": len(u.Attachments),
				})
			}
			g.Printf("Removed %s from %s. The data is untouched.\n", username, u.Database)
			if len(u.Attachments) > 0 {
				g.Printf("\nThese sites still have the dead credential in their .env:\n")
				for _, a := range u.Attachments {
					g.Printf("    ratline site env unset %s %s\n", a.Domain, a.EnvKey)
				}
			}
			return nil
		},
		ValidArgsFunction: g.completeDatabaseUsers,
	}
	cmd.Flags().StringVar(&authDB, "auth-db", "", "Authentication database, when the username is ambiguous")
	cmd.Flags().BoolVar(&force, "force", false, "Do not ask, even when a site depends on it")
	return Mutating(cmd)
}

// dbAttach writes a connection string into a site's .env and records the attachment.
//
// The URI goes into a 0600 file owned by the tenant rather than onto the terminal, which
// is the whole reason --attach exists: a password in a shell's history or a terminal's
// scrollback outlives every rotation.
func (g *Globals) dbAttach(ctx context.Context, st *state.Store, domain, username, database, envKey, uri string) (string, error) {
	site, err := st.FindSiteByName(ctx, domain)
	if err != nil {
		return "", err
	}
	if envKey == "" {
		envKey = g.Cfg.Databases.MongoDB.EnvKey
	}
	if envKey == "" {
		envKey = "MONGODB_URI"
	}
	// The same validation `site env set` applies, so an unusable name is refused here
	// rather than producing a .env line no shell will source.
	if err := validate.EnvKey(envKey); err != nil {
		return "", err
	}

	env, err := g.openEnvFile(site)
	if err != nil {
		return "", err
	}
	if err := env.set(envKey, uri); err != nil {
		return "", err
	}
	if g.DryRun {
		g.Log.Info("would write the connection string", "domain", domain, "key", envKey)
		return envKey, nil
	}
	if err := env.write(); err != nil {
		return "", err
	}
	if err := st.PutDatabaseAttachment(ctx, &state.DatabaseAttachment{
		Domain: domain, Username: username, AuthDB: database, EnvKey: envKey,
	}); err != nil {
		return "", err
	}
	g.Log.Info("wrote the connection string", "domain", domain, "key", envKey,
		"note", "the value is redacted in logs and in --json output")
	return envKey, nil
}

// dbCredential is what the commands that mint a credential print.
type dbCredential struct {
	Verb       string
	Username   string
	Database   string
	Role       string
	URI        string
	AttachedTo string
	EnvKey     string
}

func (g *Globals) dbPrintCredential(c dbCredential) error {
	if g.JSON {
		out := map[string]any{
			"user": c.Username, "database": c.Database, "role": c.Role, "created": true,
		}
		if c.EnvKey != "" {
			out["attached_to"] = c.AttachedTo
			out["env_key"] = c.EnvKey
		} else {
			out["connection_uri"] = c.URI
		}
		return g.EmitJSON(out)
	}
	g.Printf("%s %s on %s with the role %s.\n\n", c.Verb, c.Username, c.Database, c.Role)
	if c.EnvKey != "" {
		if err := g.Fields(
			[2]string{"written to", c.AttachedTo + " (" + c.EnvKey + ")"},
		); err != nil {
			return err
		}
		g.Printf("\nThe application picks it up on its next start:\n"+
			"    ratline site restart %s\n", c.AttachedTo)
		return nil
	}
	g.Printf("The connection string, shown once — MongoDB stores a hash, so this cannot\n" +
		"be displayed again. Rotate it with 'ratline db user password' if lost.\n\n")
	g.Printf("    %s\n", c.URI)
	return nil
}

// completeDatabaseUsers completes usernames from ratline's index.
func (g *Globals) completeDatabaseUsers(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	st, err := g.Store(cmd.Context())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	users, err := st.ListDatabaseUsers(cmd.Context(), "")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, u := range users {
		out = append(out, u.Username+"\t"+u.Role+" on "+u.Database)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
