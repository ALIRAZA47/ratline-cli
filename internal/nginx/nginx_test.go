package nginx

import (
	"strings"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

func testManager() *Manager {
	return &Manager{Cfg: config.Default(), Log: log.Discard()}
}

func render(t *testing.T, site *state.Site, cert *state.Certificate) string {
	t.Helper()
	body, err := testManager().RenderVhost(site, cert)
	if err != nil {
		t.Fatalf("RenderVhost = %v", err)
	}
	return string(body)
}

func staticSite() *state.Site {
	return &state.Site{
		Domain: "example.com", Owner: "alice", Runtime: "static", Slug: "alice-example_com",
		Enabled: true, DocRoot: "public", IndexFile: "index.html",
	}
}

func pythonSite() *state.Site {
	return &state.Site{
		Domain: "api.example.com", Owner: "alice", Runtime: "python", Slug: "alice-api_example_com",
		Enabled: true, AppModule: "app.main:app", ASGI: true, Workers: 3, Listen: "socket",
		Instances: 1, AppServer: "gunicorn",
	}
}

func trustedCert() *state.Certificate {
	return &state.Certificate{
		Name: "example.com", Source: state.CertSourceLetsEncrypt,
		CertPath:  "/etc/letsencrypt/live/example.com/fullchain.pem",
		KeyPath:   "/etc/letsencrypt/live/example.com/privkey.pem",
		ChainPath: "/etc/letsencrypt/live/example.com/chain.pem",
		NotAfter:  time.Now().Add(60 * 24 * time.Hour),
		SANs:      []string{"example.com", "www.example.com"},
	}
}

// Properties every rendered vhost must have, whatever the runtime.
func TestVhostInvariants(t *testing.T) {
	cases := map[string]struct {
		site *state.Site
		cert *state.Certificate
	}{
		"static http":  {staticSite(), nil},
		"static https": {staticSite(), trustedCert()},
		"python http":  {pythonSite(), nil},
		"python https": {pythonSite(), trustedCert()},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out := render(t, tc.site, tc.cert)

			// Ownership marker, so nothing overwrites a file it did not create.
			if !strings.Contains(out, "# managed-by: ratline") {
				t.Error("no managed-by header")
			}
			// The ACME location goes in every vhost, always: renewal must not
			// depend on the application being up or the site being enabled.
			if !strings.Contains(out, "acme-challenge.conf") {
				t.Error("the ACME challenge include is missing")
			}
			// Secrets and metadata must never be servable.
			if !strings.Contains(out, "deny-hidden.conf") {
				t.Error("the dotfile deny include is missing")
			}
			// The operator's own include survives regeneration.
			if !strings.Contains(out, "/etc/nginx/ratline/custom/") {
				t.Error("the custom include is missing")
			}
			if !strings.Contains(out, "client_max_body_size 20M") {
				t.Error("client_max_body_size is missing, which is the usual cause of a mystery 413")
			}
			if !strings.Contains(out, "access_log") || !strings.Contains(out, "error_log") {
				t.Error("per-site logs are missing")
			}
		})
	}
}

func TestVhostHTTPRedirectComesAfterTheChallenge(t *testing.T) {
	out := render(t, staticSite(), trustedCert())
	if !strings.Contains(out, "return 301 https://$host$request_uri") {
		t.Fatal("no HTTP to HTTPS redirect")
	}
	// The challenge include must appear before the redirect in the port-80
	// block, or renewal 301s to HTTPS and fails.
	port80 := out[strings.Index(out, "listen 80;"):]
	acme := strings.Index(port80, "acme-challenge.conf")
	redirect := strings.Index(port80, "return 301")
	if acme < 0 || redirect < 0 || acme > redirect {
		t.Error("the ACME challenge does not precede the HTTPS redirect")
	}
}

func TestStaticVhost(t *testing.T) {
	out := render(t, staticSite(), nil)
	if !strings.Contains(out, "root /home/alice/example.com/public;") {
		t.Errorf("the document root is wrong:\n%s", out)
	}
	if !strings.Contains(out, "try_files $uri $uri/ =404;") {
		t.Error("a non-SPA static site should 404 on an unmatched path")
	}
	// A static site has no application, so it must have no proxy block at all.
	if strings.Contains(out, "proxy_pass") {
		t.Error("a static vhost contains a proxy_pass")
	}
	if !strings.Contains(out, `add_header Cache-Control "public, immutable, max-age=31536000"`) {
		t.Error("hashed assets are not long-cached")
	}
	if !strings.Contains(out, `add_header Cache-Control "no-cache, must-revalidate"`) {
		t.Error("the index document is cached, so a deploy would not be visible")
	}
}

func TestStaticSPAFallback(t *testing.T) {
	site := staticSite()
	site.SPA = true
	out := render(t, site, nil)
	// Without this, a refresh on a deep client-side route returns 404.
	if !strings.Contains(out, "try_files $uri $uri/ /index.html;") {
		t.Errorf("no SPA fallback:\n%s", out)
	}
}

func TestPythonVhostProxiesToTheSocket(t *testing.T) {
	out := render(t, pythonSite(), nil)
	want := "proxy_pass http://unix:/run/ratline/alice-api_example_com/app.sock:;"
	if !strings.Contains(out, want) {
		t.Errorf("expected %q in:\n%s", want, out)
	}
	if !strings.Contains(out, "proxy-params.conf") {
		t.Error("the proxy header include is missing")
	}
	if !strings.Contains(out, "proxy_read_timeout") {
		t.Error("proxy_read_timeout is missing")
	}
}

func TestNodePortVhost(t *testing.T) {
	site := &state.Site{
		Domain: "app.example.com", Owner: "bob", Runtime: "node", Slug: "bob-app_example_com",
		Enabled: true, Entry: "server.js", Listen: "port", Port: 20001, Instances: 1,
	}
	out := render(t, site, nil)
	if !strings.Contains(out, "proxy_pass http://127.0.0.1:20001;") {
		t.Errorf("the port proxy target is wrong:\n%s", out)
	}
}

func TestMultiInstanceUpstream(t *testing.T) {
	site := pythonSite()
	site.Instances = 3
	out := render(t, site, nil)
	if !strings.Contains(out, "upstream alice-api_example_com {") {
		t.Errorf("no upstream block:\n%s", out)
	}
	if !strings.Contains(out, "least_conn;") {
		t.Error("no load-balancing method")
	}
	for i := 1; i <= 3; i++ {
		want := "app-" + string(rune('0'+i)) + ".sock"
		if !strings.Contains(out, want) {
			t.Errorf("instance socket %s is missing", want)
		}
	}
	if !strings.Contains(out, "proxy_pass http://alice-api_example_com;") {
		t.Error("the vhost does not proxy to the upstream pool")
	}
}

func TestDisabledSiteServes503ButStillAnswersACME(t *testing.T) {
	site := staticSite()
	site.Enabled = false
	out := render(t, site, nil)
	if !strings.Contains(out, "return 503;") {
		t.Errorf("a disabled site does not return 503:\n%s", out)
	}
	// This is the point: a paused site must still be able to renew, or the
	// certificate quietly expires while nobody is looking.
	if !strings.Contains(out, "acme-challenge.conf") {
		t.Error("a disabled site cannot answer the ACME challenge")
	}
	if strings.Contains(out, "root /home/alice") {
		t.Error("a disabled site still serves files")
	}
}

func TestHSTSIsOptInAndRefusedOnAnUntrustedCertificate(t *testing.T) {
	site := staticSite()
	out := render(t, site, trustedCert())
	if strings.Contains(out, "Strict-Transport-Security") {
		t.Error("HSTS was rendered without being asked for")
	}

	site.HSTS = true
	out = render(t, site, trustedCert())
	if !strings.Contains(out, "Strict-Transport-Security") {
		t.Error("HSTS was requested but not rendered")
	}

	// A browser that has seen HSTS refuses plain HTTP afterwards. Pinning it to
	// a certificate it cannot verify would lock the site out of its own domain.
	for _, source := range []string{state.CertSourceSelfSigned, state.CertSourceStaging} {
		cert := trustedCert()
		cert.Source = source
		out = render(t, site, cert)
		if strings.Contains(out, "Strict-Transport-Security") {
			t.Errorf("HSTS was rendered with a %s certificate", source)
		}
	}
}

func TestWWWRedirect(t *testing.T) {
	site := staticSite()
	site.Aliases = []string{"www.example.com"}
	site.WWWRedirect = "apex"
	out := render(t, site, trustedCert())

	if !strings.Contains(out, "server_name www.example.com") {
		t.Errorf("no redirect block for the www host:\n%s", out)
	}
	if !strings.Contains(out, "return 301 https://example.com$request_uri;") {
		t.Error("the redirect target is wrong")
	}
	// The redirected name must not also serve content from the main block.
	mainBlock := out[strings.LastIndex(out, "server {"):]
	if strings.Contains(mainBlock, "www.example.com") {
		t.Error("the www host appears in the content-serving block as well as the redirect")
	}
}

func TestVhostRefusesAnEscapingDocumentRoot(t *testing.T) {
	site := staticSite()
	site.DocRoot = "../../etc"
	if _, err := testManager().RenderVhost(site, nil); err == nil {
		t.Fatal("RenderVhost accepted a document root outside the home directory")
	}
}

func TestSnippetsAreSelfConsistent(t *testing.T) {
	// proxy-params.conf uses $connection_upgrade, which only exists because
	// ratline-http.conf defines it. If they drift apart, every WebSocket breaks.
	proxy, err := testManager().readSnippet("proxy-params.conf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(proxy, "$connection_upgrade") {
		t.Error("proxy-params.conf no longer uses the upgrade map")
	}
	http, err := renderTemplate("nginx/ratline-http.conf.tmpl", map[string]bool{"Gzip": true, "Brotli": false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(http), "map $http_upgrade $connection_upgrade") {
		t.Error("ratline-http.conf no longer defines the upgrade map that proxy-params.conf needs")
	}
}

func TestDenyHiddenCoversTheFilesThatMatter(t *testing.T) {
	body, err := testManager().readSnippet("deny-hidden.conf")
	if err != nil {
		t.Fatal(err)
	}
	// A leaked .env is a full compromise, and it has happened to every hosting
	// platform that did not deny it explicitly.
	for _, want := range []string{`\.env`, `\.git`, "node_modules", "venv", "__pycache__", "package\\.json"} {
		if !strings.Contains(body, want) {
			t.Errorf("deny-hidden.conf does not cover %s", want)
		}
	}
}

// readSnippet exposes an embedded snippet for assertions.
func (m *Manager) readSnippet(name string) (string, error) {
	b, err := templatesRead("nginx/snippets/" + name)
	return string(b), err
}
