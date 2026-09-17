package site

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// The manifest is the only record of a site that travels with the site's own directory,
// which makes it the only thing a restore from an archive has to go on. Writer and
// reader therefore have to agree, and the way to know they do is to round-trip a fully
// populated site rather than to check a handful of fields.

func writeAndRead(t *testing.T, site *state.Site) *state.Site {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Paths.HomeBase = root
	mgr := &Manager{Cfg: cfg, Log: log.Discard()}

	dir := filepath.Join(cfg.SiteDir(site.Owner, site.Domain), ".ratline")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := mgr.writeManifest(site, &system.Identity{UID: os.Getuid(), GID: os.Getgid()}); err != nil {
		t.Fatalf("writeManifest = %v", err)
	}
	got, err := mgr.ReadSiteManifest(site.Owner, site.Domain)
	if err != nil {
		t.Fatalf("ReadSiteManifest = %v", err)
	}
	return got
}

// A restore renames a fresh tree into a directory the tenant owns and then writes the
// manifest back as root — with an nginx reload and a systemd cycle in between, a window wide
// enough for the tenant to replace the site directory with a symlink to somewhere else.
// writeManifest walks the path from the root-owned home boundary down and refuses, rather
// than following the link and landing a root-owned file wherever it points.
func TestWriteManifestRefusesASymlinkedSiteTree(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Paths.HomeBase = root
	mgr := &Manager{Cfg: cfg, Log: log.Discard()}

	site := &state.Site{Domain: "app.example.com", Owner: "acme", Runtime: "static"}
	id := &system.Identity{UID: os.Getuid(), GID: os.Getgid()}

	// The home is real; the site directory is a symlink whose target already has the
	// .ratline directory the write expects, so only the guard stands between the write and
	// a root-owned file appearing outside the tenant's tree.
	if err := os.MkdirAll(cfg.HomeDir(site.Owner), 0o750); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(filepath.Join(elsewhere, ".ratline"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, cfg.SiteDir(site.Owner, site.Domain)); err != nil {
		t.Fatal(err)
	}

	if err := mgr.writeManifest(site, id); err == nil {
		t.Fatal("writeManifest wrote through a symlinked site directory")
	}
	if _, err := os.Stat(filepath.Join(elsewhere, ".ratline", "site.yaml")); err == nil {
		t.Error("a manifest was written through the symlink as root")
	}

	// The ordinary case — a real site tree — still writes and reads back.
	real := &state.Site{Domain: "shop.example.com", Owner: "acme", Runtime: "static"}
	if err := os.MkdirAll(filepath.Join(cfg.SiteDir(real.Owner, real.Domain), ".ratline"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := mgr.writeManifest(real, id); err != nil {
		t.Errorf("writeManifest refused a real site tree: %v", err)
	}
}

func TestAFullyPopulatedSiteSurvivesTheRoundTrip(t *testing.T) {
	want := &state.Site{
		Domain: "app.example.com", Owner: "acme", Runtime: "python",
		Slug: "acme-app_example_com", Enabled: true,
		Aliases:       []string{"www.app.example.com", "api.example.com"},
		DocRoot:       "public",
		AppModule:     "app.main:app",
		PythonVersion: "3.12",
		Listen:        "socket",
		AppServer:     "gunicorn",
		BuildCommand:  "make assets",
		BuildOutput:   "dist",
		PublicDir:     "static",
		Requirements:  "requirements.txt",
		StaticURL:     "/assets/",
		StaticDir:     "app/static",
		Repo:          "https://github.com/acme/app",
		Branch:        "main",
		MemoryMax:     "512M",
		CPUQuota:      "50%",
		WWWRedirect:   "apex",
		Workers:       4,
		Instances:     1,
		Port:          0,
		ASGI:          true,
		HSTS:          true,
	}
	got := writeAndRead(t, want)

	for _, f := range []struct {
		name string
		want any
		got  any
	}{
		{"domain", want.Domain, got.Domain},
		{"owner", want.Owner, got.Owner},
		{"runtime", want.Runtime, got.Runtime},
		{"slug", want.Slug, got.Slug},
		{"enabled", want.Enabled, got.Enabled},
		{"doc_root", want.DocRoot, got.DocRoot},
		{"app_module", want.AppModule, got.AppModule},
		{"python_version", want.PythonVersion, got.PythonVersion},
		{"listen", want.Listen, got.Listen},
		{"app_server", want.AppServer, got.AppServer},
		{"build_command", want.BuildCommand, got.BuildCommand},
		{"build_output", want.BuildOutput, got.BuildOutput},
		{"public_dir", want.PublicDir, got.PublicDir},
		{"requirements", want.Requirements, got.Requirements},
		{"static_url", want.StaticURL, got.StaticURL},
		{"static_dir", want.StaticDir, got.StaticDir},
		{"repo", want.Repo, got.Repo},
		{"branch", want.Branch, got.Branch},
		{"memory_max", want.MemoryMax, got.MemoryMax},
		{"cpu_quota", want.CPUQuota, got.CPUQuota},
		{"www_redirect", want.WWWRedirect, got.WWWRedirect},
		{"workers", want.Workers, got.Workers},
		{"asgi", want.ASGI, got.ASGI},
		{"hsts", want.HSTS, got.HSTS},
	} {
		if f.want != f.got {
			t.Errorf("%s: wrote %v, read back %v", f.name, f.want, f.got)
		}
	}
	if strings.Join(got.Aliases, ",") != strings.Join(want.Aliases, ",") {
		t.Errorf("aliases: wrote %v, read back %v", want.Aliases, got.Aliases)
	}
}

func TestANodeSiteRoundTripsItsOwnFields(t *testing.T) {
	// The node branch sets different columns from the python one, so a reader that
	// happened to cover python would still lose a node site.
	want := &state.Site{
		Domain: "web.example.com", Owner: "acme", Runtime: "node",
		Enabled: true, Entry: "server.js", NodeVersion: "22",
		Listen: "port", Port: 41000, ProcessManager: "pm2", Instances: 4,
		StartCommand: "node server.js", SPA: false,
	}
	got := writeAndRead(t, want)
	for _, f := range [][3]any{
		{"entry", want.Entry, got.Entry},
		{"node_version", want.NodeVersion, got.NodeVersion},
		{"listen", want.Listen, got.Listen},
		{"port", want.Port, got.Port},
		{"start_command", want.StartCommand, got.StartCommand},
		{"instances", want.Instances, got.Instances},
	} {
		if f[1] != f[2] {
			t.Errorf("%v: wrote %v, read back %v", f[0], f[1], f[2])
		}
	}
}

func TestABunSiteRoundTripsItsOwnFields(t *testing.T) {
	// bun_version is its own column, so a manifest written by a bun site and read back
	// through the node branch's fields would restore a site pinned to nothing — which
	// then silently follows whatever bun_default happens to be on the new server.
	want := &state.Site{
		Domain: "edge.example.com", Owner: "acme", Runtime: "bun",
		Enabled: true, Entry: "src/index.tsx", BunVersion: "1.2",
		Listen: "socket", BuildCommand: "bun run build",
	}
	got := writeAndRead(t, want)
	for _, f := range [][3]any{
		{"runtime", want.Runtime, got.Runtime},
		{"entry", want.Entry, got.Entry},
		{"bun_version", want.BunVersion, got.BunVersion},
		{"listen", want.Listen, got.Listen},
		{"build_command", want.BuildCommand, got.BuildCommand},
	} {
		if f[1] != f[2] {
			t.Errorf("%v: wrote %v, read back %v", f[0], f[1], f[2])
		}
	}
	// A .tsx entry point survives the round trip only because validateSiteRow judges it
	// against the runtime the manifest names. Read as a node site it would be refused.
	if got.NodeVersion != "" {
		t.Errorf("node_version = %q on a bun site, want empty", got.NodeVersion)
	}
}

func TestAStaticSPARoundTrips(t *testing.T) {
	want := &state.Site{
		Domain: "docs.example.com", Owner: "acme", Runtime: "static",
		Enabled: true, DocRoot: "public", IndexFile: "index.html", SPA: true,
		ClientMaxBodySize: "20M",
	}
	got := writeAndRead(t, want)
	if !got.SPA {
		t.Error("spa was lost, so the fallback location would not be rendered")
	}
	if got.DocRoot != want.DocRoot {
		t.Errorf("doc_root = %q, want %q", got.DocRoot, want.DocRoot)
	}
}

func TestAManifestFromOutsideIsValidatedLikeInput(t *testing.T) {
	// The manifest arrives inside an archive, which may have been edited, moved between
	// servers or restored from somewhere untrusted. The domain reaches a systemd unit
	// name, an nginx server_name and a filesystem path, so it gets the same treatment
	// as a typed argument.
	dir := t.TempDir()
	write := func(body string) string {
		t.Helper()
		p := filepath.Join(dir, "site.yaml")
		if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, tc := range []struct {
		name, body, wantErr string
	}{
		{
			"a traversing domain",
			"domain: ../../etc/nginx\nowner: acme\nruntime: static\n",
			"domain",
		},
		{
			"a root owner",
			"domain: app.example.com\nowner: root\nruntime: static\n",
			"owner",
		},
		{
			"an owner with a slash",
			"domain: app.example.com\nowner: ../root\nruntime: static\n",
			"owner",
		},
		{
			"an unknown runtime",
			"domain: app.example.com\nowner: acme\nruntime: php\n",
			"runtime",
		},
		{
			"no runtime at all",
			"domain: app.example.com\nowner: acme\n",
			"missing",
		},
		{
			"an alias that is not a domain",
			"domain: app.example.com\nowner: acme\nruntime: static\naliases: [not a domain]\n",
			"alias",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadManifest(write(tc.body))
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("the refusal should mention %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestTheSlugIsRecomputedRatherThanTrusted(t *testing.T) {
	// The slug names the systemd unit. A manifest claiming someone else's slug would
	// otherwise produce a site whose unit belongs to a different site — which is a
	// route to controlling another tenant's service.
	dir := t.TempDir()
	p := filepath.Join(dir, "site.yaml")
	body := "domain: app.example.com\nowner: acme\nruntime: static\nslug: victim-other_example_com\n"
	if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManifest(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Slug == "victim-other_example_com" {
		t.Error("the manifest's slug was trusted; it must be recomputed from owner and domain")
	}
	if got.Slug == "" {
		t.Error("no slug was computed, so the unit would have no name")
	}
}

func TestAnUnknownKeyIsCarriedOverRatherThanRefused(t *testing.T) {
	// A manifest written by a newer ratline has to restore under an older one. Refusing
	// over a field this version has no column for would make an archive unrestorable by
	// the very version someone rolled back to in order to restore it.
	dir := t.TempDir()
	p := filepath.Join(dir, "site.yaml")
	body := "domain: app.example.com\nowner: acme\nruntime: static\n" +
		"some_future_field: \"whatever it means\"\n"
	if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManifest(p)
	if err != nil {
		t.Fatalf("an unknown key was refused: %v", err)
	}
	if got.Domain != "app.example.com" {
		t.Errorf("domain = %q", got.Domain)
	}
}

func TestAMissingManifestSaysWhatToDo(t *testing.T) {
	cfg := config.Default()
	cfg.Paths.HomeBase = t.TempDir()
	mgr := &Manager{Cfg: cfg, Log: log.Discard()}
	_, err := mgr.ReadSiteManifest("acme", "absent.example.com")
	if err == nil {
		t.Fatal("a missing manifest should be an error")
	}
	if !strings.Contains(err.Error(), "no manifest") {
		t.Errorf("unexpected error: %v", err)
	}
}

// Every field that becomes an nginx directive or a systemd line is untrusted in a manifest,
// not just the domain and the owner. Before this, these values were parsed and stored and
// rendered as root; `nginx -t` and `systemd-analyze verify` accept them because each one is
// syntactically valid — which is the whole trick.
func TestAManifestCannotInjectAConfigDirective(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		t.Helper()
		p := filepath.Join(dir, "site.yaml")
		if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := "domain: app.example.com\nowner: acme\nruntime: static\n"
	for _, tc := range []struct{ name, extra string }{
		// nginx: index, root, static location, body size.
		{"an index file that ends the directive", "index_file: \"index.html; root /etc\"\n"},
		{"a doc root with a space", "doc_root: \"public alias /etc\"\n"},
		{"a static url that opens a location", "static_url: \"/x { } location / { root /etc; }\"\n"},
		{"a body size that is not a size", "client_max_body_size: \"1m; add_header X 1\"\n"},
		// systemd: the command and the limits.
		{"a start command with a pipe", "start_command: \"a | b\"\n"},
		{"a memory ceiling that is not a size", "memory_max: \"1G\\nExecStartPre=/bin/rm\"\n"},
		{"a cpu quota that is not one", "cpu_quota: \"100%; evil\"\n"},
		// git argv.
		{"a repo that starts like a flag", "repo: \"--upload-pack=/bin/sh\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ReadManifest(write(base + tc.extra)); err == nil {
				t.Errorf("%s was accepted; it reaches a root-owned config file", tc.name)
			}
		})
	}

	// The proxy settings a streaming site overrides, on a node base rather than the
	// static one above. A static site is refused these outright, so running them
	// against `base` would have proved only that the static check fires first — the
	// values themselves would never have been looked at.
	nodeBase := "domain: app.example.com\nowner: acme\nruntime: node\nentry: server.js\n"
	for _, tc := range []struct{ name, extra string }{
		{"a read timeout that ends the directive", "proxy_read_timeout: \"60s; add_header X 1\"\n"},
		{"a read timeout with a newline", "proxy_read_timeout: \"60s\\nproxy_pass http://evil\"\n"},
		{"a read timeout that is not a duration", "proxy_read_timeout: \"forever\"\n"},
		{"a buffering value that ends the directive", "proxy_buffering: \"off; add_header X 1\"\n"},
		{"a buffering value that is neither on nor off", "proxy_buffering: \"maybe\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ReadManifest(write(nodeBase + tc.extra)); err == nil {
				t.Errorf("%s was accepted; it reaches a root-owned config file", tc.name)
			}
		})
	}
	// The same manifest without the bad value parses, so the cases above are being
	// refused for the value and not for the base.
	if _, err := ReadManifest(write(nodeBase + "proxy_buffering: off\nproxy_read_timeout: 1h\n")); err != nil {
		t.Errorf("a legitimate streaming manifest was rejected: %v", err)
	}

	// And a legitimate, fully-specified manifest still parses — the gate must not reject
	// the sites `site add` produces.
	ok := "domain: shop.example.com\nowner: acme\nruntime: python\n" +
		"app_module: app.main:app\nstatic_url: /static/\nstatic_dir: static\n" +
		"index_file: index.html\nmemory_max: 512M\ncpu_quota: 100%\nbranch: main\n"
	if _, err := ReadManifest(write(ok)); err != nil {
		t.Errorf("a legitimate manifest was rejected: %v", err)
	}
}

// Every key the reader understands is a key the writer emits.
//
// The two lists are maintained by hand in different files, and they had drifted:
// index_file, process_manager and client_max_body_size were all parsed and never
// written, so a site rebuilt from its own manifest came back on the default
// supervisor, serving the wrong index document, with an upload ceiling an operator
// had raised silently back at the 20M default. Asserting the two sets against each
// other catches the next omission without anyone having to think of the field.
func TestTheManifestWriterEmitsEveryKeyTheReaderUnderstands(t *testing.T) {
	src, err := os.ReadFile("manifest.go")
	if err != nil {
		t.Fatal(err)
	}
	// The key list, read from the reader's own switch rather than restated here —
	// a copy would drift in exactly the way this test exists to catch.
	understood := map[string]bool{}
	for _, m := range regexp.MustCompile(`case "([a-z_]+)":`).FindAllStringSubmatch(string(src), -1) {
		understood[m[1]] = true
	}
	// The runtime switch in the same file is not a manifest key.
	for _, notAKey := range []string{"static", "node", "bun", "python"} {
		delete(understood, notAKey)
	}
	if len(understood) < 20 {
		t.Fatalf("only found %d manifest keys, so the scan is broken, not the writer", len(understood))
	}

	// Every field populated, so nothing is omitted merely for being zero.
	site := &state.Site{
		Domain: "app.example.com", Owner: "acme", Runtime: "node",
		Slug: "acme-app_example_com", Enabled: true,
		Aliases: []string{"www.app.example.com"}, DocRoot: "public", IndexFile: "main.html",
		Entry: "server.js", NodeVersion: "22", BunVersion: "1.2", PythonVersion: "3.12",
		AppModule: "app.main:app", Listen: "socket", AppServer: "gunicorn",
		ProcessManager: "pm2", StartCommand: "node server.js", BuildCommand: "npm run build",
		BuildOutput: "dist", PublicDir: "static", Requirements: "requirements.txt",
		StaticURL: "/assets/", StaticDir: "app/static", Repo: "https://github.com/acme/app",
		Branch: "main", MemoryMax: "512M", CPUQuota: "50%", ClientMaxBodySize: "100M",
		WWWRedirect: "apex", ProxyBuffering: "off", ProxyReadTimeout: "1h",
		Workers: 4, Instances: 2, Port: 3000, SPA: true, ASGI: true, HSTS: true,
	}

	root := t.TempDir()
	cfg := config.Default()
	cfg.Paths.HomeBase = root
	mgr := &Manager{Cfg: cfg, Log: log.Discard()}
	dir := filepath.Join(cfg.SiteDir(site.Owner, site.Domain), ".ratline")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := mgr.writeManifest(site, &system.Identity{UID: os.Getuid(), GID: os.Getgid()}); err != nil {
		t.Fatalf("writeManifest = %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "site.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	written := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if k, _, ok := strings.Cut(line, ":"); ok {
			written[strings.TrimSpace(k)] = true
		}
	}
	for key := range understood {
		if !written[key] {
			t.Errorf("the manifest reader understands %q but the writer never emits it, "+
				"so a reconcile silently drops it", key)
		}
	}
}
