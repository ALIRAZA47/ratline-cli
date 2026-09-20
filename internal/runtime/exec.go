package runtime

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// One command, run as a site's tenant, under the conditions the site's own build runs
// under: the tenant's identity, the application directory, the site's .env, and a PATH
// that starts with whatever interpreter the site is pinned to.
//
// It exists because there was no way to run `npm run bootstrap` — a seed, a migration,
// a one-off management command — against a site. The managed runtimes are deliberately
// not on anyone's PATH (they are per-site, and a login shell is not what systemd reads),
// so the honest answer was a hand-built `sudo -u acme env PATH=/opt/ratline/runtimes/…`
// incantation that nothing validated, nothing recorded and nobody could remember. Every
// such incantation is a chance to run the wrong interpreter as the wrong user.
//
// It is not a shell. The argv arrives already split — `site exec <domain> -- npm run
// bootstrap` — and is handed to execve verbatim, so no string ever becomes a command
// line. Anything wanting a pipeline belongs in a script in the repository, which is the
// same answer `site cron add` and `site hook set` give.

// ExecPlan is what an exec resolved to, before anything ran.
//
// Separate from running it because a rehearsal has to be able to print exactly what
// would happen without doing any of it — and because the two things an operator wants
// to check first are which program was found and which PATH found it.
//
// It deliberately carries no environment. The plan is printed by --dry-run and emitted
// under --json, and the site's .env is in that environment: a DATABASE_URL belongs in
// neither. Only the PATH is here, which is ratline's own and holds no secret.
type ExecPlan struct {
	// Program is the absolute path that will be executed.
	Program string
	// Args are the arguments after the program.
	Args []string
	// Dir is the working directory: the site's application directory.
	Dir string
	// User is the tenant the command runs as.
	User string
	// Path is the PATH the command will run with.
	Path string
}

// CommandLine renders the invocation for a human. Nothing parses or executes it.
func (p *ExecPlan) CommandLine() string {
	return strings.Join(append([]string{p.Program}, p.Args...), " ")
}

// ExecOptions are the caller's choices about how the command is run.
type ExecOptions struct {
	// Timeout bounds the command. Zero means the runner's default.
	Timeout time.Duration
	// Stdout and Stderr receive the child's output as it arrives. Left nil — which is
	// what --json does, because stdout has to stay one parseable object — the output is
	// captured in the Result instead.
	Stdout io.Writer
	Stderr io.Writer
	// Stdin is handed to the child. Nil gives it /dev/null, which is what a command run
	// from a timer or through the panel must have: a child that blocks on a read nobody
	// is going to answer looks exactly like a hang.
	Stdin io.Reader
}

// execShellTokens are argv elements that mean nothing outside a shell.
//
// A refusal rather than a pass-through. `site exec app -- npm run build && npm start`
// is a thing somebody will type, and handing "&&" to npm as an argument does something
// baffling instead of what they meant. `;` is not here on purpose: `find … -exec rm {} ;`
// passes one legitimately, and refusing it would break a real command to catch a typo.
var execShellTokens = map[string]bool{
	"|": true, "||": true, "&&": true, "&": true,
	">": true, ">>": true, "<": true, "<<": true,
}

// PlanExec resolves what `site exec` would run, and refuses what it will not.
//
// This is where the values enter, so this is where they are checked — the CLI and the
// panel both arrive here, and neither gets to skip it.
func PlanExec(c *Context, argv []string) (*ExecPlan, error) {
	if len(argv) == 0 {
		return nil, rlerr.Usagef("nothing to run").
			WithHint("ratline site exec %s -- npm run bootstrap", c.Site.Domain)
	}
	// The same check the runner applies before execve. Here as well as there so the
	// message names the argument rather than appearing from inside the machinery.
	if err := system.ValidateArgv(argv); err != nil {
		return nil, err
	}
	for i, a := range argv {
		if execShellTokens[a] {
			return nil, rlerr.Usagef("argument %d is %q, which nothing here interprets", i+1, a).
				WithHint("the program is run directly, not through a shell — put the " +
					"pipeline in a script in the repository and run that")
		}
	}

	program := argv[0]
	if i := strings.IndexAny(program, "|&;<>`$()"); i >= 0 {
		return nil, rlerr.Usagef("%q is a shell line, not a program", program).
			WithHint("everything after -- is an argv: the first word is the program and " +
				"the rest are its arguments, with no shell in between")
	}

	// The same resolution the build command gets, which is the point: `npm` has to mean
	// the npm belonging to the version this site is pinned to, on a server that has no
	// system Node at all.
	//
	// Cleaned because the runner refuses a path that is not, and an operator's
	// /home/acme/app/./bin/seed should not come back as an internal error.
	//
	// Root stats a path inside the tenant's tree here, and the tenant can swap what is
	// underneath it before the exec. That is not a hole to close: the exec happens as
	// the tenant, so the most a race wins them is running their own code as themselves,
	// which they had already. Nothing root-owned is read through this path — the
	// trusted-path helpers exist for the cases where something is.
	path := filepath.Clean(resolveProgram(program, c))
	fi, err := os.Stat(path)
	switch {
	case err != nil:
		// One hint, composed rather than overwritten: WithHint replaces, and where it
		// looked is the detail that stops an operator from concluding the server needs
		// a system-wide Node — which is the one thing managed runtimes exist to avoid.
		hint := "looked in " + strings.Join(programSearchDirs(c), ", ") + ", then the system path"
		if !HasApplicationCode(c.AppDir) {
			// The likeliest reason a project's own tooling is missing is that the
			// project is not there yet, and "no such program" does not say so.
			hint = c.Site.Domain + " has no application code yet — deploy it first with " +
				"'ratline site deploy " + c.Site.Domain + " --pull --install'. Otherwise: " + hint
		}
		return nil, rlerr.Preconditionf("%s: no such program for %s", program, c.Site.Domain).
			WithHint("%s", hint)
	case fi.IsDir():
		return nil, rlerr.Preconditionf("%s is a directory, not a program", path)
	case fi.Mode()&0o111 == 0:
		return nil, rlerr.Preconditionf("%s is not executable", path).
			WithHint("as the tenant: chmod +x %s", path)
	}

	user := ""
	if c.Identity != nil {
		user = c.Identity.Name
	}
	return &ExecPlan{
		Program: path,
		Args:    argv[1:],
		Dir:     c.AppDir,
		User:    user,
		Path:    execPath(c),
	}, nil
}

// RunExec runs a resolved plan as the tenant.
func RunExec(ctx context.Context, c *Context, p *ExecPlan, opts ExecOptions) (*system.Result, error) {
	c.Log.Info("exec", "domain", c.Site.Domain, "as", p.User, "program", p.Program)
	return runAsOwner(ctx, c, system.Cmd{
		Path:    p.Program,
		Args:    p.Args,
		Dir:     p.Dir,
		Env:     tenantEnv(c, "RATLINE_DOMAIN="+c.Site.Domain),
		Timeout: opts.Timeout,
		Stdout:  opts.Stdout,
		Stderr:  opts.Stderr,
		Stdin:   opts.Stdin,
		// The program's own name, so a failure reads "npm failed (exit 1)" rather than
		// naming a ratline internal nobody typed.
		Label: filepath.Base(p.Program),
	})
}
