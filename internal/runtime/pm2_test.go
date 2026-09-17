package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// stubRunner answers with canned output instead of running anything, so the PM2
// wiring can be tested on a machine that has neither node nor pm2.
type stubRunner struct {
	stdout string
	exit   int
	err    error
	calls  [][]string
}

func (r *stubRunner) Run(_ context.Context, c system.Cmd) (*system.Result, error) {
	name := c.Path
	if name == "" {
		name = c.Name
	}
	r.calls = append(r.calls, append([]string{name}, c.Args...))
	if r.err != nil {
		return nil, r.err
	}
	return &system.Result{Path: name, Args: c.Args, Stdout: r.stdout, ExitCode: r.exit}, nil
}

func nodeContext(t *testing.T, mutate func(*state.Site)) *Context {
	t.Helper()
	cfg := config.Default()
	cfg.Runtimes.NodeDefault = "22"
	site := &state.Site{
		Domain: "app.example.com", Owner: "alice", Runtime: "node",
		Slug: "alice-app_example_com", Enabled: true,
		Entry: "server.js", Listen: "socket", Instances: 1,
	}
	if mutate != nil {
		mutate(site)
	}
	id := &system.Identity{Name: "alice", UID: 1001, GID: 1001, Home: cfg.HomeDir("alice")}
	// DryRun so the managed interpreter and PM2 resolve by path without having to
	// exist on the machine running the tests.
	return NewContext(cfg, log.Discard(), &stubRunner{}, site, id, true)
}

// installFakeRuntime lays down the binaries a live (non-dry-run) resolution needs,
// so a refusal in a test is the one being tested rather than "node is missing".
func installFakeRuntime(t *testing.T, c *Context, names ...string) {
	t.Helper()
	c.Cfg.Paths.RuntimesDir = t.TempDir()
	bin := filepath.Join(c.Cfg.Paths.RuntimesDir, "node", "22", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func decodeEcosystem(t *testing.T, c *Context) ecosystemApp {
	t.Helper()
	body, err := (Node{}).RenderEcosystem(c)
	if err != nil {
		t.Fatalf("RenderEcosystem = %v", err)
	}
	var eco ecosystem
	if err := json.Unmarshal(body, &eco); err != nil {
		t.Fatalf("the generated configuration is not valid JSON: %v\n%s", err, body)
	}
	if len(eco.Apps) != 1 {
		t.Fatalf("want exactly one app, got %d", len(eco.Apps))
	}
	return eco.Apps[0]
}

func TestEcosystemIsValidJSONAndNotCode(t *testing.T) {
	c := nodeContext(t, nil)
	body, err := (Node{}).RenderEcosystem(c)
	if err != nil {
		t.Fatalf("RenderEcosystem = %v", err)
	}
	// The file has to be data. ecosystem.config.js would be evaluated by PM2 as
	// the tenant, which turns a configuration file into an execution surface.
	if !json.Valid(body) {
		t.Fatalf("the PM2 configuration must be valid JSON:\n%s", body)
	}
	if strings.Contains(string(body), "module.exports") {
		t.Error("the configuration must not be JavaScript")
	}
}

func TestEcosystemUsesClusterModeForAJavaScriptEntry(t *testing.T) {
	app := decodeEcosystem(t, nodeContext(t, nil))
	// Cluster mode even for a single instance: it is what makes `pm2 reload` bring
	// up a replacement before retiring the original. fork mode would not.
	if app.ExecMode != "cluster" {
		t.Errorf("exec_mode = %q, want cluster", app.ExecMode)
	}
	if app.Instances != 1 {
		t.Errorf("instances = %d, want 1", app.Instances)
	}
	if app.Interpreter != "" {
		t.Errorf("interpreter = %q, want it left to PM2 for a .js entry", app.Interpreter)
	}
	if !strings.HasSuffix(app.Script, "/app/server.js") {
		t.Errorf("script = %q, want the entry inside the app directory", app.Script)
	}
}

func TestEcosystemFallsBackToForkForANonJavaScriptCommand(t *testing.T) {
	// `npm start` cannot be fanned out by node's cluster module, and PM2 would try
	// to evaluate npm as a script unless told not to.
	app := decodeEcosystem(t, nodeContext(t, func(s *state.Site) {
		s.StartCommand = "npm start"
	}))
	if app.ExecMode != "fork" {
		t.Errorf("exec_mode = %q, want fork for a non-JavaScript command", app.ExecMode)
	}
	if app.Interpreter != "none" {
		t.Errorf("interpreter = %q, want none so PM2 runs it rather than evaluating it", app.Interpreter)
	}
	if app.Instances != 1 {
		t.Errorf("instances = %d, want 1 in fork mode", app.Instances)
	}
	if len(app.Args) != 1 || app.Args[0] != "start" {
		t.Errorf("args = %v, want the arguments kept as a list rather than one string", app.Args)
	}
}

func TestEcosystemPassesTheSocketAndNeverALimit(t *testing.T) {
	c := nodeContext(t, nil)
	app := decodeEcosystem(t, c)
	socket := c.Cfg.SocketPath("alice", "app.example.com")
	for _, key := range []string{"PORT", "RATLINE_SOCKET", "SOCKET_PATH"} {
		if app.Env[key] != socket {
			t.Errorf("env[%s] = %q, want %q", key, app.Env[key], socket)
		}
	}
	// A PM2-level memory limit would fire before MemoryMax and mask the ceiling
	// that is actually kernel-enforced.
	body, _ := (Node{}).RenderEcosystem(c)
	if strings.Contains(string(body), "max_memory_restart") {
		t.Error("the memory ceiling belongs to systemd's cgroup, not to PM2")
	}
	// Logs must land where `ratline site logs` and logrotate already look.
	if !strings.HasSuffix(app.OutFile, "/logs/app.log") || app.OutFile != app.ErrFile {
		t.Errorf("out_file = %q, error_file = %q, want both at logs/app.log", app.OutFile, app.ErrFile)
	}
}

// The socket-permission chmod touches a path inside a directory the tenant owns, so it must
// run as the tenant — not root via systemd's '+' prefix. Running it as root through a
// tenant-controlled path lets a swapped symlink redirect the chmod onto a file outside the
// site; running it as the tenant cannot, since a non-root chmod only touches inodes the
// tenant already owns.
func TestSocketChmodExecStartPostRunsAsTenantNotRoot(t *testing.T) {
	check := func(name string, post []string) {
		t.Helper()
		found := false
		for _, p := range post {
			if !strings.Contains(p, "chmod 0660") {
				continue
			}
			found = true
			if strings.HasPrefix(strings.TrimSpace(p), "+") {
				t.Errorf("%s: the socket chmod runs as root (+ prefix): %q", name, p)
			}
		}
		if !found {
			t.Errorf("%s: expected a socket-permission ExecStartPost, got %v", name, post)
		}
	}

	// node defaults to the PM2 supervisor, so exercise both supervisors explicitly.
	_, nodeOpts, err := (Node{}).StartCommand(context.Background(), nodeContext(t, func(s *state.Site) {
		s.ProcessManager = "direct"
	}))
	if err != nil {
		t.Fatalf("node(direct) StartCommand = %v", err)
	}
	check("node(direct)", nodeOpts.ExecStartPost)

	_, pm2Opts, err := (Node{}).StartCommand(context.Background(), nodeContext(t, func(s *state.Site) {
		s.ProcessManager = "pm2"
	}))
	if err != nil {
		t.Fatalf("pm2 StartCommand = %v", err)
	}
	check("pm2", pm2Opts.ExecStartPost)

	// python/uvicorn on a socket uses the same fix.
	cfg := config.Default()
	site := &state.Site{
		Domain: "api.example.com", Owner: "alice", Runtime: "python",
		Slug: "alice-api_example_com", Enabled: true, AppModule: "app.main:app",
		AppServer: "uvicorn", Listen: "socket", Instances: 1,
	}
	id := &system.Identity{Name: "alice", UID: 1001, GID: 1001, Home: cfg.HomeDir("alice")}
	_, pyOpts, err := (Python{}).StartCommand(context.Background(),
		NewContext(cfg, log.Discard(), &stubRunner{}, site, id, true))
	if err != nil {
		t.Fatalf("uvicorn StartCommand = %v", err)
	}
	check("uvicorn", pyOpts.ExecStartPost)
}

func TestEcosystemUsesPortForAPortSite(t *testing.T) {
	app := decodeEcosystem(t, nodeContext(t, func(s *state.Site) {
		s.Listen, s.Port = "port", 20001
	}))
	if app.Env["PORT"] != "20001" || app.Env["HOST"] != "127.0.0.1" {
		t.Errorf("env = %v, want PORT=20001 and HOST=127.0.0.1", app.Env)
	}
	if _, ok := app.Env["RATLINE_SOCKET"]; ok {
		t.Error("a port site must not be handed a socket path as well")
	}
}

func TestPM2UnitIsForkingWithAPIDFileAndAReload(t *testing.T) {
	c := nodeContext(t, nil)
	exec, opts, err := (Node{}).pm2StartCommand(context.Background(), c)
	if err != nil {
		t.Fatalf("pm2StartCommand = %v", err)
	}
	// Type=forking with a PIDFile, because PM2 daemonises. Without the PIDFile
	// systemd would guess at the main process after the fork.
	if opts.Type != "forking" {
		t.Errorf("Type = %q, want forking", opts.Type)
	}
	if !strings.HasPrefix(opts.PIDFile, c.SiteDir) || !strings.HasSuffix(opts.PIDFile, "pm2.pid") {
		t.Errorf("PIDFile = %q, want PM2's pid file inside the site directory", opts.PIDFile)
	}
	// The reload is the entire reason PM2 is the default.
	if !strings.Contains(opts.ExecReload, " reload ") {
		t.Errorf("ExecReload = %q, want a pm2 reload", opts.ExecReload)
	}
	// Without ExecStop the daemon outlives the unit.
	if !strings.HasSuffix(opts.ExecStop, " kill") {
		t.Errorf("ExecStop = %q, want pm2 kill", opts.ExecStop)
	}
	if !strings.Contains(exec, " start ") {
		t.Errorf("ExecStart = %q, want pm2 start", exec)
	}
	if strings.Contains(exec, "--no-daemon") {
		t.Errorf("ExecStart = %q must let PM2 fork, which is what Type=forking expects", exec)
	}
}

func TestPM2HomeIsPerSiteAndWritable(t *testing.T) {
	c := nodeContext(t, nil)
	_, opts, err := (Node{}).pm2StartCommand(context.Background(), c)
	if err != nil {
		t.Fatalf("pm2StartCommand = %v", err)
	}
	home := pm2Home(c)
	// Per site, so one tenant's daemon is never another's, and so it is removed
	// with the site rather than left behind in a shared location.
	if !strings.HasPrefix(home, c.SiteDir) {
		t.Errorf("PM2_HOME = %q, want it inside %q", home, c.SiteDir)
	}
	var found bool
	for _, e := range opts.Environment {
		if e == "PM2_HOME="+home {
			found = true
		}
	}
	if !found {
		t.Errorf("Environment = %v, want PM2_HOME=%s", opts.Environment, home)
	}
	// ProtectSystem=strict makes the whole filesystem read-only, so PM2's own
	// directory has to be named or the daemon cannot write its socket.
	if opts.ExtraReadWritePaths != home {
		t.Errorf("ExtraReadWritePaths = %q, want %q", opts.ExtraReadWritePaths, home)
	}
}

func TestProcessManagerPrecedence(t *testing.T) {
	for _, tc := range []struct{ name, site, cfg, want string }{
		{"the default is pm2", "", "", ProcessManagerPM2},
		{"configuration overrides the default", "", "direct", "direct"},
		{"the site overrides configuration", "pm2", "direct", "pm2"},
		{"a site can opt out", "direct", "pm2", "direct"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := nodeContext(t, func(s *state.Site) { s.ProcessManager = tc.site })
			c.Cfg.Runtimes.NodeProcessManager = tc.cfg
			if got := ProcessManagerFor(c); got != tc.want {
				t.Errorf("ProcessManagerFor = %q, want %q", got, tc.want)
			}
		})
	}
}

const jlistTwoWorkers = `[
  {"name":"alice-app_example_com","pm2_env":{"status":"online","restart_time":2,"exec_mode":"cluster_mode"},
   "monit":{"memory":100,"cpu":1.5}},
  {"name":"alice-app_example_com","pm2_env":{"status":"stopped","restart_time":7,"exec_mode":"cluster_mode"},
   "monit":{"memory":50,"cpu":0.5}},
  {"name":"someone-else","pm2_env":{"status":"online","restart_time":99,"exec_mode":"fork_mode"},
   "monit":{"memory":10,"cpu":9}}
]`

func TestPM2ReportCountsWorkersAndTakesTheWorstRestartCount(t *testing.T) {
	c := nodeContext(t, nil)
	c.Runner = &stubRunner{stdout: jlistTwoWorkers}
	st, err := (Node{}).PM2Report(context.Background(), c)
	if err != nil {
		t.Fatalf("PM2Report = %v", err)
	}
	if st.Instances != 2 || st.Online != 1 {
		t.Errorf("instances = %d, online = %d, want 2 and 1", st.Instances, st.Online)
	}
	// The maximum, not the sum: one worker restarting seven times is the signal,
	// and summing would inflate it by the instance count.
	if st.Restarts != 7 {
		t.Errorf("restarts = %d, want 7 (the worst worker, not the total)", st.Restarts)
	}
	if st.Memory != 150 {
		t.Errorf("memory = %d, want the workers summed", st.Memory)
	}
	if st.Mode != "cluster_mode" {
		t.Errorf("mode = %q, want cluster_mode", st.Mode)
	}
}

func TestPM2ReportTreatsNoDaemonAsEmptyRatherThanBroken(t *testing.T) {
	c := nodeContext(t, nil)
	// PM2 exits non-zero with no output when its daemon is not running, which is
	// the normal state of a stopped site.
	c.Runner = &stubRunner{stdout: "", exit: 1}
	st, err := (Node{}).PM2Report(context.Background(), c)
	if err != nil {
		t.Fatalf("a stopped site must not be an error: %v", err)
	}
	if st.Instances != 0 || st.Online != 0 || st.Restarts != 0 {
		t.Errorf("got %+v, want a zeroed report", st)
	}
}

func TestReloadRefusesWithoutPM2AndSaysHowToFixIt(t *testing.T) {
	c := nodeContext(t, func(s *state.Site) { s.ProcessManager = ProcessManagerDirect })
	err := (Node{}).Reload(context.Background(), c)
	if err == nil {
		t.Fatal("a node site without PM2 cannot reload gracefully, so this must refuse")
	}
	// Refusing is only acceptable if the way out is in the message.
	if hint := rlerr.Hint(err); !strings.Contains(hint, "--daemon pm2") {
		t.Errorf("the refusal must name the fix, got hint %q for: %v", hint, err)
	}
}

func TestReloadUnderPM2IsAReloadAndNotARestart(t *testing.T) {
	c := nodeContext(t, nil)
	c.DryRun = false
	runner := &stubRunner{}
	c.Runner = runner
	installFakeRuntime(t, c, "node", "pm2")
	if err := (Node{}).pm2Reload(context.Background(), c); err != nil {
		t.Fatalf("pm2Reload = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("want exactly one command, got %v", runner.calls)
	}
	call := runner.calls[0]
	if len(call) < 2 || call[1] != "reload" {
		t.Errorf("ran %v, want a pm2 reload", call)
	}
	// Without --update-env a reload would keep the old environment, which makes
	// `env set` followed by `site reload` a silent no-op.
	if !containsArg(call, "--update-env") {
		t.Errorf("ran %v, want --update-env so a changed .env is picked up", call)
	}
}

func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

func TestPM2BinaryRefusalNamesBothWaysForward(t *testing.T) {
	c := nodeContext(t, nil)
	c.DryRun = false
	// Node present, PM2 absent: the refusal has to be about PM2 specifically, and
	// has to offer both ways forward rather than leaving the operator stuck.
	installFakeRuntime(t, c, "node")
	_, err := (Node{}).pm2Binary(c)
	if err == nil {
		t.Fatal("pm2Binary must refuse when PM2 is not installed")
	}
	hint := rlerr.Hint(err)
	for _, want := range []string{"--with-pm2", "--daemon direct"} {
		if !strings.Contains(hint, want) {
			t.Errorf("the refusal should offer %q:\n%s\n%s", want, err, hint)
		}
	}
}

func TestThePM2UnitPutsNodeOnPath(t *testing.T) {
	// `pm2` is a JavaScript file with a `#!/usr/bin/env node` shebang. systemd execs
	// it directly, env searches PATH, and a unit's PATH does not include a managed
	// runtime — so without this every PM2-supervised site failed to start with
	// "/usr/bin/env: 'node': No such file or directory" and status 127. PM2 is the
	// default supervisor, so that was every node site.
	c := nodeContext(t, nil)
	_, opts, err := (Node{}).pm2StartCommand(context.Background(), c)
	if err != nil {
		t.Fatalf("pm2StartCommand = %v", err)
	}
	var path string
	for _, e := range opts.Environment {
		if rest, ok := strings.CutPrefix(e, "PATH="); ok {
			path = rest
		}
	}
	if path == "" {
		t.Fatalf("the unit sets no PATH: %v", opts.Environment)
	}
	// The managed runtime's bin directory has to come first, or a system node of a
	// different major version answers instead.
	if !strings.HasPrefix(path, "/opt/ratline/runtimes/node/22/bin:") {
		t.Errorf("PATH = %q, want the managed node bin directory first", path)
	}
}

func TestEveryPM2InvocationHasNodeOnPath(t *testing.T) {
	// `pm2` is a JS file with a `#!/usr/bin/env node` shebang, so *every* way ratline
	// runs it needs the managed node on PATH — not only the systemd unit. Two of them
	// failed silently: PM2Report, which made PM2 supervision invisible to site
	// status, doctor and troubleshoot; and pm2Reload, where a reload that never ran
	// was indistinguishable from one that dropped no requests.
	c := nodeContext(t, nil)
	c.DryRun = false
	installFakeRuntime(t, c, "node", "pm2")

	env, err := (Node{}).pm2Env(c)
	if err != nil {
		t.Fatalf("pm2Env = %v", err)
	}
	var path, home string
	for _, e := range env {
		if rest, ok := strings.CutPrefix(e, "PATH="); ok {
			path = rest
		}
		if rest, ok := strings.CutPrefix(e, "PM2_HOME="); ok {
			home = rest
		}
	}
	if path == "" {
		t.Errorf("no PATH in the pm2 environment: %v", env)
	} else if !strings.Contains(path, "/node/22/bin") {
		t.Errorf("PATH = %q, want the managed node bin directory", path)
	}
	if home != pm2Home(c) {
		t.Errorf("PM2_HOME = %q, want %q", home, pm2Home(c))
	}
}

// bunPM2Context is a bun site that has asked for PM2.
func bunPM2Context(t *testing.T, mutate func(*state.Site)) *Context {
	t.Helper()
	cfg := config.Default()
	cfg.Runtimes.NodeDefault = "22"
	cfg.Runtimes.BunDefault = "1.2"
	site := &state.Site{
		Domain: "mcp.example.com", Owner: "alice", Runtime: "bun",
		Slug: "alice-mcp_example_com", Enabled: true,
		Entry: "server.ts", Listen: "port", Port: 3000, Instances: 1,
		ProcessManager: ProcessManagerPM2,
	}
	if mutate != nil {
		mutate(site)
	}
	id := &system.Identity{Name: "alice", UID: 1001, GID: 1001, Home: cfg.HomeDir("alice")}
	return NewContext(cfg, log.Discard(), &stubRunner{}, site, id, true)
}

// PM2 supervising bun is fork mode with bun named as the interpreter, never cluster.
//
// This is the whole shape of the arrangement, and getting it wrong is silent: PM2's
// cluster mode *is* node's cluster module, so asking for cluster with bun as the
// interpreter has PM2 fork through node and hand a .ts file to the wrong engine.
func TestEcosystemRunsBunInForkModeWithBunAsTheInterpreter(t *testing.T) {
	app := decodeEcosystem(t, bunPM2Context(t, nil))
	if app.ExecMode != "fork" {
		t.Errorf("exec_mode = %q, want fork — cluster mode is node's own module", app.ExecMode)
	}
	if !strings.HasSuffix(app.Interpreter, "/bun") {
		t.Errorf("interpreter = %q, want the managed bun binary", app.Interpreter)
	}
	if !filepath.IsAbs(app.Interpreter) {
		t.Errorf("interpreter = %q, want an absolute path so PATH cannot redirect it", app.Interpreter)
	}
	if !strings.HasSuffix(app.Script, "/app/server.ts") {
		t.Errorf("script = %q, want the TypeScript entry inside the app directory", app.Script)
	}
	// The negative: a node site on the same code path still gets cluster mode, or
	// this test would pass against an implementation that had broken node too.
	if node := decodeEcosystem(t, nodeContext(t, nil)); node.ExecMode != "cluster" {
		t.Errorf("a node site's exec_mode = %q, want cluster", node.ExecMode)
	}
}

// More than one instance is carried through, because for bun each one is a whole
// process rather than a cluster worker.
func TestEcosystemKeepsBunInstancesRatherThanCollapsingThemToOne(t *testing.T) {
	app := decodeEcosystem(t, bunPM2Context(t, func(s *state.Site) { s.Instances = 4 }))
	if app.Instances != 4 {
		t.Errorf("instances = %d, want 4", app.Instances)
	}
	if app.ExecMode != "fork" {
		t.Errorf("exec_mode = %q, want fork", app.ExecMode)
	}
}

// A bun site under PM2 gets the same port variables the direct path sets, so an
// application works unchanged when PM2 is put in front of it.
func TestEcosystemGivesBunTheSamePortSpellingsAsDirectSupervision(t *testing.T) {
	app := decodeEcosystem(t, bunPM2Context(t, nil))
	for _, key := range []string{"PORT", "BUN_PORT", "HOST"} {
		if app.Env[key] == "" {
			t.Errorf("env %s is unset; a default-exported Bun.serve reads BUN_PORT", key)
		}
	}
	if app.Env["PORT"] != "3000" || app.Env["BUN_PORT"] != "3000" {
		t.Errorf("PORT=%q BUN_PORT=%q, want both 3000", app.Env["PORT"], app.Env["BUN_PORT"])
	}
	// A node site has no business being told BUN_PORT.
	if node := decodeEcosystem(t, nodeContext(t, func(s *state.Site) {
		s.Listen, s.Port = "port", 3000
	})); node.Env["BUN_PORT"] != "" {
		t.Errorf("a node site got BUN_PORT=%q", node.Env["BUN_PORT"])
	}
}

// PATH carries bun first and node behind it.
//
// node is not optional even for a bun site: pm2 is a JavaScript file with a
// `#!/usr/bin/env node` shebang and resolves its own interpreter through PATH. Losing
// node here is the failure that used to surface as status 127 and nothing else.
func TestABunSiteUnderPM2StillHasNodeOnPath(t *testing.T) {
	app := decodeEcosystem(t, bunPM2Context(t, nil))
	dirs := strings.Split(app.Env["PATH"], ":")
	if len(dirs) < 2 {
		t.Fatalf("PATH = %q, want the interpreter and node ahead of the default", app.Env["PATH"])
	}
	if !strings.Contains(dirs[0], "/bun/") {
		t.Errorf("PATH starts with %q, want bun's directory first", dirs[0])
	}
	var hasNode bool
	for _, d := range dirs {
		if strings.Contains(d, "/node/") {
			hasNode = true
		}
	}
	if !hasNode {
		t.Errorf("PATH = %q has no node directory; pm2's shebang resolves node through it",
			app.Env["PATH"])
	}
}

// `site reload` on a bun site refuses whether or not PM2 is in front of it, and says
// which of the two reasons applies.
func TestBunReloadRefusesUnderPM2Too(t *testing.T) {
	direct := bunPM2Context(t, func(s *state.Site) { s.ProcessManager = ProcessManagerDirect })
	err := (Bun{}).Reload(context.Background(), direct)
	if err == nil {
		t.Fatal("a bun site without PM2 should refuse to reload")
	}
	if !strings.Contains(err.Error()+rlerr.Hint(err), "restart") {
		t.Errorf("the refusal should name the restart, got: %v", err)
	}

	err = (Bun{}).Reload(context.Background(), bunPM2Context(t, nil))
	if err == nil {
		t.Fatal("a bun site on PM2 should still refuse to reload: fork mode cannot")
	}
	combined := err.Error() + " " + rlerr.Hint(err)
	if !strings.Contains(combined, "fork mode") {
		t.Errorf("the refusal should explain that fork mode cannot reload, got: %s", combined)
	}
}

// The unit for a PM2-supervised bun site is PM2's unit, not bun's.
func TestABunSiteOnPM2GetsThePM2Unit(t *testing.T) {
	c := bunPM2Context(t, nil)
	execStart, opts, err := (Bun{}).StartCommand(context.Background(), c)
	if err != nil {
		t.Fatalf("StartCommand = %v", err)
	}
	if !strings.Contains(execStart, "pm2") {
		t.Errorf("ExecStart = %q, want pm2", execStart)
	}
	if opts.Type != "forking" {
		t.Errorf("unit Type = %q, want forking", opts.Type)
	}
	if opts.PIDFile == "" {
		t.Error("a forking unit needs a PIDFile or systemd follows the wrong process")
	}

	// The negative: without PM2 it is still bun straight under systemd.
	direct := bunPM2Context(t, func(s *state.Site) { s.ProcessManager = ProcessManagerDirect })
	execStart, opts, err = (Bun{}).StartCommand(context.Background(), direct)
	if err != nil {
		t.Fatalf("StartCommand = %v", err)
	}
	if strings.Contains(execStart, "pm2") {
		t.Errorf("ExecStart = %q, want bun itself", execStart)
	}
	if !strings.HasSuffix(strings.Fields(execStart)[0], "/bun") {
		t.Errorf("ExecStart = %q, want the bun binary as the main process", execStart)
	}
	if opts.Type == "forking" {
		t.Error("direct supervision should not be a forking unit")
	}
}
