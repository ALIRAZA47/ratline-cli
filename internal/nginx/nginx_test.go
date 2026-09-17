package nginx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
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

// A bun site is proxied exactly as a node one is. The runtime switch in buildData is a
// literal list, so a runtime missing from it does not render a degraded vhost — it
// returns "unknown runtime" and the whole site fails to provision.
func TestBunVhostProxiesLikeANodeSite(t *testing.T) {
	site := &state.Site{
		Domain: "edge.example.com", Owner: "bob", Runtime: "bun", Slug: "bob-edge_example_com",
		Enabled: true, Entry: "server.ts", Listen: "socket", Instances: 1,
	}
	out := render(t, site, nil)
	want := "proxy_pass http://unix:/run/ratline/bob-edge_example_com/app.sock:;"
	if !strings.Contains(out, want) {
		t.Errorf("expected %q in:\n%s", want, out)
	}
	if !strings.Contains(out, "proxy-params.conf") {
		t.Error("the proxy header include is missing")
	}
}

func TestTheVhostOnlyEverProxiesToASocketThatExists(t *testing.T) {
	// A dynamic site is one unit binding one socket. Concurrency lives inside the
	// unit — PM2 cluster workers, gunicorn workers — sharing that one listening
	// handle, so there is nothing for an upstream pool to balance across.
	//
	// This replaces a test that asserted the opposite. The vhost used to render a
	// pool over app-1.sock … app-N.sock whenever --instances was above one, and
	// nothing in the codebase ever created those sockets: every request to such a
	// site was a 502 against a path that did not exist.
	for _, instances := range []int{1, 3, 16} {
		site := pythonSite()
		site.Instances = instances
		out := render(t, site, nil)

		if strings.Contains(out, "upstream ") {
			t.Errorf("instances=%d rendered an upstream block:\n%s", instances, out)
		}
		if strings.Contains(out, "app-1.sock") || strings.Contains(out, "app-2.sock") {
			t.Errorf("instances=%d proxies to a socket nothing creates:\n%s", instances, out)
		}
		want := "proxy_pass http://unix:/run/ratline/alice-api_example_com/app.sock:;"
		if !strings.Contains(out, want) {
			t.Errorf("instances=%d does not proxy to the site's real socket:\n%s", instances, out)
		}
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
	http, err := renderTemplate("nginx/ratline-http.conf.tmpl", map[string]any{
		"Compression": compressionLines(true, false, map[string]bool{}),
		"Skipped":     "",
	})
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

func TestHTTPSnippetOmitsDirectivesTheDistroAlreadySets(t *testing.T) {
	// Debian and Ubuntu ship `gzip on;` inside nginx.conf's http block, and include
	// conf.d/*.conf — where ratline links its snippet — into that same block. nginx
	// treats a repeated directive in one context as a hard error, so emitting it
	// unconditionally made `nginx -t` fail and every single `site add` fail with it,
	// on exactly the platform ratline targets.
	dir := t.TempDir()
	conf := filepath.Join(dir, "nginx.conf")
	if err := os.WriteFile(conf, []byte(`user www-data;
events { worker_connections 768; }
http {
	sendfile on;
	# gzip_vary on;
	gzip on;
	include /etc/nginx/conf.d/*.conf;
	server {
		gzip_comp_level 9;
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	taken := httpDirectivesAlreadySet(conf)
	if !taken["gzip"] {
		t.Error("gzip is set in the http block and was not detected")
	}
	// Commented out is not set.
	if taken["gzip_vary"] {
		t.Error("a commented-out directive was treated as set")
	}
	// One level deeper is a server block, where a duplicate is a legal override.
	if taken["gzip_comp_level"] {
		t.Error("a directive inside a server block was treated as http-level")
	}

	lines := compressionLines(true, false, taken)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "gzip on;") {
		t.Errorf("gzip on; was emitted even though nginx.conf sets it:\n%s", joined)
	}
	// Everything the distro left commented out is still ours to set.
	for _, want := range []string{"gzip_vary on;", "gzip_comp_level 5;", "gzip_types"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q from:\n%s", want, joined)
		}
	}
	// And the omission is stated rather than silent.
	if skipped := skippedNames(true, false, taken); !strings.Contains(skipped, "gzip") {
		t.Errorf("skippedNames = %q, want it to name gzip", skipped)
	}
}

func TestHTTPSnippetEmitsEverythingWhenNothingCollides(t *testing.T) {
	// A host whose nginx.conf sets none of them — or no nginx.conf at all — must get
	// the full set, or turning gzip on in configuration would silently do nothing.
	for _, taken := range []map[string]bool{
		{},
		httpDirectivesAlreadySet(filepath.Join(t.TempDir(), "absent.conf")),
	} {
		lines := compressionLines(true, true, taken)
		joined := strings.Join(lines, "\n")
		for _, want := range []string{"gzip on;", "gzip_types", "brotli on;", "brotli_types"} {
			if !strings.Contains(joined, want) {
				t.Errorf("missing %q from:\n%s", want, joined)
			}
		}
		if skippedNames(true, true, taken) != "" {
			t.Error("nothing collided, so nothing should be reported as skipped")
		}
	}
}

func TestHTTPSnippetEmitsNothingWhenCompressionIsOff(t *testing.T) {
	if lines := compressionLines(false, false, map[string]bool{}); len(lines) != 0 {
		t.Errorf("compression is off, so no directives should be emitted: %v", lines)
	}
}

// A reload is only finished when nothing is still answering with the old
// configuration. Waiting for a *new* worker to appear is not that: nginx starts the
// new workers before it tells the old ones to stop accepting, so `site add` and
// `cert issue` returned inside a window where a request could still be served the
// previous vhost — a missing redirect, a stale root, the old certificate.

func TestADrainingWorkerDoesNotHoldUpAReload(t *testing.T) {
	before := map[string]bool{"100": true, "101": true}
	for _, tc := range []struct {
		name  string
		now   map[string]bool
		stale bool
	}{
		{
			"the old workers are still accepting",
			map[string]bool{"100": true, "101": true},
			true,
		},
		{
			// The window this exists for: new workers up, old ones not yet signalled.
			"new workers exist but the old ones still accept",
			map[string]bool{"100": true, "101": true, "200": true, "201": true},
			true,
		},
		{
			// A draining worker takes no new connections, so its lingering — possibly
			// for as long as a client holds a websocket — must not block provisioning.
			"the old workers are draining",
			map[string]bool{"100": false, "101": false, "200": true, "201": true},
			false,
		},
		{
			"the old workers are gone",
			map[string]bool{"200": true, "201": true},
			false,
		},
		{
			"one of the two is still accepting",
			map[string]bool{"100": false, "101": true, "200": true},
			true,
		},
	} {
		if got := anyStaleWorkerAccepting(before, tc.now); got != tc.stale {
			t.Errorf("%s: anyStaleWorkerAccepting = %v, want %v", tc.name, got, tc.stale)
		}
	}
}

func TestWorkerStatesAreReadFromPgrep(t *testing.T) {
	// pgrep -a prints "<pid> <command line>", and a worker that has been told to stop
	// renames itself. Parsing only the PID loses the one distinction that matters.
	cfg := config.Default()
	runner := systest.NewFakeRunner()
	runner.ExpectOutput("pgrep -a -f nginx: worker process",
		"100 nginx: worker process is shutting down\n"+
			"200 nginx: worker process\n"+
			"201 nginx: worker process\n")
	m := &Manager{Cfg: cfg, Log: log.Discard(), Runner: runner}

	got := m.nginxWorkers(context.Background())
	want := map[string]bool{"100": false, "200": true, "201": true}
	if len(got) != len(want) {
		t.Fatalf("parsed %d workers, want %d: %v", len(got), len(want), got)
	}
	for pid, accepting := range want {
		if got[pid] != accepting {
			t.Errorf("worker %s accepting = %v, want %v", pid, got[pid], accepting)
		}
	}
}

func TestAnUnobservableReloadDoesNotBlock(t *testing.T) {
	// If pgrep is missing, or nginx is not a conventional master-plus-workers, the
	// reload almost certainly worked and refusing to return would break provisioning
	// on that host.
	cfg := config.Default()
	runner := systest.NewFakeRunner()
	runner.ExpectFailure("pgrep -a -f nginx: worker process", 127, "pgrep: not found")
	m := &Manager{Cfg: cfg, Log: log.Discard(), Runner: runner}

	if got := m.nginxWorkers(context.Background()); len(got) != 0 {
		t.Errorf("with pgrep unavailable = %v, want no workers", got)
	}
	start := time.Now()
	m.waitForReload(context.Background(), map[string]bool{"100": true}, 2*time.Second)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waited %s for an unobservable reload; it should return promptly", elapsed)
	}
}

// Without a default_server nginx answers a request for a host it does not know — the
// bare IP, a scanner's name, somebody else's domain — with whichever tenant's site
// sorts first. The catch-all closes the connection instead.
func TestTheCatchAllServesNothingToUnknownHosts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reject bool
	}{{"modern nginx", true}, {"old nginx", false}} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := renderTemplate("nginx/ratline-default.conf.tmpl", map[string]any{"RejectHandshake": tc.reject})
			if err != nil {
				t.Fatal(err)
			}
			body := string(out)
			for _, want := range []string{
				"# managed-by: ratline",
				"listen 80 default_server;",
				"listen [::]:80 default_server;",
				"server_name _;",
				"return 444;",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("the catch-all is missing %q:\n%s", want, body)
				}
			}
			has443 := strings.Contains(body, "listen 443 ssl default_server;")
			hasReject := strings.Contains(body, "ssl_reject_handshake on;")
			if has443 != tc.reject || hasReject != tc.reject {
				t.Errorf("443 block present=%v reject=%v, want both %v — an unsupported directive would refuse the whole configuration",
					has443, hasReject, tc.reject)
			}
			// It must never present a certificate, or need one: nothing named goes out.
			if strings.Contains(body, "ssl_certificate") {
				t.Error("the catch-all names a certificate; an unknown SNI should get no handshake, not somebody's cert")
			}
		})
	}
}

// ssl_reject_handshake arrived in nginx 1.19.4. Emitting it to an older nginx is an
// unknown directive, which refuses the entire configuration and takes every site down —
// so unknown means no.
func TestRejectHandshakeIsGatedOnNginxVersion(t *testing.T) {
	for _, tc := range []struct {
		major, minor, patch int
		ok, want            bool
	}{
		{1, 18, 0, true, false}, // Ubuntu 22.04
		{1, 19, 3, true, false},
		{1, 19, 4, true, true},
		{1, 22, 1, true, true}, // Debian bookworm
		{1, 24, 0, true, true}, // Ubuntu 24.04
		{1, 26, 0, true, true},
		{2, 0, 0, true, true},
		{0, 0, 0, false, false}, // version unknown
	} {
		if got := rejectHandshakeSupported(tc.major, tc.minor, tc.patch, tc.ok); got != tc.want {
			t.Errorf("rejectHandshakeSupported(%d.%d.%d, ok=%v) = %v, want %v", tc.major, tc.minor, tc.patch, tc.ok, got, tc.want)
		}
	}
}

// nginx refuses two default servers on one address. An operator who already has one
// keeps it: `nginx -T` attributes every listen line to its file, and any default_server
// that is not ratline's own means ratline installs nothing rather than breaking every
// later `site add` with "duplicate default server".
func TestAnOperatorsOwnDefaultServerIsRespected(t *testing.T) {
	own := "/etc/nginx/ratline/ratline-default.conf"
	link := "/etc/nginx/conf.d/ratline-default.conf"
	dump := func(files map[string]string) string {
		var sb strings.Builder
		for path, body := range files {
			sb.WriteString("# configuration file " + path + ":\n" + body + "\n")
		}
		return sb.String()
	}

	// Only ratline's own catch-all present: nothing foreign.
	if n := countForeignDefaultServers(dump(map[string]string{
		"/etc/nginx/nginx.conf": "http {\n    include /etc/nginx/conf.d/*.conf;\n}\n",
		link:                    "server {\n    listen 80 default_server;\n    listen [::]:80 default_server;\n    return 444;\n}\n",
	}), own, link); n != 0 {
		t.Errorf("ratline's own catch-all was counted as foreign: %d", n)
	}
	// The operator's own default in nginx.conf: foreign, so ratline stands down.
	if n := countForeignDefaultServers(dump(map[string]string{
		"/etc/nginx/nginx.conf": "http {\n    server {\n        listen 80 default_server;\n        return 444;\n    }\n}\n",
	}), own, link); n != 1 {
		t.Errorf("an operator's default_server was not detected: %d", n)
	}
	// A commented-out one does not count.
	if n := countForeignDefaultServers(dump(map[string]string{
		"/etc/nginx/sites-enabled/old.conf": "server {\n    # listen 80 default_server;\n    listen 80;\n}\n",
	}), own, link); n != 0 {
		t.Errorf("a commented default_server was counted: %d", n)
	}
	// nginx -T unavailable or empty: zero, so provisioning is never blocked on it.
	if n := countForeignDefaultServers("", own, link); n != 0 {
		t.Errorf("an empty dump counted %d", n)
	}
}

// A streaming site turns buffering off and widens the read timeout, and a site that
// says nothing renders exactly what it always did.
//
// Both halves matter. nginx buffers a proxied response by default, which holds a
// Server-Sent Events stream until the buffer fills, and closes the upstream after
// proxy_read_timeout of quiet — so a stream with a slow producer dies at 60 seconds.
func TestStreamingSiteRendersUnbufferedWithItsOwnReadTimeout(t *testing.T) {
	site := &state.Site{
		Domain: "mcp.example.com", Owner: "bob", Runtime: "bun", Slug: "bob-mcp_example_com",
		Enabled: true, Entry: "server.ts", Listen: "socket", Instances: 1,
		ProxyBuffering: "off", ProxyReadTimeout: "1h",
	}
	out := render(t, site, nil)
	for _, want := range []string{"proxy_buffering off;", "proxy_read_timeout 1h0m0s;"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in:\n%s", want, out)
		}
	}
	// Both the main location and the @app fallback proxy, and a stream that arrives
	// through one of them and not the other is the bug this counts against.
	if got := strings.Count(out, "proxy_buffering off;"); got != 2 {
		t.Errorf("proxy_buffering off appears %d times, want 2 (location / and @app)", got)
	}

	// The negative case: without the fields, nothing changes.
	site.ProxyBuffering, site.ProxyReadTimeout = "", ""
	out = render(t, site, nil)
	if !strings.Contains(out, "proxy_buffering on;") {
		t.Errorf("a site that asked for nothing should still be buffered:\n%s", out)
	}
	if !strings.Contains(out, "proxy_read_timeout 1m0s;") {
		t.Errorf("a site that asked for nothing should keep the 60s default:\n%s", out)
	}
}

// "on" is spelled out by some operators and means the same as saying nothing.
func TestProxyBufferingOnIsTheSameAsUnset(t *testing.T) {
	site := &state.Site{
		Domain: "edge.example.com", Owner: "bob", Runtime: "bun", Slug: "bob-edge_example_com",
		Enabled: true, Entry: "server.ts", Listen: "socket", Instances: 1,
		ProxyBuffering: "on",
	}
	if out := render(t, site, nil); !strings.Contains(out, "proxy_buffering on;") {
		t.Errorf("expected buffering on:\n%s", out)
	}
}

// What reaches the vhost is rendered from the parsed duration, never from the stored
// string, so a row that somehow carries a directive separator cannot add a directive.
//
// validateSiteRow is what stops such a row being written; this proves the render is
// not relying on that alone, and that a corrupt row degrades to the default rather
// than producing a vhost nginx will not load.
func TestACorruptStoredReadTimeoutFallsBackToTheDefault(t *testing.T) {
	site := &state.Site{
		Domain: "edge.example.com", Owner: "bob", Runtime: "bun", Slug: "bob-edge_example_com",
		Enabled: true, Entry: "server.ts", Listen: "socket", Instances: 1,
		ProxyReadTimeout: "60s; add_header X-Injected yes",
	}
	out := render(t, site, nil)
	if strings.Contains(out, "X-Injected") {
		t.Fatalf("a stored value reached the vhost verbatim:\n%s", out)
	}
	if !strings.Contains(out, "proxy_read_timeout 1m0s;") {
		t.Errorf("expected the default read timeout, got:\n%s", out)
	}
}
