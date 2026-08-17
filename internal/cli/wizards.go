package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/runtime"
	"github.com/ALIRAZA47/ratline-cli/internal/site"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/user"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// wizardUserAdd collects what `user add` needs, prompting only for what was not
// already supplied as a flag.
//
// It returns options, never side effects: the wizard's whole job is to produce the
// same input the flags would have, which is why the echoed command is guaranteed
// to reproduce the result.
func wizardUserAdd(g *Globals, opts user.AddOptions, keys []string) (user.AddOptions, []string, error) {
	p := newPrompter(g)
	p.heading("Create a tenant account")
	p.note("Each user is a sandbox: its own account, group, home tree and SSH keys.")

	for {
		if opts.Name == "" {
			name, err := p.ask("Username:", "", func(s string) error {
				if err := validate.Username(s); err != nil {
					return err
				}
				if system.UserExists(s) {
					return rlerr.Preconditionf("%s already exists on this system", s)
				}
				return nil
			})
			if err != nil {
				return opts, keys, err
			}
			opts.Name = name
		}

		if len(keys) == 0 {
			p.note("A public key, so the account can be used. Blank to add one later.")
			ref, err := p.ask("Public key (path, https URL, or blank):", defaultKeyPath(), func(s string) error {
				if s == "" {
					return nil
				}
				if strings.HasPrefix(s, "https://") || s == "-" {
					return nil
				}
				if !system.Exists(expandHome(s)) {
					return rlerr.Preconditionf("no such file: %s", expandHome(s))
				}
				return nil
			})
			if err != nil {
				return opts, keys, err
			}
			if ref != "" {
				keys = append(keys, ref)
			}
		}

		if opts.Shell == "" && !opts.SFTPOnly {
			access, err := p.pick("What access does this account need?", []choice{
				{Value: "shell", Label: "A shell", Note: "the usual choice for a client who deploys"},
				{Value: "sftp", Label: "SFTP only", Note: "chrooted to the home, no shell"},
				{Value: "none", Label: "No login", Note: "the account owns files but nobody logs in"},
			}, "shell")
			if err != nil {
				return opts, keys, err
			}
			switch access {
			case "sftp":
				opts.SFTPOnly = true
			case "none":
				opts.Shell = "/usr/sbin/nologin"
			default:
				opts.Shell = g.Cfg.Defaults.Shell
			}
		}

		if opts.Quota == "" && g.Cfg.Users.QuotaEnabled {
			quota, err := p.ask("Disk quota (e.g. 20G, blank for none):", "", func(s string) error {
				if s == "" {
					return nil
				}
				_, err := validate.Size(s)
				return err
			})
			if err != nil {
				return opts, keys, err
			}
			opts.Quota = quota
		}

		argv := []string{"ratline", "user", "add", opts.Name}
		fields := [][2]string{
			{"username", opts.Name},
			{"home", g.Cfg.HomeDir(opts.Name)},
		}
		for _, k := range keys {
			argv = append(argv, "--ssh-key", k)
		}
		if len(keys) > 0 {
			fields = append(fields, [2]string{"ssh keys", strings.Join(keys, ", ")})
		} else {
			fields = append(fields, [2]string{"ssh keys", "none — the account cannot log in yet"})
		}
		if opts.SFTPOnly {
			argv = append(argv, "--sftp-only")
			fields = append(fields, [2]string{"access", "sftp only"})
		} else if opts.Shell != "" && opts.Shell != g.Cfg.Defaults.Shell {
			argv = append(argv, "--shell", opts.Shell)
			fields = append(fields, [2]string{"shell", opts.Shell})
		} else {
			fields = append(fields, [2]string{"shell", g.Cfg.Defaults.Shell})
		}
		if opts.Quota != "" {
			argv = append(argv, "--quota", opts.Quota)
			fields = append(fields, [2]string{"quota", opts.Quota})
		}

		action, err := p.summary("Ready to create", fields, argv)
		if err != nil {
			return opts, keys, err
		}
		switch action {
		case actionRun:
			// The echoed command goes to the audit log, so the trail records what
			// the wizard produced rather than just "the wizard ran".
			g.Argv = argv
			return opts, keys, nil
		case actionCancel:
			return opts, keys, ErrCancelled
		default:
			// Editing clears the field the operator most likely wants to change.
			which, err := p.pick("Which field?", []choice{
				{Value: "name", Label: "Username"},
				{Value: "keys", Label: "SSH keys"},
				{Value: "access", Label: "Access"},
				{Value: "quota", Label: "Quota"},
			}, "name")
			if err != nil {
				return opts, keys, err
			}
			switch which {
			case "name":
				opts.Name = ""
			case "keys":
				keys = nil
			case "access":
				opts.Shell, opts.SFTPOnly = "", false
			case "quota":
				opts.Quota = ""
			}
		}
	}
}

// wizardSiteAdd collects what `site add` needs, branching on the runtime and
// sniffing the project where there is one to sniff.
func wizardSiteAdd(g *Globals, ctx context.Context, opts site.AddOptions) (site.AddOptions, error) {
	p := newPrompter(g)
	st, err := g.Store(ctx)
	if err != nil {
		return opts, err
	}
	users, err := st.ListUsers(ctx)
	if err != nil {
		return opts, err
	}
	if len(users) == 0 {
		return opts, rlerr.Preconditionf("there are no users yet").
			WithHint("create one first: ratline user add <username>")
	}

	p.heading("Create a site")

	if opts.Domain == "" {
		domain, err := p.ask("Domain:", "", func(s string) error {
			normalised, err := validate.Domain(s)
			if err != nil {
				return err
			}
			if owner, used, err := st.NameInUse(ctx, normalised); err == nil && used {
				return rlerr.Preconditionf("%s is already served by %s", normalised, owner)
			}
			return nil
		})
		if err != nil {
			return opts, err
		}
		opts.Domain, _ = validate.Domain(domain)
	}

	if opts.Owner == "" {
		// The picker is populated from the live system, with site counts, so the
		// operator can see which tenant is which.
		options := make([]choice, 0, len(users))
		for _, u := range users {
			n, _ := st.CountSitesForUser(ctx, u.Name)
			options = append(options, choice{
				Value: u.Name, Label: u.Name,
				Note: fmt.Sprintf("%d site(s)", n),
			})
		}
		owner, err := p.pick("Which tenant owns it?", options, users[0].Name)
		if err != nil {
			return opts, err
		}
		opts.Owner = owner
	}

	// Sniff the application directory, if there is already something in it.
	appDir := filepath.Join(g.Cfg.SiteDir(opts.Owner, opts.Domain), "app")
	detected := sniffProject(appDir)
	if detected.runtime != "" && opts.Runtime == "" {
		p.note("Detected %s in %s (%s).", detected.runtime, appDir, detected.why)
	}

	if opts.Runtime == "" {
		def := detected.runtime
		if def == "" {
			def = "static"
		}
		rt, err := p.pick("Runtime?", []choice{
			{Value: "static", Label: "static", Note: "nginx serves files; nothing runs"},
			{Value: "node", Label: "node", Note: "a Node server under its own systemd unit"},
			{Value: "bun", Label: "bun", Note: "bun under systemd; runs TypeScript with no build step"},
			{Value: "python", Label: "python", Note: "Gunicorn in a per-site virtualenv"},
		}, def)
		if err != nil {
			return opts, err
		}
		opts.Runtime = rt
	}

	switch opts.Runtime {
	case "static":
		if opts.DocRoot == "" {
			root, err := p.ask("Document root, under the site directory:", "public", validate.Subdir)
			if err != nil {
				return opts, err
			}
			opts.DocRoot = root
		}
		spa, err := p.confirm("Is this a single-page application? (deep links served the index document)", opts.SPA)
		if err != nil {
			return opts, err
		}
		opts.SPA = spa

	case "node":
		if opts.Entry == "" && opts.StartCommand == "" {
			entry, err := p.ask("Entry point, relative to the application directory:",
				orDefault2(detected.entry, "server.js"), validate.NodeEntry)
			if err != nil {
				return opts, err
			}
			opts.Entry = entry
		}
		if opts.NodeVersion == "" {
			installed := listRuntimeVersions(filepath.Join(g.Cfg.Paths.RuntimesDir, "node"))
			options := make([]choice, 0, len(installed)+1)
			for _, v := range installed {
				options = append(options, choice{Value: v, Label: "Node " + v})
			}
			options = append(options, choice{Value: "", Label: "Whatever is on the system", Note: "not pinned"})
			if len(installed) == 0 {
				p.note("No managed Node is installed. 'ratline runtime install node 22' adds one.")
			}
			version, err := p.pick("Node version?", options, orDefault2(g.Cfg.Runtimes.NodeDefault, ""))
			if err != nil {
				return opts, err
			}
			opts.NodeVersion = version
		}
		if opts.PackageManager == "" && detected.packageManager != "" {
			opts.PackageManager = detected.packageManager
			p.note("Package manager: %s (from the lockfile).", detected.packageManager)
		}
		if opts.Listen == "" {
			listen, err := p.pick("How should nginx reach it?", []choice{
				{Value: "socket", Label: "A Unix socket", Note: "no port to manage; the default"},
				{Value: "port", Label: "A localhost port", Note: "allocated automatically"},
			}, "socket")
			if err != nil {
				return opts, err
			}
			opts.Listen = listen
		}

	case "bun":
		if opts.Entry == "" && opts.StartCommand == "" {
			entry, err := p.ask("Entry point, relative to the application directory:",
				orDefault2(detected.entry, "server.ts"), validate.BunEntry)
			if err != nil {
				return opts, err
			}
			opts.Entry = entry
		}
		if opts.BunVersion == "" {
			installed := listRuntimeVersions(filepath.Join(g.Cfg.Paths.RuntimesDir, "bun"))
			options := make([]choice, 0, len(installed)+1)
			for _, v := range installed {
				options = append(options, choice{Value: v, Label: "Bun " + v})
			}
			options = append(options, choice{Value: "", Label: "Whatever is on the system", Note: "not pinned"})
			if len(installed) == 0 {
				p.note("No managed Bun is installed. 'ratline runtime install bun 1.2' adds one.")
			}
			version, err := p.pick("Bun version?", options, orDefault2(g.Cfg.Runtimes.BunDefault, ""))
			if err != nil {
				return opts, err
			}
			opts.BunVersion = version
		}
		if opts.PackageManager == "" && detected.packageManager != "" && detected.packageManager != "bun" {
			// Only worth recording when it is *not* bun: a bun site installs with bun
			// unless told otherwise, so storing "bun" would be noise in the site row.
			opts.PackageManager = detected.packageManager
			p.note("Package manager: %s (from the lockfile).", detected.packageManager)
		}
		if opts.Listen == "" {
			listen, err := p.pick("How should nginx reach it?", []choice{
				{Value: "socket", Label: "A Unix socket", Note: "no port to manage; the default"},
				{Value: "port", Label: "A localhost port", Note: "allocated automatically"},
			}, "socket")
			if err != nil {
				return opts, err
			}
			opts.Listen = listen
		}
		p.note("bun runs directly under systemd, so 'site reload' on this site is a restart.")

	case "python":
		if opts.AppModule == "" {
			module, err := p.ask("Application module (module.path:callable):",
				orDefault2(detected.appModule, "app.main:app"), validate.AppModule)
			if err != nil {
				return opts, err
			}
			opts.AppModule = module
		}
		if opts.ASGI == nil {
			isASGI := runtime.DetectASGI(appDir, opts.AppModule)
			answer, err := p.pick("Which interface does the application use?", []choice{
				{Value: "asgi", Label: "ASGI", Note: "FastAPI, Starlette, Django with ASGI"},
				{Value: "wsgi", Label: "WSGI", Note: "Flask, Django, anything synchronous"},
			}, map[bool]string{true: "asgi", false: "wsgi"}[isASGI])
			if err != nil {
				return opts, err
			}
			v := answer == "asgi"
			opts.ASGI = &v
		}
		if opts.Workers == 0 {
			def := runtime.DefaultWorkers(g.Cfg.Defaults.WorkerCap)
			answer, err := p.ask("Worker processes:", strconv.Itoa(def), func(s string) error {
				n, err := strconv.Atoi(s)
				if err != nil || n < 1 || n > 64 {
					return rlerr.Usagef("give a number between 1 and 64")
				}
				return nil
			})
			if err != nil {
				return opts, err
			}
			opts.Workers, _ = strconv.Atoi(answer)
		}
		if detected.managePy != "" && opts.ManagePy == "" {
			useManage, err := p.confirm("Django detected. Enable --migrate and --collectstatic?", true)
			if err != nil {
				return opts, err
			}
			if useManage {
				opts.ManagePy = detected.managePy
			}
		}
	}

	if len(opts.Aliases) == 0 && !strings.HasPrefix(opts.Domain, "www.") {
		addWWW, err := p.confirm("Also serve www."+opts.Domain+"?", true)
		if err != nil {
			return opts, err
		}
		if addWWW {
			opts.Aliases = append(opts.Aliases, "www."+opts.Domain)
			redirect, err := p.pick("Which host should be canonical?", []choice{
				{Value: "apex", Label: opts.Domain, Note: "www redirects to it"},
				{Value: "www", Label: "www." + opts.Domain, Note: "the apex redirects to it"},
				{Value: "none", Label: "Both serve content", Note: "no redirect"},
			}, "apex")
			if err != nil {
				return opts, err
			}
			opts.WWWRedirect = redirect
		}
	}

	argv, fields := siteAddArgv(opts)
	action, err := p.summary("Ready to create", fields, argv)
	if err != nil {
		return opts, err
	}
	switch action {
	case actionRun:
		g.Argv = argv
		return opts, nil
	case actionCancel:
		return opts, ErrCancelled
	default:
		return opts, ErrCancelled
	}
}

// siteAddArgv renders the exact non-interactive invocation for a set of options.
//
// This function is the contract behind "the wizard prints a command that
// reproduces the result": both paths go through the same options struct, so there
// is only one place the two could diverge.
func siteAddArgv(opts site.AddOptions) ([]string, [][2]string) {
	argv := []string{"ratline", "site", "add", opts.Domain, "--user", opts.Owner, "--runtime", opts.Runtime}
	fields := [][2]string{
		{"domain", opts.Domain},
		{"user", opts.Owner},
		{"runtime", opts.Runtime},
	}
	add := func(flag, value, label string) {
		if value == "" {
			return
		}
		argv = append(argv, flag, value)
		fields = append(fields, [2]string{label, value})
	}
	for _, a := range opts.Aliases {
		argv = append(argv, "--alias", a)
	}
	if len(opts.Aliases) > 0 {
		fields = append(fields, [2]string{"aliases", strings.Join(opts.Aliases, ", ")})
	}
	switch opts.Runtime {
	case "static":
		add("--root", opts.DocRoot, "document root")
		if opts.SPA {
			argv = append(argv, "--spa")
			fields = append(fields, [2]string{"routing", "single-page application"})
		}
		add("--build-command", opts.BuildCommand, "build")
		add("--build-output", opts.BuildOutput, "build output")
	case "node":
		add("--entry", opts.Entry, "entry point")
		add("--start-command", opts.StartCommand, "start command")
		add("--node", opts.NodeVersion, "node version")
		add("--package-manager", opts.PackageManager, "package manager")
		if opts.Listen != "" && opts.Listen != "socket" {
			add("--listen", opts.Listen, "listen")
		}
	case "bun":
		add("--entry", opts.Entry, "entry point")
		add("--start-command", opts.StartCommand, "start command")
		add("--bun", opts.BunVersion, "bun version")
		add("--package-manager", opts.PackageManager, "package manager")
		if opts.Listen != "" && opts.Listen != "socket" {
			add("--listen", opts.Listen, "listen")
		}
	case "python":
		add("--app-module", opts.AppModule, "application module")
		add("--python", opts.PythonVersion, "python version")
		if opts.ASGI != nil {
			if *opts.ASGI {
				argv = append(argv, "--asgi")
				fields = append(fields, [2]string{"interface", "ASGI"})
			} else {
				argv = append(argv, "--wsgi")
				fields = append(fields, [2]string{"interface", "WSGI"})
			}
		}
		if opts.Workers > 0 {
			argv = append(argv, "--workers", strconv.Itoa(opts.Workers))
			fields = append(fields, [2]string{"workers", strconv.Itoa(opts.Workers)})
		}
		add("--manage-py", opts.ManagePy, "django manage.py")
		add("--static-url", opts.StaticURL, "static url")
		add("--static-dir", opts.StaticDir, "static dir")
	}
	if opts.WWWRedirect != "" && opts.WWWRedirect != "none" {
		add("--www-redirect", opts.WWWRedirect, "canonical host")
	}
	add("--repo", opts.Repo, "repository")
	add("--memory-max", opts.MemoryMax, "memory ceiling")
	return argv, fields
}

// project is what sniffing an application directory found.
type project struct {
	runtime        string
	why            string
	entry          string
	appModule      string
	packageManager string
	managePy       string
	buildCommand   string
	buildOutput    string
}

// sniffProject reads an existing application directory so the wizard can offer
// defaults that match what is actually there, and say why.
func sniffProject(appDir string) project {
	var p project
	if !system.IsDir(appDir) {
		return p
	}
	if system.Exists(filepath.Join(appDir, "package.json")) {
		p.runtime = "node"
		p.why = "package.json"
		p.packageManager = runtime.DetectPackageManager(appDir)
		p.entry = runtime.DetectEntry(appDir)
		// A bun lockfile is the project saying which engine it expects. It is a
		// stronger signal than the package.json alone, and getting it wrong sends a
		// TypeScript entry point to an interpreter that cannot run it.
		if runtime.UsesBunLockfile(appDir) {
			p.runtime = "bun"
			p.why = "package.json with a bun lockfile"
			p.entry = runtime.DetectBunEntry(appDir)
		}
		// A build output directory with no server file means the project is a
		// static bundle rather than a server.
		if p.entry == "" {
			for _, dir := range []string{"dist", "build", "out", "public"} {
				if system.IsDir(filepath.Join(appDir, dir)) {
					p.runtime = "static"
					p.why = "package.json with a " + dir + " directory and no server entry point"
					p.buildOutput = dir
					p.buildCommand = "npm run build"
					if runtime.UsesBunLockfile(appDir) {
						p.buildCommand = "bun run build"
					}
					break
				}
			}
		}
		return p
	}
	for _, marker := range []string{"requirements.txt", "pyproject.toml", "manage.py", "setup.py"} {
		if system.Exists(filepath.Join(appDir, marker)) {
			p.runtime = "python"
			p.why = marker
			if marker == "manage.py" {
				p.managePy = "manage.py"
				// Django's WSGI callable is conventionally <project>.wsgi:application.
				if name := djangoProjectName(appDir); name != "" {
					p.appModule = name + ".wsgi:application"
				}
			}
			return p
		}
	}
	if system.Exists(filepath.Join(appDir, "index.html")) {
		p.runtime = "static"
		p.why = "index.html"
		return p
	}
	return p
}

// djangoProjectName finds the package holding settings.py.
func djangoProjectName(appDir string) string {
	entries, err := os.ReadDir(appDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if system.Exists(filepath.Join(appDir, e.Name(), "settings.py")) {
			return e.Name()
		}
	}
	return ""
}

func defaultKeyPath() string {
	// Under sudo, HOME is often still the operator's, which is what they mean by
	// "my key".
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, name := range []string{"id_ed25519.pub", "id_rsa.pub"} {
		path := filepath.Join(home, ".ssh", name)
		if system.Exists(path) {
			return path
		}
	}
	return ""
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func orDefault2(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

var _ = state.ScopeGlobal
