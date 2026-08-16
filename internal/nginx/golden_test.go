package nginx

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

// update regenerates the golden files:
//
//	go test ./internal/nginx -update
//
// Review the diff before committing. A golden file is only useful if a change to
// it is a deliberate decision rather than a rubber stamp.
var update = flag.Bool("update", false, "rewrite the golden files")

// goldenCases is every combination that changes the shape of a vhost: one per
// runtime, crossed with TLS, SPA fallback and multi-instance. These are the
// files an operator will actually find in /etc/nginx, so they are reviewable as
// text in the repository and diffable across versions.
// goldenCase is one rendered vhost the suite pins.
type goldenCase struct {
	site *state.Site
	cert *state.Certificate
	// ocsp is what the OCSP probe returns for this case's certificate — whether it
	// names an OCSP responder — which is what drives ssl_stapling.
	ocsp bool
}

func goldenCases() map[string]goldenCase {
	tlsCert := func(source string) *state.Certificate {
		return &state.Certificate{
			Name: "example.com", Source: source,
			CertPath:  "/etc/letsencrypt/live/example.com/fullchain.pem",
			KeyPath:   "/etc/letsencrypt/live/example.com/privkey.pem",
			ChainPath: "/etc/letsencrypt/live/example.com/chain.pem",
			// Fixed so the rendered output does not change from one day to the next.
			NotAfter: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
			SANs:     []string{"example.com", "www.example.com"},
		}
	}
	base := func(runtime string) *state.Site {
		return &state.Site{
			Domain: "example.com", Owner: "alice", Runtime: runtime,
			Slug: "alice-example_com", Enabled: true, Instances: 1,
			IndexFile: "index.html",
		}
	}
	staticSite := func() *state.Site {
		s := base("static")
		s.DocRoot = "public"
		return s
	}
	pySite := func() *state.Site {
		s := base("python")
		s.AppModule = "app.main:app"
		s.ASGI = true
		s.Workers = 3
		s.Listen = "socket"
		s.AppServer = "gunicorn"
		return s
	}
	nodeSite := func() *state.Site {
		s := base("node")
		s.Entry = "server.js"
		s.Listen = "socket"
		return s
	}

	cases := map[string]goldenCase{
		"static-http": {site: staticSite()},
		// A modern Let's Encrypt certificate names no OCSP responder, so no
		// ssl_stapling is emitted — this is the fix for the per-reload warning.
		"static-tls":  {site: staticSite(), cert: tlsCert(state.CertSourceLetsEncrypt)},
		"python-http": {site: pySite()},
		"python-tls":  {site: pySite(), cert: tlsCert(state.CertSourceLetsEncrypt)},
		"node-http":   {site: nodeSite()},
		"node-tls":    {site: nodeSite(), cert: tlsCert(state.CertSourceLetsEncrypt)},
	}

	spa := staticSite()
	spa.SPA = true
	cases["static-spa-tls"] = goldenCase{site: spa, cert: tlsCert(state.CertSourceLetsEncrypt)}

	port := nodeSite()
	port.Listen = "port"
	port.Port = 20001
	cases["node-port-http"] = goldenCase{site: port}

	hsts := staticSite()
	hsts.HSTS = true
	cases["static-hsts-tls"] = goldenCase{site: hsts, cert: tlsCert(state.CertSourceLetsEncrypt)}

	// HSTS asked for but refused, because the certificate is not trusted. The
	// golden file is what proves the header is genuinely absent.
	hstsSelf := staticSite()
	hstsSelf.HSTS = true
	cases["static-hsts-refused-selfsigned"] = goldenCase{site: hstsSelf, cert: tlsCert(state.CertSourceSelfSigned)}

	redirect := staticSite()
	redirect.Aliases = []string{"www.example.com"}
	redirect.WWWRedirect = "apex"
	cases["static-www-redirect-tls"] = goldenCase{site: redirect, cert: tlsCert(state.CertSourceLetsEncrypt)}

	disabled := staticSite()
	disabled.Enabled = false
	cases["static-disabled"] = goldenCase{site: disabled}

	// A certificate that DOES name an OCSP responder — an imported or private-CA
	// cert — still staples. This proves the directive is emitted when the cert
	// warrants it rather than being blanket-disabled by source, and the www-redirect
	// makes it cover both TLS server blocks (the redirect one and the main one).
	stapled := staticSite()
	stapled.Aliases = []string{"www.example.com"}
	stapled.WWWRedirect = "apex"
	cases["static-tls-ocsp-stapled"] = goldenCase{
		site: stapled, cert: tlsCert(state.CertSourceImported), ocsp: true,
	}

	return cases
}

func TestVhostGoldenFiles(t *testing.T) {
	for name, tc := range goldenCases() {
		t.Run(name, func(t *testing.T) {
			mgr := testManager()
			// The certificate files in these fixtures do not exist on disk, so the
			// OCSP probe is injected: each case declares whether its certificate
			// names a responder, and the render must follow that.
			mgr.ocspProbe = func(string) bool { return tc.ocsp }
			body, err := mgr.RenderVhost(tc.site, tc.cert)
			if err != nil {
				t.Fatalf("RenderVhost = %v", err)
			}
			// The generated-at line is the one thing that legitimately differs
			// between runs, so it is normalised rather than making the file churn.
			got := normaliseGenerated(string(body))
			path := filepath.Join("testdata", "vhost", name+".conf")

			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("wrote %s", path)
				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("no golden file: %v\n\nrun: go test ./internal/nginx -update", err)
			}
			if got != string(want) {
				t.Errorf("the rendered vhost differs from %s.\n\n"+
					"If the change is deliberate, review it and re-run:\n"+
					"    go test ./internal/nginx -update\n\n%s",
					path, firstDifference(string(want), got))
			}
		})
	}
}

// normaliseGenerated replaces the timestamp so a golden file does not change
// every time it is rendered.
func normaliseGenerated(s string) string {
	out := make([]byte, 0, len(s))
	for _, line := range splitLines(s) {
		if len(line) > 12 && line[:12] == "# generated:" {
			out = append(out, "# generated: <normalised>\n"...)
			continue
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	return string(out)
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// firstDifference reports the first differing line, which is far more useful in a
// failure than two hundred lines of unified diff.
func firstDifference(want, got string) string {
	wl, gl := splitLines(want), splitLines(got)
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return "first difference at line " + itoa(i+1) + ":\n  want: " + w + "\n  got:  " + g
		}
	}
	return "the files differ only in trailing whitespace"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
