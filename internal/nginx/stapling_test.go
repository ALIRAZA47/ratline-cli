package nginx

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/templates"
)

// writeTestCert creates a self-signed certificate, optionally naming an OCSP
// responder, and returns the path to a PEM file holding it. This is the real thing
// leafAdvertisesOCSP has to read: a certificate whose AIA extension either carries an
// OCSP URI or does not.
func writeTestCert(t *testing.T, ocspURLs ...string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		OCSPServer:   ocspURLs, // nil → no responder in the AIA extension
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLeafAdvertisesOCSP(t *testing.T) {
	// The whole basis for the fix: a certificate with an OCSP responder staples, one
	// without does not. A modern Let's Encrypt certificate is the second kind.
	if !leafAdvertisesOCSP(writeTestCert(t, "http://ocsp.example.com")) {
		t.Error("a certificate that names an OCSP responder was read as not advertising one")
	}
	if leafAdvertisesOCSP(writeTestCert(t)) {
		t.Error("a certificate with no OCSP responder was read as advertising one")
	}

	// Every failure mode resolves to "no responder", so a broken read never emits a
	// directive nginx would warn about.
	if leafAdvertisesOCSP("") {
		t.Error("an empty path advertised a responder")
	}
	if leafAdvertisesOCSP(filepath.Join(t.TempDir(), "absent.pem")) {
		t.Error("a missing file advertised a responder")
	}
	garbage := filepath.Join(t.TempDir(), "garbage.pem")
	if err := os.WriteFile(garbage, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if leafAdvertisesOCSP(garbage) {
		t.Error("an unparseable file advertised a responder")
	}
}

// leafAdvertisesOCSP must read the LEAF, which is the first certificate in a
// fullchain. A responder on the intermediate but not the leaf does not staple, and the
// reverse — a leaf with a responder followed by an intermediate without — must still
// read as stapling.
func TestLeafAdvertisesOCSPReadsTheFirstCertificate(t *testing.T) {
	leafWith := readPEM(t, writeTestCert(t, "http://ocsp.example.com"))
	leafWithout := readPEM(t, writeTestCert(t))

	fullchainStapling := filepath.Join(t.TempDir(), "fullchain-yes.pem")
	if err := os.WriteFile(fullchainStapling, append(append([]byte{}, leafWith...), leafWithout...), 0o600); err != nil {
		t.Fatal(err)
	}
	if !leafAdvertisesOCSP(fullchainStapling) {
		t.Error("a fullchain whose leaf names a responder did not staple")
	}

	fullchainNoStapling := filepath.Join(t.TempDir(), "fullchain-no.pem")
	if err := os.WriteFile(fullchainNoStapling, append(append([]byte{}, leafWithout...), leafWith...), 0o600); err != nil {
		t.Fatal(err)
	}
	if leafAdvertisesOCSP(fullchainNoStapling) {
		t.Error("a fullchain whose leaf names no responder stapled off a later certificate")
	}
}

func readPEM(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The bug: the shared TLS snippet emitted ssl_stapling for every certificate. It is
// included by every vhost, so a stray directive here reintroduces the per-reload
// warning for every site on the box.
func TestSSLParamsSnippetDoesNotStapleUnconditionally(t *testing.T) {
	body, err := templates.FS.ReadFile("nginx/snippets/ssl-params.conf")
	if err != nil {
		t.Fatal(err)
	}
	// A directive, not the word: the comment explaining the move legitimately names
	// ssl_stapling, so only non-comment lines count.
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "ssl_stapling") {
			t.Errorf("ssl-params.conf still emits %q; stapling must come per-vhost from the "+
				"certificate's OCSP responder, not apply to every certificate here", trimmed)
		}
	}
}

// The wiring: a rendered TLS vhost carries ssl_stapling exactly when the certificate
// names a responder. This proves the snippet fix and the template condition work
// together, which is what a real nginx sees.
func TestRenderedVhostStaplesOnlyWithAResponder(t *testing.T) {
	site := &state.Site{
		Domain: "example.com", Owner: "alice", Runtime: "static",
		Slug: "alice-example_com", Enabled: true, Instances: 1,
		IndexFile: "index.html", DocRoot: "public",
	}
	cert := &state.Certificate{
		Name: "example.com", Source: state.CertSourceLetsEncrypt,
		CertPath:  "/etc/letsencrypt/live/example.com/fullchain.pem",
		KeyPath:   "/etc/letsencrypt/live/example.com/privkey.pem",
		ChainPath: "/etc/letsencrypt/live/example.com/chain.pem",
		NotAfter:  time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	mgr := testManager()
	mgr.ocspProbe = func(string) bool { return false }
	noStaple, err := mgr.RenderVhost(site, cert)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(noStaple), "ssl_stapling") {
		t.Error("a certificate with no OCSP responder still rendered ssl_stapling")
	}

	mgr.ocspProbe = func(string) bool { return true }
	staple, err := mgr.RenderVhost(site, cert)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(staple), "ssl_stapling on;") ||
		!strings.Contains(string(staple), "ssl_stapling_verify on;") {
		t.Error("a certificate that names an OCSP responder did not render ssl_stapling")
	}
}
