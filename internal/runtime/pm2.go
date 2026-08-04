package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
)

// The PM2 supervision mode for Node sites, and why it is the default.
//
// Why it is the default rather than running node directly under systemd: PM2's
// cluster mode is the only way a Node site gets a genuinely graceful reload.
// `pm2 reload` starts a replacement worker, waits for it to come up, and only then
// retires the old one, so a deploy drops no requests. systemd cannot do that for
// Node — there is no signal a plain node process handles that way — which is why
// `site reload` used to refuse on a Node site rather than pretend.
//
// What that costs, stated plainly: systemd now supervises PM2 and PM2 supervises
// the application, so there are two layers. The important properties survive
// because of how it is wired:
//
//   - PM2 runs with PM2_HOME inside the site directory, so each site has its own
//     daemon, its own socket and its own process list. There is no shared global
//     daemon that outlives a site or leaks state between tenants.
//   - systemd still owns the cgroup, and a cgroup contains every descendant, so
//     MemoryMax, CPUQuota and TasksMax remain kernel-enforced across PM2 and all
//     of its workers. The ceiling is not weakened by the extra layer.
//   - ExecStop runs `pm2 kill`, so stopping the unit leaves no orphan daemon.
//
// What genuinely changes: systemd's own restart counter stays at zero because PM2
// does the restarting, so `doctor` reads PM2's counter instead. That is handled in
// the diagnostics rather than left as a silent gap.
//
// `--daemon direct` runs node straight under systemd, which is the better choice
// for a single-process app that never needs a reload: one less moving part, and
// systemd sees the application itself.
//
// ProcessManagerPM2 and ProcessManagerDirect are the two supervision modes.
const (
	ProcessManagerPM2    = "pm2"
	ProcessManagerDirect = "direct"
)

// ecosystemFile is the PM2 configuration ratline generates per site.
const ecosystemFile = "ecosystem.config.json"

// pm2Home is where a site's PM2 daemon keeps its socket, pid file and logs.
//
// Inside the site directory rather than the tenant's home, so it is removed with
// the site and covered by the unit's BindPaths.
func pm2Home(c *Context) string { return filepath.Join(c.SiteDir, ".pm2") }

// ecosystemPath is where the generated PM2 configuration lives.
func ecosystemPath(c *Context) string { return filepath.Join(c.SiteDir, ".ratline", ecosystemFile) }

// pm2Binary resolves PM2 for the site's Node version.
//
// Installed per Node version rather than globally: a PM2 built against Node 18's
// ABI is not the one a Node 22 site should run, and a single global PM2 would make
// `runtime default` change the supervisor for every existing site at once.
func (n Node) pm2Binary(c *Context) (string, error) {
	nodeBin, err := n.binary(c, "node")
	if err != nil {
		return "", err
	}
	pm2 := filepath.Join(filepath.Dir(nodeBin), "pm2")
	if system.Exists(pm2) || c.DryRun {
		return pm2, nil
	}
	// A site-local install is a legitimate answer too, and is what a project that
	// pins its own PM2 version will have.
	local := filepath.Join(c.AppDir, "node_modules", ".bin", "pm2")
	if system.Exists(local) {
		return local, nil
	}
	version := c.Site.NodeVersion
	if version == "" {
		version = c.Cfg.Runtimes.NodeDefault
	}
	return "", rlerr.Preconditionf("PM2 is not installed for this Node version").
		WithHint("install it: ratline runtime install node %s --with-pm2\n"+
			"      or run this site without PM2: ratline site runtime %s --daemon direct",
			orDefault(version, "22"), c.Site.Domain)
}

// ecosystem is the subset of PM2's configuration ratline generates.
//
// JSON rather than the more common ecosystem.config.js, because a JavaScript
// config file is code: it would be evaluated by PM2 as the tenant, and generating
// code to configure a supervisor is a needless way to introduce an injection
// surface. PM2 reads .json identically.
type ecosystem struct {
	Apps []ecosystemApp `json:"apps"`
}

type ecosystemApp struct {
	Name string `json:"name"`
	// Script and Args rather than a single command string, so nothing is ever
	// re-parsed by a shell.
	Script    string            `json:"script"`
	Args      []string          `json:"args,omitempty"`
	Cwd       string            `json:"cwd"`
	Instances int               `json:"instances"`
	ExecMode  string            `json:"exec_mode"`
	Env       map[string]string `json:"env,omitempty"`

	// Interpreter is set to "none" for anything that is not a JavaScript file.
	// Without it PM2 assumes node and tries to evaluate the program as a script,
	// so `npm start` would fail with a syntax error rather than run.
	Interpreter string `json:"interpreter,omitempty"`

	// Logs go to the site's own log directory, so `ratline site logs` and
	// logrotate see the same files as PM2 does.
	OutFile   string `json:"out_file"`
	ErrFile   string `json:"error_file"`
	MergeLogs bool   `json:"merge_logs"`
	Time      bool   `json:"time"`

	// A worker that has not signalled readiness within this window is treated as
	// failed, which is what makes `pm2 reload` wait rather than cut over blindly.
	WaitReady     bool   `json:"wait_ready"`
	ListenTimeout int    `json:"listen_timeout"`
	KillTimeout   int    `json:"kill_timeout"`
	MaxRestarts   int    `json:"max_restarts"`
	MinUptime     string `json:"min_uptime"`
	RestartDelay  int    `json:"restart_delay"`
	// Left to systemd: MemoryMax kills the whole cgroup, which is the ceiling that
	// actually holds. A PM2-level limit here would fire first and mask it.
	Autorestart bool `json:"autorestart"`
}

// RenderEcosystem produces the PM2 configuration for a site.
func (n Node) RenderEcosystem(c *Context) ([]byte, error) {
	nodeBin, err := n.binary(c, "node")
	if err != nil {
		return nil, err
	}

	script, args, err := n.entryScript(c)
	if err != nil {
		return nil, err
	}

	env := map[string]string{
		"NODE_ENV": "production",
		// PM2 spawns workers itself, so the interpreter has to be on PATH for any
		// child process the application starts.
		"PATH": filepath.Dir(nodeBin) + ":" + system.DefaultPath,
	}
	socket := c.Cfg.SocketPath(c.Site.Owner, c.Site.Domain)
	if c.Site.Listen == "port" {
		env["PORT"] = fmt.Sprint(c.Site.Port)
		env["HOST"] = "127.0.0.1"
	} else {
		// Cluster mode shares one listening handle across workers, so every worker
		// binds the same socket path — which is exactly what makes a reload
		// seamless.
		env["PORT"] = socket
		env["RATLINE_SOCKET"] = socket
		env["SOCKET_PATH"] = socket
	}

	instances := c.Site.Instances
	if instances <= 1 {
		// One instance in cluster mode still gets a graceful reload, because PM2
		// starts the replacement before retiring the original. fork mode would
		// not, so cluster is the default even for a single worker.
		instances = 1
	}

	// Cluster mode is node's own cluster module, which can only fan out a
	// JavaScript entry point. A start command that runs a package manager or a
	// binary has to be fork mode, and fork mode's reload is a restart — said out
	// loud rather than left to be discovered during a deploy.
	execMode, interpreter := "cluster", ""
	if !isJavaScript(script) {
		execMode, interpreter, instances = "fork", "none", 1
		c.Log.Warn("this site's start command is not a JavaScript file, so PM2 runs it in fork mode",
			"consequence", "'site reload' restarts it instead of reloading gracefully",
			"advice", "point --entry at the file that calls listen() to get a zero-downtime reload")
	}

	app := ecosystemApp{
		Name:        c.Site.Slug,
		Script:      script,
		Args:        args,
		Cwd:         c.AppDir,
		Instances:   instances,
		ExecMode:    execMode,
		Env:         env,
		Interpreter: interpreter,
		OutFile:     filepath.Join(c.LogDir, "app.log"),
		ErrFile:     filepath.Join(c.LogDir, "app.log"),
		MergeLogs:   true,
		Time:        true,
		// wait_ready is off unless the application opts in by calling
		// process.send('ready'). With it on and an app that never signals, every
		// reload would stall for listen_timeout and then be reported as a failure.
		WaitReady:     false,
		ListenTimeout: 10000,
		KillTimeout:   5000,
		MaxRestarts:   10,
		MinUptime:     "5s",
		RestartDelay:  1000,
		Autorestart:   true,
	}

	body, err := json.MarshalIndent(ecosystem{Apps: []ecosystemApp{app}}, "", "  ")
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "encoding the PM2 configuration")
	}
	var out bytes.Buffer
	// PM2 does not read comments out of JSON, so the marker goes in a sibling
	// field-free header written by the caller. The file itself stays valid JSON.
	out.Write(body)
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// isJavaScript reports whether PM2 can treat a path as a node script.
func isJavaScript(script string) bool {
	switch strings.ToLower(filepath.Ext(script)) {
	case ".js", ".mjs", ".cjs":
		return true
	}
	return false
}

// entryScript resolves what PM2 should run, as a script plus arguments.
func (n Node) entryScript(c *Context) (string, []string, error) {
	if c.Site.StartCommand != "" {
		parsed, err := system.ParseCommand(c.Site.StartCommand)
		if err != nil {
			return "", nil, err
		}
		return resolveProgram(parsed.Argv[0], c), parsed.Argv[1:], nil
	}
	if err := validateNodeEntry(c.Site.Entry); err != nil {
		return "", nil, err
	}
	entry, err := resolveEntry(c)
	if err != nil {
		return "", nil, err
	}
	return entry, nil, nil
}

// WriteEcosystem renders and installs the PM2 configuration.
func (n Node) WriteEcosystem(ctx context.Context, c *Context) error {
	body, err := n.RenderEcosystem(c)
	if err != nil {
		return err
	}
	path := ecosystemPath(c)
	if c.DryRun {
		c.Log.Info("would write the PM2 configuration", "path", path)
		return nil
	}
	if _, err := system.EnsureDir(filepath.Dir(path), 0o750, c.Identity.UID, c.Identity.GID); err != nil {
		return err
	}
	if _, err := system.EnsureDir(pm2Home(c), 0o750, c.Identity.UID, c.Identity.GID); err != nil {
		return err
	}
	return system.WriteFileAtomic(path, body, 0o640, c.Identity.UID, c.Identity.GID)
}

// pm2StartCommand builds the unit for a PM2-supervised site.
func (n Node) pm2StartCommand(ctx context.Context, c *Context) (string, unit.RenderOptions, error) {
	var opts unit.RenderOptions
	pm2, err := n.pm2Binary(c)
	if err != nil {
		return "", opts, err
	}
	if err := n.WriteEcosystem(ctx, c); err != nil {
		return "", opts, err
	}

	home := pm2Home(c)
	config := ecosystemPath(c)

	// Type=forking, because PM2 daemonises and the daemon is what holds the
	// workers. systemd tracks the cgroup rather than the pid, so the resource
	// limits still cover every worker.
	opts.Type = "forking"
	opts.Environment = []string{
		"PM2_HOME=" + home,
		"NODE_ENV=production",
		"PM2_DISCRETE_MODE=true",
	}
	// PM2 writes its own pid file; naming it lets systemd follow the right process
	// rather than guessing after the fork.
	opts.PIDFile = filepath.Join(home, "pm2.pid")

	// `pm2 reload` is the whole point: it brings up a replacement worker, waits,
	// and only then retires the old one.
	opts.ExecReload = shellSafeJoin(pm2, []string{"reload", config, "--update-env"})
	// `pm2 kill` stops the daemon as well as the workers, so nothing is orphaned
	// when the unit stops.
	opts.ExecStop = shellSafeJoin(pm2, []string{"kill"})

	if c.Site.Listen != "port" {
		socket := c.Cfg.SocketPath(c.Site.Owner, c.Site.Domain)
		// The socket-permission fix still applies: PM2's workers create the socket
		// with their own umask, and connect(2) needs write permission on it.
		opts.ExecStartPost = []string{
			"+/bin/sh -c 'for i in $(seq 1 100); do if [ -S " + socket + " ]; then chmod 0660 " + socket +
				"; exit 0; fi; sleep 0.1; done; exit 0'",
		}
	}
	// PM2's own directory has to be writable, on top of logs and tmp.
	opts.ExtraReadWritePaths = home

	// No --no-daemon: PM2 is meant to fork here, which is what Type=forking and
	// the PIDFile above are for.
	return shellSafeJoin(pm2, []string{"start", config}), opts, nil
}

// PM2Status is what PM2 reports about a site's workers.
type PM2Status struct {
	Name      string  `json:"name"`
	Instances int     `json:"instances"`
	Online    int     `json:"online"`
	Restarts  int     `json:"restarts"`
	Memory    int64   `json:"memory_bytes"`
	CPU       float64 `json:"cpu_percent"`
	Uptime    string  `json:"uptime,omitempty"`
	Mode      string  `json:"exec_mode,omitempty"`
}

// pm2ListEntry is the shape of one item in `pm2 jlist`.
type pm2ListEntry struct {
	Name   string `json:"name"`
	PM2Env struct {
		Status      string `json:"status"`
		RestartTime int    `json:"restart_time"`
		ExecMode    string `json:"exec_mode"`
		PMUptime    int64  `json:"pm_uptime"`
	} `json:"pm2_env"`
	Monit struct {
		Memory int64   `json:"memory"`
		CPU    float64 `json:"cpu"`
	} `json:"monit"`
}

// PM2Report asks a site's PM2 daemon what it is running.
//
// This is what makes PM2 supervision visible to `doctor` and `site status`. Without
// it, systemd's restart counter would read zero for a crash-looping app, because
// PM2 is doing the restarting.
func (n Node) PM2Report(ctx context.Context, c *Context) (*PM2Status, error) {
	pm2, err := n.pm2Binary(c)
	if err != nil {
		return nil, err
	}
	res, err := c.Runner.Run(ctx, system.Cmd{
		Path: pm2, Args: []string{"jlist"},
		As:  c.Identity,
		Env: append(system.UserEnv(c.Identity), "PM2_HOME="+pm2Home(c)),
		// PM2 exits non-zero when no daemon is running, which is a normal state
		// for a stopped site rather than an error.
		OKExit: []int{1},
	})
	if err != nil || res == nil {
		return nil, rlerr.Preconditionf("could not read PM2's process list for %s", c.Site.Domain)
	}
	payload := strings.TrimSpace(res.Stdout)
	if payload == "" || payload == "[]" {
		return &PM2Status{Name: c.Site.Slug}, nil
	}
	var entries []pm2ListEntry
	if err := json.Unmarshal([]byte(payload), &entries); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "PM2's process list did not parse")
	}

	st := &PM2Status{Name: c.Site.Slug}
	for _, e := range entries {
		if e.Name != c.Site.Slug {
			continue
		}
		st.Instances++
		if e.PM2Env.Status == "online" {
			st.Online++
		}
		// The restart count is the maximum across workers rather than the sum: one
		// worker restarting ten times is the signal, and summing would inflate it
		// by the instance count.
		if e.PM2Env.RestartTime > st.Restarts {
			st.Restarts = e.PM2Env.RestartTime
		}
		st.Memory += e.Monit.Memory
		st.CPU += e.Monit.CPU
		st.Mode = e.PM2Env.ExecMode
	}
	return st, nil
}

// pm2Reload performs the graceful reload.
func (n Node) pm2Reload(ctx context.Context, c *Context) error {
	pm2, err := n.pm2Binary(c)
	if err != nil {
		return err
	}
	if c.DryRun {
		c.Log.Info("would reload the PM2 workers", "site", c.Site.Domain)
		return nil
	}
	// --update-env so a changed .env is picked up by the replacement workers; a
	// reload that kept the old environment would be a confusing half-measure.
	_, err = c.Runner.Run(ctx, system.Cmd{
		Path: pm2, Args: []string{"reload", ecosystemPath(c), "--update-env"},
		As:      c.Identity,
		Env:     append(system.UserEnv(c.Identity), "PM2_HOME="+pm2Home(c)),
		Mutates: true, Stream: true, Label: "pm2 reload",
	})
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeExternal, "the PM2 reload failed").
			WithHint("the previous workers are still serving; check 'ratline site logs %s'", c.Site.Domain)
	}
	return nil
}
