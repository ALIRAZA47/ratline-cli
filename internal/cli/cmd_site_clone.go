package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// `ratline site clone` — a staging copy of a site that already works.
//
// Standing up staging by hand means reading `site show` and retyping fifteen flags, and
// the whole value of staging is that it is the same as production. A copy made by hand
// differs in the one setting somebody forgot, which is usually the setting that matters.
//
// It composes the commands, like `ratline new` and `ratline import`, so a clone is built by
// the same code path as anything else and cannot develop its own idea of what a site is.
// The plan is decided first and printed under --dry-run, for the same reason.

type cloner struct {
	g *Globals
	c *composer

	from  string
	to    string
	owner string

	withFiles bool
	withDB    bool
	dbName    string
	start     bool
}

func (cl *cloner) plan(ctx context.Context) (plan, error) {
	var p plan
	if _, err := validate.Domain(cl.to); err != nil {
		return p, err
	}
	if cl.from == cl.to {
		return p, rlerr.Usagef("a site cannot be cloned onto itself")
	}

	st, err := cl.g.Store(ctx)
	if err != nil {
		return p, err
	}
	src, err := st.FindSiteByName(ctx, cl.from)
	if err != nil {
		return p, err
	}
	if _, gerr := st.GetSite(ctx, cl.to); gerr == nil {
		return p, rlerr.Preconditionf("%s already exists", cl.to).
			WithHint("clone to a domain that is free, or delete that site first")
	}

	owner := cl.owner
	if owner == "" {
		owner = src.Owner
	}
	if err := validate.Username(owner); err != nil {
		return p, err
	}
	if _, uerr := st.GetUser(ctx, owner); uerr != nil {
		return p, rlerr.Preconditionf("no such tenant: %s", owner).
			WithHint("create it first, or clone into the source's own tenant by " +
				"leaving --user off")
	}

	// The copy is the source's own settings with the domain and owner swapped. Built by
	// the same generator `import` uses, so a flag added to `site add` is carried by both
	// and there is one place that can be wrong rather than two.
	copied := *src
	copied.Domain = cl.to
	copied.Owner = owner
	// Not copied: the port is reallocated by `site add`, and the slug is derived from the
	// new owner and domain. Carrying either across would collide with the source.
	copied.Port = 0
	copied.Slug = ""

	p.add(step{
		what: "creating " + cl.to + " with " + cl.from + "'s settings",
		argv: siteArgvFor(&copied),
		undo: []string{"site", "delete", cl.to, "--purge"},
	})

	// Aliases are deliberately not copied. An alias is a hostname, and two sites claiming
	// the same server_name is a configuration nginx accepts and then resolves by whichever
	// vhost it read first — which is the kind of bug that takes a day to find.
	if len(src.Aliases) > 0 {
		p.note("not copying the aliases: a hostname can only belong to one site, and " +
			"nginx would resolve the clash by whichever vhost it read first")
	}

	if src.PreDeployCommand != "" || src.PostDeployCommand != "" {
		argv := []string{"site", "hook", "set", cl.to}
		argv = appendIf(argv, "--before", src.PreDeployCommand)
		argv = appendIf(argv, "--after", src.PostDeployCommand)
		p.add(step{what: "copying the deploy hooks", argv: argv})
	}

	// Jobs and workers, disabled.
	//
	// This is the one place a clone must not be faithful. A staging copy of a nightly job
	// that emails customers, armed the moment it is created, sends every one of those
	// emails twice — and the second time from a server nobody is watching. They come
	// across so the shape is right, and switched off so somebody has to choose.
	units, err := st.ListSiteUnits(ctx, cl.from, "")
	if err != nil {
		return p, err
	}
	for _, u := range units {
		clone := *u
		clone.Domain = cl.to
		clone.Enabled = false
		p.add(step{
			what: "copying the " + u.Kind + " " + u.Name + ", switched off",
			argv: siteUnitArgvFor(&clone),
		})
	}
	if len(units) > 0 {
		p.note(plural(len(units), "job or worker") + " will be copied but left disabled — " +
			"a staging copy of a nightly job that emails customers should not fire tonight")
	}

	if cl.withFiles {
		p.add(step{
			what: "copying the application files",
			argv: []string{"site", "deploy", cl.to, "--pull", "--install", "--build"},
		})
		p.note("--with-files clones the repository the source deploys from, which means the " +
			"source's branch. It does not copy the source's working tree.")
	}

	if cl.withDB {
		name := cl.dbName
		if name == "" {
			name = databaseNameFor(cl.to)
			if err := validate.DatabaseName(name); err != nil {
				return p, rlerr.Wrap(err, rlerr.CodeUsage,
					"the database name derived from %s is not usable", cl.to).
					WithHint("pass --db-name with one you choose")
			}
		}
		p.add(step{
			what: "creating the database " + name,
			argv: []string{"db", "create", name, "--owner", owner, "--attach", cl.to},
			undo: []string{"db", "drop", name, "--force"},
		})
		p.note("--with-db creates an empty database and attaches it. To copy the source's " +
			"data: 'ratline db dump <source-db>' then 'ratline db restore <archive> --into " +
			name + " --drop'")
	}

	if cl.start && copied.Dynamic() {
		p.add(step{
			what: "starting " + cl.to,
			argv: []string{"site", "start", cl.to},
		})
	}
	return p, nil
}

func newSiteCloneCommand(g *Globals) *cobra.Command {
	cl := &cloner{g: g}
	cmd := &cobra.Command{
		Use:   "clone <source-domain> <new-domain>",
		Short: "Copy a site's configuration to a new domain",
		Args:  cobra.ExactArgs(2),
		Long: "Every setting the source has, on a new domain: runtime, versions, commands,\n" +
			"limits, deploy hooks, and its jobs and workers.\n\n" +
			"Standing up staging by hand means reading 'site show' and retyping fifteen flags,\n" +
			"and the value of staging is that it is the same as production — a copy made by\n" +
			"hand differs in the one setting somebody forgot.\n\n" +
			"Three things are deliberately not faithful. Aliases are not copied, because a\n" +
			"hostname can only belong to one site. Jobs and workers come across switched off,\n" +
			"because a staging copy of a nightly job that emails customers should not fire\n" +
			"tonight from a server nobody is watching. And TLS is off, because the new domain\n" +
			"has no certificate and DNS may not point here yet.\n\n" +
			"Nothing is copied that this cannot copy honestly: --with-files clones the\n" +
			"repository, and --with-db creates an empty database and tells you the two\n" +
			"commands that move the data.",
		Example: "  ratline site clone app.example.com staging.example.com\n" +
			"  ratline site clone app.example.com staging.example.com --with-files --start\n" +
			"  ratline site clone app.example.com sandbox.example.com --user sandbox --with-db",
		RunE: func(cmd *cobra.Command, args []string) error {
			cl.from, cl.to = args[0], args[1]
			cl.c = &composer{g: g}

			p, err := cl.plan(cmd.Context())
			if err != nil {
				return err
			}
			for _, n := range p.notes {
				g.Printf("\n→ %s\n", n)
			}
			planned := make([]string, 0, len(p.steps))
			for _, st := range p.steps {
				planned = append(planned, commandLine(st.argv))
			}

			if g.DryRun {
				cl.c.rehearse(p, "The names were checked and the source was read; the steps\n"+
					"themselves were not run, so anything only the server knows — whether the\n"+
					"runtime is installed, whether the domain is taken — is not decided yet.")
				if g.JSON {
					return g.EmitJSON(map[string]any{
						"from": cl.from, "to": cl.to, "dry_run": true, "plan": planned,
					})
				}
				return nil
			}

			if err := cl.c.execute(cmd.Context(), p); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{
					"from": cl.from, "to": cl.to, "dry_run": false, "plan": planned,
				})
			}
			g.Printf("\n%s is a copy of %s.\n", cl.to, cl.from)
			g.Printf("\nStill to do:\n")
			if !cl.withFiles {
				g.Printf("    ratline site deploy %s          # its code\n", cl.to)
			}
			g.Printf("    ratline cert issue %s           # once DNS points here\n", cl.to)
			if n := countSiteUnits(p); n > 0 {
				g.Printf("    ratline site cron list %s       # %s copied, all switched off\n",
					cl.to, plural(n, "job or worker"))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&cl.owner, "user", "", "Tenant to own the copy (default: the source's)")
	f.BoolVar(&cl.withFiles, "with-files", false, "Also clone the repository and install and build it")
	f.BoolVar(&cl.withDB, "with-db", false, "Also create an empty database and attach it")
	f.StringVar(&cl.dbName, "db-name", "", "Name for that database (default: derived from the new domain)")
	f.BoolVar(&cl.start, "start", false, "Start the copy once it is built")
	return Mutating(cmd)
}

// countSiteUnits counts planned job and worker steps.
func countSiteUnits(p plan) int {
	n := 0
	for _, st := range p.steps {
		if len(st.argv) >= 3 && st.argv[0] == "site" &&
			(st.argv[1] == "cron" || st.argv[1] == "worker") && st.argv[2] == "add" {
			n++
		}
	}
	return n
}
