package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// `ratline site hook` — the two points in a deploy where a site gets to run its own thing.
//
// A deploy was a fixed chain: pull, install, build, migrate, restart. Anything
// site-specific — warming a cache, a smoke test, telling a chat room — had nowhere to go,
// so it ended up in a wrapper script that reimplemented the chain badly, or nowhere.
//
// Stored on the site, like its build command, because that is what these are: the same
// shape, the same lifetime, the same place an operator looks. Which means `export` carries
// them, `import` restores them and `site show` lists them, without any of that being
// wired up separately.

func newSiteHookCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Run something of your own before or after a deploy",
		Long: "Two hooks per site, each a command run as the tenant, in the application\n" +
			"directory, with the site's environment — the same conditions as the build\n" +
			"command.\n\n" +
			"The pre-deploy hook runs after the pull and before install and build. After the\n" +
			"pull deliberately: a hook script lives in the repository, so running it earlier\n" +
			"would run the previous deploy's version of it.\n\n" +
			"The post-deploy hook runs once the site is up and has answered a health check.\n" +
			"A failing post-deploy hook reports and exits non-zero but does not roll the\n" +
			"deploy back: the site is already serving the new code, and reverting a healthy\n" +
			"site because a notification failed would be worse than the failure.\n\n" +
			"A failing pre-deploy hook stops the deploy before anything restarts, so the\n" +
			"previous version keeps serving.",
	}
	cmd.AddCommand(newSiteHookSetCommand(g), newSiteHookClearCommand(g))
	return cmd
}

func newSiteHookSetCommand(g *Globals) *cobra.Command {
	var before, after string
	cmd := &cobra.Command{
		Use:   "set <domain>",
		Short: "Set a site's pre- or post-deploy hook",
		Args:  cobra.ExactArgs(1),
		Example: "  ratline site hook set app.example.com \\\n" +
			"      --after /home/acme/app.example.com/app/bin/smoke-test\n\n" +
			"  ratline site hook set app.example.com \\\n" +
			"      --before …/bin/maintenance-on --after …/bin/maintenance-off",
		RunE: func(cmd *cobra.Command, args []string) error {
			if before == "" && after == "" {
				return rlerr.Usagef("nothing to set").
					WithHint("pass --before, --after, or both")
			}
			for _, c := range []string{before, after} {
				if err := checkHookCommand(c); err != nil {
					return err
				}
			}
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			site, err := st.FindSiteByName(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if before != "" {
				site.PreDeployCommand = before
			}
			if after != "" {
				site.PostDeployCommand = after
			}
			if g.DryRun {
				g.Printf("would set the hooks on %s\n", site.Domain)
				return nil
			}
			if err := st.PutSite(cmd.Context(), site); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{
					"domain":     site.Domain,
					"pre_deploy": site.PreDeployCommand, "post_deploy": site.PostDeployCommand,
				})
			}
			return g.Fields(
				[2]string{"domain", site.Domain},
				[2]string{"pre-deploy", orDash(site.PreDeployCommand)},
				[2]string{"post-deploy", orDash(site.PostDeployCommand)},
			)
		},
	}
	f := cmd.Flags()
	f.StringVar(&before, "before", "", "Run this after the pull, before install and build")
	f.StringVar(&after, "after", "", "Run this once the site is up and answering")
	return Mutating(cmd)
}

func newSiteHookClearCommand(g *Globals) *cobra.Command {
	var before, after bool
	cmd := &cobra.Command{
		Use:   "clear <domain>",
		Short: "Remove a site's hooks",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !before && !after {
				// Refusing rather than assuming "both" — clearing a hook somebody still
				// wanted is silent until the next deploy does not do the thing.
				return rlerr.Usagef("say which hook to clear").
					WithHint("--before, --after, or both")
			}
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			site, err := st.FindSiteByName(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if before {
				site.PreDeployCommand = ""
			}
			if after {
				site.PostDeployCommand = ""
			}
			if g.DryRun {
				g.Printf("would clear the hooks on %s\n", site.Domain)
				return nil
			}
			if err := st.PutSite(cmd.Context(), site); err != nil {
				return err
			}
			g.Printf("Cleared. %s now runs: %s\n", site.Domain,
				orDefault2(strings.TrimSpace(site.PreDeployCommand+" "+site.PostDeployCommand),
					"no hooks"))
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&before, "before", false, "Clear the pre-deploy hook")
	f.BoolVar(&after, "after", false, "Clear the post-deploy hook")
	return Mutating(cmd)
}

// checkHookCommand refuses a hook that will not do what it looks like it does.
//
// systemd is not involved here — a hook runs through system.Runner like the build command
// — and the same rule applies for the same reason: nothing is passed to a shell, so `a | b`
// would run `a` with `|` and `b` as arguments and appear to work.
func checkHookCommand(command string) error {
	if command == "" {
		return nil
	}
	// A control character is caught by ParseCommand when the hook runs at deploy time, but
	// rejecting it here turns a deploy-time surprise into an error at the moment it is set.
	if err := validate.NoControlChars("hook", command); err != nil {
		return err
	}
	for _, meta := range []string{"|", "&&", "||", ";", ">", "<", "$(", "`"} {
		if strings.Contains(command, meta) {
			return rlerr.Usagef("the hook contains %q, which is not interpreted", meta).
				WithHint("nothing here is passed to a shell, so this would be handed to " +
					"the program as an argument. Put it in a script and run that")
		}
	}
	return nil
}
