package site

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// ManifestName is where a site's rendered manifest lives, relative to the site
// directory.
const ManifestName = ".ratline/site.yaml"

// ReadManifest parses a site's manifest back into a state row.
//
// writeManifest has always claimed the manifest exists "so a site survives the loss of
// the state database" — and until this, nothing read it, so it did not. The file is the
// only record of a site that travels with the site's own directory, which makes it the
// only thing a restore from an archive has to go on: the archive contains the site, not
// the database.
//
// Deliberately hand-parsed rather than run through a YAML library. The writer emits a
// flat map of scalars plus one inline list, the format is ours on both ends, and a
// dependency that could accept *more* than the writer produces would be a way for a
// hand-edited manifest to inject a field nobody meant to expose.
func ReadManifest(path string) (*state.Site, error) {
	data, err := system.ReadFileLimit(path, 1<<20)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodePrecondition, "reading the site manifest %s", path)
	}
	site := &state.Site{}
	var seen int

	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, raw, ok := strings.Cut(line, ":")
		if !ok {
			return nil, rlerr.Preconditionf("%s:%d is not a key: value pair: %q", path, n+1, line)
		}
		key = strings.TrimSpace(key)
		value := unquote(strings.TrimSpace(raw))

		switch key {
		case "domain":
			site.Domain, seen = value, seen+1
		case "owner":
			site.Owner, seen = value, seen+1
		case "runtime":
			site.Runtime, seen = value, seen+1
		case "slug":
			site.Slug = value
		case "enabled":
			site.Enabled = value == "true"
		case "aliases":
			site.Aliases = parseInlineList(value)
		case "doc_root":
			site.DocRoot = value
		case "index_file":
			site.IndexFile = value
		case "entry":
			site.Entry = value
		case "app_module":
			site.AppModule = value
		case "node_version":
			site.NodeVersion = value
		case "bun_version":
			site.BunVersion = value
		case "python_version":
			site.PythonVersion = value
		case "listen":
			site.Listen = value
		case "app_server":
			site.AppServer = value
		case "process_manager":
			site.ProcessManager = value
		case "start_command":
			site.StartCommand = value
		case "build_command":
			site.BuildCommand = value
		case "build_output":
			site.BuildOutput = value
		case "public_dir":
			site.PublicDir = value
		case "requirements":
			site.Requirements = value
		case "static_url":
			site.StaticURL = value
		case "static_dir":
			site.StaticDir = value
		case "repo":
			site.Repo = value
		case "branch":
			site.Branch = value
		case "memory_max":
			site.MemoryMax = value
		case "cpu_quota":
			site.CPUQuota = value
		case "www_redirect":
			site.WWWRedirect = value
		case "client_max_body_size":
			site.ClientMaxBodySize = value
		case "workers":
			site.Workers = atoiOrZero(value)
		case "instances":
			site.Instances = atoiOrZero(value)
		case "port":
			site.Port = atoiOrZero(value)
		case "spa":
			site.SPA = value == "true"
		case "asgi":
			site.ASGI = value == "true"
		case "hsts":
			site.HSTS = value == "true"
		default:
			// Forward compatible on purpose: a manifest written by a newer ratline must
			// restore under an older one rather than refusing over a field it has no
			// column for. Anything unrecognised is simply not carried across, and the
			// three required keys below are what makes a manifest usable at all.
			continue
		}
	}

	if seen < 3 {
		return nil, rlerr.Preconditionf("%s is missing domain, owner or runtime", path).
			WithHint("a manifest without those three cannot identify a site; " +
				"recreate it with 'ratline site add'")
	}
	// The domain and owner reach a systemd unit name, an nginx server_name and a
	// filesystem path, and this file has been outside ratline's control — inside an
	// archive that may have been edited, moved between servers, or restored from
	// somewhere untrusted. Validate exactly as if it had been typed.
	// Domain returns the normalised form, which is what should be stored — a manifest
	// carrying a trailing dot or mixed case must not produce a different server_name
	// from the one `site add` would have written.
	normalised, err := validate.Domain(site.Domain)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodePrecondition, "the manifest's domain is not usable")
	}
	site.Domain = normalised
	if err := validate.Username(site.Owner); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodePrecondition, "the manifest's owner is not usable")
	}
	// Syntax is not enough: "root" is a well-formed username, and a site owned by it
	// would render a unit with User=root and chown a tenant's files to uid 0. The
	// reserved list is the same one `user add` refuses from.
	if err := validate.UsernameNotReserved(site.Owner, nil); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodePrecondition,
			"the manifest's owner cannot own a site")
	}
	for i, a := range site.Aliases {
		alias, err := validate.Domain(a)
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodePrecondition, "the manifest alias %q is not usable", a)
		}
		site.Aliases[i] = alias
	}
	switch site.Runtime {
	case "static", "node", "bun", "python":
	default:
		return nil, rlerr.Preconditionf("the manifest names an unknown runtime %q", site.Runtime)
	}
	// Recomputed rather than trusted: the slug names the systemd unit, and one that
	// disagreed with the domain and owner would produce a site whose unit belongs to a
	// different site.
	site.Slug = validate.Slug(site.Owner, site.Domain)
	if site.Instances < 1 {
		site.Instances = 1
	}
	// Every other field that reaches a generated config, held to the same rules as if it
	// had been typed. The four fields above were validated on the argument that they reach
	// a unit name and a server_name; so do the document root, the index file, the static
	// location, the commands and the limits, and this file is exactly as untrusted.
	if err := validateSiteRow(site); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodePrecondition, "the manifest describes a site ratline would not create")
	}
	return site, nil
}

// ManifestPath is where a site's manifest lives on disk.
func (m *Manager) ManifestPath(owner, domain string) string {
	return filepath.Join(m.Cfg.SiteDir(owner, domain), ManifestName)
}

// ReadSiteManifest reads the manifest of a site already on disk.
func (m *Manager) ReadSiteManifest(owner, domain string) (*state.Site, error) {
	path := m.ManifestPath(owner, domain)
	if !system.Exists(path) {
		return nil, rlerr.Preconditionf("%s has no manifest at %s", domain, path).
			WithHint("it predates manifests, or was created outside ratline; " +
				"'ratline reconcile --fix' writes one")
	}
	return ReadManifest(path)
}

// unquote removes the quoting writeManifest applies to string values with %q.
func unquote(s string) string {
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		if out, err := strconv.Unquote(s); err == nil {
			return out
		}
		return s[1 : len(s)-1]
	}
	return s
}

// parseInlineList reads the `[a, b]` form writeManifest uses for aliases.
func parseInlineList(s string) []string {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "["), "]"))
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := unquote(strings.TrimSpace(part)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// ManifestExistsIn reports whether an extracted tree carries a site manifest, and
// where. Used by restore to tell a site archive from a home archive.
func ManifestExistsIn(dir string) (string, bool) {
	path := filepath.Join(dir, ManifestName)
	if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
		return path, true
	}
	return "", false
}
