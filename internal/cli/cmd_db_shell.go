package cli

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/mongo"
	"github.com/ALIRAZA47/ratline-cli/internal/mysql"
	"github.com/ALIRAZA47/ratline-cli/internal/redis"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// `ratline db shell` opens the engine's own client — mongosh, mysql, redis-cli — already
// authenticated with the admin credentials ratline keeps, so an operator never has to
// find, copy or type them. The work is entirely in how those credentials reach the
// client, which is never through argv: see ShellCommand in each engine package.

// dbShellRequest is one `db shell` invocation after cobra has parsed it.
type dbShellRequest struct {
	Engine    string
	Database  string
	Eval      string
	Command   []string // redis: the command after --
	Unmanaged bool
}

func newDBShellCommand(g *Globals) *cobra.Command {
	var (
		eval      string
		unmanaged bool
	)
	cmd := &cobra.Command{
		Use:   "shell [<database>]",
		Short: "Open mongosh, mysql or redis-cli logged in with ratline's admin credentials",
		Args:  cobra.ArbitraryArgs,
		Long: "Starts the engine's own interactive client, connected to the server ratline\n" +
			"manages and authenticated with the admin credentials it already holds, so there\n" +
			"is nothing to look up and nothing to paste.\n\n" +
			"Those are the only credentials ratline has. It never stores a database user's\n" +
			"password — MongoDB and MySQL keep a hash, and ratline shows a new password once —\n" +
			"so this session is the admin, not the application's user: every database on\n" +
			"the server is reachable from it, and it can change anything. Per engine:\n\n" +
			"    mongo   the URI in paths.mongo_uri_file, handed to mongosh in its\n" +
			"            environment; mongosh starts with --nodb and connects from inside\n" +
			"    mysql   the [client] file at paths.mysql_defaults_file, read by the client\n" +
			"            through --defaults-extra-file — the file, never the password\n" +
			"    redis   the URI in paths.redis_uri_file: host and port as flags, the\n" +
			"            password in REDISCLI_AUTH\n\n" +
			"No password, and no URI that contains one, appears in any process's argv,\n" +
			"which every account on the server can read in /proc.\n\n" +
			"A database name selects it for the session, and has to be one ratline created —\n" +
			"a typo is refused rather than silently opening an empty database. --unmanaged\n" +
			"allows one ratline has no record of. Redis takes no name: a ratline keyspace is\n" +
			"a key prefix guarded by an ACL user, not something a session can select.\n\n" +
			"--eval runs one thing and exits instead of opening a prompt — JavaScript for\n" +
			"mongosh, SQL for mysql (written to the client's stdin). For Redis the command\n" +
			"goes after --, and is written to redis-cli's stdin too. An --eval for mongosh is\n" +
			"an argument, visible in the process table while it runs, so a password does not\n" +
			"belong in one; use the prompt for that. Without --eval or a command, standard\n" +
			"input has to be a terminal.\n\n" +
			"There is no flag to point the session at a different server or user, and the\n" +
			"client's own connection flags are refused: that is what 'ratline db connect' is\n" +
			"for. The session does not hold ratline's lock, so certificate renewals and\n" +
			"deploys carry on while it is open. --dry-run prints the command and the names of\n" +
			"the environment variables it would set, and starts nothing.",
		Example: "  ratline db shell\n" +
			"  ratline db shell shop\n" +
			"  ratline db shell shop --eval 'db.orders.countDocuments()'\n" +
			"  ratline db shell shop --engine mysql\n" +
			"  ratline db shell shop --engine mysql --eval 'SHOW TABLES'\n" +
			"  ratline db shell --engine redis\n" +
			"  ratline db shell --engine redis -- GET shop:counter",
		RunE: func(cmd *cobra.Command, args []string) error {
			engine, err := g.dbEngineChoice(cmd)
			if err != nil {
				return err
			}
			req := dbShellRequest{Engine: engine, Eval: eval, Unmanaged: unmanaged}
			before, after := args, []string(nil)
			if at := cmd.ArgsLenAtDash(); at >= 0 {
				before, after = args[:at], args[at:]
			}
			switch len(before) {
			case 0:
			case 1:
				req.Database = before[0]
			default:
				return rlerr.Usagef("one database at most, got %d: %s", len(before), strings.Join(before, " ")).
					WithHint("a Redis command goes after --: ratline db shell --engine redis -- GET key")
			}
			req.Command = after
			return g.dbShell(cmd.Context(), req)
		},
		ValidArgsFunction: g.completeDatabases,
	}
	f := cmd.Flags()
	f.StringVarP(&eval, "eval", "e", "",
		"Run this and exit instead of opening a prompt (JavaScript for mongo, SQL for mysql)")
	f.BoolVar(&unmanaged, "unmanaged", false,
		"Allow a database ratline has no record of")
	// Mutating because the session can change anything, which also makes the panel's
	// fail-safe treat it as a mutation and --dry-run start nothing. SkipLock because the
	// lock is held for the life of the process, and a prompt somebody leaves open over
	// lunch must not stop the renewal timer and every deploy until they come back.
	return SkipLock(Mutating(cmd))
}

// connectionFlags are the client options that would point a session at a different
// server or identity. None of them can reach a client through `db shell` — values go in
// as single --name=value elements or on stdin — but a word that looks like one is
// refused by name, so the operator learns why rather than meeting a client error.
var connectionFlags = map[string]bool{
	"host": true, "port": true, "user": true, "username": true, "password": true, "pass": true,
	"uri": true, "socket": true, "protocol": true, "tls": true, "ssl": true,
	"defaults-file": true, "defaults-extra-file": true, "defaults-group-suffix": true,
	"login-path": true, "no-defaults": true, "print-defaults": true,
	"authenticationDatabase": true, "authenticationMechanism": true, "nodb": true,
	"askpass": true, "sni": true, "cacert": true, "cert": true, "key": true,
	"h": true, "p": true, "P": true, "u": true, "a": true, "S": true, "n": true,
}

// refuseConnectionOverride refuses any argument shaped like an option: a database name
// and a Redis command's name never legitimately begin with a dash, and one that does is
// either a typo or an attempt to reach a different server with ratline's password.
func refuseConnectionOverride(what string, args []string) error {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") || a == "-" {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if connectionFlags[name] {
			return rlerr.Usagef("%s %q would change which server or account the session uses", what, a).
				WithHint("db shell connects with the credentials ratline stores, and only those; " +
					"to change them, run 'ratline db connect'")
		}
		return rlerr.Usagef("%s %q looks like an option, and db shell passes no options to the client", what, a).
			WithHint("ratline's own flags go before --; anything after it is a Redis command")
	}
	return nil
}

// validate checks the request against itself and the terminal, before anything is read.
func (r dbShellRequest) validate(stdinTTY, json bool) error {
	if json {
		return rlerr.Usagef("db shell has no --json form").
			WithHint("the client writes its own output; for a machine-readable answer use " +
				"'ratline db show' or 'ratline db list --live'")
	}
	if err := refuseConnectionOverride("the database", []string{r.Database}); err != nil {
		return err
	}
	// Only the command's name: its arguments are values (INCRBY k -5), written quoted to
	// stdin where nothing reads them as options.
	if len(r.Command) > 0 {
		if err := refuseConnectionOverride("the command", r.Command[:1]); err != nil {
			return err
		}
	}
	switch r.Engine {
	case engineRedis:
		if r.Database != "" {
			return rlerr.Usagef("Redis has no database to select").
				WithHint("a ratline keyspace is a key prefix that an ACL user is confined to; this "+
					"session is the admin and sees them all — try: SCAN 0 MATCH %s:*", r.Database)
		}
		if r.Eval != "" {
			return rlerr.Usagef("--eval is for mongo and mysql").
				WithHint("give the Redis command after --: ratline db shell --engine redis -- %s", r.Eval)
		}
		if r.Unmanaged {
			return rlerr.Usagef("--unmanaged has nothing to allow without a database, and Redis takes none")
		}
	default:
		if len(r.Command) > 0 {
			return rlerr.Usagef("a command after -- is for Redis").
				WithHint("for %s, pass it with --eval", r.Engine)
		}
		if r.Unmanaged && r.Database == "" {
			return rlerr.Usagef("--unmanaged names no database").
				WithHint("it allows a database ratline has no record of: ratline db shell <name> --unmanaged")
		}
	}
	if r.interactive() && !stdinTTY {
		return rlerr.Usagef("db shell opens an interactive prompt, and standard input is not a terminal").
			WithHint("run one thing with --eval (mongo, mysql) or after -- (redis), " +
				"or connect with 'ssh -t' so there is a terminal")
	}
	return nil
}

func (r dbShellRequest) interactive() bool { return r.Eval == "" && len(r.Command) == 0 }

// dbShell resolves the session and runs it.
func (g *Globals) dbShell(ctx context.Context, req dbShellRequest) error {
	if err := req.validate(g.StdinTTY, g.JSON); err != nil {
		return err
	}
	term := shellTerm(os.Getenv("TERM"))

	var (
		cmd    system.Cmd
		server string
	)
	switch req.Engine {
	case engineMySQL:
		mgr, st, err := g.mysqlManager(ctx)
		if err != nil {
			return err
		}
		// Validated by the engine's own rule before it is used as a lookup key.
		if cmd, server, err = mgr.ShellCommand(mysql.ShellOptions{
			Database: req.Database, Execute: req.Eval, Term: term,
		}); err != nil {
			return err
		}
		if req.Database != "" && !req.Unmanaged {
			if _, err := st.GetEngineDatabase(ctx, engineMySQL, req.Database); err != nil {
				return unrecordedDatabase(err, req)
			}
		}
	case engineRedis:
		mgr, _, err := g.redisManager(ctx)
		if err != nil {
			return err
		}
		if cmd, server, err = mgr.ShellCommand(redis.ShellOptions{Command: req.Command, Term: term}); err != nil {
			return err
		}
	default:
		mgr, st, err := g.dbManager(ctx)
		if err != nil {
			return err
		}
		if cmd, server, err = mgr.ShellCommand(mongo.ShellOptions{
			Database: req.Database, Eval: req.Eval, Term: term,
		}); err != nil {
			return err
		}
		if req.Database != "" && !req.Unmanaged {
			if _, err := st.GetDatabase(ctx, req.Database); err != nil {
				return unrecordedDatabase(err, req)
			}
		}
	}

	if g.DryRun {
		// The argv is safe to print — that is the design — and the environment is
		// named but never shown, because it is where the secret is.
		g.Log.Info("would open a database shell", "engine", req.Engine, "server", server,
			"cmd", log.ArgvString(append([]string{cmd.Name}, cmd.Args...)),
			"env", strings.Join(envNames(cmd.Env), " "),
			"stdin", map[bool]string{true: "the terminal", false: "the command"}[cmd.Stdin == nil])
		return nil
	}

	// The terminal's streams, unless the engine has put the one-off command on stdin.
	if cmd.Stdin == nil {
		cmd.Stdin = g.Stdin
	}
	cmd.Stdout, cmd.Stderr = g.Stdout, g.Stderr
	if req.interactive() {
		g.Log.Info("connecting as the admin user; every database on this server is reachable from here",
			"engine", req.Engine, "server", server)
	}
	_, err := g.Runner.Run(ctx, cmd)
	return err
}

// unrecordedDatabase turns a missing state row into the refusal db shell means by it.
func unrecordedDatabase(err error, req dbShellRequest) error {
	if !errors.Is(err, state.ErrNotFound) {
		return err
	}
	flag := ""
	if req.Engine != engineMongo {
		flag = " --engine " + req.Engine
	}
	return rlerr.Wrap(err, rlerr.CodePrecondition, "ratline has no %s database called %s", req.Engine, req.Database).
		WithHint("'ratline db list%s' shows the ones it made; add --unmanaged to open one it did not", flag)
}

// envNames lists the variable names in an environment block, sorted.
func envNames(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// shellTerm passes the operator's TERM through to an interactive client, which needs it
// to edit lines, and drops anything that is not a plain terminal name. Every other
// child gets TERM=dumb; this one has a person typing at it.
func shellTerm(t string) string {
	if t == "" || len(t) > 64 {
		return ""
	}
	for _, c := range t {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._+-", c)) {
			return ""
		}
	}
	return t
}
