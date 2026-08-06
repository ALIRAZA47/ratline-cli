package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/mongo"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// `ratline db` provisions MongoDB databases and users.
//
// Two things shape the whole surface. First, ratline provisions inside a MongoDB server
// rather than installing one — a database server is stateful, with backups and a
// replication topology, and a tool that silently apt-gets one has decided something that
// belongs to whoever owns the data. Second, a password is shown once and never stored:
// MongoDB keeps a hash and will not give it back, so a lost password is rotated rather
// than recovered, and there is no credential store here to be stolen.

func newDBCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "db",
		Short:   "Provision MongoDB databases and users",
		GroupID: GroupOps,
		Long: "Creates databases and least-privilege users on a MongoDB server, and writes the\n" +
			"connection string into a site's .env so the application picks it up on restart.\n\n" +
			"ratline does not install MongoDB. It manages what lives inside a server you point\n" +
			"it at — a local mongod or a managed cluster, the only difference being the admin\n" +
			"connection string. That string lives in a file rather than in config.yaml, at\n" +
			"paths.mongo_uri_file, mode 0600: it is the root password for every database on\n" +
			"the server.\n\n" +
			"    ratline db connect\n\n" +
			"That prompts for the string — not echoed, never in argv, never in your shell\n" +
			"history — creates the directory, writes the file, turns provisioning on, and\n" +
			"proves the credentials work before keeping any of it.\n\n" +
			"Every role ratline grants is scoped to a single database. The cluster-wide ones —\n" +
			"root, readWriteAnyDatabase — are deliberately not offered: granting one to a\n" +
			"tenant's application hands it every other tenant's data.",
		Example: "  ratline db ping\n" +
			"  ratline db create shop --owner acme --attach shop.example.com\n" +
			"  ratline db user add reports --database shop --role read\n" +
			"  ratline db user password shop_app --attach shop.example.com\n" +
			"  ratline db list --live",
	}
	cmd.AddCommand(
		newDBConnectCommand(g),
		newDBEnableCommand(g),
		newDBDisableCommand(g),
		newDBPingCommand(g),
		newDBCreateCommand(g),
		newDBListCommand(g),
		newDBShowCommand(g),
		newDBDropCommand(g),
		newDBUserCommand(g),
		newDBRolesCommand(g),
	)
	return cmd
}

// dbManager builds the MongoDB manager, refusing early when the feature is off.
//
// The flag is checked here rather than by hiding the command: an operator who types
// `ratline db create` on a server where it is off deserves to be told which setting to
// change, not "unknown command".
func (g *Globals) dbManager(ctx context.Context) (*mongo.Manager, *state.Store, error) {
	if !g.Cfg.Features.DBProvisioning {
		return nil, nil, rlerr.Preconditionf("database provisioning is turned off").
			WithHint("set features.db_provisioning: true in %s, then point "+
				"paths.mongo_uri_file at a MongoDB admin connection string", g.Cfg.SourcePath)
	}
	st, err := g.Store(ctx)
	if err != nil {
		return nil, nil, err
	}
	return &mongo.Manager{
		Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, Bins: g.Bins, State: st, DryRun: g.DryRun,
	}, st, nil
}

func newDBPingCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ping",
		Short: "Check that the MongoDB server is reachable and enforcing authentication",
		Args:  cobra.NoArgs,
		Long: "Connects with the configured admin credentials and reports the server's version\n" +
			"and topology.\n\n" +
			"It also reports whether the server enforces authentication, which is worth knowing:\n" +
			"a mongod started without it answers every command from anyone who can reach the\n" +
			"port, so the users ratline creates would be decoration.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, _, err := g.dbManager(cmd.Context())
			if err != nil {
				return err
			}
			info, err := mgr.Ping(cmd.Context())
			if err != nil {
				return err
			}
			uri, _ := mgr.AdminURI()
			if g.JSON {
				return g.EmitJSON(map[string]any{
					"reachable": true, "version": info.Version,
					"topology": info.Topology, "auth_enabled": info.AuthEnabled,
					"server": mongo.Redact(uri),
				})
			}
			if err := g.Fields(
				[2]string{"server", mongo.Redact(uri)},
				[2]string{"version", orDash(info.Version)},
				[2]string{"topology", orDash(info.Topology)},
				[2]string{"authentication", yesNo(info.AuthEnabled)},
			); err != nil {
				return err
			}
			if !info.AuthEnabled {
				g.Log.Warn("this MongoDB server does not appear to enforce authentication",
					"note", "any process that can reach the port has full access, and the users "+
						"ratline creates would not restrict anything")
			}
			return nil
		},
	}
	return NonRoot(cmd)
}

func newDBCreateCommand(g *Globals) *cobra.Command {
	var (
		owner      string
		username   string
		role       string
		attach     string
		envKey     string
		collection string
		noUser     bool
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a database, a user scoped to it, and optionally attach it to a site",
		Args:  cobra.ExactArgs(1),
		Long: "Creates the database, creates a user whose only role is on that database, and\n" +
			"prints the connection string once.\n\n" +
			"MongoDB has no createDatabase — a database exists once something is written into\n" +
			"it — so an initial collection is created too. Without it a new database is\n" +
			"invisible to 'db list' until the application writes, which reads as the create\n" +
			"having silently failed.\n\n" +
			"With --attach the connection string is written into that site's .env instead of\n" +
			"being printed, which keeps it out of your shell history and the terminal\n" +
			"scrollback. An application reads its environment at startup, so the variable\n" +
			"takes effect on the next deploy or restart.",
		Example: "  ratline db create shop --owner acme\n" +
			"  ratline db create shop --owner acme --attach shop.example.com\n" +
			"  ratline db create analytics --owner acme --role dbOwner\n" +
			"  ratline db create legacy --owner acme --no-user   # adopt an existing schema",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := validate.DatabaseName(name); err != nil {
				return err
			}
			mgr, st, err := g.dbManager(cmd.Context())
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			// The owner is a real tenant, so that `user delete --purge` can find the
			// databases to revoke. Without it a database outlives the account it was for.
			if owner == "" {
				return rlerr.Usagef("--owner is required").
					WithHint("it names the tenant this database belongs to, which is how " +
						"'user delete --purge' knows what to revoke")
			}
			if _, err := st.GetUser(ctx, owner); err != nil {
				return err
			}

			if role == "" {
				role = g.Cfg.Databases.MongoDB.DefaultRole
			}
			if err := validate.DatabaseRole(role); err != nil {
				return err
			}
			if username == "" {
				username = mongo.DefaultUsername(name)
			}
			if err := validate.DatabaseUsername(username); err != nil {
				return err
			}
			if collection == "" {
				collection = g.Cfg.Databases.MongoDB.InitialCollection
			}

			// --attach needs a credential to write, and --no-user is the instruction not
			// to create one. Accepting both silently dropped the attach, which is how an
			// operator comes to believe a site was given a connection string it never
			// received — and then debugs the application instead of the provisioning.
			if attach != "" && noUser {
				return rlerr.Usagef("--attach and --no-user contradict each other").
					WithHint("--attach writes a user's connection string into %s, and "+
						"--no-user means there is no user to write. Drop one of them", attach)
			}

			// Idempotence, the same as everywhere else: an existing row with the same
			// owner is reported rather than failed.
			if existing, gerr := st.GetDatabase(ctx, name); gerr == nil {
				if existing.Owner != owner {
					return rlerr.Preconditionf("%s already exists and belongs to %s", name, existing.Owner).
						WithHint("pick another name, or 'ratline db drop %s' first", name)
				}
				g.Printf("%s already exists, owned by %s.\n\n", name, owner)
				return g.dbShow(ctx, mgr, st, name, false)
			}

			if g.DryRun {
				g.Log.Info("would create the database", "name", name, "owner", owner,
					"user", username, "role", role)
				return nil
			}

			rb := system.NewRollback(g.Log)
			defer rb.UnwindOn(ctx, &err)

			if _, err = mgr.CreateDatabase(ctx, name, collection); err != nil {
				return err
			}
			rb.Push("created the database "+name, func(ctx context.Context) error {
				return mgr.DropDatabase(ctx, name)
			})

			var password, uri string
			if !noUser {
				if password, err = mgr.CreateUser(ctx, name, username, role, ""); err != nil {
					return err
				}
				rb.Push("created the user "+username, func(ctx context.Context) error {
					return mgr.DropUser(ctx, name, username)
				})
				adminURI, aerr := mgr.AdminURI()
				if aerr != nil {
					return aerr
				}
				if uri, err = mgr.ConnectionURI(adminURI, name, username, password); err != nil {
					return err
				}
			}

			if err = st.PutDatabase(ctx, &state.Database{
				Name: name, Owner: owner, Server: mongo.Redact(adminURIOrEmpty(mgr)),
				CreatedBy: g.Invoked(),
			}); err != nil {
				return err
			}
			if !noUser {
				if err = st.PutDatabaseUser(ctx, &state.DatabaseUser{
					Username: username, AuthDB: name, Database: name,
					Role: role, CreatedBy: g.Invoked(),
				}); err != nil {
					return err
				}
			}

			// Attaching writes the URI into the site's .env, which keeps the password out
			// of the terminal entirely.
			var attached string
			if attach != "" {
				if attached, err = g.dbAttach(ctx, st, attach, username, name, envKey, uri); err != nil {
					return err
				}
			}
			rb.Commit()

			if g.JSON {
				out := map[string]any{
					"database": name, "owner": owner, "created": true,
					"user": username, "role": role,
				}
				if attached != "" {
					out["attached_to"] = attach
					out["env_key"] = attached
				} else if uri != "" {
					// Only when it was not written to a file: a caller asking for
					// machine-readable output is provisioning, and has nowhere else to
					// get the password from.
					out["connection_uri"] = uri
				}
				return g.EmitJSON(out)
			}

			g.Printf("Created %s, owned by %s.\n\n", name, owner)
			pairs := [][2]string{
				{"database", name},
				{"user", username},
				{"role", role},
			}
			if attached != "" {
				pairs = append(pairs, [2]string{"written to", attach + " (" + attached + ")"})
			}
			if err = g.Fields(pairs...); err != nil {
				return err
			}
			if attached != "" {
				g.Printf("\nThe application picks it up on its next start:\n"+
					"    ratline site restart %s\n", attach)
				return nil
			}
			if uri != "" {
				g.Printf("\nThe connection string, shown once — MongoDB stores a hash, so this\n" +
					"cannot be displayed again. Rotate it with 'ratline db user password' if lost.\n\n")
				g.Printf("    %s\n", uri)
				g.Printf("\nTo hand it to a site instead of copying it by hand:\n"+
					"    ratline db user password %s --attach <domain>\n", username)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&owner, "owner", "", "Tenant that owns this database (required)")
	f.StringVar(&username, "user", "", "Username to create (default: <database>_app)")
	f.StringVar(&role, "role", "", "Role on this database (default: databases.mongodb.default_role)")
	f.StringVar(&attach, "attach", "", "Write the connection string into this site's .env")
	f.StringVar(&envKey, "env-key", "", "Variable name for --attach (default: databases.mongodb.env_key)")
	f.StringVar(&collection, "collection", "", "Initial collection, so the database is visible")
	f.BoolVar(&noUser, "no-user", false, "Create the database only, without a user")
	_ = cmd.RegisterFlagCompletionFunc("owner", g.completeUsers)
	_ = cmd.RegisterFlagCompletionFunc("attach", g.completeDomains)
	_ = cmd.RegisterFlagCompletionFunc("role", completeDBRoles)
	Required(cmd, "owner")
	return Mutating(cmd)
}

func newDBListCommand(g *Globals) *cobra.Command {
	var (
		owner string
		live  bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List databases ratline provisioned, or everything on the server",
		Args:  cobra.NoArgs,
		Long: "By default this lists what ratline recorded, with the tenant that owns each.\n\n" +
			"--live asks the server instead and marks anything it does not recognise. That\n" +
			"difference is the useful part: a database on the server with no row was created\n" +
			"outside ratline, and nothing will revoke its users when the tenant is deleted.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, st, err := g.dbManager(cmd.Context())
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			recorded, err := st.ListDatabases(ctx, owner)
			if err != nil {
				return err
			}
			known := map[string]*state.Database{}
			for _, d := range recorded {
				known[d.Name] = d
			}

			if !live {
				if g.JSON {
					return g.EmitJSON(map[string]any{"databases": recorded})
				}
				if len(recorded) == 0 {
					g.Println("No databases. Create one with 'ratline db create <name> --owner <tenant>'.")
					return nil
				}
				tbl := g.Table("database", "owner", "users", "created")
				for _, d := range recorded {
					tbl.Row(d.Name, d.Owner, fmt.Sprint(len(d.Users)),
						d.CreatedAt.Format("2006-01-02"))
				}
				return tbl.Render()
			}

			onServer, err := mgr.LiveDatabases(ctx)
			if err != nil {
				return err
			}
			type row struct {
				Name      string `json:"name"`
				Owner     string `json:"owner,omitempty"`
				Managed   bool   `json:"managed"`
				SizeBytes int64  `json:"size_bytes"`
				Users     int    `json:"users"`
			}
			var rows []row
			for _, d := range onServer {
				// Only MongoDB's own three are skipped. Filtering on whether ratline
				// *would* create the name hid the databases this command exists to
				// surface: one created outside ratline — by another tool, or by hand,
				// with a name ratline would have refused — is precisely the case worth
				// knowing about, because nothing will revoke its users when the tenant
				// goes. Hiding it made `--live` agree with the index it was meant to be
				// checked against.
				if validate.IsMongoSystemDatabase(d.Name) {
					continue
				}
				r := row{Name: d.Name, SizeBytes: d.SizeOnDisk}
				if rec, ok := known[d.Name]; ok {
					r.Managed, r.Owner, r.Users = true, rec.Owner, len(rec.Users)
				}
				rows = append(rows, r)
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"databases": rows, "source": "server"})
			}
			tbl := g.Table("database", "owner", "size", "users", "managed")
			for _, r := range rows {
				shown, managed := r.Owner, "no"
				if r.Managed {
					managed = "yes"
				} else {
					shown = "—"
				}
				tbl.Row(r.Name, shown, humanBytes(r.SizeBytes), fmt.Sprint(r.Users), managed)
			}
			if err := tbl.Render(); err != nil {
				return err
			}
			var unmanaged int
			for _, r := range rows {
				if !r.Managed {
					unmanaged++
				}
			}
			if unmanaged > 0 {
				g.Printf("\n%s on the server but not recorded here.\n"+
					"Nothing will revoke their users when a tenant is deleted.\n",
					plural(unmanaged, "database"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&owner, "owner", "", "Only this tenant's databases")
	cmd.Flags().BoolVar(&live, "live", false, "Ask the server rather than reading ratline's index")
	_ = cmd.RegisterFlagCompletionFunc("owner", g.completeUsers)
	return NonRoot(cmd)
}

func newDBShowCommand(g *Globals) *cobra.Command {
	var live bool
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a database, its users and what it holds",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, st, err := g.dbManager(cmd.Context())
			if err != nil {
				return err
			}
			return g.dbShow(cmd.Context(), mgr, st, args[0], live)
		},
		ValidArgsFunction: g.completeDatabases,
	}
	cmd.Flags().BoolVar(&live, "live", true, "Also ask the server for its statistics and users")
	return NonRoot(cmd)
}

// dbShow prints one database. Shared with `db create`, so what you see after creating
// something is what you see inspecting it later.
func (g *Globals) dbShow(ctx context.Context, mgr *mongo.Manager, st *state.Store, name string, live bool) error {
	d, err := st.GetDatabase(ctx, name)
	if err != nil {
		return err
	}
	var stats *mongo.Stats
	var liveUsers []mongo.LiveUser
	if live {
		// A warning rather than an error: the row is real and worth showing even when the
		// server is unreachable, and that is exactly when someone looks.
		if stats, err = mgr.Stats(ctx, name); err != nil {
			g.Log.Warn("could not read the database statistics", "database", name, "err", err)
		}
		if liveUsers, err = mgr.LiveUsers(ctx, name); err != nil {
			g.Log.Debug("could not list the users on the server", "database", name, "err", err)
		}
	}

	if g.JSON {
		out := map[string]any{"database": d}
		if stats != nil {
			out["stats"] = stats
		}
		if liveUsers != nil {
			out["server_users"] = liveUsers
		}
		return g.EmitJSON(out)
	}

	pairs := [][2]string{
		{"database", d.Name},
		{"owner", d.Owner},
		{"server", orDash(d.Server)},
		{"created", d.CreatedAt.Format(time.RFC3339)},
	}
	if stats != nil {
		pairs = append(pairs,
			[2]string{"collections", fmt.Sprint(stats.Collections)},
			[2]string{"documents", fmt.Sprint(stats.Objects)},
			[2]string{"data", humanBytes(stats.DataSize)},
			[2]string{"indexes", fmt.Sprint(stats.Indexes) + " (" + humanBytes(stats.IndexSize) + ")"},
		)
	}
	if err := g.Fields(pairs...); err != nil {
		return err
	}

	if len(d.Users) > 0 {
		g.Println()
		tbl := g.Table("user", "role", "attached to", "rotated")
		for _, u := range d.Users {
			var to []string
			for _, a := range u.Attachments {
				to = append(to, a.Domain+":"+a.EnvKey)
			}
			rotated := "never"
			if !u.RotatedAt.IsZero() {
				rotated = u.RotatedAt.Format("2006-01-02")
			}
			tbl.Row(u.Username, u.Role, orDash(strings.Join(to, " ")), rotated)
		}
		if err := tbl.Render(); err != nil {
			return err
		}
	}

	// A user on the server that ratline did not create will not be revoked with the
	// tenant, so it is worth naming rather than leaving to a later audit.
	if len(liveUsers) > 0 {
		recorded := map[string]bool{}
		for _, u := range d.Users {
			recorded[u.Username] = true
		}
		var extra []string
		for _, u := range liveUsers {
			if !recorded[u.Username] {
				extra = append(extra, u.Username)
			}
		}
		if len(extra) > 0 {
			g.Printf("\nOn the server but not recorded here: %s\n"+
				"Nothing will revoke these when the tenant is deleted.\n",
				strings.Join(extra, ", "))
		}
	}
	return nil
}

func newDBDropCommand(g *Globals) *cobra.Command {
	var (
		force  bool
		keepDB bool
	)
	cmd := &cobra.Command{
		Use:   "drop <name>",
		Short: "Drop a database and its users",
		Args:  cobra.ExactArgs(1),
		Long: "Drops the database, every collection in it, and every user ratline created for\n" +
			"it. This destroys data and cannot be undone, so it asks first and needs the\n" +
			"database's name typed back.\n\n" +
			"--keep-database removes the users and ratline's record but leaves the data, which\n" +
			"is what you want when handing a database over to someone else's tooling.",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			mgr, st, err := g.dbManager(cmd.Context())
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			d, err := st.GetDatabase(ctx, name)
			if err != nil {
				return err
			}

			var stats *mongo.Stats
			if !keepDB {
				stats, _ = mgr.Stats(ctx, name)
			}
			if !force {
				// The document count is the number that makes someone stop. "Are you
				// sure?" does not.
				what := "database " + name
				if stats != nil && stats.Objects > 0 {
					what = fmt.Sprintf("database %s and its %d document(s) in %d collection(s)",
						name, stats.Objects, stats.Collections)
				}
				if keepDB {
					what = fmt.Sprintf("%s of %s, leaving the data",
						plural(len(d.Users), "user"), name)
				}
				g.Printf("This will permanently remove the %s.\n", what)
				if err := g.ConfirmTyped(name, "Type the database name to confirm:"); err != nil {
					return err
				}
			}
			if g.DryRun {
				g.Log.Info("would drop the database", "name", name, "keep_data", keepDB)
				return nil
			}

			// Users first: dropping the database does not remove them, and a user left
			// behind still authenticates and still holds a role on a database that
			// springs back into existence the moment anything writes through it.
			for _, u := range d.Users {
				if derr := mgr.DropUser(ctx, u.AuthDB, u.Username); derr != nil {
					// Reported and carried on: a user already removed by hand must not
					// stop the rest of the teardown.
					g.Log.Warn("could not remove a database user", "user", u.Username, "err", derr)
				}
				for _, a := range u.Attachments {
					if aerr := st.DeleteDatabaseAttachment(ctx, a.Domain, a.EnvKey); aerr != nil {
						g.Log.Debug("could not clear an attachment", "domain", a.Domain, "err", aerr)
					}
					g.Log.Info("a site still has this connection string in its .env",
						"domain", a.Domain, "key", a.EnvKey,
						"fix", "ratline site env unset "+a.Domain+" "+a.EnvKey)
				}
				if err := st.DeleteDatabaseUser(ctx, u.Username, u.AuthDB); err != nil {
					return err
				}
			}
			if !keepDB {
				if err := mgr.DropDatabase(ctx, name); err != nil {
					return err
				}
			}
			if err := st.DeleteDatabase(ctx, name); err != nil {
				return err
			}

			if g.JSON {
				return g.EmitJSON(map[string]any{
					"database": name, "dropped": true, "kept_data": keepDB,
					"users_removed": len(d.Users),
				})
			}
			if keepDB {
				g.Printf("Removed %s and ratline's record of %s. The data is still there.\n",
					plural(len(d.Users), "user"), name)
			} else {
				g.Printf("Dropped %s and %s.\n", name, plural(len(d.Users), "user"))
			}
			return nil
		},
		ValidArgsFunction: g.completeDatabases,
	}
	cmd.Flags().BoolVar(&force, "force", false, "Skip the confirmation")
	cmd.Flags().BoolVar(&keepDB, "keep-database", false, "Remove the users but leave the data")
	return Mutating(cmd)
}

func newDBRolesCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "roles",
		Short: "List the roles ratline will grant, and what each allows",
		Args:  cobra.NoArgs,
		Long: "Every one of these is scoped to a single database.\n\n" +
			"The cluster-wide roles — root, readWriteAnyDatabase, userAdminAnyDatabase — are\n" +
			"deliberately absent. Granting one to a tenant's application would give it every\n" +
			"other tenant's data, which is the thing ratline exists to prevent, and it would\n" +
			"be one flag away if the list were open.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			roles := validate.DatabaseRoles()
			if g.JSON {
				out := make([]map[string]string, 0, len(roles))
				for _, r := range roles {
					out = append(out, map[string]string{"role": r[0], "allows": r[1]})
				}
				return g.EmitJSON(map[string]any{"roles": out,
					"default": g.Cfg.Databases.MongoDB.DefaultRole})
			}
			tbl := g.Table("role", "allows")
			for _, r := range roles {
				name := r[0]
				if name == g.Cfg.Databases.MongoDB.DefaultRole {
					name += " (default)"
				}
				tbl.Row(name, r[1])
			}
			return tbl.Render()
		},
	}
	return NonRoot(cmd)
}

func completeDBRoles(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	var out []string
	for _, r := range validate.DatabaseRoles() {
		out = append(out, r[0]+"\t"+r[1])
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeDatabases completes database names from ratline's index.
func (g *Globals) completeDatabases(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	st, err := g.Store(cmd.Context())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	dbs, err := st.ListDatabases(cmd.Context(), "")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, d := range dbs {
		out = append(out, d.Name+"\t"+d.Owner)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// adminURIOrEmpty is used only for the redacted form recorded in state; a failure here
// has already been reported by the operation that needed the real one.
func adminURIOrEmpty(m *mongo.Manager) string {
	uri, err := m.AdminURI()
	if err != nil {
		return ""
	}
	return uri
}

// humanBytes formats a size for a table.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, u := range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, u)
		}
	}
	return fmt.Sprintf("%.1f PB", value/unit)
}
