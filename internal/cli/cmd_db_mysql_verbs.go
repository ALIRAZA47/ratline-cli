package cli

import (
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/mysql"
	"github.com/ALIRAZA47/ratline-cli/internal/mysqld"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// The MySQL implementations of each db verb, dispatched to from the shared commands. Kept
// terse: the CLI framing (flags, help, --json) lives on the shared command; these do the
// engine-specific work and reuse the common output helpers.

func (g *Globals) mysqlPing(cmd *cobra.Command) error {
	ctx := cmd.Context()
	mgr, _, err := g.mysqlManager(ctx)
	if err != nil {
		return err
	}
	info, err := mgr.Ping(ctx)
	if err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"reachable": true, "version": info.Version, "engine": "mysql"})
	}
	return g.Fields(
		[2]string{"engine", "mysql"},
		[2]string{"version", orDash(info.Version)},
		[2]string{"authentication", yesNo(info.AuthEnabled)},
	)
}

// mysqlInstall is dispatched from `db install` after the shared password read.
func (g *Globals) mysqlInstall(cmd *cobra.Command, adminUser, password string) error {
	ctx := cmd.Context()
	mgr, _, err := g.mysqldManager(ctx)
	if err != nil {
		return err
	}
	if err := validate.MySQLUsername(adminUser); err != nil {
		return rlerr.Wrap(err, rlerr.CodeUsage, "the admin username is not usable")
	}

	if g.DryRun {
		if serr := mgr.Supported(); serr != nil {
			return serr
		}
		d := mgr.DistroForPlan()
		g.Printf("Would install MySQL (%s) on this host:\n", d.Flavour)
		for _, s := range []string{
			"apt-get update && apt-get install -y " + d.Package,
			"enable and start the " + d.Service + " service",
			"create the admin user over the local socket",
			"write " + d.ConfDropIn + " (bind-address 127.0.0.1)",
			"restart and verify it answers the admin credentials, localhost only",
			"store the credentials and turn provisioning on",
		} {
			g.Printf("    - %s\n", s)
		}
		return nil
	}

	res, err := mgr.Install(ctx, mysqld.InstallOptions{AdminUser: adminUser, Password: password})
	if err != nil {
		return err
	}

	attached := false
	alreadyAttached := g.Cfg.Paths.MySQLDefaultsFile != "" && system.Exists(g.Cfg.Paths.MySQLDefaultsFile)
	if !alreadyAttached {
		if _, err := g.storeMySQLAdminAndEnable(ctx, res.Creds); err != nil {
			return err
		}
		attached = true
	}

	if g.JSON {
		return g.EmitJSON(map[string]any{
			"engine": "mysql", "flavour": res.Flavour, "server_version": res.ServerVersion,
			"package_installed": res.PackageInstalled, "admin_user": res.AdminUser,
			"admin_user_created": res.AdminUserCreated, "attached": attached,
		})
	}
	g.Printf("MySQL is installed and secured.\n\n")
	if err := g.Fields(
		[2]string{"server", res.Flavour + " " + orDash(res.ServerVersion)},
		[2]string{"admin user", res.AdminUser + " (password never stored)"},
		[2]string{"listening on", "127.0.0.1 only"},
		[2]string{"config", res.ConfPath + " (managed by ratline)"},
	); err != nil {
		return err
	}
	if !res.PackageInstalled {
		g.Printf("\nThe packages were already installed; they were checked, not reinstalled.\n")
	}
	g.Printf("\nNext:\n" +
		"    ratline db create <name> --engine mysql --owner <tenant>\n" +
		"    ratline db access allow <address> --engine mysql   # only if another machine needs in\n")
	return nil
}

// mysqlConnect is dispatched from `db connect` after the shared secret read. For MySQL the
// operator pastes the admin password (the host is this machine); a full DSN is not needed.
func (g *Globals) mysqlConnect(cmd *cobra.Command, adminUser, password string) error {
	ctx := cmd.Context()
	if err := validate.MySQLUsername(adminUser); err != nil {
		return err
	}
	if password == "" {
		return rlerr.InputRequiredf("no admin password was given")
	}
	creds := mysql.Creds{User: adminUser, Password: password, Host: mysql.DefaultHost, Port: mysqld.Port}
	if g.DryRun {
		g.Log.Info("would store the MySQL admin credentials and turn provisioning on")
		return nil
	}
	info, err := g.storeMySQLAdminAndEnable(ctx, creds)
	if err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"engine": "mysql", "version": info.Version, "provisioning_enabled": true})
	}
	g.Printf("Connected.\n\n")
	if err := g.Fields(
		[2]string{"engine", "mysql"},
		[2]string{"version", orDash(info.Version)},
		[2]string{"stored at", g.Cfg.Paths.MySQLDefaultsFile + " (0600, root-owned)"},
		[2]string{"provisioning", "on"},
	); err != nil {
		return err
	}
	g.Printf("\nNext:\n    ratline db create <name> --engine mysql --owner <tenant>\n")
	return nil
}

func (g *Globals) mysqlCreate(cmd *cobra.Command, name, owner, username, role, attach, envKey string, noUser bool) error {
	ctx := cmd.Context()
	if err := validate.MySQLDatabaseName(name); err != nil {
		return err
	}
	mgr, st, err := g.mysqlManager(ctx)
	if err != nil {
		return err
	}
	if owner == "" {
		return rlerr.Usagef("--owner is required").
			WithHint("it names the tenant this database belongs to")
	}
	if _, err := st.GetUser(ctx, owner); err != nil {
		return err
	}
	if role == "" {
		role = g.Cfg.Databases.MySQL.DefaultRole
	}
	if role == "" {
		role = "readWrite"
	}
	if err := validate.MySQLRole(role); err != nil {
		return err
	}
	if username == "" {
		username = mysql.DefaultUsername(name)
	}
	if err := validate.MySQLUsername(username); err != nil {
		return err
	}
	if attach != "" && noUser {
		return rlerr.Usagef("--attach and --no-user contradict each other")
	}

	if err := mgr.CreateDatabase(ctx, name); err != nil {
		return err
	}
	if !g.DryRun {
		if err := st.PutEngineDatabase(ctx, &state.EngineDatabase{
			Engine: engineMySQL, Name: name, Owner: owner, CreatedBy: g.Invoked(),
		}); err != nil {
			return err
		}
	}
	if noUser {
		if g.JSON {
			return g.EmitJSON(map[string]any{"engine": "mysql", "database": name, "created": true, "user": nil})
		}
		g.Printf("Created the database %s (no user).\n", name)
		return nil
	}

	password, err := mgr.CreateUser(ctx, name, username, role, "")
	if err != nil {
		return err
	}
	if !g.DryRun {
		if err := st.PutEngineUser(ctx, &state.EngineUser{
			Engine: engineMySQL, Username: username, Scope: "%", Database: name, Role: role, CreatedBy: g.Invoked(),
		}); err != nil {
			return err
		}
	}
	uri := mgr.ConnectionURI(name, username, password)

	if attach != "" {
		key, err := g.dbAttachMySQL(ctx, st, attach, username, name, envKey, uri)
		if err != nil {
			return err
		}
		return g.dbPrintEngineCredential("Created", "mysql", username, name, role, uri, attach, key)
	}
	return g.dbPrintEngineCredential("Created", "mysql", username, name, role, uri, "", "")
}

func (g *Globals) mysqlDrop(cmd *cobra.Command, name string, keepDatabase bool) error {
	ctx := cmd.Context()
	mgr, st, err := g.mysqlManager(ctx)
	if err != nil {
		return err
	}
	if err := validate.MySQLDatabaseName(name); err != nil {
		return err
	}
	// Drop the users recorded for this database, then the database itself.
	users, _ := st.ListEngineUsers(ctx, engineMySQL, name)
	for _, u := range users {
		if err := mgr.DropUser(ctx, u.Username, u.Scope); err != nil {
			return err
		}
		if !g.DryRun {
			_ = st.DeleteEngineUser(ctx, engineMySQL, u.Username, u.Scope)
		}
	}
	if !keepDatabase {
		if err := mgr.DropDatabase(ctx, name); err != nil {
			return err
		}
	}
	if !g.DryRun {
		_ = st.DeleteEngineDatabase(ctx, engineMySQL, name)
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"engine": "mysql", "dropped": name, "kept_database": keepDatabase})
	}
	g.Printf("Dropped %s.\n", name)
	return nil
}

func (g *Globals) mysqlList(cmd *cobra.Command, owner string, live bool) error {
	ctx := cmd.Context()
	mgr, st, err := g.mysqlManager(ctx)
	if err != nil {
		return err
	}
	if live {
		dbs, err := mgr.LiveDatabases(ctx)
		if err != nil {
			return err
		}
		if g.JSON {
			return g.EmitJSON(map[string]any{"engine": "mysql", "databases": dbs})
		}
		for _, d := range dbs {
			if validate.IsMySQLSystemDatabase(d.Name) {
				continue
			}
			g.Println(d.Name)
		}
		return nil
	}
	rows, err := st.ListEngineDatabases(ctx, engineMySQL, owner)
	if err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"engine": "mysql", "databases": rows})
	}
	for _, d := range rows {
		g.Printf("%s\t%s\n", d.Name, d.Owner)
	}
	return nil
}

func (g *Globals) mysqlRoles() error {
	roles := validate.MySQLRoles()
	if g.JSON {
		out := map[string]string{}
		for _, r := range roles {
			out[r[0]] = r[1]
		}
		return g.EmitJSON(map[string]any{"engine": "mysql", "roles": out})
	}
	pairs := make([][2]string, len(roles))
	copy(pairs, roles)
	return g.Fields(pairs...)
}

// --- access ---------------------------------------------------------------------

func (g *Globals) mysqlAccessAllow(cmd *cobra.Command, address, note string) error {
	ctx := cmd.Context()
	mgr, _, err := g.mysqldManager(ctx)
	if err != nil {
		return err
	}
	if g.DryRun {
		canonical, cerr := mysqld.CanonicalAddress(address)
		if cerr != nil {
			return cerr
		}
		g.Log.Info("would allow the address through to MySQL", "address", canonical, "port", mysqld.Port)
		return nil
	}
	res, err := mgr.AccessAllow(ctx, address, note, g.Invoked())
	if err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(res)
	}
	if res.AlreadyThere {
		g.Printf("%s is already allowed. Nothing changed.\n", res.Address)
		return nil
	}
	g.Printf("%s can now reach MySQL on port %s.\n", res.Address, mysqld.Port)
	if res.OpenedNetwork {
		g.Printf("\nThis was the first allowed address, so MySQL now listens on all interfaces;\n" +
			"the firewall admits only the allowed list.\n")
	}
	return nil
}

func (g *Globals) mysqlAccessRevoke(cmd *cobra.Command, address string) error {
	ctx := cmd.Context()
	mgr, _, err := g.mysqldManager(ctx)
	if err != nil {
		return err
	}
	if g.DryRun {
		canonical, cerr := mysqld.CanonicalAddress(address)
		if cerr != nil {
			return cerr
		}
		g.Log.Info("would revoke the address's access to MySQL", "address", canonical)
		return nil
	}
	res, err := mgr.AccessRevoke(ctx, address)
	if err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(res)
	}
	if res.WasAbsent {
		g.Printf("%s was not on the allowed list. Nothing changed.\n", res.Address)
		return nil
	}
	g.Printf("%s can no longer open connections to MySQL.\n", res.Address)
	if res.ClosedNetwork {
		g.Printf("\nThat was the last allowed address: MySQL is back to localhost only.\n")
	}
	return nil
}

func (g *Globals) mysqlAccessList(cmd *cobra.Command) error {
	ctx := cmd.Context()
	mgr, _, err := g.mysqldManager(ctx)
	if err != nil {
		return err
	}
	status, err := mgr.AccessList(ctx)
	if err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(status)
	}
	bind := "localhost only"
	if status.BindRemote {
		bind = "all interfaces"
	}
	firewall := "ufw not installed"
	switch {
	case status.UfwActive && status.DefaultDeny:
		firewall = "ufw active, default deny incoming"
	case status.UfwActive:
		firewall = "ufw active, but default incoming policy is not deny"
	case status.UfwPresent:
		firewall = "ufw installed but not active"
	}
	if err := g.Fields(
		[2]string{"mysql listening on", bind},
		[2]string{"firewall", firewall},
	); err != nil {
		return err
	}
	for _, a := range status.Addresses {
		line := "    " + a.Address
		if a.Note != "" {
			line += "    # " + a.Note
		}
		g.Println(line)
	}
	return nil
}

func (g *Globals) mysqlShow(cmd *cobra.Command, name string) error {
	ctx := cmd.Context()
	_, st, err := g.mysqlManager(ctx)
	if err != nil {
		return err
	}
	d, err := st.GetEngineDatabase(ctx, engineMySQL, name)
	if err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(d)
	}
	if err := g.Fields(
		[2]string{"engine", "mysql"},
		[2]string{"database", d.Name},
		[2]string{"owner", d.Owner},
	); err != nil {
		return err
	}
	for _, u := range d.Users {
		g.Printf("    user %s\trole %s\n", u.Username, u.Role)
	}
	return nil
}

func (g *Globals) mysqlUserAdd(cmd *cobra.Command, username, database, role, attach, envKey string) error {
	ctx := cmd.Context()
	mgr, st, err := g.mysqlManager(ctx)
	if err != nil {
		return err
	}
	if database == "" {
		return rlerr.Usagef("--database is required")
	}
	if err := validate.MySQLDatabaseName(database); err != nil {
		return err
	}
	if err := validate.MySQLUsername(username); err != nil {
		return err
	}
	if role == "" {
		role = g.Cfg.Databases.MySQL.DefaultRole
	}
	if role == "" {
		role = "readWrite"
	}
	if err := validate.MySQLRole(role); err != nil {
		return err
	}
	password, err := mgr.CreateUser(ctx, database, username, role, "")
	if err != nil {
		return err
	}
	if !g.DryRun {
		if err := st.PutEngineUser(ctx, &state.EngineUser{
			Engine: engineMySQL, Username: username, Scope: "%", Database: database, Role: role, CreatedBy: g.Invoked(),
		}); err != nil {
			return err
		}
	}
	uri := mgr.ConnectionURI(database, username, password)
	if attach != "" {
		key, err := g.dbAttachMySQL(ctx, st, attach, username, database, envKey, uri)
		if err != nil {
			return err
		}
		return g.dbPrintEngineCredential("Created", "mysql", username, database, role, uri, attach, key)
	}
	return g.dbPrintEngineCredential("Created", "mysql", username, database, role, uri, "", "")
}

func (g *Globals) mysqlUserList(cmd *cobra.Command, database string, live bool) error {
	ctx := cmd.Context()
	mgr, st, err := g.mysqlManager(ctx)
	if err != nil {
		return err
	}
	if live {
		if database == "" {
			return rlerr.Usagef("--database is required with --live")
		}
		users, err := mgr.LiveUsers(ctx, database)
		if err != nil {
			return err
		}
		if g.JSON {
			return g.EmitJSON(map[string]any{"engine": "mysql", "users": users})
		}
		for _, u := range users {
			g.Printf("%s@%s\n", u.Username, u.Host)
		}
		return nil
	}
	rows, err := st.ListEngineUsers(ctx, engineMySQL, database)
	if err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"engine": "mysql", "users": rows})
	}
	for _, u := range rows {
		g.Printf("%s\t%s on %s\n", u.Username, u.Role, u.Database)
	}
	return nil
}

func (g *Globals) mysqlUserPassword(cmd *cobra.Command, username, scope, attach, envKey string, allSites bool) error {
	ctx := cmd.Context()
	mgr, st, err := g.mysqlManager(ctx)
	if err != nil {
		return err
	}
	u, err := st.GetEngineUser(ctx, engineMySQL, username, scope)
	if err != nil {
		return err
	}
	password, err := mgr.SetPassword(ctx, u.Username, u.Scope, "")
	if err != nil {
		return err
	}
	if !g.DryRun {
		u.RotatedAt = time.Now().UTC()
		if err := st.PutEngineUser(ctx, u); err != nil {
			return err
		}
	}
	uri := mgr.ConnectionURI(u.Database, u.Username, password)

	// Rewrite the credential wherever it is held: one named site, all of them, or none.
	targets := map[string]string{}
	if allSites {
		for _, a := range u.Attachments {
			targets[a.Domain] = a.EnvKey
		}
	}
	if attach != "" {
		targets[attach] = envKey
	}
	if len(targets) > 0 {
		for domain, key := range targets {
			if _, err := g.dbAttachMySQL(ctx, st, domain, u.Username, u.Database, key, uri); err != nil {
				return err
			}
		}
		if g.JSON {
			return g.EmitJSON(map[string]any{"engine": "mysql", "user": u.Username, "rotated": true, "rewrote_sites": len(targets)})
		}
		g.Printf("Rotated %s and rewrote %s. Restart the affected sites.\n", u.Username, plural(len(targets), "site"))
		return nil
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"engine": "mysql", "user": u.Username, "rotated": true, "connection_uri": uri})
	}
	g.Printf("Rotated %s. The new connection string, shown once:\n\n    %s\n", u.Username, uri)
	return nil
}

func (g *Globals) mysqlUserGrant(cmd *cobra.Command, username, scope, role string) error {
	ctx := cmd.Context()
	mgr, st, err := g.mysqlManager(ctx)
	if err != nil {
		return err
	}
	if err := validate.MySQLRole(role); err != nil {
		return err
	}
	u, err := st.GetEngineUser(ctx, engineMySQL, username, scope)
	if err != nil {
		return err
	}
	if err := mgr.SetRole(ctx, u.Database, u.Username, u.Scope, role); err != nil {
		return err
	}
	if !g.DryRun {
		u.Role = role
		if err := st.PutEngineUser(ctx, u); err != nil {
			return err
		}
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"engine": "mysql", "user": u.Username, "database": u.Database, "role": role})
	}
	g.Printf("%s now has the role %s on %s.\n", u.Username, role, u.Database)
	return nil
}

func (g *Globals) mysqlUserDelete(cmd *cobra.Command, username, scope string, force bool) error {
	ctx := cmd.Context()
	mgr, st, err := g.mysqlManager(ctx)
	if err != nil {
		return err
	}
	u, err := st.GetEngineUser(ctx, engineMySQL, username, scope)
	if err != nil {
		return err
	}
	if len(u.Attachments) > 0 && !force {
		g.Printf("This user's connection string is held by %s:\n", plural(len(u.Attachments), "site"))
		for _, a := range u.Attachments {
			g.Printf("    %s (%s)\n", a.Domain, a.EnvKey)
		}
	}
	if !force && !g.DryRun {
		if err := g.ConfirmTyped(u.Username,
			"Removing the MySQL user "+u.Username+" takes those sites' database access with it."); err != nil {
			return err
		}
	}
	if err := mgr.DropUser(ctx, u.Username, u.Scope); err != nil {
		return err
	}
	if !g.DryRun {
		if err := st.DeleteEngineUser(ctx, engineMySQL, u.Username, u.Scope); err != nil {
			return err
		}
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"engine": "mysql", "removed": u.Username})
	}
	g.Printf("Removed the MySQL user %s.\n", u.Username)
	return nil
}

func (g *Globals) mysqlDump(cmd *cobra.Command, database, outDir string) error {
	ctx := cmd.Context()
	mgr, _, err := g.mysqlManager(ctx)
	if err != nil {
		return err
	}
	res, err := mgr.Dump(ctx, database, outDir)
	if err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(res)
	}
	g.Printf("Dumped %s to %s (%s).\n", res.Database, res.Path, humanBytes(res.Bytes))
	return nil
}

func (g *Globals) mysqlRestore(cmd *cobra.Command, archive, into string) error {
	ctx := cmd.Context()
	mgr, _, err := g.mysqlManager(ctx)
	if err != nil {
		return err
	}
	// `db dump --engine mysql` writes <database>.sql.gz, so the target is the archive's
	// base name when --into is not given.
	if into == "" {
		base := archive
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
		into = strings.TrimSuffix(base, ".sql.gz")
	}
	if into == "" {
		return rlerr.Usagef("cannot tell which database this archive belongs to").
			WithHint("name it with --into")
	}
	if err := mgr.Restore(ctx, archive, into); err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"engine": "mysql", "restored_into": into, "from": archive})
	}
	g.Printf("Restored %s into %s.\n", archive, into)
	return nil
}

// dbPrintEngineCredential is the engine-neutral credential print, used by the MySQL create
// and user commands. MongoDB keeps its own printer with its own wording.
func (g *Globals) dbPrintEngineCredential(verb, engine, username, database, role, uri, attachedTo, envKey string) error {
	if g.JSON {
		out := map[string]any{"engine": engine, "user": username, "database": database, "role": role, "created": true}
		if envKey != "" {
			out["attached_to"] = attachedTo
			out["env_key"] = envKey
		} else {
			out["connection_uri"] = uri
		}
		return g.EmitJSON(out)
	}
	g.Printf("%s %s on %s with the role %s.\n\n", verb, username, database, role)
	if envKey != "" {
		if err := g.Fields([2]string{"written to", attachedTo + " (" + envKey + ")"}); err != nil {
			return err
		}
		g.Printf("\nThe application picks it up on its next start:\n    ratline site restart %s\n", attachedTo)
		return nil
	}
	g.Printf("The connection string, shown once — the server stores a hash, so this cannot be\n" +
		"displayed again. Rotate it with 'ratline db user password' if lost.\n\n")
	g.Printf("    %s\n", uri)
	return nil
}
