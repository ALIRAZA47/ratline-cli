package site

import (
	"unicode"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// validateSiteRow is the one gate a site row must pass before anything renders it.
//
// Every field here is written into an nginx vhost or a systemd unit, and both are checked
// with the real tool (`nginx -t`, `systemd-analyze verify`) before they take effect — but
// those tools reject configuration that does not *parse*, not configuration that parses and
// says something the operator never asked for. A value containing a newline, or a space, or
// a semicolon, is a syntactically valid way to add a directive to a root-owned file. So the
// content has to be judged here, where the string is still just a string.
//
// It exists as one function called from two places because there are two ways a site row
// comes into being: typed at `site add`, or read from a manifest by `restore`. The manifest
// path is the one that matters — the file sits in the tenant's own directory and may have
// been edited, moved between servers, or restored from somewhere untrusted — and it used to
// validate the domain and the owner and nothing else, on the reasoning that those were the
// fields that reached a config. They are not the only ones.
func validateSiteRow(s *state.Site) error {
	// The blanket check first: no field that becomes a directive may carry a control
	// character. This is what stops a newline turning one directive into two, and no
	// legitimate value for any of these has ever contained one. Named fields rather than
	// reflection, so a field added later is a deliberate decision to include or omit.
	for name, v := range map[string]string{
		"index":           s.IndexFile,
		"root":            s.DocRoot,
		"public":          s.PublicDir,
		"static-url":      s.StaticURL,
		"static-dir":      s.StaticDir,
		"build-output":    s.BuildOutput,
		"entry":           s.Entry,
		"app-module":      s.AppModule,
		"start-command":   s.StartCommand,
		"install-command": s.InstallCommand,
		"build-command":   s.BuildCommand,
		"requirements":    s.Requirements,
		"manage-py":       s.ManagePy,
		"memory-max":      s.MemoryMax,
		"cpu-quota":       s.CPUQuota,
		"body-size":       s.ClientMaxBodySize,
		"repo":            s.Repo,
		"branch":          s.Branch,
		"www-redirect":    s.WWWRedirect,
		"node-version":    s.NodeVersion,
		"bun-version":     s.BunVersion,
		"python-version":  s.PythonVersion,
		"package-manager": s.PackageManager,
		"app-server":      s.AppServer,
		"listen":          s.Listen,
		"process-manager": s.ProcessManager,
	} {
		for _, r := range v {
			if r != '\t' && unicode.IsControl(r) {
				return rlerr.Usagef("the %s field contains a control character", name).
					WithHint("it becomes part of a generated nginx or systemd file, where a " +
						"newline would add a directive nobody asked for")
			}
		}
	}

	// Then the specific shape of each field, matching what `site add` enforces, so a
	// manifest cannot describe a site that `site add` would have refused.
	if s.IndexFile != "" {
		if err := validate.IndexFile(s.IndexFile); err != nil {
			return err
		}
	}
	if s.StaticURL != "" {
		if err := validate.URLPath(s.StaticURL); err != nil {
			return err
		}
	}
	for _, p := range []string{s.DocRoot, s.StaticDir, s.PublicDir, s.BuildOutput} {
		if p != "" {
			if err := validate.Subdir(p); err != nil {
				return err
			}
		}
	}
	if s.Entry != "" {
		// Judged against the engine that will execute it. Bun accepts .jsx and .tsx
		// where node does not, so validating every entry point with the wider set
		// would let a node site be created with a file node cannot parse — and
		// validating every one with the narrower set would refuse a perfectly ordinary
		// bun site.
		check := validate.NodeEntry
		if s.Runtime == "bun" {
			check = validate.BunEntry
		}
		if err := check(s.Entry); err != nil {
			return err
		}
	}
	if s.AppModule != "" {
		if err := validate.AppModule(s.AppModule); err != nil {
			return err
		}
	}
	for _, size := range []string{s.MemoryMax, s.ClientMaxBodySize} {
		if size != "" {
			if _, err := validate.Size(size); err != nil {
				return err
			}
		}
	}
	if s.CPUQuota != "" {
		if err := validate.CPUQuota(s.CPUQuota); err != nil {
			return err
		}
	}
	if s.Repo != "" {
		if err := validate.GitURL(s.Repo); err != nil {
			return err
		}
	}
	if s.Branch != "" {
		if err := validate.GitRef(s.Branch); err != nil {
			return err
		}
	}
	for _, cmd := range []string{s.StartCommand, s.InstallCommand, s.BuildCommand} {
		if cmd != "" {
			if _, err := system.ParseCommand(cmd); err != nil {
				return err
			}
		}
	}

	// Enumerated fields, rejected against their allowed sets rather than trusted. A bad
	// value would usually make the generated unit invalid, but "usually" is not the
	// standard for a file written as root.
	if err := oneOf("listen", s.Listen, "", "socket", "port"); err != nil {
		return err
	}
	if err := oneOf("daemon", s.ProcessManager, "", "pm2", "direct"); err != nil {
		return err
	}
	if err := oneOf("server", s.AppServer, "", "gunicorn", "uvicorn"); err != nil {
		return err
	}
	if err := oneOf("www-redirect", s.WWWRedirect, "", "none", "apex", "www"); err != nil {
		return err
	}
	if s.PackageManager != "" {
		if err := validate.PackageManager(s.PackageManager); err != nil {
			return err
		}
	}
	if s.NodeVersion != "" {
		if err := validate.NodeVersion(s.NodeVersion); err != nil {
			return err
		}
	}
	if s.BunVersion != "" {
		if err := validate.BunVersion(s.BunVersion); err != nil {
			return err
		}
	}
	if s.PythonVersion != "" {
		if err := validate.PythonVersion(s.PythonVersion); err != nil {
			return err
		}
	}
	return nil
}

func oneOf(field, value string, allowed ...string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return rlerr.Usagef("the %s field is %q, which is not one of the allowed values", field, value)
}
