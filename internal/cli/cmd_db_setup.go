package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/mongo"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// Turning database provisioning on used to be four manual steps: create a 0700 directory,
// write a connection string into it, chmod it 0600, and edit a boolean in config.yaml.
//
// Every one of those is a step somebody gets wrong, and two of them are about the mode of
// a file holding the root password for every database on the server. Getting that wrong is
// not a typo, it is every tenant able to read every other tenant's data. So it is one
// command that does all four, proves the credentials work, and undoes the lot if they do
// not.

func newDBConnectCommand(g *Globals) *cobra.Command {
	var (
		uri      string
		fromFile string
		force    bool
	)
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Point ratline at a MongoDB server and turn provisioning on",
		Args:  cobra.NoArgs,
		Long: "Stores the admin connection string, turns on features.db_provisioning, and proves\n" +
			"the credentials work before committing either. If the server cannot be reached, or\n" +
			"rejects them, nothing is left behind.\n\n" +
			"The string is read from stdin, not from a flag. Anything in argv is world-readable\n" +
			"through /proc, so a password passed as an argument is visible to every account on\n" +
			"the box for as long as the command runs — and it lands in your shell history, which\n" +
			"outlives the password.\n\n" +
			"It is written to paths.mongo_uri_file at 0600, root-owned, in a 0700 directory.",
		Example: "  # from a password manager, or a file, never as an argument\n" +
			"  printf 'mongodb://admin:PASS@127.0.0.1:27017/?authSource=admin' | \\\n" +
			"    ratline db connect --stdin\n\n" +
			"  ratline db connect --from-file /root/atlas.uri\n" +
			"  ratline db ping",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			switch {
			case fromFile != "":
				body, err := os.ReadFile(fromFile)
				if err != nil {
					return rlerr.Wrap(err, rlerr.CodePrecondition, "reading %s", fromFile)
				}
				uri = strings.TrimSpace(string(body))
			default:
				// --stdin is the only other way in, and it is required rather than
				// implied: a command that silently blocks on an empty stdin looks hung.
				if uri == "" {
					return rlerr.Usagef("no connection string was given").
						WithHint("pipe it in:\n" +
							"        printf 'mongodb://…' | ratline db connect --stdin\n" +
							"        ratline db connect --from-file /root/mongodb.uri\n\n" +
							"        It is not a flag on purpose: argv is world-readable " +
							"through /proc, and it would land in your shell history")
				}
			}
			if uri == "" {
				return rlerr.Usagef("the connection string is empty")
			}
			if !strings.HasPrefix(uri, "mongodb://") && !strings.HasPrefix(uri, "mongodb+srv://") {
				return rlerr.Usagef("that does not look like a MongoDB connection string").
					WithHint("it should begin with mongodb:// or mongodb+srv://")
			}

			path := g.Cfg.Paths.MongoURIFile
			if path == "" {
				return rlerr.Preconditionf("paths.mongo_uri_file is not configured").
					WithHint("ratline config set paths.mongo_uri_file /etc/ratline/db/mongodb.uri")
			}
			if system.Exists(path) && !force {
				return rlerr.Preconditionf("%s already exists", path).
					WithHint("pass --force to replace it. 'ratline db ping' says whether " +
						"the one already there works")
			}

			if g.DryRun {
				g.Log.Info("would store the connection string and turn provisioning on",
					"path", path, "server", mongo.Redact(uri))
				return nil
			}

			// Undone by hand rather than through the rollback stack: what has to be put
			// back depends on what was there before — a previous URI is restored, and the
			// feature flag reverts only if this command is what set it.
			var err error

			// 0700, because the directory listing alone tells anyone that a database
			// credential lives here.
			if _, err = system.EnsureDir(filepath.Dir(path), 0o700, 0, 0); err != nil {
				return err
			}
			var previous []byte
			existed := system.Exists(path)
			if existed {
				if previous, err = os.ReadFile(path); err != nil {
					return rlerr.Wrap(err, rlerr.CodeGeneric, "reading the existing %s", path)
				}
			}
			if err = system.WriteFileAtomic(path, []byte(uri+"\n"), 0o600, 0, 0); err != nil {
				return err
			}
			// Turned on before the check, because the check goes through the same code
			// path an operator will use and that path refuses when the feature is off.
			wasEnabled := g.Cfg.Features.DBProvisioning
			if !wasEnabled {
				if err = g.setConfigValue("features.db_provisioning", "true"); err != nil {
					return err
				}
			}
			g.Cfg.Features.DBProvisioning = true

			mgr := &mongo.Manager{
				Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, Bins: g.Bins, DryRun: false,
			}
			info, perr := mgr.Ping(ctx)
			if perr != nil {
				if existed {
					_ = system.WriteFileAtomic(path, previous, 0o600, 0, 0)
				} else {
					_ = os.Remove(path)
				}
				if !wasEnabled {
					_ = g.setConfigValue("features.db_provisioning", "false")
				}
				err = rlerr.Wrap(perr, rlerr.CodePrecondition,
					"those credentials did not work, so nothing was stored")
				return err
			}

			if g.JSON {
				return g.EmitJSON(map[string]any{
					"server": mongo.Redact(uri), "stored_at": path,
					"provisioning_enabled": true,
					"version":              info.Version, "topology": info.Topology,
					"auth_enabled": info.AuthEnabled,
				})
			}
			g.Printf("Connected.\n\n")
			if err = g.Fields(
				[2]string{"server", mongo.Redact(uri)},
				[2]string{"version", orDash(info.Version)},
				[2]string{"topology", orDash(info.Topology)},
				[2]string{"stored at", path + " (0600, root-owned)"},
				[2]string{"provisioning", "on"},
			); err != nil {
				return err
			}
			if !info.AuthEnabled {
				g.Log.Warn("this server does not appear to enforce authentication",
					"note", "any process that can reach the port has full access, so the "+
						"users ratline creates would not restrict anything")
			}
			g.Printf("\nNext:\n    ratline db create <name> --owner <tenant>\n")
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(new(bool), "stdin", false, "Read the connection string from stdin (the usual way)")
	f.StringVar(&fromFile, "from-file", "", "Read the connection string from a file")
	f.BoolVar(&force, "force", false, "Replace an existing connection string")

	// Reading stdin happens in PreRunE so that RunE sees a value either way, and so a
	// missing --stdin is a usage error rather than a hang.
	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		if useStdin, _ := cmd.Flags().GetBool("stdin"); !useStdin {
			return nil
		}
		if fromFile != "" {
			return rlerr.Usagef("--stdin and --from-file contradict each other")
		}
		body, err := io.ReadAll(io.LimitReader(g.Stdin, 8<<10))
		if err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "reading the connection string from stdin")
		}
		uri = strings.TrimSpace(string(body))
		return nil
	}
	return Mutating(cmd)
}

func newDBEnableCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Turn database provisioning on",
		Args:  cobra.NoArgs,
		Long: "Sets features.db_provisioning. Use 'ratline db connect' instead if you have not\n" +
			"stored a connection string yet — that does both, and proves the credentials work\n" +
			"before committing to either.\n\n" +
			"This checks that a usable connection string exists first, because turning the\n" +
			"feature on without one produces a command group that only ever refuses.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if g.Cfg.Features.DBProvisioning {
				g.Println("Database provisioning is already on.")
				return nil
			}
			mgr := &mongo.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, Bins: g.Bins}
			if _, err := mgr.AdminURI(); err != nil {
				return rlerr.Wrap(err, rlerr.CodePrecondition,
					"there is no usable connection string, so turning this on would do nothing").
					WithHint("printf 'mongodb://…' | ratline db connect --stdin")
			}
			if g.DryRun {
				g.Log.Info("would turn database provisioning on")
				return nil
			}
			if err := g.setConfigValue("features.db_provisioning", "true"); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"provisioning_enabled": true})
			}
			g.Printf("Database provisioning is on.\n\nCheck the server:\n    ratline db ping\n")
			return nil
		},
	}
	return Mutating(cmd)
}

func newDBDisableCommand(g *Globals) *cobra.Command {
	var forget bool
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Turn database provisioning off",
		Args:  cobra.NoArgs,
		Long: "Clears features.db_provisioning, so the db commands refuse rather than acting.\n\n" +
			"Nothing on the MongoDB server is touched: the databases, the users and their\n" +
			"credentials all keep working, and the sites holding those credentials keep\n" +
			"connecting. This only stops ratline managing them.\n\n" +
			"--forget also removes the stored admin connection string. Worth doing when handing\n" +
			"a server over, and worth not doing otherwise, because it is the one copy ratline\n" +
			"has.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !g.Cfg.Features.DBProvisioning && !forget {
				g.Println("Database provisioning is already off.")
				return nil
			}
			if g.DryRun {
				g.Log.Info("would turn database provisioning off", "forget_credentials", forget)
				return nil
			}
			if g.Cfg.Features.DBProvisioning {
				if err := g.setConfigValue("features.db_provisioning", "false"); err != nil {
					return err
				}
			}
			removed := false
			if forget {
				path := g.Cfg.Paths.MongoURIFile
				if path != "" && system.Exists(path) {
					if err := os.Remove(path); err != nil {
						return rlerr.Wrap(err, rlerr.CodeGeneric, "removing %s", path)
					}
					removed = true
				}
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{
					"provisioning_enabled": false, "credentials_removed": removed,
				})
			}
			g.Println("Database provisioning is off.")
			if removed {
				g.Printf("The stored connection string was removed.\n")
			}
			g.Printf("\nNothing on the MongoDB server changed. The databases, their users and\n" +
				"every site holding a credential keep working.\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&forget, "forget", false, "Also remove the stored admin connection string")
	return Mutating(cmd)
}

// setConfigValue changes one setting and writes the file, preserving its comments.
//
// Shared by the db commands so that turning a feature on goes through the same validated,
// comment-preserving path as `ratline config set` rather than a second implementation that
// could disagree with it.
func (g *Globals) setConfigValue(key, value string) error {
	path := g.configPath()
	body, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", path)
		}
		if _, err := config.Seed(path); err != nil {
			return err
		}
		if body, err = os.ReadFile(path); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", path)
		}
	}
	updated, err := config.SetValue(body, key, config.FormatScalar(value))
	if err != nil {
		return err
	}
	if err := config.Check(updated); err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition,
			"setting %s would produce a configuration that does not load, so %s is unchanged",
			key, path)
	}
	return system.WriteFileAtomic(path, updated, 0o644, system.KeepUnchanged, system.KeepUnchanged)
}
