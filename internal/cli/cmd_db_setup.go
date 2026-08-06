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
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
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
			"With no flags it prompts, and what you paste is not echoed. It is never a flag\n" +
			"value: anything in argv is world-readable through /proc for as long as the command\n" +
			"runs, and it would land in your shell history, which outlives the password.\n\n" +
			"Do not pipe it through printf. A password containing a % is read as a format verb\n" +
			"and the string arrives truncated, usually with no host in it. The prompt has\n" +
			"nothing in between.\n\n" +
			"It is written to paths.mongo_uri_file at 0600, root-owned, in a 0700 directory.",
		Example: "  # paste it at the prompt: not echoed, not in argv, not in shell history\n" +
			"  ratline db connect\n\n" +
			"  # for automation, where there is no terminal\n" +
			"  ratline db connect --stdin < /root/mongodb.uri\n" +
			"  ratline db connect --from-file /root/atlas.uri\n\n" +
			"  ratline db ping",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			switch {
			case fromFile != "":
				body, err := os.ReadFile(fromFile)
				if err != nil {
					return rlerr.Wrap(err, rlerr.CodePrecondition, "reading %s", fromFile)
				}
				// The same reader AdminURI uses, so a file ratline accepts here is a file
				// ratline can read back. Writing that tolerance into only one of the two
				// meant a hand-written file with a comment at the top — the whole reason
				// --from-file exists — was refused on the way in and fine on the way out.
				if uri, err = mongo.ParseURIFile(string(body), fromFile); err != nil {
					return err
				}
			case uri != "":
				// Already read from stdin, possibly a redirected file: same rules.
				var err error
				if uri, err = mongo.ParseURIFile(uri, "the connection string on stdin"); err != nil {
					return err
				}
			case g.CanPrompt():
				// Asked for, rather than demanded on stdin.
				//
				// Telling people to pipe it in with `printf` was wrong, and it broke a real
				// setup: printf reads `%` in the password as a format verb, so a perfectly
				// good connection string arrived truncated at the percent sign with no host
				// in it. `!` has the same problem under history expansion. A prompt has no
				// such layer — nothing between the paste and the variable — and it keeps the
				// secret out of argv and shell history just as well, which was the actual
				// reason for the rule.
				var err error
				if uri, err = g.readSecret(
					"MongoDB admin connection string (not echoed): "); err != nil {
					return err
				}
				uri = strings.TrimSpace(uri)
			default:
				return rlerr.InputRequiredf("no connection string was given").
					WithHint("run 'ratline db connect' on a terminal and paste it at the prompt.\n" +
						"        For automation, without a terminal:\n" +
						"        ratline db connect --stdin < /root/mongodb.uri\n" +
						"        ratline db connect --from-file /root/mongodb.uri\n\n" +
						"        It is not a flag on purpose: argv is world-readable " +
						"through /proc, and it would land in your shell history")
			}

			// Validated before anything is written. This used to check only the prefix, and
			// the real parse happened when the file was read back — so a mangled string was
			// stored, then rejected by a message naming a file the operator had never
			// touched, from a command that said in the same breath that nothing was stored.
			if err := validate.MongoURI(uri); err != nil {
				return err
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
			// Written with a header, because the next person to find this file will be
			// somebody auditing /etc who does not know what it is. Comments and blank
			// lines are skipped on the way back in, so the note costs nothing.
			body := "# " + system.ManagedHeader + "\n" +
				"# MongoDB admin connection string. This is the root credential for every\n" +
				"# database on that server, which is why this file is 0600 and root-owned\n" +
				"# and why it is not in config.yaml.\n" +
				"#\n" +
				"# Replace it with:  ratline db connect --force\n" +
				"# Check it with:    ratline db ping\n\n" +
				uri + "\n"
			if err = system.WriteFileAtomic(path, []byte(body), 0o600, 0, 0); err != nil {
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
	f.BoolVar(new(bool), "stdin", false, "Read the connection string from stdin (for automation; a terminal is prompted)")
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
					WithHint("ratline db connect")
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
