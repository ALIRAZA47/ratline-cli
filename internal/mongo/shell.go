package mongo

import (
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// ShellConnect is the JavaScript `db shell` hands mongosh to connect, as its first
// --eval. It is a constant: nothing an operator types is ever spliced into it.
//
// The admin URI reaches it the same way it reaches op.js — in the environment, read as
// process.env.RATLINE_MONGO_URI — because mongosh's usual form, the URI as its first
// argument, puts the admin password in /proc/PID/cmdline for every account on the box.
// With --nodb mongosh starts unconnected and this line connects from inside. Assigning
// to `db` is mongosh's own way of switching the session's database, so the prompt that
// follows is connected and pointed at RATLINE_MONGO_DB when one was asked for.
//
// It is an --eval rather than a staged --file, the shape every other operation uses,
// for one reason found by reading mongosh's startup: every --eval runs before any file.
// A one-off `--eval` from the operator would then run unconnected. Two --evals run in
// order, so connecting in the first leaves the second, and the interactive prompt
// --shell keeps open, talking to the server. It is also one line because ratline's
// argv validation refuses a newline in any argument.
//
// After connecting it deletes the URI from the session's process.env, so nothing typed
// at the prompt — and nothing the prompt spawns — can print the admin password back.
// `void 0` keeps mongosh from echoing the connection object as a result.
const ShellConnect = "db = connect(process.env.RATLINE_MONGO_URI); " +
	"if (process.env.RATLINE_MONGO_DB) { db = db.getSiblingDB(process.env.RATLINE_MONGO_DB); } " +
	"delete process.env.RATLINE_MONGO_URI; void 0"

// ShellOptions shapes one `db shell` session.
type ShellOptions struct {
	// Database selects the session's database. Optional; validated here.
	Database string
	// Eval, when set, runs this JavaScript and exits instead of opening a prompt.
	Eval string
	// Term is the operator's TERM, already sanitised by the caller, so the prompt can
	// edit lines. Empty leaves MinimalEnv's dumb terminal.
	Term string
}

// ShellCommand builds the mongosh invocation for `db shell`, connected with the admin
// credentials at paths.mongo_uri_file. It returns the command and a redacted name for
// the server, for telling the operator where they are.
//
// The command is Attach (it owns the terminal) and Mutates (anything can be typed at
// it, so --dry-run must not start it).
func (m *Manager) ShellCommand(opts ShellOptions) (system.Cmd, string, error) {
	if opts.Database != "" {
		if err := validate.DatabaseName(opts.Database); err != nil {
			return system.Cmd{}, "", err
		}
	}
	if m.Bins != nil && !m.Bins.Available("mongosh") {
		return system.Cmd{}, "", rlerr.Preconditionf("mongosh is not installed").
			WithHint("apt-get install mongodb-mongosh, or see " +
				"https://www.mongodb.com/docs/mongodb-shell/install/")
	}
	uri, err := m.AdminURI()
	if err != nil {
		return system.Cmd{}, "", err
	}

	// Every value travels as one --name=value element. mongosh's parser reads the
	// two-element form's value as a flag when it begins with a dash, so `--eval
	// --host=…` would have been a different server rather than some JavaScript.
	args := []string{"--nodb", "--quiet"}
	if opts.Eval == "" {
		args = append(args, "--shell")
	}
	args = append(args, "--eval="+ShellConnect)
	if opts.Eval != "" {
		args = append(args, "--eval="+opts.Eval)
	}

	env := []string{"RATLINE_MONGO_URI=" + uri}
	if opts.Database != "" {
		env = append(env, "RATLINE_MONGO_DB="+opts.Database)
	}
	if opts.Term != "" {
		env = append(env, "TERM="+opts.Term)
	}
	return system.Cmd{
		Name:    "mongosh",
		Args:    args,
		Env:     system.MinimalEnv(env...),
		Attach:  true,
		Mutates: true,
		Label:   "mongosh",
	}, Redact(uri), nil
}
