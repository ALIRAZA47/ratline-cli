package cli

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/redis"
	"github.com/ALIRAZA47/ratline-cli/internal/redisd"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// The Redis engine handlers behind `ratline db … --engine redis`. A ratline "database" is
// an ACL user confined to a key-prefix keyspace; the keyspace name plays the database role
// and the ACL user is the credential.

func (g *Globals) redisManager(ctx context.Context) (*redis.Manager, *state.Store, error) {
	if !g.Cfg.Features.DBProvisioning {
		return nil, nil, rlerr.Preconditionf("database provisioning is turned off").
			WithHint("run 'ratline db connect --engine redis' or 'ratline db install --engine redis'")
	}
	st, err := g.Store(ctx)
	if err != nil {
		return nil, nil, err
	}
	return &redis.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, Bins: g.Bins, State: st, DryRun: g.DryRun}, st, nil
}

func (g *Globals) redisdManager(ctx context.Context) (*redisd.Manager, *state.Store, error) {
	st, err := g.Store(ctx)
	if err != nil {
		return nil, nil, err
	}
	return &redisd.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, Bins: g.Bins, State: st, OS: g.OS, DryRun: g.DryRun}, st, nil
}

// storeRedisAdminAndEnable writes the admin URI and turns provisioning on, verifying first.
func (g *Globals) storeRedisAdminAndEnable(ctx context.Context, uri string) (*redis.ServerInfo, error) {
	path := g.Cfg.Paths.RedisURIFile
	if path == "" {
		return nil, rlerr.Preconditionf("paths.redis_uri_file is not configured")
	}
	if _, err := system.EnsureDir(filepath.Dir(path), 0o700, 0, 0); err != nil {
		return nil, err
	}
	var previous []byte
	existed := system.Exists(path)
	if existed {
		var err error
		if previous, err = os.ReadFile(path); err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the existing %s", path)
		}
	}
	body := "# " + system.ManagedHeader + "\n" +
		"# Redis admin connection string. The credential that manages every ACL user on\n" +
		"# the server, which is why this file is 0600 and root-owned.\n\n" + uri + "\n"
	if err := system.WriteFileAtomic(path, []byte(body), 0o600, 0, 0); err != nil {
		return nil, err
	}
	wasEnabled := g.Cfg.Features.DBProvisioning
	if !wasEnabled {
		if err := g.setConfigValue("features.db_provisioning", "true"); err != nil {
			return nil, err
		}
	}
	g.Cfg.Features.DBProvisioning = true

	mgr := &redis.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, Bins: g.Bins}
	info, perr := mgr.Ping(ctx)
	if perr != nil {
		if existed {
			_ = system.WriteFileAtomic(path, previous, 0o600, 0, 0)
		} else {
			_ = os.Remove(path)
		}
		if !wasEnabled {
			_ = g.setConfigValue("features.db_provisioning", "false")
			g.Cfg.Features.DBProvisioning = false
		}
		return nil, rlerr.Wrap(perr, rlerr.CodePrecondition, "those credentials did not work, so nothing was stored")
	}
	return info, nil
}

func (g *Globals) dbAttachRedis(ctx context.Context, st *state.Store, domain, username, keyspace, envKey, uri string) (string, error) {
	site, err := st.FindSiteByName(ctx, domain)
	if err != nil {
		return "", err
	}
	if envKey == "" {
		envKey = g.Cfg.Databases.Redis.EnvKey
	}
	if envKey == "" {
		envKey = "REDIS_URL"
	}
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
	if err := st.PutEngineAttachment(ctx, &state.EngineAttachment{
		Engine: engineRedis, Domain: domain, Username: username, Scope: keyspace, EnvKey: envKey,
	}); err != nil {
		return "", err
	}
	g.Log.Info("wrote the connection string", "domain", domain, "key", envKey,
		"note", "the value is redacted in logs and in --json output")
	return envKey, nil
}

func (g *Globals) redisPing(cmd *cobra.Command) error {
	ctx := cmd.Context()
	mgr, _, err := g.redisManager(ctx)
	if err != nil {
		return err
	}
	info, err := mgr.Ping(ctx)
	if err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"reachable": true, "version": info.Version, "engine": "redis"})
	}
	return g.Fields(
		[2]string{"engine", "redis"},
		[2]string{"version", orDash(info.Version)},
		[2]string{"authentication", yesNo(info.AuthEnabled)},
	)
}

func (g *Globals) redisInstall(cmd *cobra.Command, password string) error {
	ctx := cmd.Context()
	mgr, _, err := g.redisdManager(ctx)
	if err != nil {
		return err
	}
	if g.DryRun {
		g.Printf("Would install Redis on this host:\n")
		for _, s := range []string{
			"apt-get update && apt-get install -y redis-server",
			"write " + redisd.ACLFile + " (default user, the admin, with your password)",
			"write " + redisd.ConfPath + " (bind 127.0.0.1, aclfile) and include it from " + redisd.MainConf,
			"restart and verify it requires the password and refuses an unauthenticated ping",
			"store the connection string and turn provisioning on",
		} {
			g.Printf("    - %s\n", s)
		}
		return nil
	}
	res, err := mgr.Install(ctx, redisd.InstallOptions{Password: password})
	if err != nil {
		return err
	}
	attached := false
	already := g.Cfg.Paths.RedisURIFile != "" && system.Exists(g.Cfg.Paths.RedisURIFile)
	if !already {
		if _, err := g.storeRedisAdminAndEnable(ctx, res.AdminURI); err != nil {
			return err
		}
		attached = true
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{
			"engine": "redis", "server_version": res.ServerVersion,
			"package_installed": res.PackageInstalled, "attached": attached,
		})
	}
	g.Printf("Redis is installed and secured.\n\n")
	if err := g.Fields(
		[2]string{"server", "redis " + orDash(res.ServerVersion)},
		[2]string{"authentication", "required (verified against the running server)"},
		[2]string{"listening on", "127.0.0.1 only"},
		[2]string{"config", res.ConfPath + " (managed by ratline)"},
	); err != nil {
		return err
	}
	g.Printf("\nNext:\n    ratline db create <name> --engine redis --owner <tenant>\n")
	return nil
}

func (g *Globals) redisConnect(cmd *cobra.Command, uri string) error {
	ctx := cmd.Context()
	if err := validate.RedisURI(uri); err != nil {
		return err
	}
	if g.DryRun {
		g.Log.Info("would store the Redis admin connection string and turn provisioning on")
		return nil
	}
	info, err := g.storeRedisAdminAndEnable(ctx, uri)
	if err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"engine": "redis", "version": info.Version, "provisioning_enabled": true})
	}
	g.Printf("Connected.\n\n")
	return g.Fields(
		[2]string{"engine", "redis"},
		[2]string{"version", orDash(info.Version)},
		[2]string{"stored at", g.Cfg.Paths.RedisURIFile + " (0600, root-owned)"},
		[2]string{"provisioning", "on"},
	)
}

func (g *Globals) redisCreate(cmd *cobra.Command, keyspace, owner, username, role, attach, envKey string, noUser bool) error {
	ctx := cmd.Context()
	if err := validate.RedisKeyspace(keyspace); err != nil {
		return err
	}
	mgr, st, err := g.redisManager(ctx)
	if err != nil {
		return err
	}
	if owner == "" {
		return rlerr.Usagef("--owner is required").WithHint("it names the tenant this keyspace belongs to")
	}
	if _, err := st.GetUser(ctx, owner); err != nil {
		return err
	}
	if role == "" {
		role = g.Cfg.Databases.Redis.DefaultRole
	}
	if role == "" {
		role = "readWrite"
	}
	if err := validate.RedisRole(role); err != nil {
		return err
	}
	if username == "" {
		username = redis.DefaultUsername(keyspace)
	}
	if err := validate.RedisUsername(username); err != nil {
		return err
	}
	if attach != "" && noUser {
		return rlerr.Usagef("--attach and --no-user contradict each other")
	}
	// A keyspace with no user is just a name; Redis creates nothing until a key is
	// written. Record it so `db list` shows it, and stop there.
	if !g.DryRun {
		if err := st.PutEngineDatabase(ctx, &state.EngineDatabase{
			Engine: engineRedis, Name: keyspace, Owner: owner, CreatedBy: g.Invoked(),
		}); err != nil {
			return err
		}
	}
	if noUser {
		if g.JSON {
			return g.EmitJSON(map[string]any{"engine": "redis", "keyspace": keyspace, "created": true, "user": nil})
		}
		g.Printf("Recorded the keyspace %s (no user).\n", keyspace)
		return nil
	}
	password, err := mgr.CreateKeyspaceUser(ctx, keyspace, username, role, "")
	if err != nil {
		return err
	}
	if !g.DryRun {
		if err := st.PutEngineUser(ctx, &state.EngineUser{
			Engine: engineRedis, Username: username, Scope: keyspace, Database: keyspace, Role: role, CreatedBy: g.Invoked(),
		}); err != nil {
			return err
		}
	}
	uri := mgr.ConnectionURI(username, password)
	if attach != "" {
		key, err := g.dbAttachRedis(ctx, st, attach, username, keyspace, envKey, uri)
		if err != nil {
			return err
		}
		return g.dbPrintEngineCredential("Created", "redis", username, keyspace, role, uri, attach, key)
	}
	return g.dbPrintEngineCredential("Created", "redis", username, keyspace, role, uri, "", "")
}

func (g *Globals) redisList(cmd *cobra.Command, owner string, live bool) error {
	ctx := cmd.Context()
	mgr, st, err := g.redisManager(ctx)
	if err != nil {
		return err
	}
	if live {
		users, err := mgr.LiveUsers(ctx)
		if err != nil {
			return err
		}
		if g.JSON {
			return g.EmitJSON(map[string]any{"engine": "redis", "users": users})
		}
		for _, u := range users {
			g.Println(u.Username)
		}
		return nil
	}
	rows, err := st.ListEngineDatabases(ctx, engineRedis, owner)
	if err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"engine": "redis", "keyspaces": rows})
	}
	for _, d := range rows {
		g.Printf("%s\t%s\n", d.Name, d.Owner)
	}
	return nil
}

func (g *Globals) redisShow(cmd *cobra.Command, name string) error {
	ctx := cmd.Context()
	_, st, err := g.redisManager(ctx)
	if err != nil {
		return err
	}
	d, err := st.GetEngineDatabase(ctx, engineRedis, name)
	if err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(d)
	}
	if err := g.Fields([2]string{"engine", "redis"}, [2]string{"keyspace", d.Name}, [2]string{"owner", d.Owner}); err != nil {
		return err
	}
	for _, u := range d.Users {
		g.Printf("    user %s\trole %s\n", u.Username, u.Role)
	}
	return nil
}

func (g *Globals) redisDrop(cmd *cobra.Command, name string, keepData bool) error {
	ctx := cmd.Context()
	mgr, st, err := g.redisManager(ctx)
	if err != nil {
		return err
	}
	if err := validate.RedisKeyspace(name); err != nil {
		return err
	}
	users, _ := st.ListEngineUsers(ctx, engineRedis, name)
	for _, u := range users {
		if err := mgr.DropUser(ctx, u.Username); err != nil {
			return err
		}
		if !g.DryRun {
			_ = st.DeleteEngineUser(ctx, engineRedis, u.Username, u.Scope)
		}
	}
	if !keepData && !g.DryRun {
		if err := mgr.FlushKeyspace(ctx, name); err != nil {
			return err
		}
	}
	if !g.DryRun {
		_ = st.DeleteEngineDatabase(ctx, engineRedis, name)
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"engine": "redis", "dropped": name, "kept_data": keepData})
	}
	g.Printf("Dropped the keyspace %s.\n", name)
	return nil
}

func (g *Globals) redisRoles() error {
	roles := validate.RedisRoles()
	if g.JSON {
		out := map[string]string{}
		for _, r := range roles {
			out[r[0]] = r[1]
		}
		return g.EmitJSON(map[string]any{"engine": "redis", "roles": out})
	}
	pairs := make([][2]string, len(roles))
	copy(pairs, roles)
	return g.Fields(pairs...)
}

func (g *Globals) redisUserAdd(cmd *cobra.Command, username, keyspace, role, attach, envKey string) error {
	ctx := cmd.Context()
	mgr, st, err := g.redisManager(ctx)
	if err != nil {
		return err
	}
	if keyspace == "" {
		return rlerr.Usagef("--database (the keyspace) is required")
	}
	if role == "" {
		role = g.Cfg.Databases.Redis.DefaultRole
	}
	if role == "" {
		role = "readWrite"
	}
	if err := validate.RedisRole(role); err != nil {
		return err
	}
	password, err := mgr.CreateKeyspaceUser(ctx, keyspace, username, role, "")
	if err != nil {
		return err
	}
	if !g.DryRun {
		if err := st.PutEngineUser(ctx, &state.EngineUser{
			Engine: engineRedis, Username: username, Scope: keyspace, Database: keyspace, Role: role, CreatedBy: g.Invoked(),
		}); err != nil {
			return err
		}
	}
	uri := mgr.ConnectionURI(username, password)
	if attach != "" {
		key, err := g.dbAttachRedis(ctx, st, attach, username, keyspace, envKey, uri)
		if err != nil {
			return err
		}
		return g.dbPrintEngineCredential("Created", "redis", username, keyspace, role, uri, attach, key)
	}
	return g.dbPrintEngineCredential("Created", "redis", username, keyspace, role, uri, "", "")
}

func (g *Globals) redisUserPassword(cmd *cobra.Command, username, attach, envKey string, allSites bool) error {
	ctx := cmd.Context()
	mgr, st, err := g.redisManager(ctx)
	if err != nil {
		return err
	}
	u, err := st.GetEngineUser(ctx, engineRedis, username, "")
	if err != nil {
		return err
	}
	password, err := mgr.SetPassword(ctx, u.Username, "")
	if err != nil {
		return err
	}
	if !g.DryRun {
		u.RotatedAt = time.Now().UTC()
		if err := st.PutEngineUser(ctx, u); err != nil {
			return err
		}
	}
	uri := mgr.ConnectionURI(u.Username, password)
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
			if _, err := g.dbAttachRedis(ctx, st, domain, u.Username, u.Scope, key, uri); err != nil {
				return err
			}
		}
		if g.JSON {
			return g.EmitJSON(map[string]any{"engine": "redis", "user": u.Username, "rotated": true, "rewrote_sites": len(targets)})
		}
		g.Printf("Rotated %s and rewrote %s. Restart the affected sites.\n", u.Username, plural(len(targets), "site"))
		return nil
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"engine": "redis", "user": u.Username, "rotated": true, "connection_uri": uri})
	}
	g.Printf("Rotated %s. The new connection string, shown once:\n\n    %s\n", u.Username, uri)
	return nil
}

func (g *Globals) redisUserGrant(cmd *cobra.Command, username, role string) error {
	ctx := cmd.Context()
	mgr, st, err := g.redisManager(ctx)
	if err != nil {
		return err
	}
	if err := validate.RedisRole(role); err != nil {
		return err
	}
	u, err := st.GetEngineUser(ctx, engineRedis, username, "")
	if err != nil {
		return err
	}
	if err := mgr.SetRole(ctx, u.Scope, u.Username, role); err != nil {
		return err
	}
	if !g.DryRun {
		u.Role = role
		if err := st.PutEngineUser(ctx, u); err != nil {
			return err
		}
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"engine": "redis", "user": u.Username, "keyspace": u.Scope, "role": role})
	}
	g.Printf("%s now has the role %s on keyspace %s.\n", u.Username, role, u.Scope)
	return nil
}

func (g *Globals) redisUserList(cmd *cobra.Command, keyspace string, live bool) error {
	ctx := cmd.Context()
	mgr, st, err := g.redisManager(ctx)
	if err != nil {
		return err
	}
	if live {
		users, err := mgr.LiveUsers(ctx)
		if err != nil {
			return err
		}
		if g.JSON {
			return g.EmitJSON(map[string]any{"engine": "redis", "users": users})
		}
		for _, u := range users {
			g.Println(u.Username)
		}
		return nil
	}
	rows, err := st.ListEngineUsers(ctx, engineRedis, keyspace)
	if err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"engine": "redis", "users": rows})
	}
	for _, u := range rows {
		g.Printf("%s\t%s on %s\n", u.Username, u.Role, u.Database)
	}
	return nil
}

func (g *Globals) redisUserDelete(cmd *cobra.Command, username string, force bool) error {
	ctx := cmd.Context()
	mgr, st, err := g.redisManager(ctx)
	if err != nil {
		return err
	}
	u, err := st.GetEngineUser(ctx, engineRedis, username, "")
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
		if err := g.ConfirmTyped(u.Username, "Removing the Redis user "+u.Username+" takes those sites' access with it."); err != nil {
			return err
		}
	}
	if err := mgr.DropUser(ctx, u.Username); err != nil {
		return err
	}
	if !g.DryRun {
		if err := st.DeleteEngineUser(ctx, engineRedis, u.Username, u.Scope); err != nil {
			return err
		}
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"engine": "redis", "removed": u.Username})
	}
	g.Printf("Removed the Redis user %s.\n", u.Username)
	return nil
}

func (g *Globals) redisAccessAllow(cmd *cobra.Command, address, note string) error {
	ctx := cmd.Context()
	mgr, _, err := g.redisdManager(ctx)
	if err != nil {
		return err
	}
	if g.DryRun {
		canonical, cerr := redisd.CanonicalAddress(address)
		if cerr != nil {
			return cerr
		}
		g.Log.Info("would allow the address through to Redis", "address", canonical, "port", redisd.Port)
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
	g.Printf("%s can now reach Redis on port %s.\n", res.Address, redisd.Port)
	if res.OpenedNetwork {
		g.Printf("\nThis was the first allowed address, so Redis now listens on all interfaces;\n" +
			"the firewall admits only the allowed list.\n")
	}
	return nil
}

func (g *Globals) redisAccessRevoke(cmd *cobra.Command, address string) error {
	ctx := cmd.Context()
	mgr, _, err := g.redisdManager(ctx)
	if err != nil {
		return err
	}
	if g.DryRun {
		canonical, cerr := redisd.CanonicalAddress(address)
		if cerr != nil {
			return cerr
		}
		g.Log.Info("would revoke the address's access to Redis", "address", canonical)
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
	g.Printf("%s can no longer open connections to Redis.\n", res.Address)
	if res.ClosedNetwork {
		g.Printf("\nThat was the last allowed address: Redis is back to localhost only.\n")
	}
	return nil
}

func (g *Globals) redisAccessList(cmd *cobra.Command) error {
	ctx := cmd.Context()
	mgr, _, err := g.redisdManager(ctx)
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
	if err := g.Fields([2]string{"redis listening on", bind}, [2]string{"firewall", firewall}); err != nil {
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

// redisNoDump reports that Redis has no per-keyspace dump — its backups are server-level.
func (g *Globals) redisNoDump(verb string) error {
	return rlerr.Preconditionf("`db %s --engine redis` is not supported", verb).
		WithHint("Redis backups are server-level (RDB snapshots / AOF), not per-keyspace; " +
			"back the server up with its own snapshotting")
}
