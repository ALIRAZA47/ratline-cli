// Package site is the lifecycle that ties everything together: the directory
// tree, the nginx vhost, the systemd unit, the runtime and the state row.
//
// Every mutation is staged, verified and committed. A failure at any point
// unwinds what came before it, so the server is never left half configured.
package site

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/nginx"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/runtime"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// Manager performs site operations.
type Manager struct {
	Cfg     *config.Config
	Log     *log.Logger
	Runner  system.Runner
	State   *state.Store
	Nginx   *nginx.Manager
	Unit    *unit.Manager
	Invoker string
	DryRun  bool
}

// AddOptions is the resolved form of `ratline site add`.
type AddOptions struct {
	Domain  string
	Owner   string
	Runtime string
	Aliases []string

	WWWRedirect string
	NoEnable    bool
	Repo        string
	Branch      string

	// static
	DocRoot   string
	SPA       bool
	IndexFile string

	// node
	Entry          string
	NodeVersion    string
	PackageManager string
	Listen         string
	ProcessManager string
	Instances      int

	// python
	AppModule     string
	PythonVersion string
	ASGI          *bool
	AppServer     string
	Workers       int
	Requirements  string
	ManagePy      string
	StaticURL     string
	StaticDir     string

	// shared
	StartCommand   string
	InstallCommand string
	BuildCommand   string
	BuildOutput    string
	PublicDir      string

	MemoryMax         string
	CPUQuota          string
	ClientMaxBodySize string
	HSTS              bool

	// Relaxed names systemd hardening directives to turn off for this site.
	// Validated by the caller against unit.HardeningDirectives, so a typo is a
	// usage error rather than a directive that silently stays on.
	Relaxed []string
}

// AddResult reports what happened.
type AddResult struct {
	Site    *state.Site `json:"site"`
	Created bool        `json:"created"`
	Health  string      `json:"health,omitempty"`
	Port    int         `json:"port,omitempty"`
	Steps   []string    `json:"steps,omitempty"`
}

// Add provisions a site end to end.
func (m *Manager) Add(ctx context.Context, opts AddOptions) (res *AddResult, err error) {
	site, err := m.buildSite(ctx, &opts)
	if err != nil {
		return nil, err
	}

	// Idempotency: an identical re-run succeeds without touching anything.
	if existing, err := m.State.GetSite(ctx, site.Domain); err == nil {
		if sameConfiguration(existing, site) {
			m.Log.Info("already configured", "domain", site.Domain)
			return &AddResult{Site: existing, Created: false}, nil
		}
		return nil, rlerr.Preconditionf("%s already exists with a different configuration", site.Domain).
			WithHint("change it in place with 'ratline site scale', 'ratline site runtime' or "+
				"'ratline site alias', or remove it first with 'ratline site delete %s --purge'", site.Domain)
	}

	if err := m.checkPreconditions(ctx, site); err != nil {
		return nil, err
	}

	rb := system.NewRollback(m.Log)
	defer rb.UnwindOn(ctx, &err)

	res = &AddResult{Site: site, Created: true}
	id, err := m.identity(site.Owner)
	if err != nil {
		return nil, err
	}

	// A port has to be reserved before the vhost is rendered, since the vhost
	// names it.
	if site.Dynamic() && site.Listen == "port" {
		if m.DryRun {
			// A plausible port, so the rendered preview names something realistic,
			// without reserving anything. Reserving under --dry-run would leak a port
			// on every preview.
			site.Port = m.Cfg.Ports.RangeStart
			res.Port = site.Port
			m.Log.Info("would allocate a port", "from", m.Cfg.Ports.RangeStart,
				"to", m.Cfg.Ports.RangeEnd)
		} else {
			port, err := m.State.AllocatePort(ctx, site.Domain,
				m.Cfg.Ports.RangeStart, m.Cfg.Ports.RangeEnd, system.PortFree)
			if err != nil {
				return nil, err
			}
			site.Port = port
			res.Port = port
			rb.Push(fmt.Sprintf("allocated port %d", port), func(ctx context.Context) error {
				return m.State.ReleasePort(ctx, site.Domain)
			})
			m.Log.Info("allocated a port", "port", port, "domain", site.Domain)
		}
	}

	if err := m.buildTree(ctx, site, id, rb); err != nil {
		return nil, err
	}
	res.Steps = append(res.Steps, "directories")

	// A preview must not write the row. It used to, which meant `--dry-run site add`
	// left a real record behind — and the subsequent real command refused with
	// "already exists with a different configuration", against a site that had never
	// been created. A dry run that poisons the database is worse than none.
	if m.DryRun {
		m.Log.Info("would record the site in state", "domain", site.Domain)
	} else {
		if err := m.State.PutSite(ctx, site); err != nil {
			return nil, err
		}
		rb.Push("recorded the site in state", func(ctx context.Context) error {
			return m.State.DeleteSite(ctx, site.Domain)
		})
	}

	if opts.Repo != "" {
		if err := m.clone(ctx, site, id, opts); err != nil {
			return nil, err
		}
		res.Steps = append(res.Steps, "clone")
	}

	rt, err := runtime.For(site.Runtime)
	if err != nil {
		return nil, err
	}
	rc := runtime.NewContext(m.Cfg, m.Log, m.Runner, site, id, m.DryRun)

	if err := rt.Provision(ctx, rc); err != nil {
		return nil, err
	}
	res.Steps = append(res.Steps, "provision")
	rb.Push("provisioned the runtime", func(ctx context.Context) error { return rt.Teardown(ctx, rc) })

	// Dependencies and a build only make sense once there is code to work with.
	if opts.Repo != "" || hasApplicationCode(rc.AppDir) {
		if err := rt.Install(ctx, rc); err != nil {
			return nil, err
		}
		res.Steps = append(res.Steps, "install")
		if err := rt.Build(ctx, rc); err != nil {
			return nil, err
		}
		if site.BuildCommand != "" {
			res.Steps = append(res.Steps, "build")
		}
	} else if site.Dynamic() {
		m.Log.Warn("the application directory is empty, so nothing was installed or built",
			"dir", rc.AppDir, "next", "deploy your code, then run 'ratline site deploy "+site.Domain+" --install --build --restart'")
	}

	if err := m.Unit.InstallLogrotate(ctx, site); err != nil {
		return nil, err
	}

	if site.Dynamic() {
		if err := m.applyUnit(ctx, site, rt, rc, rb); err != nil {
			return nil, err
		}
		res.Steps = append(res.Steps, "unit")
	}

	// The vhost goes last, so nginx only ever points at something that is
	// already up.
	if err := m.Nginx.Apply(ctx, site, nil, rb); err != nil {
		return nil, err
	}
	res.Steps = append(res.Steps, "vhost")

	// Started only when there is something to start.
	//
	// A site created before its code arrives is a normal thing to want: a private
	// repository the server cannot clone, a build produced by CI, an rsync from a laptop.
	// It was impossible. `site add` warned "the application directory is empty … deploy
	// your code, then run site deploy", wrote the unit, started it, watched PM2 report
	// "Script not found", and rolled the whole site back out of existence — so the advice
	// it had just printed could never be followed. --repo was the only way through.
	//
	// Now the site is configured and left stopped, and the next step is the one already
	// being recommended.
	switch {
	case site.Dynamic() && site.Enabled && !hasApplicationCode(rc.AppDir):
		m.Log.Warn("configured, but not started: there is no code in the application directory yet",
			"dir", rc.AppDir,
			"next", "deploy your code, then 'ratline site deploy "+site.Domain+" --install --build --restart'")
		res.Steps = append(res.Steps, "awaiting-code")
	case site.Dynamic() && site.Enabled:
		health, err := m.startAndWait(ctx, site)
		if err != nil {
			return nil, err
		}
		res.Health = health
		res.Steps = append(res.Steps, "health")
	}

	if err := m.writeManifest(site, id); err != nil {
		return nil, err
	}
	m.Log.Info("site created", "domain", site.Domain, "runtime", site.Runtime, "owner", site.Owner)
	return res, nil
}

// buildSite turns options into a validated site row.
func (m *Manager) buildSite(ctx context.Context, opts *AddOptions) (*state.Site, error) {
	domain, err := validate.Domain(opts.Domain)
	if err != nil {
		return nil, err
	}
	if err := validate.Username(opts.Owner); err != nil {
		return nil, err
	}
	if err := validate.RuntimeName(opts.Runtime); err != nil {
		return nil, err
	}
	aliases, err := validate.Aliases(domain, opts.Aliases)
	if err != nil {
		return nil, err
	}
	// --www-redirect implies the www alias exists, otherwise the redirect block
	// would have nothing to match.
	if opts.WWWRedirect == "apex" || opts.WWWRedirect == "www" {
		www := "www." + domain
		found := false
		for _, a := range aliases {
			if a == www {
				found = true
			}
		}
		if !found && !strings.HasPrefix(domain, "www.") {
			aliases = append(aliases, www)
		}
	}

	site := &state.Site{
		Domain:            domain,
		Owner:             opts.Owner,
		Runtime:           opts.Runtime,
		Slug:              validate.Slug(opts.Owner, domain),
		Enabled:           !opts.NoEnable,
		Aliases:           aliases,
		DocRoot:           opts.DocRoot,
		SPA:               opts.SPA,
		IndexFile:         orDefault(opts.IndexFile, "index.html"),
		Entry:             opts.Entry,
		NodeVersion:       opts.NodeVersion,
		PackageManager:    opts.PackageManager,
		Listen:            orDefault(opts.Listen, "socket"),
		ProcessManager:    opts.ProcessManager,
		Instances:         maxInt(opts.Instances, 1),
		AppModule:         opts.AppModule,
		PythonVersion:     opts.PythonVersion,
		AppServer:         opts.AppServer,
		Workers:           opts.Workers,
		Requirements:      opts.Requirements,
		ManagePy:          opts.ManagePy,
		StaticURL:         opts.StaticURL,
		StaticDir:         opts.StaticDir,
		StartCommand:      opts.StartCommand,
		InstallCommand:    opts.InstallCommand,
		BuildCommand:      opts.BuildCommand,
		BuildOutput:       opts.BuildOutput,
		PublicDir:         opts.PublicDir,
		Repo:              opts.Repo,
		Branch:            orDefault(opts.Branch, "main"),
		MemoryMax:         opts.MemoryMax,
		CPUQuota:          opts.CPUQuota,
		ClientMaxBodySize: opts.ClientMaxBodySize,
		WWWRedirect:       orDefault(opts.WWWRedirect, "none"),
		HSTS:              opts.HSTS,
		Relaxed:           opts.Relaxed,
		CreatedAt:         time.Now().UTC(),
		CreatedBy:         m.Invoker,
	}

	switch opts.Runtime {
	case "static":
		site.DocRoot = orDefault(opts.DocRoot, "public")
		if err := validate.Subdir(site.DocRoot); err != nil {
			return nil, err
		}
	case "node":
		if opts.Entry == "" && opts.StartCommand == "" {
			return nil, rlerr.Usagef("a node site needs --entry or --start-command").
				WithHint("--entry server.js is the usual answer; --start-command adds a process between systemd and your server")
		}
		if opts.Entry != "" && opts.StartCommand != "" {
			return nil, rlerr.Usagef("--entry and --start-command contradict each other")
		}
		if opts.Entry != "" {
			if err := validate.NodeEntry(opts.Entry); err != nil {
				return nil, err
			}
		}
		if opts.PackageManager != "" {
			if err := validate.PackageManager(opts.PackageManager); err != nil {
				return nil, err
			}
		}
		if opts.NodeVersion != "" {
			if err := validate.NodeVersion(opts.NodeVersion); err != nil {
				return nil, err
			}
		}
		switch site.Listen {
		case "socket", "port":
		default:
			return nil, rlerr.Usagef("--listen must be socket or port, got %q", site.Listen)
		}
		switch site.ProcessManager {
		case "", runtime.ProcessManagerPM2, runtime.ProcessManagerDirect:
		default:
			return nil, rlerr.Usagef("--daemon must be pm2 or direct, got %q", site.ProcessManager).
				WithHint("pm2 reloads without dropping requests; direct is one fewer moving part")
		}
	case "python":
		if opts.AppModule == "" {
			return nil, rlerr.Usagef("a python site needs --app-module").
				WithHint("pass the import path of your callable, for example --app-module app.main:app")
		}
		if err := validate.AppModule(opts.AppModule); err != nil {
			return nil, err
		}
		if opts.PythonVersion != "" {
			if err := validate.PythonVersion(opts.PythonVersion); err != nil {
				return nil, err
			}
		}
		site.AppServer = orDefault(opts.AppServer, "gunicorn")
		if site.AppServer != "gunicorn" && site.AppServer != "uvicorn" {
			return nil, rlerr.Usagef("--server must be gunicorn or uvicorn, got %q", site.AppServer)
		}
		if opts.ASGI != nil {
			site.ASGI = *opts.ASGI
		} else {
			// Auto-detect, then say what was decided: a silent guess about the
			// worker class is very hard to debug from a 500.
			site.ASGI = runtime.DetectASGI(filepath.Join(m.Cfg.SiteDir(opts.Owner, domain), "app"), opts.AppModule)
			m.Log.Info("detected the application interface", "asgi", site.ASGI,
				"override", "pass --wsgi or --asgi to decide explicitly")
		}
		if site.Workers <= 0 {
			site.Workers = runtime.DefaultWorkers(m.Cfg.Defaults.WorkerCap)
		}
		if opts.StaticURL != "" && opts.StaticDir == "" {
			return nil, rlerr.Usagef("--static-url needs --static-dir")
		}
		if opts.StaticDir != "" {
			if err := validate.Subdir(opts.StaticDir); err != nil {
				return nil, err
			}
		}
	}

	for _, spec := range []struct {
		name, value string
	}{{"--build-output", opts.BuildOutput}, {"--public", opts.PublicDir}} {
		if spec.value != "" {
			if err := validate.Subdir(spec.value); err != nil {
				return nil, err
			}
		}
	}
	for _, cmd := range []string{opts.StartCommand, opts.InstallCommand, opts.BuildCommand} {
		if cmd == "" {
			continue
		}
		if _, err := system.ParseCommand(cmd); err != nil {
			return nil, err
		}
	}
	if opts.MemoryMax != "" {
		if _, err := validate.Size(opts.MemoryMax); err != nil {
			return nil, err
		}
	}
	if opts.CPUQuota != "" {
		if err := validate.CPUQuota(opts.CPUQuota); err != nil {
			return nil, err
		}
	}
	if opts.ClientMaxBodySize != "" {
		if _, err := validate.Size(opts.ClientMaxBodySize); err != nil {
			return nil, err
		}
	}
	if opts.Repo != "" {
		if err := validate.GitURL(opts.Repo); err != nil {
			return nil, err
		}
		if err := validate.GitRef(site.Branch); err != nil {
			return nil, err
		}
	}
	if err := validateInstances(site, m.Cfg.Runtimes.NodeProcessManager); err != nil {
		return nil, err
	}
	if site.HSTS {
		m.Log.Warn("HSTS will only be rendered once a trusted certificate is attached")
	}
	return site, nil
}

// validateInstances checks that a request for more than one instance can actually
// be honoured.
//
// A dynamic site is one unit binding one socket, and concurrency lives inside that
// unit. So --instances means "PM2 cluster workers", and it only has a meaning where
// PM2 is doing the supervising. Everywhere else it is refused and the flag that
// does work is named, rather than being accepted and silently ignored — which is
// how an operator ends up believing a site is running four workers when it is
// running one.
//
// configuredManager is the server-wide default, needed because a site that never
// chose one follows it. Checking only site.ProcessManager would accept --instances on
// a server configured for direct supervision, which is precisely the case this
// refusal exists for.
func validateInstances(site *state.Site, configuredManager string) error {
	if site.Instances <= 1 {
		return nil
	}
	if !site.Dynamic() {
		return rlerr.Usagef("--instances only applies to node and python sites")
	}
	if site.Runtime == "python" {
		return rlerr.Usagef("a python site scales with workers, not instances").
			WithHint("gunicorn workers share the one socket: ratline site scale %s --workers %d",
				site.Domain, site.Instances)
	}
	manager := site.ProcessManager
	if manager == "" {
		manager = configuredManager
	}
	if manager == runtime.ProcessManagerDirect {
		return rlerr.Usagef("a node site running without PM2 is a single process").
			WithHint("PM2 cluster mode is what fans it out: ratline site runtime %s --daemon pm2",
				site.Domain)
	}
	return nil
}

// checkPreconditions verifies everything that would otherwise fail part way
// through, so a refusal costs nothing.
func (m *Manager) checkPreconditions(ctx context.Context, site *state.Site) error {
	var problems []string

	if _, err := m.State.GetUser(ctx, site.Owner); err != nil {
		return rlerr.Preconditionf("no such user: %s", site.Owner).
			WithHint("create it first: ratline user add %s", site.Owner)
	}
	if !m.DryRun && !system.UserExists(site.Owner) {
		problems = append(problems, fmt.Sprintf("%s is recorded in state but has no system account", site.Owner))
	}

	for _, name := range site.ServerNames() {
		if owner, used, err := m.State.NameInUse(ctx, name); err != nil {
			return err
		} else if used {
			problems = append(problems, fmt.Sprintf("%s is already served by %s", name, owner))
		}
		// Not `err == nil && conflict != ""`: that treated "the check could not run" as
		// "there is no conflict", so an unreadable sites-enabled silently opened the
		// gate this loop exists to close. nginx resolves a duplicate server_name
		// unpredictably, which is the outage this prevents.
		conflict, err := m.Nginx.ConflictingServerName(name, site.Domain)
		if err != nil {
			return err
		}
		if conflict != "" {
			problems = append(problems,
				fmt.Sprintf("%s is already claimed by the nginx configuration %s, which ratline did not create", name, conflict))
		}
	}

	if taken, err := m.State.SlugInUse(ctx, site.Slug, site.Domain); err != nil {
		return err
	} else if taken {
		problems = append(problems, fmt.Sprintf("the identifier %q is already in use, so the systemd unit would collide", site.Slug))
	}

	if !m.DryRun {
		if free, err := system.FreeBytes(m.Cfg.Paths.HomeBase); err == nil && free < 256<<20 {
			problems = append(problems, fmt.Sprintf("only %s is free on %s", validate.FormatSize(int64(free)), m.Cfg.Paths.HomeBase))
		}
	}

	if len(problems) > 0 {
		return rlerr.Preconditionf("%s cannot be created:\n  - %s", site.Domain, strings.Join(problems, "\n  - ")).
			WithHint("nothing was changed")
	}
	return nil
}

// buildTree creates the site's directory layout with exact modes.
func (m *Manager) buildTree(ctx context.Context, site *state.Site, id *system.Identity, rb *system.Rollback) error {
	siteDir := m.Cfg.SiteDir(site.Owner, site.Domain)
	if m.DryRun {
		m.Log.Info("would create the site tree", "path", siteDir)
		return nil
	}

	logGID := id.GID
	if gid, err := system.LookupGroupID(m.Cfg.Users.LogGroup); err == nil {
		logGID = gid
	}

	dirs := []struct {
		path string
		mode os.FileMode
		gid  int
	}{
		{siteDir, os.FileMode(m.Cfg.SiteFileMode()), id.GID},
		{filepath.Join(siteDir, "app"), 0o750, id.GID},
		{filepath.Join(siteDir, "logs"), 0o750, logGID},
		// 0700: nothing else has any business reading a site's temporary files.
		{filepath.Join(siteDir, "tmp"), 0o700, id.GID},
		{filepath.Join(siteDir, ".ratline"), 0o750, id.GID},
	}
	if site.Runtime != "static" && site.PublicDir != "" {
		dirs = append(dirs, struct {
			path string
			mode os.FileMode
			gid  int
		}{filepath.Join(siteDir, site.PublicDir), 0o750, id.GID})
	}

	for _, d := range dirs {
		created, err := system.EnsureDir(d.path, d.mode, id.UID, d.gid)
		if err != nil {
			return err
		}
		if created {
			path := d.path
			rb.Push("created "+path, func(context.Context) error { return os.RemoveAll(path) })
		}
	}

	// The log files are created up front so that logrotate's create directive and
	// nginx's append both find what they expect.
	for _, name := range []string{"access.log", "error.log", "app.log"} {
		path := filepath.Join(siteDir, "logs", name)
		if system.Exists(path) {
			continue
		}
		if err := system.WriteFileAtomic(path, nil, 0o640, id.UID, logGID); err != nil {
			return err
		}
	}

	// .env is 0600 and owned by the tenant. systemd reads it as root before
	// dropping privileges, which is what lets it hold secrets nginx can never
	// serve — and it is outside the document root by construction.
	envPath := filepath.Join(siteDir, ".env")
	if !system.Exists(envPath) {
		header := "# Environment for " + site.Domain + "\n" +
			"# Loaded by systemd before privileges are dropped. Never served by nginx.\n" +
			"# Manage it with: ratline site env set " + site.Domain + " KEY=VALUE\n"
		if err := system.WriteFileAtomic(envPath, []byte(header), 0o600, id.UID, id.GID); err != nil {
			return err
		}
	}
	return nil
}

// writeManifest records the rendered site next to the site itself, so that
// `reconcile` can rebuild state from the filesystem alone.
func (m *Manager) writeManifest(site *state.Site, id *system.Identity) error {
	if m.DryRun {
		return nil
	}
	path := filepath.Join(m.Cfg.SiteDir(site.Owner, site.Domain), ".ratline", "site.yaml")
	var b strings.Builder
	b.WriteString("# " + system.ManagedHeader + "\n")
	b.WriteString("# The rendered manifest for this site. ratline reads it during reconcile,\n")
	b.WriteString("# so a site survives the loss of the state database.\n")
	fmt.Fprintf(&b, "domain: %s\n", site.Domain)
	fmt.Fprintf(&b, "owner: %s\n", site.Owner)
	fmt.Fprintf(&b, "runtime: %s\n", site.Runtime)
	fmt.Fprintf(&b, "slug: %s\n", site.Slug)
	fmt.Fprintf(&b, "enabled: %t\n", site.Enabled)
	if len(site.Aliases) > 0 {
		fmt.Fprintf(&b, "aliases: [%s]\n", strings.Join(site.Aliases, ", "))
	}
	for _, kv := range [][2]string{
		{"doc_root", site.DocRoot}, {"entry", site.Entry}, {"app_module", site.AppModule},
		{"node_version", site.NodeVersion}, {"python_version", site.PythonVersion},
		{"listen", site.Listen}, {"app_server", site.AppServer},
		{"start_command", site.StartCommand}, {"build_command", site.BuildCommand},
		{"build_output", site.BuildOutput}, {"public_dir", site.PublicDir},
		{"requirements", site.Requirements}, {"static_url", site.StaticURL},
		{"static_dir", site.StaticDir}, {"repo", site.Repo}, {"branch", site.Branch},
		{"memory_max", site.MemoryMax}, {"cpu_quota", site.CPUQuota},
		{"www_redirect", site.WWWRedirect},
	} {
		if kv[1] != "" {
			fmt.Fprintf(&b, "%s: %q\n", kv[0], kv[1])
		}
	}
	if site.Workers > 0 {
		fmt.Fprintf(&b, "workers: %d\n", site.Workers)
	}
	if site.Port > 0 {
		fmt.Fprintf(&b, "port: %d\n", site.Port)
	}
	if site.Instances > 1 {
		fmt.Fprintf(&b, "instances: %d\n", site.Instances)
	}
	fmt.Fprintf(&b, "spa: %t\nasgi: %t\nhsts: %t\n", site.SPA, site.ASGI, site.HSTS)
	return system.WriteFileAtomic(path, []byte(b.String()), 0o640, id.UID, id.GID)
}

func (m *Manager) applyUnit(ctx context.Context, site *state.Site, rt runtime.Runtime, rc *runtime.Context, rb *system.Rollback) error {
	// Before the unit, because the unit is what creates the socket directory inside it
	// and systemd would otherwise leave the shared parent owned by this one tenant.
	if err := m.Unit.EnsureRuntimeDir(ctx); err != nil {
		return err
	}
	execStart, opts, err := rt.StartCommand(ctx, rc)
	if err != nil {
		return err
	}
	if execStart == "" {
		return rlerr.Genericf("internal error: the %s runtime produced no start command", site.Runtime)
	}
	body, err := m.Unit.Render(site, execStart, opts)
	if err != nil {
		return err
	}
	return m.Unit.Install(ctx, site, body, rb)
}

// startAndWait starts the service and proves it is really serving.
func (m *Manager) startAndWait(ctx context.Context, site *state.Site) (string, error) {
	if err := m.Unit.Control(ctx, site, "restart"); err != nil {
		return "", err
	}
	health, err := m.Unit.WaitHealthy(ctx, site, m.Cfg.Defaults.HealthTimeout.D())
	if err != nil {
		return "", m.hardeningHint(err)
	}
	m.Log.Info("the application is healthy", "domain", site.Domain, "check", health)
	return health, nil
}

// hardeningHint recognises the failures caused by the systemd sandbox, and names
// the directive to relax rather than leaving the operator to guess.
func (m *Manager) hardeningHint(err error) error {
	fields := rlerr.Fields(err)
	logs := fields["recent_logs"]
	if logs == "" {
		return err
	}
	lower := strings.ToLower(logs)
	for _, c := range []struct{ marker, directive, why string }{
		{"cannot allocate memory in static tls block", "MemoryDenyWriteExecute", "a native library needs writable-executable memory"},
		{"operation not permitted", "SystemCallFilter", "a syscall outside @system-service was refused"},
		{"read-only file system", "ProtectSystem", "the application writes outside its site directory"},
		{"permission denied", "ProtectHome", "the application reads outside its own home"},
		{"mmap failed", "MemoryDenyWriteExecute", "a JIT needs writable-executable memory"},
		{"no such file or directory: '/tmp", "PrivateTmp", "the application expects the shared /tmp"},
	} {
		if strings.Contains(lower, c.marker) {
			return rlerr.Wrap(err, rlerr.CodeUnhealthy,
				"the application failed to start, and the cause looks like the systemd sandbox: %s", c.why).
				WithHint("re-run with --relax %s to turn that one directive off, keeping the rest of the hardening", c.directive)
		}
	}
	return err
}

func (m *Manager) clone(ctx context.Context, site *state.Site, id *system.Identity, opts AddOptions) error {
	appDir := filepath.Join(m.Cfg.SiteDir(site.Owner, site.Domain), "app")
	if m.DryRun {
		m.Log.Info("would clone the repository", "repo", opts.Repo, "into", appDir)
		return nil
	}
	if entries, err := os.ReadDir(appDir); err == nil && len(entries) > 0 {
		return rlerr.Preconditionf("%s is not empty, so nothing was cloned into it", appDir).
			WithHint("clone by hand, or remove the contents first")
	}
	m.Log.Info("cloning", "repo", opts.Repo, "branch", site.Branch)
	_, err := m.Runner.Run(ctx, system.Cmd{
		Name: "git",
		// --branch and -- separate the ref and the paths, so a branch name can
		// never be read as a flag.
		Args:    []string{"clone", "--depth", "1", "--branch", site.Branch, "--", opts.Repo, appDir},
		As:      id,
		Dir:     m.Cfg.SiteDir(site.Owner, site.Domain),
		Mutates: true, Stream: true, Timeout: 10 * time.Minute, Label: "git clone",
	})
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeExternal, "cloning %s failed", opts.Repo).
			WithHint("for a private repository, create a deploy key first: ratline site deploy-key create %s", site.Domain)
	}
	return nil
}

func (m *Manager) identity(owner string) (*system.Identity, error) {
	if m.DryRun && !system.UserExists(owner) {
		return &system.Identity{Name: owner, UID: -1, GID: -1, Home: m.Cfg.HomeDir(owner)}, nil
	}
	return system.LookupIdentity(owner)
}

func hasApplicationCode(appDir string) bool {
	entries, err := os.ReadDir(appDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			return true
		}
	}
	return false
}

// sameConfiguration decides whether a repeated `site add` is a no-op.
func sameConfiguration(a, b *state.Site) bool {
	if a.Owner != b.Owner || a.Runtime != b.Runtime || a.Enabled != b.Enabled {
		return false
	}
	if a.DocRoot != b.DocRoot || a.SPA != b.SPA || a.Entry != b.Entry ||
		a.AppModule != b.AppModule || a.StartCommand != b.StartCommand ||
		a.Listen != b.Listen || a.Instances != b.Instances {
		return false
	}
	if strings.Join(a.Aliases, ",") != strings.Join(b.Aliases, ",") {
		return false
	}
	return true
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
