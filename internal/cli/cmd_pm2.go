package cli

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// `ratline pm2` — run pm2 against a tenant's shared daemon.
//
// The gap it fills: a tenant's PM2 daemon runs as the tenant, with PM2_HOME under
// ~/.ratline/pm2/<node> and a managed node that is on nobody's PATH. A bare `pm2 list`
// as root talks to root's own ~/.pm2 — and, finding no daemon there, starts one, which
// is the worst possible answer to "what is running". This resolves the daemon from its
// unit, drops to its tenant and hands pm2 the environment the daemon itself has.
//
// Pass-through with an allowlist rather than everything. pm2 also has verbs that would
// fight the units ratline owns — `kill` stops the daemon behind systemd's back, `delete`
// and `stop` take a site's application away while its unit still says active, `startup`
// writes a second boot unit for the same daemon — and each of those is refused with the
// ratline command that does it properly.

// pm2ReadVerbs only look. They are the ones allowed against every daemon at once.
var pm2ReadVerbs = map[string]bool{
	"list": true, "ls": true, "l": true, "status": true, "jlist": true, "prettylist": true,
}

// pm2TargetVerbs need one daemon: they inspect or act on its applications.
var pm2TargetVerbs = map[string]bool{
	"describe": true, "desc": true, "info": true, "show": true, "env": true, "id": true, "pid": true,
	"logs": true, "log": true, "monit": true, "imonit": true, "dashboard": true, "dash": true,
	"report": true, "ping": true,
	"restart": true, "reload": true, "reset": true, "flush": true, "reloadLogs": true,
	"sendSignal": true, "trigger": true,
}

// pm2Streaming run until interrupted, so they get a day rather than the default timeout.
var pm2Streaming = map[string]bool{
	"logs": true, "log": true, "monit": true, "imonit": true, "dashboard": true, "dash": true,
}

// pm2AppVerbs take an application name. Given a site and no name, the site's own
// application is meant — said in the help, so it is not a guess.
var pm2AppVerbs = map[string]bool{
	"describe": true, "desc": true, "info": true, "show": true, "logs": true, "log": true,
	"restart": true, "reload": true, "reset": true, "flush": true,
}

// pm2Refused are the verbs with a better home, and where it is.
var pm2Refused = map[string]string{
	"kill":        "it stops the tenant's daemon behind systemd's back; restart it with 'ratline systemctl -- restart <unit>', which brings every site in it back",
	"delete":      "the site's unit would still say active; use 'ratline site disable <domain>' or 'ratline site delete <domain>'",
	"del":         "the site's unit would still say active; use 'ratline site disable <domain>' or 'ratline site delete <domain>'",
	"stop":        "the site's unit would still say active; use 'ratline site disable <domain>'",
	"start":       "an application started here is not a site, and is gone on the daemon's next restart; use 'ratline site add' or 'ratline site worker add'",
	"scale":       "the site's configuration would disagree with what is running; use 'ratline site scale <domain> --instances N'",
	"save":        "systemd brings the applications back, from each site's unit; a dump is a second, stale copy",
	"dump":        "systemd brings the applications back, from each site's unit; a dump is a second, stale copy",
	"resurrect":   "systemd brings the applications back, from each site's unit; restart the daemon instead",
	"startup":     "the daemon is already a systemd unit; a second boot unit would start a second daemon",
	"unstartup":   "the daemon is a unit ratline owns; remove the tenant's PM2 sites to remove it",
	"update":      "upgrade PM2 with 'ratline runtime install node <version> --with-pm2', then restart the daemon",
	"updatePM2":   "upgrade PM2 with 'ratline runtime install node <version> --with-pm2', then restart the daemon",
	"install":     "a PM2 module is code run inside the daemon of every site of the tenant; it is not something ratline manages",
	"uninstall":   "PM2 modules are not something ratline manages",
	"link":        "PM2 Plus sends the daemon's process data to a third party; it is not something ratline turns on",
	"plus":        "PM2 Plus sends the daemon's process data to a third party; it is not something ratline turns on",
	"deploy":      "use 'ratline site deploy <domain>'",
	"serve":       "a static site is 'ratline site add <domain> --runtime static', served by nginx",
	"ecosystem":   "ratline writes each site's ecosystem.config.json itself",
	"init":        "ratline writes each site's ecosystem.config.json itself",
	"cleardump":   "systemd brings the applications back, from each site's unit; there is no dump to clear",
	"attach":      "it takes over the application's standard input; use 'ratline pm2 <site> -- logs' to watch it",
	"set":         "PM2 module configuration is not something ratline manages",
	"multiset":    "PM2 module configuration is not something ratline manages",
	"unset":       "PM2 module configuration is not something ratline manages",
	"autoinstall": "PM2 modules are not something ratline manages",
}

// pm2Daemon is one tenant daemon, as its unit file describes it — which is what is
// actually running, whatever any site row says it ought to be.
type pm2Daemon struct {
	Unit  string `json:"unit"`
	Owner string `json:"user"`
	Home  string `json:"pm2_home"`
	PM2   string `json:"pm2"`
	Path  string `json:"-"`
	// App is the site's application name, when the target was a site.
	App string `json:"app,omitempty"`
}

func newPM2Command(g *Globals) *cobra.Command {
	var node string
	cmd := &cobra.Command{
		Use:     "pm2 [<domain>|<user>] -- <pm2 arguments>",
		Short:   "Run pm2 against a tenant's shared PM2 daemon",
		GroupID: GroupRuntimes,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return rlerr.Usagef("nothing to ask pm2").
					WithHint("ratline pm2 -- list, or ratline pm2 app.example.com -- logs")
			}
			return nil
		},
		Long: "Every tenant's PM2-supervised sites share one PM2 daemon per Node version, run by\n" +
			"systemd as that tenant (ratline-pm2@<user>.<node>.service). This runs pm2 against\n" +
			"it as the tenant, with the daemon's own PM2_HOME and node — a bare 'pm2' as root\n" +
			"talks to root's ~/.pm2 instead, and starts an empty daemon there.\n\n" +
			"With a domain, the daemon that site runs in; and a verb that takes an application\n" +
			"name (logs, describe, restart, reload, reset, flush) is given the site's own when\n" +
			"none follows. With a user, that tenant's daemon (--node picks one when they have\n" +
			"sites on more than one Node version). With neither, a listing verb — list, status,\n" +
			"jlist, prettylist — runs against every daemon on the server in turn.\n\n" +
			"The verbs that would fight ratline's own units are refused, each with the command\n" +
			"that does it properly: kill, delete, stop, start, scale, save, resurrect, startup,\n" +
			"update, install and their relatives. A daemon that is not running is not asked at\n" +
			"all, because every pm2 command that cannot reach a daemon starts one.\n\n" +
			"Nothing is interpreted by a shell; everything after -- is pm2's argv.",
		Example: "  ratline pm2 -- list\n" +
			"  ratline pm2 app.example.com -- logs --lines 200\n" +
			"  ratline pm2 app.example.com -- describe\n" +
			"  ratline pm2 acme -- monit\n" +
			"  ratline pm2 acme --node 18 -- list",
		RunE: func(cmd *cobra.Command, args []string) error {
			// `ratline pm2 <target> -- <args>`, or without the -- when the first word is
			// not itself a pm2 verb: `ratline pm2 acme list` names a user, `ratline pm2 list`
			// does not.
			target, pm2Args := "", args
			dash := cmd.ArgsLenAtDash()
			if dash == 1 || (dash < 0 && len(args) > 1 && !isPM2Verb(args[0])) {
				target, pm2Args = args[0], args[1:]
			}
			if len(pm2Args) == 0 {
				return rlerr.Usagef("nothing to ask pm2 about %s", target).
					WithHint("the pm2 arguments go after --, for example 'ratline pm2 %s -- logs'", target)
			}
			daemons, err := g.resolvePM2Targets(cmd.Context(), target, node)
			if err != nil {
				return err
			}
			argv, err := pm2Argv(pm2Args, daemons)
			if err != nil {
				return err
			}
			return g.runPM2(cmd.Context(), daemons, argv)
		},
	}
	cmd.Flags().StringVar(&node, "node", "", "With a user: the daemon for this Node version")
	// Mutating so the audit log records it and the panel treats it as one; no lock,
	// because `pm2 logs` can run for hours and the lock would stop every renewal on
	// the box for as long as somebody watched.
	return ProgramArgvExample(ProgramArgv(SkipLock(Mutating(cmd))), "ratline pm2 app.example.com -- logs --lines 100")
}

// pm2Argv checks pm2's arguments against the allowlist and fills in the site's
// application name where the verb takes one and none was given.
func pm2Argv(args []string, daemons []*pm2Daemon) ([]string, error) {
	verb := args[0]
	switch {
	case verb == "--version" || verb == "-v" || verb == "-V" || verb == "version":
		return []string{"--version"}, nil
	case strings.HasPrefix(verb, "-"):
		return nil, rlerr.Usagef("pm2's verb comes first, got %q", verb).
			WithHint("ratline pm2 <site> -- logs --lines 100")
	}
	if why, refused := pm2Refused[verb]; refused {
		return nil, rlerr.Usagef("'pm2 %s' is not passed through", verb).WithHint("%s", why)
	}
	if !pm2ReadVerbs[verb] && !pm2TargetVerbs[verb] {
		return nil, rlerr.Usagef("'pm2 %s' is not one ratline passes through", verb).
			WithHint("allowed: %s", strings.Join(allowedPM2Verbs(), ", "))
	}
	if len(daemons) != 1 && !pm2ReadVerbs[verb] {
		return nil, rlerr.Usagef("'pm2 %s' needs one daemon", verb).
			WithHint("name a site or a user: ratline pm2 app.example.com -- %s", verb)
	}
	argv := append([]string(nil), args...)
	if len(daemons) == 1 && daemons[0].App != "" && pm2AppVerbs[verb] && !hasPositional(args[1:]) {
		argv = append([]string{verb, daemons[0].App}, args[1:]...)
	}
	return argv, nil
}

// hasPositional reports whether anything after the verb is not a flag. `--lines 100`
// carries a value, which is a flag's and not an application's, so a flag that takes one
// swallows the word after it.
func hasPositional(rest []string) bool {
	valued := map[string]bool{"--lines": true, "--timestamp": false}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if strings.HasPrefix(a, "-") {
			if valued[a] {
				i++
			}
			continue
		}
		return true
	}
	return false
}

func isPM2Verb(w string) bool {
	_, refused := pm2Refused[w]
	return pm2ReadVerbs[w] || pm2TargetVerbs[w] || refused
}

func allowedPM2Verbs() []string {
	var out []string
	for v := range pm2ReadVerbs {
		out = append(out, v)
	}
	for v := range pm2TargetVerbs {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// resolvePM2Targets turns the optional target into the daemons to ask.
func (g *Globals) resolvePM2Targets(ctx context.Context, target, node string) ([]*pm2Daemon, error) {
	if node != "" && target == "" {
		return nil, rlerr.Usagef("--node picks one of a user's daemons; name the user too")
	}
	switch {
	case target == "":
		units, err := pm2DaemonUnits(g.Cfg.Paths.SystemdDir, "")
		if err != nil {
			return nil, err
		}
		if len(units) == 0 {
			return nil, rlerr.Preconditionf("no tenant has a PM2 daemon on this server").
				WithHint("a node site gets one with 'ratline site add <domain> --runtime node'")
		}
		return g.readPM2Daemons(units)

	case strings.Contains(target, "."):
		// A domain: usernames cannot contain a dot.
		if node != "" {
			return nil, rlerr.Usagef("--node applies to a user; a site's daemon follows its Node version")
		}
		mgr, err := g.siteManager(ctx)
		if err != nil {
			return nil, err
		}
		site, err := mgr.State.FindSiteByName(ctx, target)
		if err != nil {
			return nil, err
		}
		name := mgr.PM2DaemonUnit(site)
		if name == "" {
			return nil, rlerr.Preconditionf("%s is not supervised by PM2", site.Domain).
				WithHint("its service is ratline-%s.service: ratline journalctl %s", site.Slug, site.Domain)
		}
		if mgr.RunsOwnPM2(site) {
			return nil, rlerr.Preconditionf("%s still runs its own PM2 daemon, from an earlier release", site.Domain).
				WithHint("ratline reconcile --fix, then ratline site restart %s, moves it into its tenant's", site.Domain)
		}
		ds, err := g.readPM2Daemons([]string{name})
		if err != nil {
			return nil, err
		}
		if ds[0].Owner != site.Owner {
			return nil, rlerr.Genericf("%s names %s as its user, but %s belongs to %s", name, ds[0].Owner, site.Domain, site.Owner)
		}
		ds[0].App = site.Slug
		return ds, nil

	default:
		if err := validate.Username(target); err != nil {
			return nil, err
		}
		units, err := pm2DaemonUnits(g.Cfg.Paths.SystemdDir, target)
		if err != nil {
			return nil, err
		}
		if node != "" {
			if err := validate.NodeVersion(node); err != nil {
				return nil, err
			}
			want := validate.PM2UnitName(target, "node"+strings.TrimPrefix(node, "v"))
			units = filterStrings(units, want)
		}
		switch len(units) {
		case 0:
			if node != "" {
				return nil, rlerr.Preconditionf("%s has no PM2 daemon for Node %s", target, node)
			}
			return nil, rlerr.Preconditionf("%s has no PM2 daemon", target).
				WithHint("a tenant gets one with their first node site; 'ratline site list --user %s' shows theirs", target)
		case 1:
			return g.readPM2Daemons(units)
		}
		return nil, rlerr.Usagef("%s has a PM2 daemon for more than one Node version: %s", target, strings.Join(units, ", ")).
			WithHint("pick one with --node, or name a site instead of the user")
	}
}

func filterStrings(in []string, keep string) []string {
	var out []string
	for _, s := range in {
		if s == keep {
			out = append(out, s)
		}
	}
	return out
}

// pm2DaemonUnits lists the daemon unit files on disk, for one user or all of them.
func pm2DaemonUnits(dir, user string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", dir)
	}
	prefix := "ratline-pm2@"
	if user != "" {
		prefix += user + "."
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) && unit.IsPM2DaemonUnit(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// readPM2Daemons reads what each daemon runs as and with, from its unit file. The unit
// is root's, in /etc/systemd/system, and carries ratline's header — so it is the
// trusted description of the daemon, and nothing here is taken from the tenant's tree.
func (g *Globals) readPM2Daemons(units []string) ([]*pm2Daemon, error) {
	var out []*pm2Daemon
	for _, name := range units {
		path := filepath.Join(g.Cfg.Paths.SystemdDir, name)
		if managed, err := system.HasManagedHeader(path); err != nil || !managed {
			return nil, rlerr.Preconditionf("%s was not written by ratline", path)
		}
		body, err := system.ReadFileLimit(path, 1<<20)
		if err != nil {
			return nil, err
		}
		d := parsePM2Unit(name, string(body))
		if d.Owner == "" || d.Home == "" || d.PM2 == "" || d.Owner == "root" {
			return nil, rlerr.Preconditionf("%s does not describe a tenant's PM2 daemon", path).
				WithHint("ratline reconcile --fix re-renders it")
		}
		out = append(out, d)
	}
	return out, nil
}

// parsePM2Unit reads the four facts the passthrough needs from a daemon's unit.
func parsePM2Unit(name, body string) *pm2Daemon {
	d := &pm2Daemon{Unit: name}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "User="):
			d.Owner = strings.TrimPrefix(line, "User=")
		case strings.HasPrefix(line, "Environment=PM2_HOME="):
			d.Home = strings.TrimPrefix(line, "Environment=PM2_HOME=")
		case strings.HasPrefix(line, "Environment=PATH="):
			d.Path = strings.TrimPrefix(line, "Environment=PATH=")
		case strings.HasPrefix(line, "ExecStart="):
			if f := strings.Fields(strings.TrimPrefix(line, "ExecStart=")); len(f) == 2 && f[1] == "ping" {
				d.PM2 = f[0]
			}
		}
	}
	return d
}

// runPM2 runs the argv against each daemon in turn, as its tenant.
func (g *Globals) runPM2(ctx context.Context, daemons []*pm2Daemon, argv []string) error {
	timeout := 2 * time.Minute
	if pm2Streaming[argv[0]] {
		// One envelope at the end of a stream that never ends is no envelope at all,
		// and the panel — which always asks for --json — would hold a job open for a day.
		if g.JSON {
			return rlerr.Usagef("'pm2 %s' follows until interrupted, so it has no --json form", argv[0]).
				WithHint("use 'pm2 jlist' or 'pm2 describe' for something to parse")
		}
		timeout = 24 * time.Hour
	}
	if g.DryRun {
		if g.JSON {
			return g.EmitJSON(map[string]any{"daemons": daemons, "args": argv, "dry_run": true})
		}
		for _, d := range daemons {
			g.Printf("would run as %s, with PM2_HOME=%s:\n    %s %s\n", d.Owner, d.Home, d.PM2, strings.Join(argv, " "))
		}
		return nil
	}

	type outcome struct {
		*pm2Daemon
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		Error    string `json:"error,omitempty"`
	}
	var (
		results []outcome
		failed  error
	)
	for _, d := range daemons {
		if len(daemons) > 1 && !g.JSON {
			g.Printf("== %s (%s)\n", d.Unit, d.Owner)
		}
		if !(&unit.Manager{Cfg: g.Cfg, Log: g.Log, Runner: g.Runner}).IsActive(ctx, d.Unit) {
			// Asking would start a daemon outside the unit; there is nothing to ask.
			err := rlerr.Preconditionf("%s is not running", d.Unit).
				WithHint("starting any of %s's PM2 sites starts it: ratline site start <domain>", d.Owner)
			results = append(results, outcome{pm2Daemon: d, Error: err.Error()})
			if len(daemons) == 1 {
				return err
			}
			g.Log.Warn("skipped a daemon that is not running", "unit", d.Unit)
			continue
		}
		id, err := system.LookupIdentity(d.Owner)
		if err != nil {
			return err
		}
		c := system.Cmd{
			Path: d.PM2, Args: argv, As: id, Dir: d.Home,
			Env:     system.UserEnv(id, "PATH="+orDefault2(d.Path, system.DefaultPath), "PM2_HOME="+d.Home),
			Mutates: !pm2ReadVerbs[argv[0]] && argv[0] != "--version",
			Timeout: timeout, Label: "pm2 " + argv[0],
		}
		if !g.JSON {
			c.Stdout, c.Stderr = g.Stdout, g.Stderr
		}
		res, err := g.Runner.Run(ctx, c)
		o := outcome{pm2Daemon: d}
		if res != nil {
			o.ExitCode, o.Stdout, o.Stderr = res.ExitCode, res.Stdout, res.Stderr
		}
		if err != nil {
			o.Error = err.Error()
			failed = rlerr.Wrap(err, rlerr.CodeExternal, "pm2 %s failed against %s", argv[0], d.Unit)
		}
		results = append(results, o)
	}
	if g.JSON {
		if jerr := g.EmitJSON(map[string]any{"args": argv, "daemons": results}); jerr != nil {
			return jerr
		}
	}
	return failed
}
