package cli

import (
	"context"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/mysql"
	"github.com/ALIRAZA47/ratline-cli/internal/mysqld"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// The MySQL engine handlers behind `ratline db … --engine mysql`. The MongoDB verbs are
// untouched: each db command dispatches here at the top of its RunE only when the engine
// flag is not mongo, so the shipped MongoDB path is exactly as it was. What is shared —
// the .env writing, the credential printing, secret prompting — is reused from the common
// helpers; what differs is the manager and the state tables.

const (
	engineMongo = "mongo"
	engineMySQL = "mysql"
	engineRedis = "redis"
)

// dbEngineChoice reads and validates the --engine flag.
func (g *Globals) dbEngineChoice(cmd *cobra.Command) (string, error) {
	e, _ := cmd.Flags().GetString("engine")
	if e == "" {
		e = engineMongo
	}
	switch e {
	case engineMongo, engineMySQL:
		return e, nil
	case engineRedis:
		return "", rlerr.Usagef("the redis engine is not available yet").
			WithHint("mongo and mysql are supported")
	default:
		return "", rlerr.Usagef("unknown engine %q", e).WithHint("one of: mongo, mysql")
	}
}

// mysqlManager builds the inside-the-server manager, refusing when provisioning is off or
// no admin credentials are stored.
func (g *Globals) mysqlManager(ctx context.Context) (*mysql.Manager, *state.Store, error) {
	if !g.Cfg.Features.DBProvisioning {
		return nil, nil, rlerr.Preconditionf("database provisioning is turned off").
			WithHint("run 'ratline db connect --engine mysql' or 'ratline db install --engine mysql'")
	}
	st, err := g.Store(ctx)
	if err != nil {
		return nil, nil, err
	}
	mgr := &mysql.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, Bins: g.Bins, State: st, DryRun: g.DryRun}
	return mgr, st, nil
}

// mysqldManager builds the server manager.
func (g *Globals) mysqldManager(ctx context.Context) (*mysqld.Manager, *state.Store, error) {
	st, err := g.Store(ctx)
	if err != nil {
		return nil, nil, err
	}
	return &mysqld.Manager{
		Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, Bins: g.Bins, State: st,
		OS: g.OS, DryRun: g.DryRun,
	}, st, nil
}

// storeMySQLAdminAndEnable writes the admin defaults-file, turns provisioning on, and
// proves the credentials work — the MySQL analog of storeAdminURIAndEnable. Undone by hand
// because what to restore depends on the prior state.
func (g *Globals) storeMySQLAdminAndEnable(ctx context.Context, creds mysql.Creds) (*mysql.ServerInfo, error) {
	path := g.Cfg.Paths.MySQLDefaultsFile
	if path == "" {
		return nil, rlerr.Preconditionf("paths.mysql_defaults_file is not configured")
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
	body := mysql.RenderDefaultsFile(creds)
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

	mgr := &mysql.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, Bins: g.Bins}
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
		return nil, rlerr.Wrap(perr, rlerr.CodePrecondition,
			"those credentials did not work, so nothing was stored")
	}
	return info, nil
}

// dbAttachMySQL writes a connection string into a site's .env and records the attachment.
func (g *Globals) dbAttachMySQL(ctx context.Context, st *state.Store, domain, username, database, envKey, uri string) (string, error) {
	site, err := st.FindSiteByName(ctx, domain)
	if err != nil {
		return "", err
	}
	if envKey == "" {
		envKey = g.Cfg.Databases.MySQL.EnvKey
	}
	if envKey == "" {
		envKey = "DATABASE_URL"
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
		Engine: engineMySQL, Domain: domain, Username: username, Scope: "%", EnvKey: envKey,
	}); err != nil {
		return "", err
	}
	g.Log.Info("wrote the connection string", "domain", domain, "key", envKey,
		"note", "the value is redacted in logs and in --json output")
	return envKey, nil
}
