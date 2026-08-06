package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// `ratline new` provisions a whole stack in one command.
//
// Everything here is already possible with four commands. What is not possible by hand is
// the part that matters: if the database fails, the tenant and the site that were created
// thirty seconds earlier are still there, and the operator is left reconciling a half-built
// stack against a command that has already exited. Running four commands in a shell gives
// you four independent transactions; this gives you one.
//
// It composes the commands rather than the managers. Every step is the same code path an
// operator gets by typing it, with the same validation, the same refusals and the same
// messages — so this cannot develop its own opinion about what a node site is, and a flag
// added to `site add` tomorrow is available here tomorrow. The cost is that compensation is
// explicit rather than free: undoing a step means running its delete, which is exactly what
// the operator would do.

// stack is one provisioning run.
type stack struct {
	g *Globals

	Domain string
	Owner  string
	SSHKey string

	WithDB bool
	DBName string
	EnvKey string

	TLS   bool
	Email string

	// siteArgs are the runtime-specific flags, built by each recipe.
	siteArgs []string

	// done records what to undo, innermost last.
	done []step
}

// step is one command to run, and how to take it back.
type step struct {
	what string
	argv []string
	// undo is nil for a step that must not be undone: one that built something we did not
	// create, or one whose cost has already been paid. kept says why, in a sentence the
	// preview can print — a step that is not taken back is exactly what somebody reading a
	// preview needs told, and leaving it implicit is how it gets missed.
	undo []string
	kept string
}

// plan is everything this run has decided, before any of it happens.
//
// Deciding first and executing second is what makes --dry-run answerable and keeps the
// closing summary honest: both read the same list, so the commands printed at the end are
// the commands that ran rather than a second derivation that can drift from them.
type plan struct {
	steps []step
	notes []string
}

// inherited carries the global flags into each step.
//
// Each step builds a fresh root command, and binding the flags writes their defaults back
// over the fields — so --dry-run on the composite would silently become a real run three
// steps in. They have to be passed explicitly rather than assumed to survive.
func (s *stack) inherited() []string {
	var out []string
	if s.g.DryRun {
		out = append(out, "--dry-run")
	}
	if s.g.Yes {
		out = append(out, "--yes")
	}
	if s.g.Quiet {
		out = append(out, "--quiet")
	}
	if s.g.NoInput {
		out = append(out, "--no-input")
	}
	return out
}

// run executes one step and records how to undo it.
func (s *stack) run(ctx context.Context, st step) error {
	full := append(append([]string{}, st.argv...), s.inherited()...)
	s.g.Printf("\n→ %s\n", st.what)
	if err := s.g.runArgv(ctx, full); err != nil {
		return err
	}
	if st.undo != nil && !s.g.DryRun {
		s.done = append(s.done, st)
	}
	return nil
}

// unwind undoes what was built, most recent first.
//
// Best effort, and loud about it: a compensation that fails leaves something behind, and
// the operator needs to know which thing rather than being told the whole command failed.
func (s *stack) unwind(ctx context.Context) {
	for i := len(s.done) - 1; i >= 0; i-- {
		st := s.done[i]
		s.g.Log.Warn("undoing", "step", st.what)
		argv := append(append([]string{}, st.undo...), "--yes")
		if err := s.g.runArgv(ctx, argv); err != nil {
			s.g.Log.Error("could not undo a step; it is still there",
				"step", st.what, "fix", strings.Join(st.undo, " "), "err", err)
		}
	}
}

// plan decides every step and runs none of them.
//
// Everything the composite chooses on the operator's behalf is resolved here — whether the
// tenant needs creating, the database name derived from the domain — so that --dry-run has
// something true to print and the summary at the end has one source.
func (s *stack) plan(ctx context.Context) (plan, error) {
	var p plan
	if _, err := validate.Domain(s.Domain); err != nil {
		return p, err
	}
	if err := validate.Username(s.Owner); err != nil {
		return p, err
	}

	// The tenant, if it is not already there. An existing one is left alone and is not
	// undone on failure: it was not ours to create, so it is not ours to remove.
	st, err := s.g.Store(ctx)
	if err != nil {
		return p, err
	}
	if _, gerr := st.GetUser(ctx, s.Owner); gerr != nil {
		argv := []string{"user", "add", s.Owner}
		if s.SSHKey != "" {
			argv = append(argv, "--ssh-key", s.SSHKey)
		}
		p.steps = append(p.steps, step{
			what: "creating the tenant " + s.Owner,
			argv: argv,
			undo: []string{"user", "delete", s.Owner, "--purge"},
		})
	} else {
		p.notes = append(p.notes, "tenant "+s.Owner+" already exists, leaving it alone")
		if s.SSHKey != "" {
			p.steps = append(p.steps, step{
				what: "adding the key to " + s.Owner,
				argv: []string{"key", "add", "--scope", "user", "--user", s.Owner,
					"--label", s.Domain + " deploy", "--key", s.SSHKey},
			})
		}
	}

	// The site. TLS is a separate step below, so that a certificate failure — the likeliest
	// step to fail, and the one with a rate limit attached — does not take the site with it.
	p.steps = append(p.steps, step{
		what: "creating the site " + s.Domain,
		argv: append([]string{"site", "add", s.Domain, "--user", s.Owner, "--ssl", "none"},
			s.siteArgs...),
		undo: []string{"site", "delete", s.Domain, "--purge"},
	})

	if s.WithDB {
		name := s.DBName
		if name == "" {
			name = databaseNameFor(s.Domain)
			if err := validate.DatabaseName(name); err != nil {
				return p, rlerr.Wrap(err, rlerr.CodeUsage,
					"the database name derived from %s is not usable", s.Domain).
					WithHint("pass --db-name with one you choose")
			}
		}
		argv := []string{"db", "create", name, "--owner", s.Owner, "--attach", s.Domain}
		if s.EnvKey != "" {
			argv = append(argv, "--env-key", s.EnvKey)
		}
		p.steps = append(p.steps, step{
			what: "creating the database " + name,
			argv: argv,
			undo: []string{"db", "drop", name, "--force"},
		})
	}

	if s.TLS {
		argv := []string{"cert", "issue", s.Domain}
		if s.Email != "" {
			argv = append(argv, "--email", s.Email)
		}
		// Deliberately not undone. A certificate that was issued has already been counted
		// against the rate limit, and throwing it away would mean spending another one to
		// get back to where we are. It is attached to a site that is about to be removed,
		// which `cert list` reports as orphaned and `doctor` will mention.
		p.steps = append(p.steps, step{
			what: "issuing a certificate for " + s.Domain,
			argv: argv,
			kept: "A certificate is the exception: issuing one spends a rate limit, so it is " +
				"not revoked.",
		})
	}
	return p, nil
}

// provision runs the plan, unwinding everything it created on the first failure.
func (s *stack) provision(ctx context.Context, p plan) (err error) {
	defer func() {
		if err != nil && len(s.done) > 0 {
			s.g.Log.Warn("a step failed, so everything this command created is being removed",
				"created", len(s.done))
			s.unwind(ctx)
		}
	}()
	for _, st := range p.steps {
		if err = s.run(ctx, st); err != nil {
			return err
		}
	}
	return nil
}

// rehearse prints the plan for --dry-run.
//
// The steps are not run, not even with --dry-run passed down. Each one preconditions on the
// one before it having really happened, so rehearsing them in order means the site step is
// told there is no such user — an error that is not real, reported for a stack that is
// perfectly buildable. Printing the resolved plan says the true thing instead.
func (s *stack) rehearse(p plan) {
	if s.g.Quiet {
		return
	}
	s.g.Printf("\nThis would run %s:\n\n", plural(len(p.steps), "command"))
	for _, st := range p.steps {
		s.g.Printf("    %s\n", commandLine(st.argv))
	}
	// Deliberately not a count. How many things come back depends on which step fails, so
	// any number printed here is wrong for every case but one — and the first version said
	// "the 3 things before it would be removed" about a three-step plan, where the most that
	// can ever be removed is two.
	if undoable(p) > 0 {
		s.g.Printf("\nIf any of them failed, everything created before it would be removed.\n")
	}
	for _, st := range p.steps {
		if st.kept != "" {
			s.g.Printf("%s\n", st.kept)
		}
	}
	s.g.Printf("\nNothing was written. The domain, the tenant name and the database name were\n" +
		"checked; the steps themselves were not run, so anything only the server knows —\n" +
		"whether that runtime is installed, whether the domain is already taken — is not\n" +
		"decided yet. Rehearse a single step with its own --dry-run.\n")
}

// summarise prints what exists now and the commands that built it.
//
// The equivalent commands are the point: this is a shortcut for the common case, not a
// replacement for knowing the tool. An operator who reads them once can do the uncommon
// case by hand. They come from the plan, so they are what ran — a tenant that already
// existed does not get a `user add` line it never needed.
func (s *stack) summarise(p plan) {
	if s.g.JSON || s.g.Quiet {
		return
	}
	s.g.Printf("\n%s is ready.\n", s.Domain)
	s.g.Printf("\nThe same thing, one command at a time:\n")
	for _, st := range p.steps {
		s.g.Printf("    %s\n", commandLine(st.argv))
	}
	s.g.Printf("\nNext:\n    ratline site show %s\n", s.Domain)
	if !s.TLS {
		s.g.Printf("    ratline cert issue %s        # once DNS points here\n", s.Domain)
	}
}

// commandLine renders a step for a human to copy.
//
// Only for display: nothing here is ever parsed back into a command. Quoting is applied to
// arguments containing a space so that copying the line into a shell runs what it appears to
// run, which matters for a value like --install-command 'npm ci --omit=dev'.
func commandLine(argv []string) string {
	out := make([]string, 0, len(argv)+1)
	out = append(out, "ratline")
	for _, a := range argv {
		if strings.ContainsAny(a, " \t'\"") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		out = append(out, a)
	}
	return strings.Join(out, " ")
}

func undoable(p plan) int {
	n := 0
	for _, st := range p.steps {
		if st.undo != nil {
			n++
		}
	}
	return n
}

func newNewCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "new",
		Short:   "Provision a whole stack in one command",
		GroupID: GroupSites,
		Long: "A tenant, a site, optionally a database and a certificate — in one command, with\n" +
			"defaults that suit the runtime.\n\n" +
			"All of it is possible with four commands. The difference is what happens when one\n" +
			"of them fails: four commands leave you a tenant and a site and no database, and a\n" +
			"command that has already exited. This removes everything it created and tells you\n" +
			"what it removed.\n\n" +
			"Each step is the same command you would have typed, so nothing here can develop\n" +
			"its own idea of what a site is. The equivalent commands are printed at the end.",
		Example: "  ratline new node app.example.com --user acme --with-db\n" +
			"  ratline new python api.example.com --user acme --app-module app.main:app --asgi\n" +
			"  ratline new static www.example.com --user acme --spa --tls --email ops@example.com",
	}
	cmd.AddCommand(
		newNewNodeCommand(g),
		newNewPythonCommand(g),
		newNewStaticCommand(g),
	)
	return cmd
}

// commonFlags are the ones every recipe takes.
func (s *stack) bind(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&s.Owner, "user", "", "Tenant that owns this site; created if it does not exist (required)")
	f.StringVar(&s.SSHKey, "ssh-key", "", "Public key for the tenant: the key itself, a path, or an https URL")
	f.BoolVar(&s.WithDB, "with-db", false, "Also create a MongoDB database and attach it to the site")
	f.StringVar(&s.DBName, "db-name", "", "Name for that database (default: derived from the domain)")
	f.StringVar(&s.EnvKey, "db-env-key", "", "Variable the connection string is written to (default MONGODB_URI)")
	f.BoolVar(&s.TLS, "tls", false, "Also issue a certificate; DNS must already point here")
	f.StringVar(&s.Email, "email", "", "ACME contact address, for --tls")
	Required(cmd, "user")
}

func (s *stack) finish(cmd *cobra.Command, args []string) error {
	if err := RequireFlags(cmd, s.g, "user"); err != nil {
		return err
	}
	s.Domain = args[0]
	if s.TLS && s.Email == "" && s.g.Cfg.ACME.Email == "" {
		return rlerr.Usagef("--tls needs an email address").
			WithHint("pass --email, or record one once with 'ratline cert account register --email …'")
	}
	p, err := s.plan(cmd.Context())
	if err != nil {
		return err
	}
	for _, n := range p.notes {
		s.g.Printf("\n→ %s\n", n)
	}

	planned := make([]string, 0, len(p.steps))
	for _, st := range p.steps {
		planned = append(planned, commandLine(st.argv))
	}

	if s.g.DryRun {
		s.rehearse(p)
		if s.g.JSON {
			return s.g.EmitJSON(map[string]any{
				"domain": s.Domain, "owner": s.Owner,
				"database": s.WithDB, "tls": s.TLS,
				"dry_run": true, "plan": planned,
			})
		}
		return nil
	}

	if err := s.provision(cmd.Context(), p); err != nil {
		return err
	}
	if s.g.JSON {
		return s.g.EmitJSON(map[string]any{
			"domain": s.Domain, "owner": s.Owner,
			"database": s.WithDB, "tls": s.TLS,
			"dry_run": false, "plan": planned,
		})
	}
	s.summarise(p)
	return nil
}

func newNewNodeCommand(g *Globals) *cobra.Command {
	s := &stack{g: g}
	var (
		nodeVersion, entry, pm, install, build, public, listen string
		instances                                              int
	)
	cmd := &cobra.Command{
		Use:   "node <domain>",
		Short: "A tenant, a Node site and optionally a database, in one command",
		Args:  cobra.ExactArgs(1),
		Long: "Defaults suited to a Node application: PM2 supervision, a Unix socket, and the\n" +
			"package manager detected from the lockfile.\n\n" +
			"A framework that cannot bind a socket — Next.js standalone is the common one —\n" +
			"needs --listen port.",
		Example: "  ratline new node app.example.com --user acme --with-db\n\n" +
			"  # Next.js standalone\n" +
			"  ratline new node app.example.com --user acme --listen port \\\n" +
			"      --entry .next/standalone/server.js --build-command ./bin/build",
		RunE: func(cmd *cobra.Command, args []string) error {
			s.siteArgs = []string{"--runtime", "node"}
			s.siteArgs = appendIf(s.siteArgs, "--node", nodeVersion)
			s.siteArgs = appendIf(s.siteArgs, "--entry", entry)
			s.siteArgs = appendIf(s.siteArgs, "--package-manager", pm)
			s.siteArgs = appendIf(s.siteArgs, "--install-command", install)
			s.siteArgs = appendIf(s.siteArgs, "--build-command", build)
			s.siteArgs = appendIf(s.siteArgs, "--public", public)
			s.siteArgs = appendIf(s.siteArgs, "--listen", listen)
			if instances > 0 {
				s.siteArgs = append(s.siteArgs, "--instances", fmt.Sprint(instances))
			}
			return s.finish(cmd, args)
		},
	}
	s.bind(cmd)
	f := cmd.Flags()
	f.StringVar(&nodeVersion, "node", "", "Managed Node version, e.g. 24")
	f.StringVar(&entry, "entry", "server.js", "The file that starts the server")
	f.StringVar(&pm, "package-manager", "", "npm, pnpm, yarn or bun (detected from the lockfile)")
	f.StringVar(&install, "install-command", "", "Dependency install command")
	f.StringVar(&build, "build-command", "", "Build command; a multi-step build belongs in a script")
	f.StringVar(&public, "public", "", "Directory nginx serves directly, bypassing the application")
	f.StringVar(&listen, "listen", "", "socket (default) or port")
	f.IntVar(&instances, "instances", 0, "PM2 cluster workers")
	return Mutating(cmd)
}

func newNewPythonCommand(g *Globals) *cobra.Command {
	s := &stack{g: g}
	var (
		pyVersion, appModule, server, requirements, managePy, staticDir, staticURL string
		asgi                                                                       bool
		workers                                                                    int
	)
	cmd := &cobra.Command{
		Use:   "python <domain>",
		Short: "A tenant, a Python site and optionally a database, in one command",
		Args:  cobra.ExactArgs(1),
		Long: "Defaults suited to a Python application: a virtualenv, Gunicorn on a Unix socket,\n" +
			"and workers derived from the CPU count.\n\n" +
			"--asgi for FastAPI, Starlette or Django's asgi.py; leave it off for Flask and for\n" +
			"Django's wsgi.py.",
		Example: "  ratline new python api.example.com --user acme --app-module app.main:app --asgi --with-db\n\n" +
			"  # Django\n" +
			"  ratline new python site.example.com --user acme \\\n" +
			"      --app-module project.wsgi:application --manage-py manage.py",
		RunE: func(cmd *cobra.Command, args []string) error {
			if appModule == "" {
				return rlerr.Usagef("--app-module is required").
					WithHint("the import path of the callable, for example app.main:app")
			}
			s.siteArgs = []string{"--runtime", "python", "--app-module", appModule}
			s.siteArgs = appendIf(s.siteArgs, "--python", pyVersion)
			s.siteArgs = appendIf(s.siteArgs, "--server", server)
			s.siteArgs = appendIf(s.siteArgs, "--requirements", requirements)
			s.siteArgs = appendIf(s.siteArgs, "--manage-py", managePy)
			s.siteArgs = appendIf(s.siteArgs, "--static-dir", staticDir)
			s.siteArgs = appendIf(s.siteArgs, "--static-url", staticURL)
			if asgi {
				s.siteArgs = append(s.siteArgs, "--asgi")
			}
			if workers > 0 {
				s.siteArgs = append(s.siteArgs, "--workers", fmt.Sprint(workers))
			}
			return s.finish(cmd, args)
		},
	}
	s.bind(cmd)
	f := cmd.Flags()
	f.StringVar(&pyVersion, "python", "", "Managed Python version, e.g. 3.12")
	f.StringVar(&appModule, "app-module", "", "Import path of the callable, e.g. app.main:app (required)")
	f.BoolVar(&asgi, "asgi", false, "Treat the application as ASGI")
	f.StringVar(&server, "server", "", "gunicorn (default) or uvicorn")
	f.StringVar(&requirements, "requirements", "", "Requirements file")
	f.StringVar(&managePy, "manage-py", "", "Django manage.py, enabling --migrate and --collectstatic on deploys")
	f.StringVar(&staticDir, "static-dir", "", "Directory nginx serves directly")
	f.StringVar(&staticURL, "static-url", "", "URL prefix for it")
	f.IntVar(&workers, "workers", 0, "Gunicorn workers")
	Required(cmd, "app-module")
	return Mutating(cmd)
}

func newNewStaticCommand(g *Globals) *cobra.Command {
	s := &stack{g: g}
	var (
		root, index, build, output string
		spa                        bool
	)
	cmd := &cobra.Command{
		Use:   "static <domain>",
		Short: "A tenant and a static site, in one command",
		Args:  cobra.ExactArgs(1),
		Long: "No unit, no socket, nothing running: nginx serves the files and that is all.\n\n" +
			"--spa serves the index document for unmatched paths, which is what a client-side\n" +
			"router needs and what a plain static site must not have.",
		Example: "  ratline new static www.example.com --user acme --tls --email ops@example.com\n" +
			"  ratline new static app.example.com --user acme --spa \\\n" +
			"      --build-command 'npm run build' --build-output dist",
		RunE: func(cmd *cobra.Command, args []string) error {
			s.siteArgs = []string{"--runtime", "static"}
			s.siteArgs = appendIf(s.siteArgs, "--root", root)
			s.siteArgs = appendIf(s.siteArgs, "--index", index)
			s.siteArgs = appendIf(s.siteArgs, "--build-command", build)
			s.siteArgs = appendIf(s.siteArgs, "--build-output", output)
			if spa {
				s.siteArgs = append(s.siteArgs, "--spa")
			}
			// A static site has nothing to connect to a database with.
			if s.WithDB {
				return rlerr.Usagef("--with-db does not apply to a static site").
					WithHint("nothing is running to read the connection string; a static site " +
						"is files and nginx")
			}
			return s.finish(cmd, args)
		},
	}
	s.bind(cmd)
	f := cmd.Flags()
	f.StringVar(&root, "root", "", "Document root under the site directory (default public)")
	f.StringVar(&index, "index", "", "Index document (default index.html)")
	f.BoolVar(&spa, "spa", false, "Serve the index document for unmatched paths")
	f.StringVar(&build, "build-command", "", "Build command")
	f.StringVar(&output, "build-output", "", "Directory the build writes, published as the document root")
	return Mutating(cmd)
}

// appendIf adds a flag only when it was given, so the site command applies its own
// defaults rather than having this file duplicate them.
func appendIf(args []string, flag, value string) []string {
	if value == "" {
		return args
	}
	return append(args, flag, value)
}

// databaseNameFor derives a database name from a domain.
//
// MongoDB forbids a dot — it is the namespace separator, and a name containing one cannot
// be addressed unambiguously in a role document — and ratline caps the name at 38
// characters so a collection still fits inside the 64-byte namespace limit. app.example.com
// therefore becomes app_example_com, and a very long domain is truncated rather than
// refused, because a name derived on the operator's behalf should not fail on its length.
//
// The result is validated before it is used: if the derivation produces something MongoDB
// would reject, that is a bug here and the operator should be told to pass --db-name rather
// than handed a confusing failure from the server.
func databaseNameFor(domain string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(domain) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := strings.Trim(b.String(), "_")
	if len(name) > 38 {
		name = strings.Trim(name[:38], "_")
	}
	return name
}
