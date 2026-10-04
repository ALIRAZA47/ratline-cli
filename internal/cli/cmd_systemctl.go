package cli

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/nginx"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// `ratline systemctl -- <verb> [unit...]` — systemctl with ratline's unit names, reads on
// anything, control only on what ratline manages. See cmd_passthrough.go for the locking.

// systemctlReadVerbs answer a question and change nothing. Allowed on any unit: knowing
// what sshd or the panel is doing is part of diagnosing a site.
var systemctlReadVerbs = map[string]bool{
	"status": true, "show": true, "cat": true,
	"is-active": true, "is-enabled": true, "is-failed": true,
	"list-units": true, "list-timers": true, "list-unit-files": true,
}

// systemctlControlVerbs change a unit's running state and nothing about its definition.
// Allowed only on units ratline manages, and on nginx.
var systemctlControlVerbs = map[string]bool{
	"start": true, "stop": true, "restart": true, "reload": true,
	"try-restart": true, "reload-or-restart": true, "reset-failed": true,
}

// systemctlReadFlags are the switches passed through on a read verb. Boolean switches by
// name; switches that take a value only in their one-element --name=value form, so that a
// value can never be read as a unit or a unit as a value.
var (
	systemctlReadBoolFlags = map[string]bool{
		"--all": true, "-a": true, "--full": true, "-l": true, "--failed": true,
		"--plain": true, "--no-legend": true, "--value": true, "--quiet": true,
		"--recursive": true, "-r": true,
	}
	systemctlReadValueFlags = map[string]bool{
		"--state": true, "--type": true, "--property": true, "--lines": true, "--output": true,
	}
)

// systemctlRefusals are the verbs ratline will not pass on, each with where to go
// instead. A verb in neither list nor here is refused with the general answer.
var systemctlRefusals = map[string]string{
	"enable":             "a site is switched on and off with 'ratline site enable <domain>' and 'ratline site disable <domain>', which keep nginx, the unit and the state database in step; ratline's own timers are installed by 'ratline init' and 'ratline update'",
	"disable":            "a site is switched on and off with 'ratline site enable <domain>' and 'ratline site disable <domain>', which keep nginx, the unit and the state database in step",
	"reenable":           "'ratline reconcile --fix' re-renders and re-enables what has drifted",
	"mask":               "a masked managed unit is drift that nothing in state records; 'ratline site disable <domain>' takes a site out of service properly",
	"unmask":             "ratline never masks a unit; 'ratline reconcile --fix' puts a drifted one back",
	"edit":               "a hand edit to a managed unit is drift that 'ratline reconcile --fix' will undo; limits are set with 'ratline site scale <domain>' (--memory-max, --cpu-quota)",
	"revert":             "'ratline reconcile --fix' re-renders a managed unit from state",
	"set-property":       "limits are set with 'ratline site scale <domain>' (--memory-max, --cpu-quota), which records them so the next render keeps them",
	"daemon-reload":      "ratline reloads systemd itself after writing a unit; 'ratline reconcile --fix' re-renders and reloads anything that has drifted",
	"daemon-reexec":      "ratline has no reason to re-execute systemd, and neither does a site",
	"kill":               "'ratline systemctl -- restart <domain>' restarts a site's unit; 'ratline site restart <domain>' also health-checks it",
	"link":               "ratline installs its units itself; 'ratline reconcile --fix' installs one that is missing",
	"preset":             "ratline enables exactly the units it manages; 'ratline reconcile --fix' corrects one that has drifted",
	"preset-all":         "ratline enables exactly the units it manages; 'ratline reconcile --fix' corrects one that has drifted",
	"add-wants":          "ratline's units carry their own dependencies; a hand-added one is drift",
	"add-requires":       "ratline's units carry their own dependencies; a hand-added one is drift",
	"set-environment":    "a site's environment is set with 'ratline site env set <domain> --stdin', which keeps the value out of every process's view",
	"unset-environment":  "a site's environment is changed with 'ratline site env unset <domain>'",
	"import-environment": "a site's environment is set with 'ratline site env set <domain> --stdin'",
	"freeze":             "a frozen site answers nothing and reports nothing; 'ratline site stop <domain>' stops it honestly",
	"thaw":               "ratline never freezes a unit",
	"clean":              "removing a unit's state, cache or log directories is not a passthrough operation; 'ratline site delete <domain>' removes a site and what it owns",
	"isolate":            "this changes the whole machine; ratline will not do that for you",
	"set-default":        "this changes the whole machine; ratline will not do that for you",
	"default":            "this changes the whole machine; ratline will not do that for you",
	"rescue":             "this changes the whole machine; ratline will not do that for you",
	"emergency":          "this changes the whole machine; ratline will not do that for you",
	"halt":               "this changes the whole machine; ratline will not do that for you",
	"poweroff":           "this changes the whole machine; ratline will not do that for you",
	"reboot":             "this changes the whole machine; ratline will not do that for you",
	"kexec":              "this changes the whole machine; ratline will not do that for you",
	"suspend":            "this changes the whole machine; ratline will not do that for you",
	"hibernate":          "this changes the whole machine; ratline will not do that for you",
	"hybrid-sleep":       "this changes the whole machine; ratline will not do that for you",
	"switch-root":        "this changes the whole machine; ratline will not do that for you",
	"exit":               "this changes the whole machine; ratline will not do that for you",
	"soft-reboot":        "this changes the whole machine; ratline will not do that for you",
}

// unitSuffixes are what make a name a unit rather than a site. A domain never ends in
// one, and a unit given without one is a .service, as systemctl itself assumes.
var unitSuffixes = []string{
	".service", ".timer", ".socket", ".target", ".path", ".mount", ".automount",
	".slice", ".scope", ".device", ".swap",
}

// readUnitRe is what a unit name or pattern may contain on a read verb: systemd's unit
// characters, its \x escapes, and glob characters, since `status 'ratline-*'` is how an
// operator sees every site at once.
var readUnitRe = regexp.MustCompile(`^[A-Za-z0-9:_.\\@*?\[\]-]+$`)

// journaldInstanceRe is a site's journald namespace instance, which a control verb may
// touch when its slug belongs to a site in state.
var journaldInstanceRe = regexp.MustCompile(`^systemd-journald@([A-Za-z0-9_.-]+)\.service$`)

func hasUnitSuffix(s string) bool {
	for _, suf := range unitSuffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

// systemctlPlan is what `ratline systemctl` decided.
type systemctlPlan struct {
	Verb    string
	Control bool
	// Argv is systemctl's argv, --no-pager first.
	Argv []string
	// Units are the unit names after translation, in the order given.
	Units []string
	// Sites maps each domain the operator typed to the unit it became.
	Sites map[string]string
	// TestNginx means nginx is about to be (re)started or reloaded, so its configuration
	// is tested first: a restart of a configuration that does not parse is nginx down.
	TestNginx bool
}

// planSystemctl reads the verb, resolves every name and refuses whatever ratline does not
// pass on. It runs nothing.
func (g *Globals) planSystemctl(ctx context.Context, args []string) (*systemctlPlan, error) {
	if len(args) == 0 {
		return nil, rlerr.Usagef("which systemctl verb?").
			WithHint("for example 'ratline systemctl -- status app.example.com'")
	}
	verb := args[0]
	p := &systemctlPlan{Verb: verb, Sites: map[string]string{}}
	switch {
	case systemctlReadVerbs[verb]:
	case systemctlControlVerbs[verb]:
		p.Control = true
	case strings.HasPrefix(verb, "-"):
		return nil, rlerr.Usagef("the verb comes first, before any switch (%s)", verb).
			WithHint("for example 'ratline systemctl -- list-units --failed'")
	default:
		if why, ok := systemctlRefusals[verb]; ok {
			return nil, rlerr.Usagef("ratline does not pass 'systemctl %s' on", verb).WithHint("%s", why)
		}
		return nil, rlerr.Usagef("ratline does not pass 'systemctl %s' on", verb).
			WithHint("the verbs it does are status, show, cat, is-active, is-enabled, is-failed, list-units, " +
				"list-timers and list-unit-files on any unit, and start, stop, restart, reload, try-restart, " +
				"reload-or-restart and reset-failed on the units ratline manages")
	}

	var flags []string
	for _, a := range args[1:] {
		if strings.HasPrefix(a, "-") {
			if err := checkSystemctlFlag(a, p.Control); err != nil {
				return nil, err
			}
			flags = append(flags, a)
			continue
		}
		u, err := g.resolveSystemctlName(ctx, a, p)
		if err != nil {
			return nil, err
		}
		p.Units = append(p.Units, u)
	}

	if p.Control {
		if len(p.Units) == 0 {
			return nil, rlerr.Usagef("'systemctl %s' needs a unit or a site", verb).
				WithHint("for example 'ratline systemctl -- %s app.example.com'", verb)
		}
		for _, u := range p.Units {
			if err := g.checkManagedUnit(ctx, u); err != nil {
				return nil, err
			}
			if u == "nginx.service" && verb != "stop" && verb != "reset-failed" {
				p.TestNginx = true
			}
		}
	} else if verb == "cat" && len(p.Units) == 0 {
		return nil, rlerr.Usagef("'systemctl cat' needs a unit or a site").
			WithHint("for example 'ratline systemctl -- cat app.example.com'")
	}

	p.Argv = append([]string{"--no-pager", verb}, flags...)
	if len(p.Units) > 0 {
		// A bare -- before the names, so that nothing a name could become is read as a
		// switch. Every name has been validated already; this is the belt.
		p.Argv = append(p.Argv, "--")
		p.Argv = append(p.Argv, p.Units...)
	}
	return p, nil
}

// systemctlFields splits each argument on whitespace. Unit names, domains and the switches
// passed through never contain any, so a field the menu or the panel collected as
// "restart app.example.com" splits without ambiguity — and a value with a space in it is
// one the plan would refuse anyway.
func systemctlFields(args []string) []string {
	var out []string
	for _, a := range args {
		out = append(out, strings.Fields(a)...)
	}
	return out
}

func checkSystemctlFlag(a string, control bool) error {
	if control {
		return rlerr.Usagef("ratline passes no switches to a control verb (%s)", a).
			WithHint("the unit is started, stopped or reloaded exactly as systemctl would by default")
	}
	if systemctlReadBoolFlags[a] {
		return nil
	}
	if name, _, ok := strings.Cut(a, "="); ok && systemctlReadValueFlags[name] {
		return nil
	}
	if systemctlReadValueFlags[a] {
		return rlerr.Usagef("%s needs its value in the same argument", a).
			WithHint("write %s=<value>, so the value can never be read as a unit", a)
	}
	switch a {
	case "-H", "--host", "-M", "--machine", "--root", "--image", "--user", "--global":
		return rlerr.Usagef("ratline reads this machine's system manager only (%s)", a).
			WithHint("ratline's units are system units on this server")
	}
	return rlerr.Usagef("ratline does not pass %s to systemctl", a).
		WithHint("on a read it passes --all, --full, --failed, --plain, --no-legend, --value, --quiet, --recursive, " +
			"and --state=, --type=, --property=, --lines= and --output= with their value")
}

// resolveSystemctlName turns one name the operator typed into a unit name: a site's
// domain into the site's service, anything else checked as a unit or a pattern.
//
// A site wins over a unit of the same name. A dotted name with no unit suffix is the only
// shape that could be either, and on a ratline server a site is the likelier meaning; one
// that is not a site is taken as a unit, as `systemctl status php8.2-fpm` means.
func (g *Globals) resolveSystemctlName(ctx context.Context, name string, p *systemctlPlan) (string, error) {
	if name == "" || strings.HasPrefix(name, "-") {
		return "", rlerr.Usagef("invalid unit name %q", name)
	}
	if !hasUnitSuffix(name) && strings.Contains(name, ".") && !hasGlob(name) {
		if s, err := g.lookupSite(ctx, name); err != nil {
			return "", err
		} else if s != nil {
			if !s.Dynamic() {
				return "", rlerr.Usagef("%s is a %s site, which has no unit: nginx serves it from disk", s.Domain, s.Runtime).
					WithHint("'ratline systemctl -- status nginx' is the service behind it")
			}
			u := validate.UnitName(s.Owner, s.Domain)
			p.Sites[name] = u
			return u, nil
		}
	}
	if p.Control {
		if hasGlob(name) {
			return "", rlerr.Usagef("a control verb takes unit names, not a pattern (%s)", name).
				WithHint("name each unit, or the site's domain")
		}
		full := name
		if !hasUnitSuffix(full) {
			full += ".service"
		}
		if err := validate.SystemdUnitName(full); err != nil {
			return "", err
		}
		return full, nil
	}
	if len(name) > 255 || !readUnitRe.MatchString(name) {
		return "", rlerr.Usagef("invalid unit name %q", name).
			WithHint("a unit name, a pattern such as 'ratline-*', or a site's domain")
	}
	return name, nil
}

// lookupSite finds a site by domain or alias, returning nil when there is none. Only a
// name that validates as a domain is looked up at all.
func (g *Globals) lookupSite(ctx context.Context, name string) (*state.Site, error) {
	if _, err := validate.Domain(name); err != nil {
		return nil, nil //nolint:nilerr // not a domain, so not a site; it is checked as a unit instead
	}
	st, err := g.Store(ctx)
	if err != nil {
		return nil, err
	}
	s, err := st.FindSiteByName(ctx, name)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	return s, err
}

// checkManagedUnit is the gate on a control verb: ratline's own units, a site's journald
// namespace instance, and nginx. Everything else on the machine is the operator's, and
// ratline neither starts nor stops it.
func (g *Globals) checkManagedUnit(ctx context.Context, u string) error {
	switch {
	case u == "nginx.service", u == "ratline.target":
		return nil
	case strings.HasPrefix(u, "ratline-") && (strings.HasSuffix(u, ".service") || strings.HasSuffix(u, ".timer")):
		return nil
	}
	if m := journaldInstanceRe.FindStringSubmatch(u); m != nil {
		st, err := g.Store(ctx)
		if err != nil {
			return err
		}
		sites, err := st.ListSites(ctx, state.SiteFilter{})
		if err != nil {
			return err
		}
		for _, s := range sites {
			if s.Slug == m[1] {
				return nil
			}
		}
		return rlerr.Usagef("%s is not the journal namespace of any site ratline manages", u).
			WithHint("a site's namespace is its slug, the middle of its unit name ratline-<slug>.service, which 'ratline site show <domain>' prints")
	}
	return rlerr.Usagef("ratline only starts, stops and reloads the units it manages, and %s is not one", u).
		WithHint("its own are ratline-*.service, ratline-*.timer, ratline.target, a site's systemd-journald@<slug>.service, " +
			"and nginx; anything else is yours to run with systemctl directly")
}

func newSystemctlCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "systemctl -- <verb> [unit...]",
		Short:   "Run systemctl with site names: read any unit, control ratline's own",
		GroupID: GroupOps,
		Args:    passthroughUsage("ratline systemctl -- status app.example.com"),
		Long: "systemctl, found where ratline finds it, with two things added: a site's domain\n" +
			"is accepted wherever a unit is, and becomes the site's unit — so 'restart\n" +
			"app.example.com' needs nobody to remember a slug — and --no-pager is always\n" +
			"passed, so the output pipes and nothing waits on a terminal.\n\n" +
			"Reads work on any unit or pattern: status, show, cat, is-active, is-enabled,\n" +
			"is-failed, list-units, list-timers and list-unit-files, with --all, --full,\n" +
			"--failed, --plain, --no-legend, --value, --quiet, --recursive, and --state=,\n" +
			"--type=, --property=, --lines= and --output= (value in the same argument).\n" +
			"A read never takes the server lock, so it answers in the middle of a deploy.\n\n" +
			"Control — start, stop, restart, reload, try-restart, reload-or-restart and\n" +
			"reset-failed — works only on the units ratline manages (ratline-*.service,\n" +
			"ratline-*.timer, ratline.target, a site's systemd-journald@<slug>.service) and\n" +
			"nginx, named one by one: no patterns, no switches. It takes the server lock,\n" +
			"and starting, restarting or reloading nginx tests its configuration first.\n" +
			"'ratline site restart' does more than this for a site — it prepares the socket\n" +
			"directory and health-checks the result — and is the one to use after a change.\n\n" +
			"Refused, each pointing at what does it properly: enable and disable ('site\n" +
			"enable'/'site disable'), mask, edit and set-property ('site scale', 'reconcile\n" +
			"--fix'), daemon-reload, kill, and anything that changes the whole machine.\n\n" +
			"systemctl's own exit code is reported but not adopted: 'status' of a stopped\n" +
			"unit exits 3, which ratline reports as exit 4 (external) naming the 3.",
		Example: "  ratline systemctl -- status app.example.com\n" +
			"  ratline systemctl -- restart app.example.com\n" +
			"  ratline systemctl -- list-units 'ratline-*' --all\n" +
			"  ratline systemctl -- list-timers 'ratline-*'\n" +
			"  ratline systemctl -- cat app.example.com\n" +
			"  ratline systemctl -- reload nginx\n" +
			"  ratline systemctl --dry-run -- restart app.example.com",
		ValidArgsFunction: func(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				verbs := make([]string, 0, len(systemctlReadVerbs)+len(systemctlControlVerbs))
				for v := range systemctlReadVerbs {
					verbs = append(verbs, v)
				}
				for v := range systemctlControlVerbs {
					verbs = append(verbs, v)
				}
				return completeFixed(verbs...)(cmd, args, prefix)
			}
			return g.completeDomains(cmd, nil, prefix)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			plan, err := g.planSystemctl(ctx, systemctlFields(args))
			if err != nil {
				return err
			}
			return g.runSystemctl(ctx, plan)
		},
	}
	ProgramArgvExample(cmd, "ratline systemctl -- list-units --failed")
	// Mutating because a control verb is, SkipLock because a read is not: the lock is
	// taken at run time, on the control path only. See cmd_passthrough.go.
	return SkipLock(Mutating(ProgramArgv(cmd)))
}

func (g *Globals) runSystemctl(ctx context.Context, p *systemctlPlan) error {
	extra := map[string]any{"verb": p.Verb, "units": p.Units}
	if len(p.Sites) > 0 {
		extra["sites"] = p.Sites
	}

	if !p.Control {
		return g.passthroughRun(ctx, system.Cmd{Name: "systemctl", Args: p.Argv, Label: "systemctl"}, false, extra,
			func(code int) string {
				if code == 3 && (p.Verb == "status" || p.Verb == "is-active") {
					return "systemctl exits 3 when a unit is not running; that is its answer, not a failure to ask"
				}
				return ""
			})
	}

	if g.DryRun {
		// Nothing runs, not even nginx -t: a rehearsal of a control verb is the plan.
		if g.JSON {
			extra["dry_run"] = true
			extra["args"] = p.Argv
			extra["tests_nginx_first"] = p.TestNginx
			return g.EmitJSON(extra)
		}
		if p.TestNginx {
			g.Printf("would test nginx's configuration (nginx -t), then\n")
		}
		g.Printf("would run: systemctl %s\n", strings.Join(p.Argv, " "))
		return nil
	}

	if err := g.lockNow(); err != nil {
		return err
	}
	if p.TestNginx {
		mgr := &nginx.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner, DryRun: g.DryRun}
		if err := mgr.Test(ctx); err != nil {
			return err
		}
	}
	return g.passthroughRun(ctx, system.Cmd{
		Name: "systemctl", Args: p.Argv, Label: "systemctl " + p.Verb,
		Mutates: true, Timeout: passthroughControlTimeout,
	}, false, extra, func(int) string {
		if len(p.Units) == 1 {
			return "'ratline journalctl " + p.Units[0] + " -- -n 50' shows why, from the journal the unit actually logs into"
		}
		return ""
	})
}
