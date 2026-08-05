package site

import (
	"os"
	"path/filepath"
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
