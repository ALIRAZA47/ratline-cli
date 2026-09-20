package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/runtime"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// `ratline site exec` — run one command as a site's tenant.
//
// The gap it fills: a site is pinned to a managed runtime under /opt/ratline/runtimes,
// deliberately not on anybody's PATH, so `npm run bootstrap` — a seed, a migration, a
// one-off management command — had no home. `site deploy` runs a fixed chain, a hook runs
// on every deploy, and a job needs a schedule and a unit. What was left was a hand-built
// `sudo -u acme env PATH=…` line that nothing validated and nothing recorded.
//
// Root only, like every other command that acts on a tenant's tree, and mutating: it holds
// the server lock for the duration and is bounded by --timeout, because an exec that ran
// for ever would be an exec that stopped every renewal on the box.

func newSiteExecCommand(g *Globals) *cobra.Command {
	var (
		timeout   time.Duration
		passStdin bool
		cwd       string
	)
	cmd := &cobra.Command{
		// `<command>` rather than `<program> [args...]`, and it is load-bearing: the
		// menu and the panel both build a field per positional out of this line, so
		// an "args..." box would collect `run bootstrap` as one argument and hand npm
		// a single word. One field, split by the parser that splits a build command.
		// Typing a real argv after -- still works; cobra sets no upper bound.
		Use:   "exec <domain> -- <command>",
		Short: "Run a command as the site's tenant, with the site's environment",
		// Cobra's own "requires at least 2 arg(s), only received 1" is true and tells
		// an operator nothing about what the second one is. The shape is the answer.
		Args: func(_ *cobra.Command, args []string) error {
			switch len(args) {
			case 0:
				return rlerr.Usagef("which site?").
					WithHint("ratline site exec <domain> -- npm run bootstrap")
			case 1:
				return rlerr.Usagef("nothing to run on %s", args[0]).
					WithHint("the command goes after --, for example "+
						"'ratline site exec %s -- npm run bootstrap'", args[0])
			}
			return nil
		},
		Long: "One command, under exactly the conditions the site's own build runs under: as\n" +
			"the tenant, in the application directory, with the site's .env loaded and the\n" +
			"runtime the site is pinned to first on PATH. So 'npm' is the npm belonging to\n" +
			"this site's Node version, and 'python' is the one in its venv, on a server that\n" +
			"has neither installed system-wide.\n\n" +
			"Nothing is interpreted by a shell. Everything after -- is an argv: the first\n" +
			"word is the program and the rest are its arguments, passed through untouched.\n" +
			"A single quoted argument is split the same way a build command or a hook is —\n" +
			"'npm run bootstrap' works — and either form refuses a pipe or an && rather than\n" +
			"handing it to the program as a word. Anything that genuinely needs a shell\n" +
			"belongs in a script in the repository, which this can then run.\n\n" +
			"The command's own output is this command's output, so it pipes. Its exit code\n" +
			"is reported but not adopted: ratline's exit codes are a contract, and a program\n" +
			"exiting 2 does not mean ratline was called wrongly. A failing command exits 4\n" +
			"(external) and names the code it gave; under --json the envelope carries it.\n\n" +
			"It runs in the application directory unless --cwd names another, which is how\n" +
			"a build output with its own package.json is reached: a Next.js standalone\n" +
			"directory carries its own package.json, node_modules and server.js, and npm\n" +
			"run there means that project rather than the one above it. The path is\n" +
			"relative to the application directory and has to resolve inside the site.\n\n" +
			"Standard input is /dev/null unless --stdin is given, so a program that reads\n" +
			"from it ends rather than waiting for somebody who is not there. Secrets do not\n" +
			"belong in the argv — it is world-readable in /proc while the command runs — so\n" +
			"put them in the site's environment with 'site env set' and read them there.",
		Example: "  ratline site exec app.example.com -- npm run bootstrap\n" +
			"  ratline site exec app.example.com -- npx prisma migrate deploy\n" +
			"  ratline site exec api.example.com -- python manage.py migrate\n" +
			"  ratline site exec app.example.com --dry-run -- ./bin/seed\n\n" +
			"  # a build output that carries its own package.json\n" +
			"  ratline site exec app.example.com --cwd .next/standalone -- npm ci --omit=dev\n\n" +
			"  # flags for the program go after --, or ratline reads them as its own\n" +
			"  ratline site exec app.example.com -- npm run build --if-present",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			argv, err := execArgv(args[1:])
			if err != nil {
				return err
			}
			st, err := g.Store(ctx)
			if err != nil {
				return err
			}
			site, err := st.FindSiteByName(ctx, args[0])
			if err != nil {
				return err
			}
			id, err := system.LookupIdentity(site.Owner)
			if err != nil {
				return err
			}
			return g.execInSite(ctx,
				runtime.NewContext(g.Cfg, g.Log, g.Runner, site, id, g.DryRun),
				argv, siteExecOptions{Timeout: timeout, Stdin: passStdin, Cwd: cwd})
		},
	}
	f := cmd.Flags()
	f.StringVar(&cwd, "cwd", "",
		"Run in this directory instead of app/ (relative to app/, and inside the site)")
	f.DurationVar(&timeout, "timeout", 0,
		"Give up after this long (default: runtimes.build_timeout)")
	// Named --stdin like `env set --stdin` and `db connect --stdin`, but it does the
	// opposite thing: those read a secret ratline then uses, this one pipes whatever
	// arrives straight through to the program. The usage line has to say so, because
	// the other spelling is a term of art in this tool.
	f.BoolVar(&passStdin, "stdin", false,
		"Pipe this command's standard input through to the program")
	return ProgramArgv(Mutating(cmd))
}

// siteExecOptions are the flags, separated from cobra so the body below is reachable
// from a test. The root check runs in the command's PreRun, so anything left inside
// RunE can only be exercised on a server — and a test that never reaches the code it
// names is worse than no test.
type siteExecOptions struct {
	Timeout time.Duration
	Stdin   bool
	Cwd     string
}

// execArgv turns the positional arguments after the domain into the program's argv.
//
// One argument holding a whole command line is what the interactive menu collects and
// what somebody writing a script types first. It is split by the same parser that splits
// a build command and a hook — no shell, and the same refusal of a pipeline — so the two
// spellings cannot come to mean different things.
func execArgv(args []string) ([]string, error) {
	if len(args) == 1 && strings.ContainsAny(args[0], " \t") {
		parsed, err := system.ParseCommand(args[0])
		if err != nil {
			return nil, err
		}
		return parsed.Argv, nil
	}
	return args, nil
}

func (g *Globals) execInSite(ctx context.Context, rc *runtime.Context, argv []string, o siteExecOptions) error {
	// Resolved before anything runs, so a rehearsal can print what would happen rather
	// than running the command to find out — and so a typo in the program name is
	// reported against the directories that were searched, instead of arriving later as
	// execve's opinion about /usr/bin.
	plan, err := runtime.PlanExec(rc, argv, o.Cwd)
	if err != nil {
		return err
	}

	if g.DryRun {
		if g.JSON {
			return g.EmitJSON(map[string]any{
				"domain": rc.Site.Domain, "user": plan.User, "dir": plan.Dir,
				"program": plan.Program, "args": plan.Args,
				"path": plan.Path, "dry_run": true,
			})
		}
		g.Printf("would run as %s in %s:\n    %s\n", plan.User, plan.Dir, plan.CommandLine())
		g.Printf("with PATH=%s\n", plan.Path)
		return nil
	}

	timeout := o.Timeout
	if timeout <= 0 {
		timeout = g.Cfg.Runtimes.BuildTimeout.D()
	}
	opts := runtime.ExecOptions{Timeout: timeout}
	if !g.JSON {
		// Raw, as it arrives, because the command's output is the product. Under --json
		// it is captured into the envelope instead: stdout has to stay exactly one
		// parseable object.
		opts.Stdout, opts.Stderr = g.Stdout, g.Stderr
	}
	if o.Stdin {
		opts.Stdin = g.Stdin
	}

	res, err := runtime.RunExec(ctx, rc, plan, opts)
	if g.JSON && res != nil {
		// Emitted whether the command succeeded or not, and then the error is returned
		// for the exit code. One envelope carrying the exit code and the output beats a
		// second object repeating what the exit code already says.
		if jerr := g.EmitJSON(map[string]any{
			"domain": rc.Site.Domain, "user": plan.User, "dir": plan.Dir,
			"program": plan.Program, "args": plan.Args,
			"exit_code": res.ExitCode, "duration_ms": res.Duration.Milliseconds(),
			"stdout": res.Stdout, "stderr": res.Stderr,
		}); jerr != nil {
			return jerr
		}
	}
	return err
}
