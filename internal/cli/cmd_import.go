package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/sshkey"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

// `ratline import` rebuilds a server from what `ratline export` wrote.
//
// export has said "for migration" since it was written, and nothing consumed it. A dump
// nothing reads is a promise, not a feature — the operator gets a file that looks like a
// migration and discovers on the new server that there is no far end.
//
// What this can rebuild is the shape: tenants, their SSH keys, their sites with every
// setting, the aliases, and which sites were disabled. What it cannot rebuild is anything
// the export deliberately does not hold, and the honest thing is to say so at the end
// rather than let a clean exit imply a finished migration:
//
//   - Application code. Nothing is cloned; `site deploy` does that.
//   - Environment values. `.env` is secrets, so it is not exported. Only the site's shape.
//   - Certificates. Private keys are never exported, so a certificate has to be re-issued
//     on the new server — which is right anyway, since the old one was issued for a host
//     that is about to stop answering.
//   - Databases. The export records that a site had one attached, not its contents.

// importSource is the JSON this reads.
//
// It accepts both what `ratline export` prints — the {ok, command, version, data} envelope
// — and a bare Export object, because somebody will reasonably `jq .data` before saving it
// and should not be punished for tidying.
type importSource struct {
	Data *state.Export `json:"data"`
	// The bare form: the same fields at the top level.
	state.Export
}

func parseExport(r io.Reader) (*state.Export, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 64<<20))
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "could not read the export")
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, rlerr.Usagef("the export is empty").
			WithHint("produce one with 'ratline export' on the server you are leaving")
	}
	var src importSource
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeUsage, "this is not a ratline export").
			WithHint("it should be the output of 'ratline export', enveloped or not")
	}
	// The envelope wins when present: a file holding both would be the enveloped form, and
	// the top-level fields would be the zero values that embedding produced.
	e := src.Data
	if e == nil {
		e = &src.Export
	}
	if len(e.Users) == 0 && len(e.Sites) == 0 {
		return nil, rlerr.Usagef("the export holds no tenants and no sites").
			WithHint("check it is the whole file: 'jq .data.sites' should list them")
	}
	return e, nil
}

// importer is one migration run.
type importer struct {
	g *Globals
	c *composer

	file      string
	only      []string
	skipKeys  bool
	skipSites bool
}

// plan decides what to rebuild, and runs nothing.
func (im *importer) plan(ctx context.Context, e *state.Export) (plan, error) {
	var p plan

	// A state database from a newer ratline may describe things this binary has no flag
	// for, and importing it would silently drop them. Refuse rather than half-migrate.
	here, err := schemaVersionHere(ctx, im.g)
	if err == nil && e.SchemaVersion > here {
		return p, rlerr.Preconditionf(
			"the export came from a newer ratline: schema %d, and this binary understands %d",
			e.SchemaVersion, here).
			WithHint("upgrade first with 'ratline update', then import")
	}

	wanted := func(owner string) bool {
		if len(im.only) == 0 {
			return true
		}
		for _, o := range im.only {
			if o == owner {
				return true
			}
		}
		return false
	}

	st, err := im.g.Store(ctx)
	if err != nil {
		return p, err
	}

	users := append([]*state.User(nil), e.Users...)
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })
	for _, u := range users {
		if !wanted(u.Name) {
			continue
		}
		if _, gerr := st.GetUser(ctx, u.Name); gerr == nil {
			p.note("tenant " + u.Name + " already exists, leaving it alone")
			continue
		}
		argv := []string{"user", "add", u.Name}
		argv = appendIf(argv, "--shell", u.Shell)
		argv = appendIf(argv, "--comment", u.Comment)
		argv = appendIf(argv, "--quota", u.Quota)
		argv = appendIf(argv, "--memory-max", u.MemoryMax)
		if u.SFTPOnly {
			argv = append(argv, "--sftp-only")
		}
		p.add(step{
			what: "recreating the tenant " + u.Name,
			argv: argv,
			undo: []string{"user", "delete", u.Name, "--purge"},
		})
	}

	if !im.skipKeys {
		// Which sites belong to a wanted tenant, so a site-scoped key can be filtered by
		// the same --only the sites were.
		owns := map[string]string{}
		for _, s := range e.Sites {
			owns[s.Domain] = s.Owner
		}
		keys := append([]*state.Key(nil), e.Keys...)
		sort.Slice(keys, func(i, j int) bool { return keys[i].Label < keys[j].Label })
		for _, k := range keys {
			// A revoked key is history. Re-adding it would hand back access somebody
			// deliberately took away, which is the worst thing a migration could do quietly.
			if !k.RevokedAt.IsZero() {
				p.note("not restoring the revoked key " + k.Label + " — it was revoked on purpose")
				continue
			}
			if k.Blob == "" {
				continue
			}
			// --only names tenants. A global key belongs to no tenant, and a site key
			// belongs to whoever owns the site — so neither is in scope when the operator
			// has asked for a specific list. Without this, --only still dragged in every
			// global key on the server and `--only nosuchtenant` planned work.
			if len(im.only) > 0 {
				switch k.Scope {
				case "user":
					if !wanted(k.Owner) {
						continue
					}
				case "site":
					if !wanted(owns[k.Site]) {
						continue
					}
				default:
					continue
				}
			}
			// Already here. An import is meant to be safe to run twice, and `key add`
			// refuses a duplicate rather than shrugging — correctly, since installing the
			// same key in two scopes is usually a mistake. Skipping it here is what makes
			// re-running an import report rather than fail.
			if k.Fingerprint != "" {
				if at, ferr := st.FingerprintLocations(ctx, k.Fingerprint); ferr == nil && len(at) > 0 {
					p.note("key " + k.Label + " is already installed, leaving it alone")
					continue
				}
			}
			argv := []string{"key", "add", "--scope", k.Scope, "--key", authorizedLine(k)}
			argv = appendIf(argv, "--user", k.Owner)
			argv = appendIf(argv, "--site", k.Site)
			argv = appendIf(argv, "--label", k.Label)
			p.add(step{what: "restoring the key " + k.Label, argv: argv})
		}
	}

	if !im.skipSites {
		sites := append([]*state.Site(nil), e.Sites...)
		sort.Slice(sites, func(i, j int) bool { return sites[i].Domain < sites[j].Domain })
		for _, s := range sites {
			if !wanted(s.Owner) {
				continue
			}
			if _, gerr := st.GetSite(ctx, s.Domain); gerr == nil {
				p.note("site " + s.Domain + " already exists, leaving it alone")
				continue
			}
			p.add(step{
				what: "recreating the site " + s.Domain,
				argv: siteArgvFor(s),
				undo: []string{"site", "delete", s.Domain, "--purge"},
			})
			for _, a := range s.Aliases {
				p.add(step{
					what: "restoring the alias " + a,
					argv: []string{"site", "alias", "add", s.Domain, a},
				})
			}
			// A site that was deliberately offline should come back offline. Coming back
			// serving is how a site somebody took down for a reason starts answering again.
			if !s.Enabled {
				p.add(step{
					what: "leaving " + s.Domain + " disabled, as it was",
					argv: []string{"site", "disable", s.Domain},
				})
			}
		}
	}
	return p, nil
}

// siteArgvFor reconstructs the `site add` that would produce this site.
//
// Every field the state carries and `site add` accepts. A field with no flag behind it is
// one this cannot restore, which is why the closing summary names what was left out rather
// than letting the operator assume the site came back whole.
func siteArgvFor(s *state.Site) []string {
	argv := []string{"site", "add", s.Domain, "--user", s.Owner, "--runtime", s.Runtime,
		// TLS is never restored: the private key was not exported, so there is nothing to
		// attach, and issuing here would spend a rate limit before DNS has moved.
		"--ssl", "none"}

	switch s.Runtime {
	case "static":
		argv = appendIf(argv, "--root", s.DocRoot)
		argv = appendIf(argv, "--index", s.IndexFile)
		if s.SPA {
			argv = append(argv, "--spa")
		}
	case "node":
		argv = appendIf(argv, "--entry", s.Entry)
		argv = appendIf(argv, "--node", s.NodeVersion)
		argv = appendIf(argv, "--package-manager", s.PackageManager)
		argv = appendIf(argv, "--listen", s.Listen)
		if s.Instances > 0 {
			argv = append(argv, "--instances", fmt.Sprint(s.Instances))
		}
	case "python":
		argv = appendIf(argv, "--app-module", s.AppModule)
		argv = appendIf(argv, "--python", s.PythonVersion)
		argv = appendIf(argv, "--server", s.AppServer)
		argv = appendIf(argv, "--requirements", s.Requirements)
		argv = appendIf(argv, "--manage-py", s.ManagePy)
		argv = appendIf(argv, "--static-dir", s.StaticDir)
		argv = appendIf(argv, "--static-url", s.StaticURL)
		if s.ASGI {
			argv = append(argv, "--asgi")
		}
		if s.Workers > 0 {
			argv = append(argv, "--workers", fmt.Sprint(s.Workers))
		}
	}

	argv = appendIf(argv, "--install-command", s.InstallCommand)
	argv = appendIf(argv, "--build-command", s.BuildCommand)
	argv = appendIf(argv, "--build-output", s.BuildOutput)
	argv = appendIf(argv, "--public", s.PublicDir)
	argv = appendIf(argv, "--repo", s.Repo)
	argv = appendIf(argv, "--branch", s.Branch)
	argv = appendIf(argv, "--memory-max", s.MemoryMax)
	argv = appendIf(argv, "--cpu-quota", s.CPUQuota)
	argv = appendIf(argv, "--client-max-body-size", s.ClientMaxBodySize)
	argv = appendIf(argv, "--www-redirect", s.WWWRedirect)
	for _, r := range s.Relaxed {
		argv = append(argv, "--relax", r)
	}
	return argv
}

// carriedOver lists what an import cannot bring, for the closing summary.
//
// Printed on success, not on failure, and printed every time. A migration that exits 0
// without saying this reads as finished, and the operator finds out what was missing when
// a site 502s or a certificate is absent.
func (im *importer) carriedOver(e *state.Export) []string {
	var out []string
	out = append(out, "application code — nothing was cloned; run 'ratline site deploy' for each site")
	out = append(out, "environment values — .env holds secrets and is not exported; set them with 'ratline site env set'")
	if len(e.Certificates) > 0 {
		out = append(out, fmt.Sprintf(
			"%s — private keys are never exported; re-issue with 'ratline cert issue' once DNS points here",
			plural(len(e.Certificates), "certificate")))
	}
	out = append(out, "database contents — the export records that a site had one, not what was in it")
	return out
}

func newImportCommand(g *Globals) *cobra.Command {
	im := &importer{g: g, c: &composer{g: g}}
	cmd := &cobra.Command{
		Use:     "import <file>",
		Short:   "Rebuild tenants and sites on this server from an export",
		GroupID: GroupOps,
		Args:    cobra.MaximumNArgs(1),
		Long: "Reads what 'ratline export' wrote on another server and rebuilds the shape here:\n" +
			"tenants, their SSH keys, their sites with every setting, the aliases, and which\n" +
			"sites were disabled.\n\n" +
			"It does not bring the application code, the environment values, the certificates\n" +
			"or the database contents — the export holds none of those, by design. What was\n" +
			"left out is listed at the end.\n\n" +
			"Safe to run twice: a tenant or site that already exists is reported and left\n" +
			"alone. If a step fails, everything this command created is removed.",
		Example: "  ratline export > server.json          # on the old server\n" +
			"  ratline import server.json            # on the new one\n\n" +
			"  ratline import server.json --dry-run  # the plan, writing nothing\n" +
			"  ratline import server.json --only acme",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				im.file = args[0]
			}

			var r io.Reader
			switch im.file {
			case "", "-":
				// A pipe is the obvious way to move one of these between servers, so it is
				// worth supporting; but an interactive terminal here means somebody typed
				// `ratline import` and is now staring at a cursor, which is not a prompt
				// worth inventing.
				if im.file == "" && isTTY(g.Stdin) {
					return rlerr.Usagef("no export to read").
						WithHint("give a file, or pipe one in: " +
							"ssh old-server ratline export | ratline import -")
				}
				r = g.Stdin
			default:
				f, err := os.Open(im.file)
				if err != nil {
					return rlerr.Wrap(err, rlerr.CodeUsage, "could not open %s", im.file)
				}
				defer func() { _ = f.Close() }()
				r = f
			}

			e, err := parseExport(r)
			if err != nil {
				return err
			}

			p, err := im.plan(cmd.Context(), e)
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
				im.c.rehearse(p, "The export was read and the names in it were checked; the\n"+
					"steps themselves were not run, so anything only this server knows —\n"+
					"whether a runtime is installed, whether a domain is already taken — is\n"+
					"not decided yet.")
				im.summariseGaps(e)
				if g.JSON {
					return g.EmitJSON(map[string]any{
						"dry_run": true, "plan": planned,
						"users": len(e.Users), "sites": len(e.Sites),
					})
				}
				return nil
			}

			if len(p.steps) == 0 {
				g.Printf("\nNothing to import: everything in this export is already here.\n")
			} else if err := im.c.execute(cmd.Context(), p); err != nil {
				return err
			}

			if g.JSON {
				return g.EmitJSON(map[string]any{
					"dry_run": false, "plan": planned,
					"users": len(e.Users), "sites": len(e.Sites),
				})
			}
			if len(p.steps) > 0 {
				g.Printf("\nImported %s and %s.\n",
					plural(countKind(p, "user", "add"), "tenant"),
					plural(countKind(p, "site", "add"), "site"))
			}
			im.summariseGaps(e)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&im.only, "only", nil, "Import just these tenants, and their sites")
	f.BoolVar(&im.skipKeys, "skip-keys", false, "Do not restore SSH keys")
	f.BoolVar(&im.skipSites, "skip-sites", false, "Restore the tenants but not their sites")
	return Mutating(cmd)
}

func (im *importer) summariseGaps(e *state.Export) {
	if im.g.JSON || im.g.Quiet {
		return
	}
	im.g.Printf("\nNot carried over by an export, and still to do here:\n")
	for _, l := range im.carriedOver(e) {
		im.g.Printf("  - %s\n", l)
	}
}

// countKind counts planned steps whose command is `<noun> <verb> …`.
func countKind(p plan, noun, verb string) int {
	n := 0
	for _, st := range p.steps {
		if len(st.argv) >= 2 && st.argv[0] == noun && st.argv[1] == verb {
			n++
		}
	}
	return n
}

// schemaVersionHere is the state schema this binary understands.
func schemaVersionHere(ctx context.Context, g *Globals) (int, error) {
	st, err := g.Store(ctx)
	if err != nil {
		return 0, err
	}
	return st.SchemaVersion(ctx)
}

// authorizedLine rebuilds the key line from what the state kept.
//
// The state stores a key split apart — algorithm, base64 body, comment — because that is
// what it needs to compare and de-duplicate them. `key add --key` wants a whole line, and
// handed a bare body it does the sensible thing with something that is not a key: treats it
// as a path, and reports "no such file: /AAAAC3Nz…". Which is how the first import failed.
//
// sshkey.PublicKey.Line() is the same rendering, and this defers to it rather than being a
// second opinion about the format.
func authorizedLine(k *state.Key) string {
	pk := &sshkey.PublicKey{Algorithm: k.Algorithm, Blob: k.Blob, Comment: k.Comment}
	return pk.Line()
}
