package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
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
//   - One daemon per tenant and Node version (unit.PM2Daemon), run by systemd as the
//     tenant, with PM2_HOME in the tenant's home. Never one shared between tenants:
//     that daemon would have to be root. A site's own unit is a oneshot that
//     registers the application with it and takes it out again.
//   - systemd still owns the cgroup — the daemon's — and a cgroup contains every
//     descendant, so the tenant's MemoryMax, CPUQuota and TasksMax remain
//     kernel-enforced across PM2 and all of its workers. Each site's own MemoryMax
//     becomes PM2's max_memory_restart for its workers.
//   - Stopping a site's unit runs `pm2 delete <site>`, so nothing of it is left in the
//     daemon; stopping the daemon's unit runs `pm2 kill`.
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

// pm2Env is the environment every pm2 invocation needs.
//
// PATH above all: `pm2` is a JavaScript file with a `#!/usr/bin/env node` shebang, so
// running it without the managed node on PATH fails with "env: 'node': No such file
// or directory". That failure was silent in two places — `PM2Report`, which made PM2
// supervision invisible to `site status`, `doctor` and `troubleshoot`, and
// `pm2Reload`, where a reload that never ran looked exactly like one that dropped no
// requests.
func (n Node) pm2Env(c *Context) ([]string, error) {
	nodeBin, err := n.binary(c, "node")
	if err != nil {
		return nil, err
	}
	return system.UserEnv(c.Identity,
		"PATH="+filepath.Dir(nodeBin)+":"+system.DefaultPath,
		"PM2_HOME="+pm2Home(c),
	), nil
}

// pm2Home is where the tenant's PM2 daemon for this site's Node version keeps its
// socket, pid file and logs.
//
// In the tenant's home rather than any one site's directory, because the daemon
// outlives each of the sites it holds. Under .ratline/ so that a tenant running their
// own `pm2` by hand, with the default ~/.pm2, never collides with it.
func pm2Home(c *Context) string { return PM2HomeFor(c.Cfg.HomeDir(c.Site.Owner), pm2DaemonKey(c)) }

// PM2HomeFor is a tenant daemon's PM2_HOME, given the tenant's home and its key.
func PM2HomeFor(home, key string) string { return filepath.Join(home, ".ratline", "pm2", key) }

// legacyPM2Home is where a site's own daemon lived before daemons were shared. A site
// created by an earlier release still has one running there until its unit restarts.
func legacyPM2Home(c *Context) string { return filepath.Join(c.SiteDir, ".pm2") }

// pm2DaemonKey names the Node version a site's PM2 runs on, which picks its daemon:
// PM2's cluster workers are forked from the daemon's own node, so sites on different
// versions cannot share one.
func pm2DaemonKey(c *Context) string {
	version := c.Site.NodeVersion
	if version == "" {
		version = c.Cfg.Runtimes.NodeDefault
	}
	version = strings.TrimPrefix(version, "v")
	if version == "" || validate.NodeVersion(version) != nil {
		return "system"
	}
	return "node" + version
}

// PM2Daemon describes the tenant daemon a PM2-supervised site belongs to. Sites is
// left for the caller, which is the one that can see the tenant's other sites.
func (n Node) PM2Daemon(c *Context) (*unit.PM2Daemon, error) {
	pm2, err := n.pm2Binary(c)
	if err != nil {
		return nil, err
	}
	nodeBin, err := n.binary(c, "node")
	if err != nil {
		return nil, err
	}
	return &unit.PM2Daemon{
		Owner:   c.Site.Owner,
		Key:     pm2DaemonKey(c),
		Home:    pm2Home(c),
		NodeBin: nodeBin,
		PM2:     pm2,
	}, nil
}

// PM2DaemonUnit is the unit of the daemon a site's PM2 belongs to.
func PM2DaemonUnit(c *Context) string { return validate.PM2UnitName(c.Site.Owner, pm2DaemonKey(c)) }

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
	Autorestart   bool   `json:"autorestart"`

	// The site's own memory ceiling, per worker. The kernel's ceiling is the tenant
	// daemon's — the sum of its sites' — so without this one site could grow into its
	// siblings' share. PM2 restarts a worker that passes it, one at a time, where the
	// kernel would have killed the daemon and every site in it.
	MaxMemoryRestart string `json:"max_memory_restart,omitempty"`
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

	// Which binary actually executes the application. PM2 is a Node program and is
	// always launched by node, but what it supervises need not be: a bun site runs
	// under PM2 with bun named as the interpreter.
	interpreterBin := nodeBin
	if c.Site.Runtime == "bun" {
		if interpreterBin, err = (Bun{}).binary(c); err != nil {
			return nil, err
		}
	}

	env := map[string]string{
		"NODE_ENV": "production",
		// PM2 spawns workers itself, so the interpreter has to be on PATH for any
		// child process the application starts. A bun site gets bun first and node
		// behind it, because pm2 itself still resolves node through this PATH.
		"PATH": pm2Path(interpreterBin, nodeBin),
	}
	socket := c.Cfg.SocketPath(c.Site.Owner, c.Site.Domain)
	if c.Site.Listen == "port" {
		env["PORT"] = fmt.Sprint(c.Site.Port)
		env["HOST"] = "127.0.0.1"
		if c.Site.Runtime == "bun" {
			// The same spelling the direct path sets, so an application that works
			// under systemd works unchanged when PM2 is put in front of it. A
			// default-exported Bun.serve object reads BUN_PORT and nothing else.
			env["BUN_PORT"] = fmt.Sprint(c.Site.Port)
		}
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
	switch {
	case c.Site.Runtime == "bun":
		// Bun has no cluster module, and PM2's cluster mode *is* node's: it forks
		// through node and passes the listening handle down. Naming bun as the
		// interpreter and asking for cluster mode would have PM2 fork node anyway
		// and hand the entry point to the wrong engine, so fork mode is the only
		// honest answer. Each instance is then an independent process rather than a
		// worker sharing one handle, which is what validateInstances enforces the
		// preconditions for.
		execMode, interpreter = "fork", interpreterBin
		if instances > 1 {
			// ratline cannot see inside the application, and this is the one part of
			// the arrangement it cannot verify: without SO_REUSEPORT only the first
			// process binds the port and the rest crash-loop while nginx proxies
			// contentedly to the one that won. `site status` shows PM2's online
			// count against the requested one, which is where it becomes visible.
			c.Log.Warn("bun fans out as independent processes, not cluster workers",
				"required", "Bun.serve({ reusePort: true }) — without it only one process binds the port",
				"check", "ratline site status "+c.Site.Domain+" reports how many are online")
		}
	case !isJavaScript(script):
		execMode, interpreter, instances = "fork", "none", 1
		c.Log.Warn("this site's start command is not a JavaScript file, so PM2 runs it in fork mode",
			"consequence", "'site reload' restarts it instead of reloading gracefully",
			"advice", "point --entry at the file that calls listen() to get a zero-downtime reload")
	}

	maxMemory := ""
	if limit, err := validate.Size(orDefault(c.Site.MemoryMax, c.Cfg.Defaults.MemoryMax)); err == nil && limit > 0 {
		maxMemory = fmt.Sprintf("%dK", limit/int64(instances)/1024)
	}

	app := ecosystemApp{
		MaxMemoryRestart: maxMemory,
		Name:             c.Site.Slug,
		Script:           script,
		Args:             args,
		Cwd:              c.AppDir,
		Instances:        instances,
		ExecMode:         execMode,
		Env:              env,
		Interpreter:      interpreter,
		OutFile:          filepath.Join(c.LogDir, "app.log"),
		ErrFile:          filepath.Join(c.LogDir, "app.log"),
		MergeLogs:        true,
		Time:             true,
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

// pm2Path is the PATH a PM2-supervised site's processes get.
//
// node is always on it, because pm2 is a JavaScript file with a `#!/usr/bin/env node`
// shebang and resolves its own interpreter through PATH. The application's own
// interpreter goes first when it differs, so a bun site's child processes calling
// `bun` get the managed one rather than whatever a tenant installed in their home.
func pm2Path(interpreterBin, nodeBin string) string {
	nodeDir := filepath.Dir(nodeBin)
	if dir := filepath.Dir(interpreterBin); dir != nodeDir {
		return dir + ":" + nodeDir + ":" + system.DefaultPath
	}
	return nodeDir + ":" + system.DefaultPath
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
	// Judged against the engine that will execute it: bun accepts .ts, .tsx and
	// .jsx where node does not, and this path is now reached by both.
	check := validateNodeEntry
	if c.Site.Runtime == "bun" {
		check = validate.BunEntry
	}
	if err := check(c.Site.Entry); err != nil {
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
	if err := ensurePM2Home(c); err != nil {
		return err
	}
	return system.WriteFileAtomic(path, body, 0o640, c.Identity.UID, c.Identity.GID)
}

// ensurePM2Home creates the tenant daemon's PM2_HOME, one component at a time: each
// EnsureDir refuses a symlink where the directory should be, and the home above them is
// root's own boundary. 0700 at the end, because PM2's RPC socket in it accepts any
// command from whoever can connect.
func ensurePM2Home(c *Context) error {
	home := c.Cfg.HomeDir(c.Site.Owner)
	for _, d := range []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Join(home, ".ratline"), 0o750},
		{filepath.Join(home, ".ratline", "pm2"), 0o750},
		{pm2Home(c), 0o700},
	} {
		if _, err := system.EnsureDir(d.path, d.mode, c.Identity.UID, c.Identity.GID); err != nil {
			return err
		}
	}
	return nil
}

// pm2StartCommand builds the unit for a PM2-supervised site.
//
// A oneshot bound to the tenant's daemon: starting it registers the application with
// the daemon, stopping it takes the application out, reloading it is `pm2 reload`. The
// daemon itself is unit.PM2Daemon, installed by the site lifecycle beside this one.
func (n Node) pm2StartCommand(ctx context.Context, c *Context) (string, unit.RenderOptions, error) {
	var opts unit.RenderOptions
	pm2, err := n.pm2Binary(c)
	if err != nil {
		return "", opts, err
	}
	nodeBin, err := n.binary(c, "node")
	if err != nil {
		return "", opts, err
	}
	if err := n.WriteEcosystem(ctx, c); err != nil {
		return "", opts, err
	}

	home := pm2Home(c)
	config := ecosystemPath(c)

	opts.Type = "oneshot"
	opts.RemainAfterExit = true
	opts.BindsTo = PM2DaemonUnit(c)
	opts.Environment = []string{
		// PATH first, and it is not optional. `pm2` is a JavaScript file whose
		// shebang is `#!/usr/bin/env node`, so systemd executing it runs env, which
		// searches PATH — and a unit has no PATH beyond systemd's minimal default.
		// Without this every PM2-supervised site failed to start with
		// "/usr/bin/env: 'node': No such file or directory" and status 127.
		"PATH=" + filepath.Dir(nodeBin) + ":" + system.DefaultPath,
		"PM2_HOME=" + home,
		"NODE_ENV=production",
		"PM2_DISCRETE_MODE=true",
	}

	// The application's environment is this process's: PM2 hands the client's
	// environment to what it starts, which is how the site's .env — loaded by
	// ratline-shell in ExecStart, as the tenant — reaches the workers and not the
	// daemon, which every other site of the tenant shares.
	//
	// --update-env, because `pm2 start` on a name the daemon already holds — after the
	// daemon came back and started every site it wants — restarts it, and a restart
	// that kept the old environment would quietly ignore a changed .env.
	opts.ExecReload = shellSafeJoin(pm2, []string{"reload", config, "--update-env"})
	// '-': a daemon that is already gone has nothing of this site left to delete.
	opts.ExecStop = "-" + shellSafeJoin(pm2, []string{"delete", c.Site.Slug})

	if c.Site.Listen != "port" {
		socket := c.Cfg.SocketPath(c.Site.Owner, c.Site.Domain)
		// The socket-permission fix still applies: PM2's workers create the socket
		// with their own umask, and connect(2) needs write permission on it. It runs
		// as the tenant (no '+'), never root: the socket is the tenant's own inode, so
		// a symlink swapped in cannot redirect this chmod onto a root-owned file.
		opts.ExecStartPost = []string{
			"/bin/sh -c 'for i in $(seq 1 100); do if [ -S " + socket + " ]; then chmod 0660 " + socket +
				"; exit 0; fi; sleep 0.1; done; exit 0'",
		}
	}
	// The client reaches the daemon through the RPC socket in its PM2_HOME, which is in
	// the tenant's home and outside the site directory the sandbox binds back in.
	opts.ExtraBindPaths = []string{home}

	return shellSafeJoin(pm2, []string{"start", config, "--update-env"}), opts, nil
}

// pm2Remove takes a site's application out of its tenant's daemon, and stops the
// per-site daemon an earlier release gave it if one is still running.
//
// Only when the daemon is up: every pm2 command launches a daemon when it cannot reach
// one, and one launched from here would sit outside the daemon unit's cgroup holding its
// socket. A daemon that is down holds nothing to remove.
//
// Exit 1 is accepted: "process or namespace not found" is the expected answer when the
// unit has already stopped, and a teardown that fails because there was nothing to
// tear down is not a failure.
func (n Node) pm2Remove(ctx context.Context, c *Context) error {
	pm2, err := n.pm2Binary(c)
	if err != nil {
		return err
	}
	env, err := n.pm2Env(c)
	if err != nil {
		return err
	}
	if daemonActive(ctx, c) {
		if _, err := c.Runner.Run(ctx, system.Cmd{
			Path: pm2, Args: []string{"delete", c.Site.Slug}, As: c.Identity,
			Env:     env,
			Mutates: true, OKExit: []int{1},
		}); err != nil {
			return err
		}
	}
	return n.killLegacyPM2(ctx, c)
}

// killLegacyPM2 stops a site's own PM2 daemon, from before daemons were shared.
//
// Asked by its pid file, which only a daemon ever wrote: without one there is nothing
// to stop, and running `pm2 kill` against an empty PM2_HOME would launch a daemon there
// just to kill it.
func (n Node) killLegacyPM2(ctx context.Context, c *Context) error {
	legacy := legacyPM2Home(c)
	if !system.Exists(filepath.Join(legacy, "pm2.pid")) || c.DryRun {
		return nil
	}
	pm2, err := n.pm2Binary(c)
	if err != nil {
		return err
	}
	nodeBin, err := n.binary(c, "node")
	if err != nil {
		return err
	}
	_, err = c.Runner.Run(ctx, system.Cmd{
		Path: pm2, Args: []string{"kill"}, As: c.Identity,
		Env: system.UserEnv(c.Identity,
			"PATH="+filepath.Dir(nodeBin)+":"+system.DefaultPath,
			"PM2_HOME="+legacy,
		),
		Mutates: true, OKExit: []int{1, 2},
	})
	if err == nil {
		c.Log.Info("stopped the site's own PM2 daemon from an earlier release", "pm2_home", legacy)
	}
	return err
}

// daemonActive reports whether the tenant daemon a site belongs to is running.
func daemonActive(ctx context.Context, c *Context) bool {
	res, err := c.Runner.Run(ctx, system.Cmd{
		Name: "systemctl", Args: []string{"is-active", "--quiet", PM2DaemonUnit(c)},
		OKExit: []int{1, 3, 4},
	})
	return err == nil && res != nil && res.ExitCode == 0
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

// PM2Report asks the tenant's PM2 daemon what it is running for a site.
//
// This is what makes PM2 supervision visible to `doctor` and `site status`. Without
// it, systemd's restart counter would read zero for a crash-looping app, because
// PM2 is doing the restarting.
func (n Node) PM2Report(ctx context.Context, c *Context) (*PM2Status, error) {
	pm2, err := n.pm2Binary(c)
	if err != nil {
		return nil, err
	}
	env, err := n.pm2Env(c)
	if err != nil {
		return nil, err
	}
	// A daemon that is down is running nothing, and asking it would start one outside
	// its unit — see pm2Remove.
	if !daemonActive(ctx, c) {
		return &PM2Status{Name: c.Site.Slug}, nil
	}
	res, err := c.Runner.Run(ctx, system.Cmd{
		Path: pm2, Args: []string{"jlist"},
		As:  c.Identity,
		Env: env,
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
	env, err := n.pm2Env(c)
	if err != nil {
		return err
	}
	// --update-env so a changed .env is picked up by the replacement workers; a
	// reload that kept the old environment would be a confusing half-measure.
	_, err = c.Runner.Run(ctx, system.Cmd{
		Path: pm2, Args: []string{"reload", ecosystemPath(c), "--update-env"},
		As:      c.Identity,
		Env:     env,
		Mutates: true, Stream: true, Label: "pm2 reload",
	})
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeExternal, "the PM2 reload failed").
			WithHint("the previous workers are still serving; check 'ratline site logs %s'", c.Site.Domain)
	}
	return nil
}
