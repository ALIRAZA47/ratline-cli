package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/nginx"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// `ratline nginx -- <args>` — nginx's read-only switches, and a reload that goes through
// systemd. See cmd_passthrough.go for why these commands exist and how they lock.

// nginxReadFlags are the switches that only read: test, test-and-dump, version, build
// options, and quiet (which only matters beside -t). Each is accepted as its own element;
// a cluster such as -tq is refused with the spelling that works, because parsing nginx's
// option syntax well enough to be sure what a cluster means is the guess this avoids.
var nginxReadFlags = map[string]bool{"-t": true, "-T": true, "-v": true, "-V": true, "-q": true}

// nginxPlan is what `ratline nginx` decided to do with its arguments.
type nginxPlan struct {
	// Reload is the one mutation: nginx -t, then systemctl reload nginx.
	Reload bool
	// Args is the argv for a read, when Reload is false.
	Args []string
}

// planNginx classifies the arguments, refusing everything outside the read switches and
// a reload.
func planNginx(args []string) (*nginxPlan, error) {
	if len(args) == 0 {
		return nil, rlerr.Usagef("nothing to pass to nginx").
			WithHint("for example 'ratline nginx -- -t'")
	}
	// `-s reload`, `-sreload` and a bare `reload` all mean the same thing, and none of
	// them is passed to nginx.
	switch strings.Join(args, " ") {
	case "reload", "-s reload", "-sreload":
		return &nginxPlan{Reload: true}, nil
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		if nginxReadFlags[a] {
			continue
		}
		switch {
		case a == "-s" || strings.HasPrefix(a, "-s"):
			sig := strings.TrimPrefix(a, "-s")
			if sig == "" && i+1 < len(args) {
				sig = args[i+1]
			}
			return nil, refuseNginxSignal(sig)
		case a == "reload":
			return nil, rlerr.Usagef("reload is not combined with anything else").
				WithHint("'ratline nginx -- reload' tests the configuration itself before reloading")
		case len(a) > 1 && a[0] == '-':
			return nil, refuseNginxFlag(a)
		default:
			return nil, rlerr.Usagef("nginx takes no argument %q", a).
				WithHint("ratline nginx passes -t, -T, -v, -V and -q, and turns 'reload' into a tested reload through systemd")
		}
	}
	// Every read switch except -q makes nginx do its one thing and exit. -q alone does
	// not: nginx with no -t, -T, -v or -V starts a second master, as root, beside the one
	// systemd supervises.
	for _, a := range args {
		if a != "-q" {
			return &nginxPlan{Args: args}, nil
		}
	}
	return nil, rlerr.Usagef("-q on its own would start nginx rather than test it").
		WithHint("-q quietens a test: 'ratline nginx -- -t -q'")
}

func refuseNginxSignal(sig string) error {
	switch sig {
	case "reload":
		// Reached only when -s reload came with something else.
		return rlerr.Usagef("-s reload is not combined with other switches").
			WithHint("'ratline nginx -- reload' on its own tests the configuration and then reloads it")
	case "stop", "quit":
		return rlerr.Usagef("ratline will not %s nginx", sig).
			WithHint("ratline would have no web server, and every site would go down with nothing in its state saying so; " +
				"if you really mean it, 'systemctl stop nginx' yourself")
	case "reopen":
		return rlerr.Usagef("ratline does not send nginx the reopen signal").
			WithHint("logrotate's postrotate step already signals nginx to reopen its logs after rotating them")
	case "":
		return rlerr.Usagef("-s needs a signal").
			WithHint("the only one ratline passes on is reload: 'ratline nginx -- reload'")
	}
	return rlerr.Usagef("unknown nginx signal %q", sig).
		WithHint("the only one ratline passes on is reload: 'ratline nginx -- reload'")
}

func refuseNginxFlag(a string) error {
	switch {
	case strings.HasPrefix(a, "-c"):
		return rlerr.Usagef("ratline does not point nginx at another configuration file (%s)", a).
			WithHint("ratline manages /etc/nginx; 'ratline nginx -- -t' tests the configuration nginx actually runs")
	case strings.HasPrefix(a, "-g"):
		return rlerr.Usagef("ratline does not pass nginx extra global directives (%s)", a).
			WithHint("a directive belongs in a file nginx reads, where 'nginx -t' can see it")
	case strings.HasPrefix(a, "-p"):
		return rlerr.Usagef("ratline does not change nginx's prefix path (%s)", a).
			WithHint("ratline's paths are the ones in /etc/ratline/config.yaml; 'ratline config show' prints them")
	case strings.HasPrefix(a, "-e"):
		return rlerr.Usagef("ratline does not redirect nginx's error log (%s)", a).
			WithHint("the error log is the one nginx.conf names")
	case len(a) > 2 && !strings.HasPrefix(a, "--") && allNginxReadFlags(a):
		return rlerr.Usagef("pass nginx's switches separately, not as %s", a).
			WithHint("for example 'ratline nginx -- %s'", splitCluster(a))
	}
	return rlerr.Usagef("ratline does not pass %s to nginx", a).
		WithHint("ratline nginx passes -t, -T, -v, -V and -q, and turns 'reload' into a tested reload through systemd")
}

// allNginxReadFlags reports whether a cluster such as -tq is made only of read switches.
func allNginxReadFlags(a string) bool {
	for _, c := range a[1:] {
		if !nginxReadFlags["-"+string(c)] {
			return false
		}
	}
	return true
}

func splitCluster(a string) string {
	parts := make([]string, 0, len(a)-1)
	for _, c := range a[1:] {
		parts = append(parts, "-"+string(c))
	}
	return strings.Join(parts, " ")
}

func newNginxCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "nginx -- <args>",
		Short:   "Test nginx's configuration, or reload it through systemd",
		GroupID: GroupOps,
		Args:    passthroughUsage("ratline nginx -- -t"),
		Long: "The nginx binary, found where ratline finds it rather than on PATH, with the\n" +
			"switches that only read passed straight through: -t (test the configuration),\n" +
			"-T (test it and print every file nginx loaded), -v and -V (the version and how\n" +
			"it was built), and -q. Each goes as its own argument.\n\n" +
			"'reload' (or -s reload) is not passed to nginx. It becomes what ratline itself\n" +
			"does after changing a vhost: 'nginx -t', and only if that passes, 'systemctl\n" +
			"reload nginx', waiting until no worker started under the old configuration is\n" +
			"still accepting. 'nginx -s reload' signals the master behind systemd's back,\n" +
			"and a reload of a configuration that does not test clean is how a typo in one\n" +
			"vhost becomes an outage for all of them. The reload takes the server lock, so\n" +
			"it never lands in the middle of a 'site add'; the read switches do not.\n\n" +
			"Refused, each with the reason: -s stop and -s quit (ratline would have no web\n" +
			"server), -s reopen, -c, -g, -p and -e (a configuration other than the one\n" +
			"ratline manages), and anything else nginx accepts.\n\n" +
			"nginx's own exit code is reported but not adopted: a failing test exits 4\n" +
			"(external) and names the code. Under --json the output is captured into the\n" +
			"envelope instead of printed.",
		Example: "  ratline nginx -- -t\n" +
			"  ratline nginx -- -T | less\n" +
			"  ratline nginx -- -V\n" +
			"  ratline nginx -- reload\n" +
			"  ratline nginx --dry-run -- reload",
		ValidArgsFunction: completeFixed("-t", "-T", "-v", "-V", "-q", "reload"),
		RunE: func(cmd *cobra.Command, args []string) error {
			argv, err := passthroughArgs(args)
			if err != nil {
				return err
			}
			plan, err := planNginx(argv)
			if err != nil {
				return err
			}
			return g.runNginx(cmd.Context(), plan)
		},
	}
	ProgramArgvExample(cmd, "ratline nginx -- -t")
	// Mutating because reload is, and SkipLock because -t is not: the lock is taken at
	// run time, on the reload path only. See cmd_passthrough.go.
	return SkipLock(Mutating(ProgramArgv(cmd)))
}

func (g *Globals) runNginx(ctx context.Context, plan *nginxPlan) error {
	if !plan.Reload {
		return g.passthroughRun(ctx, system.Cmd{Name: "nginx", Args: plan.Args, Label: "nginx"}, false,
			map[string]any{"action": "read"},
			func(code int) string {
				if len(plan.Args) > 0 && (plan.Args[0] == "-t" || plan.Args[0] == "-T") {
					return "the configuration does not test clean; nginx named the file and line above, and nothing was reloaded"
				}
				return ""
			})
	}

	if err := g.lockNow(); err != nil {
		return err
	}
	mgr := &nginx.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, DryRun: g.DryRun}
	// The test runs under --dry-run too: it is a read, and a rehearsal that says "would
	// reload" about a configuration that would be refused is a rehearsal that lies.
	if err := mgr.Test(ctx); err != nil {
		return err
	}
	if g.DryRun {
		if g.JSON {
			return g.EmitJSON(map[string]any{"action": "reload", "tested": true, "reloaded": false, "dry_run": true})
		}
		g.Printf("the configuration tests clean; would reload nginx through systemd (systemctl reload nginx)\n")
		return nil
	}
	if err := mgr.Reload(ctx); err != nil {
		return err
	}
	if g.JSON {
		return g.EmitJSON(map[string]any{"action": "reload", "tested": true, "reloaded": true})
	}
	g.Printf("the configuration tests clean; nginx reloaded\n")
	return nil
}
