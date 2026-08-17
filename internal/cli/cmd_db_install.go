package cli

import (
	"context"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/mongod"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// `ratline db install` is the one command in ratline that adds a package repository
// and installs software. Everything else configures what a host already has; this
// exists because "point ratline at a MongoDB" is not actionable advice on a fresh VPS
// that has none, and the manual path — repo, key, package, first user, authorization,
// restart — is long enough that people skip the authorization step, which is the one
// that matters.

func (g *Globals) mongodManager(ctx context.Context) (*mongod.Manager, error) {
	st, err := g.Store(ctx)
	if err != nil {
		return nil, err
	}
	return &mongod.Manager{
		Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, Bins: g.Bins, State: st,
		OS: g.OS, DryRun: g.DryRun,
	}, nil
}

func newDBInstallCommand(g *Globals) *cobra.Command {
	var (
		adminUser string
		version   string
		password  string
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install MongoDB on this host, secure it, and attach it",
		Args:  cobra.NoArgs,
		Long: "Installs MongoDB Community from MongoDB's official apt repository and leaves it in\n" +
			"the state the rest of `ratline db` assumes: enforcing authorization, reachable only\n" +
			"from this machine, with a root-role admin user whose password you choose.\n\n" +
			"This is the one ratline command that adds a package repository and installs\n" +
			"software. The repository's signing key ships inside the ratline binary and is\n" +
			"pinned into the apt source — nothing about the root of trust is downloaded.\n\n" +
			"The password is asked for at the prompt, not taken as a flag: anything in argv is\n" +
			"world-readable through /proc for as long as the command runs, and it would land in\n" +
			"your shell history. For automation, pipe it in with --stdin.\n\n" +
			"What happens, in order: the repository and key are written; mongodb-org is\n" +
			"installed; the service is enabled and started; your admin user is created; the\n" +
			"configuration is replaced with one that enables authorization and binds localhost\n" +
			"only; mongod restarts; and ratline proves the running server enforces\n" +
			"authorization with your credentials before storing the connection string and\n" +
			"turning provisioning on. If any step fails, every change is undone — except the\n" +
			"packages themselves, which are left installed but stopped and disabled, so a\n" +
			"re-run continues where it left off.\n\n" +
			"If a MongoDB server is already installed on this host and ratline did not set it\n" +
			"up, this refuses and points at 'ratline db connect'. If ratline is already\n" +
			"attached to a MongoDB — this one or any other — the stored connection string is\n" +
			"left alone.\n\n" +
			"The server listens only on localhost until 'ratline db access allow' opens it,\n" +
			"firewall first.",
		Example: "  # choose the password at the prompt: not echoed, not in argv\n" +
			"  ratline db install\n\n" +
			"  # for automation, where there is no terminal\n" +
			"  ratline db install --stdin < /root/mongo-admin-password\n\n" +
			"  # then\n" +
			"  ratline db create shop --owner acme\n" +
			"  ratline db access allow 203.0.113.19   # if another machine needs in",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			engine, eerr := g.dbEngineChoice(cmd)
			if eerr != nil {
				return eerr
			}
			if engine == engineMySQL {
				if g.DryRun {
					return g.mysqlInstall(cmd, adminUser, "")
				}
				if password == "" {
					pw, err := g.readSecret("Choose a password for the MySQL admin user (not echoed): ")
					if err != nil {
						return err
					}
					again, err := g.readSecret("Type it again: ")
					if err != nil {
						return err
					}
					if strings.TrimSpace(pw) != strings.TrimSpace(again) {
						return rlerr.Usagef("the two passwords do not match, so nothing was done")
					}
					password = strings.TrimSpace(pw)
				}
				if err := checkAdminPassword(password); err != nil {
					return err
				}
				return g.mysqlInstall(cmd, adminUser, password)
			}
			if engine == engineRedis {
				if g.DryRun {
					return g.redisInstall(cmd, "")
				}
				if password == "" {
					pw, err := g.readSecret("Choose a password for the Redis admin (not echoed): ")
					if err != nil {
						return err
					}
					again, err := g.readSecret("Type it again: ")
					if err != nil {
						return err
					}
					if strings.TrimSpace(pw) != strings.TrimSpace(again) {
						return rlerr.Usagef("the two passwords do not match, so nothing was done")
					}
					password = strings.TrimSpace(pw)
				}
				if err := checkAdminPassword(password); err != nil {
					return err
				}
				return g.redisInstall(cmd, password)
			}

			if err := validate.DatabaseUsername(adminUser); err != nil {
				return rlerr.Wrap(err, rlerr.CodeUsage, "the admin username is not usable")
			}

			mgr, err := g.mongodManager(ctx)
			if err != nil {
				return err
			}

			if g.DryRun {
				return g.printInstallPlan(mgr, version)
			}

			if password == "" {
				var err error
				if password, err = g.readSecret(
					"Choose a password for the MongoDB admin user (not echoed): "); err != nil {
					return err
				}
				password = strings.TrimSpace(password)
				again, err := g.readSecret("Type it again: ")
				if err != nil {
					return err
				}
				if password != strings.TrimSpace(again) {
					return rlerr.Usagef("the two passwords do not match, so nothing was done")
				}
			}
			if err := checkAdminPassword(password); err != nil {
				return err
			}

			res, err := mgr.Install(ctx, mongod.InstallOptions{
				Version: version, AdminUser: adminUser, Password: password,
			})
			if err != nil {
				return err
			}

			// Attach only when nothing is attached: an existing connection string may
			// point at a different server holding real data, and silently repointing
			// every future `db create` at the new one is not a decision to make here.
			attached := false
			alreadyAttached := g.Cfg.Paths.MongoURIFile != "" && system.Exists(g.Cfg.Paths.MongoURIFile)
			if !alreadyAttached {
				if _, err := g.storeAdminURIAndEnable(ctx, res.AdminURI); err != nil {
					return err
				}
				attached = true
			}

			if g.JSON {
				return g.EmitJSON(map[string]any{
					"version":            res.Version,
					"server_version":     res.ServerVersion,
					"package_installed":  res.PackageInstalled,
					"admin_user":         res.AdminUser,
					"admin_user_created": res.AdminUserCreated,
					"conf_path":          res.ConfPath,
					"attached":           attached,
					"already_attached":   alreadyAttached,
				})
			}

			g.Printf("MongoDB is installed and secured.\n\n")
			if err := g.Fields(
				[2]string{"server", "MongoDB " + orDash(res.ServerVersion)},
				[2]string{"admin user", res.AdminUser + " (role root, password never stored)"},
				[2]string{"authorization", "enforced (verified against the running server)"},
				[2]string{"listening on", "127.0.0.1 only"},
				[2]string{"config", res.ConfPath + " (managed by ratline)"},
			); err != nil {
				return err
			}
			if !res.PackageInstalled {
				g.Printf("\nThe packages were already installed; they were checked, not reinstalled.\n")
			}
			if alreadyAttached {
				g.Printf("\nratline was already attached to a MongoDB, so the stored connection\n" +
					"string was left alone. Point it at this server with:\n" +
					"    ratline db connect --force\n")
			} else {
				g.Printf("\nThe connection string is stored and provisioning is on.\n")
			}
			g.Printf("\nNext:\n" +
				"    ratline db create <name> --owner <tenant>\n" +
				"    ratline db access allow <address>    # only if another machine needs in\n")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&adminUser, "admin-user", "admin", "Name for the root-role admin user")
	f.StringVar(&version, "mongodb-version", "", "Release series to install (default: newest this host supports)")
	f.BoolVar(new(bool), "stdin", false, "Read the admin password from stdin (for automation; a terminal is prompted)")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		if useStdin, _ := cmd.Flags().GetBool("stdin"); !useStdin {
			return nil
		}
		body, err := io.ReadAll(io.LimitReader(g.Stdin, 8<<10))
		if err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "reading the password from stdin")
		}
		password = strings.TrimSpace(string(body))
		if password == "" {
			return rlerr.InputRequiredf("--stdin was given but nothing arrived on stdin")
		}
		return nil
	}
	return Mutating(cmd)
}

// printInstallPlan is the --dry-run answer: the plan, resolved but not rehearsed. The
// steps precondition on each other — the package must be installed before its service
// can start — so running them under --dry-run would report a false failure at step
// two. The plan is what can be said truthfully without doing anything.
func (g *Globals) printInstallPlan(mgr *mongod.Manager, version string) error {
	resolved, err := mongod.ResolveVersion(mgr.OS, version)
	if err != nil {
		return err
	}
	line, err := mongod.SourceLine(mgr.OS, resolved)
	if err != nil {
		return err
	}
	conf := mongod.ReadConfState(mongod.ConfPath)
	if mgr.Installed() && conf.Exists && !conf.Managed {
		return rlerr.Preconditionf("a MongoDB server is already installed on this host, and ratline did not set it up").
			WithHint("attach it instead with 'ratline db connect'")
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{
			"would_install": "mongodb-org", "version": resolved,
			"apt_source": line, "conf_path": mongod.ConfPath,
		})
	}
	g.Printf("Would install MongoDB %s:\n\n", resolved)
	steps := []string{
		"write the signing key (embedded in this binary) to " + mongod.KeyringPath(resolved),
		"write " + mongod.SourcesPath(resolved) + ":\n      " + line,
		"apt-get update && apt-get install -y mongodb-org",
		"enable and start the mongod service",
		"create the admin user (password read at the prompt or from --stdin)",
		"replace " + mongod.ConfPath + " (authorization enabled, localhost only)",
		"restart mongod and verify it enforces authorization with those credentials",
	}
	if g.Cfg.Paths.MongoURIFile != "" && system.Exists(g.Cfg.Paths.MongoURIFile) {
		steps = append(steps, "leave the stored connection string alone (ratline is already attached)")
	} else {
		steps = append(steps, "store the connection string and turn provisioning on")
	}
	for _, s := range steps {
		g.Printf("    - %s\n", s)
	}
	return nil
}

// checkAdminPassword refuses passwords that are not worth having on a database port.
// Eight characters is not a security claim, it is a floor under accidents: an empty
// string, a stray "y" from a mistyped pipeline.
func checkAdminPassword(password string) error {
	if len(password) < 8 {
		return rlerr.Usagef("the admin password is %d characters; use at least 8", len(password)).
			WithHint("this credential can read and write every database on the server")
	}
	if err := validate.NoControlChars("password", password); err != nil {
		return err
	}
	return nil
}
