package install

import (
	"bytes"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/ALIRAZA47/ratline-cli/templates"
)

// render is the same path SetDomain uses, so what this asserts is what lands on disk.
func render(t *testing.T, d vhostData) string {
	t.Helper()
	raw, err := templates.FS.ReadFile("panel/panel-vhost.conf.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := template.New("panel-vhost").Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, d); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func tlsData() vhostData {
	return vhostData{
		Domain: "panel.example.com", Upstream: "127.0.0.1:8420",
		TLS:         true,
		CertPath:    "/etc/letsencrypt/live/panel.example.com/fullchain.pem",
		KeyPath:     "/etc/letsencrypt/live/panel.example.com/privkey.pem",
		ChainPath:   "/etc/letsencrypt/live/panel.example.com/chain.pem",
		ACMEWebroot: ACMEWebroot, GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// `http2 on;` is an unknown directive before nginx 1.25.1. Emitting it
// unconditionally is what made `ratline-panel domain set` write a vhost that could not
// pass `nginx -t` on Ubuntu 24.04, roll back, and leave the panel unreachable on its
// domain — with the rollback message saying only that the test failed.
func TestTheVhostSpellsHTTP2TheWayThisNginxUnderstands(t *testing.T) {
	older := tlsData()
	older.HTTP2OnListen = true
	out := render(t, older)
	if strings.Contains(out, "http2 on;") {
		t.Error("an nginx older than 1.25.1 was given the `http2 on;` directive it cannot parse")
	}
	if !strings.Contains(out, "listen 443 ssl http2;") {
		t.Errorf("the listen parameter is missing:\n%s", out)
	}

	newer := tlsData()
	newer.HTTP2Directive = true
	out = render(t, newer)
	if !strings.Contains(out, "http2 on;") {
		t.Error("nginx 1.25.1+ did not get the directive")
	}
	if strings.Contains(out, "ssl http2;") {
		t.Error("the deprecated listen parameter was emitted alongside the directive")
	}

	// Neither flag set is the unknown-version case, and it must take the spelling
	// every nginx accepts rather than the one only new ones do.
	out = render(t, tlsData())
	if strings.Contains(out, "http2 on;") {
		t.Error("an unknown nginx version was given the directive; the safe fallback is the listen parameter")
	}
}

// The plain-HTTP vhost is written first, before any certificate exists. It must not
// mention one — naming a file that is not there is itself an nginx -t failure.
func TestThePlainHTTPVhostNamesNoCertificate(t *testing.T) {
	out := render(t, vhostData{
		Domain: "panel.example.com", Upstream: "127.0.0.1:8420",
		ACMEWebroot: ACMEWebroot, GeneratedAt: "now",
	})
	for _, unwanted := range []string{"ssl_certificate", "listen 443", "http2"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the pre-certificate vhost contains %q:\n%s", unwanted, out)
		}
	}
	if !strings.Contains(out, "/.well-known/acme-challenge/") {
		t.Error("the ACME challenge location is missing, so the certificate could never be issued")
	}
}
