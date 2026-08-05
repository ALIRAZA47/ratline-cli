package tls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/nginx"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
)

// testManager builds a Manager over an in-memory store and a temporary
// filesystem, with certbot scripted rather than run.
func testManager(t *testing.T) (*Manager, *state.Store, string) {
	t.Helper()
	st, err := state.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory = %v", err)
	}
	t.Cleanup(func() { st.Close() })

	root := t.TempDir()
	cfg := config.Default()
	cfg.Paths.LetsEncryptDir = filepath.Join(root, "letsencrypt")
	cfg.Paths.ImportedCerts = filepath.Join(root, "certs")
	cfg.Paths.ACMEWebroot = filepath.Join(root, "acme")
	cfg.ACME.Email = "ops@example.com"
	cfg.ACME.TOSAgreed = true

	for _, d := range []string{cfg.Paths.LetsEncryptDir, cfg.Paths.ImportedCerts, cfg.Paths.ACMEWebroot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runner := systest.NewFakeRunner()
	return &Manager{
		Cfg: cfg, Log: log.Discard(), Runner: runner, State: st,
		Nginx:  &nginx.Manager{Cfg: cfg, Log: log.Discard(), Runner: runner, DryRun: true},
		DryRun: true,
	}, st, root
}

// generateCert makes a real certificate for the tests, so the x509 handling is
// exercised against genuine DER rather than a fixture that may not parse.
func generateCert(t *testing.T, names []string, notBefore, notAfter time.Time, rsaKey bool) ([]byte, []byte, *x509.Certificate) {
	t.Helper()
	var (
		pub  any
		priv any
		err  error
	)
	if rsaKey {
		k, kerr := rsa.GenerateKey(rand.Reader, 2048)
		if kerr != nil {
			t.Fatal(kerr)
		}
		pub, priv = &k.PublicKey, k
	} else {
		k, kerr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if kerr != nil {
			t.Fatal(kerr)
		}
		pub, priv = &k.PublicKey, k
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 96))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: names[0], Organization: []string{"ratline test"}},
		Issuer:                pkix.Name{CommonName: "ratline test CA"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              names,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), leaf
}

func writeFiles(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestParsePEMAndFields(t *testing.T) {
	certPEM, _, leaf := generateCert(t, []string{"example.com", "www.example.com"},
		time.Now().Add(-time.Hour), time.Now().Add(60*24*time.Hour), false)
	dir := t.TempDir()
	path := filepath.Join(dir, "fullchain.pem")
	if err := os.WriteFile(path, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}

	parsed, chain, err := ParsePEM(path)
	if err != nil {
		t.Fatalf("ParsePEM = %v", err)
	}
	if len(chain) != 0 {
		t.Errorf("chain has %d entries, want 0", len(chain))
	}
	if parsed.SerialNumber.Cmp(leaf.SerialNumber) != 0 {
		t.Error("a different certificate was parsed")
	}
	if got := KeyTypeOf(parsed); got != "ecdsa" {
		t.Errorf("KeyTypeOf = %q, want ecdsa", got)
	}
	sans := SANsOf(parsed)
	if len(sans) != 2 || sans[0] != "example.com" {
		t.Errorf("SANsOf = %v", sans)
	}
	if fp := Fingerprint(parsed); !strings.HasPrefix(fp, "SHA256:") || len(fp) != 71 {
		t.Errorf("Fingerprint = %q", fp)
	}
}

func TestParsePEMRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"empty.pem":   "",
		"notpem.pem":  "hello",
		"onlykey.pem": "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ParsePEM(path); err == nil {
			t.Errorf("ParsePEM accepted %s", name)
		}
	}
}

func TestStatusClassification(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	in := func(days int) time.Time { return now.AddDate(0, 0, days) }

	cases := []struct {
		name string
		cert *state.Certificate
		want Status
	}{
		{"healthy", &state.Certificate{Source: state.CertSourceLetsEncrypt, NotAfter: in(60),
			Attached: []string{"example.com"}}, StatusValid},
		{"expiring", &state.Certificate{Source: state.CertSourceLetsEncrypt, NotAfter: in(15),
			Attached: []string{"example.com"}}, StatusExpiring},
		{"critical", &state.Certificate{Source: state.CertSourceLetsEncrypt, NotAfter: in(3),
			Attached: []string{"example.com"}}, StatusCritical},
		{"expired", &state.Certificate{Source: state.CertSourceLetsEncrypt, NotAfter: in(-1)}, StatusExpired},
		{"degraded beats expiring", &state.Certificate{Source: state.CertSourceLetsEncrypt,
			NotAfter: in(15), ConsecutiveFailures: 2}, StatusDegraded},
		{"staging", &state.Certificate{Source: state.CertSourceStaging, NotAfter: in(60)}, StatusStaging},
		{"self-signed", &state.Certificate{Source: state.CertSourceSelfSigned, NotAfter: in(300)}, StatusSelfSigned},
		{"orphaned", &state.Certificate{Source: state.CertSourceLetsEncrypt, NotAfter: in(60)}, StatusOrphaned},
		// Expiry beats provenance: an expired staging certificate is an expiry
		// problem first.
		{"expired staging", &state.Certificate{Source: state.CertSourceStaging, NotAfter: in(-5)}, StatusExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StatusOf(tc.cert, now); got != tc.want {
				t.Errorf("StatusOf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScanAdoptsCertificatesIssuedByHand(t *testing.T) {
	m, st, _ := testManager(t)
	m.DryRun = false
	ctx := context.Background()

	// The residue someone leaves behind by running certbot directly.
	certPEM, keyPEM, _ := generateCert(t, []string{"handmade.example.com"},
		time.Now().Add(-time.Hour), time.Now().Add(40*24*time.Hour), false)
	writeFiles(t, filepath.Join(m.Cfg.Paths.LetsEncryptDir, "live", "handmade.example.com"), map[string][]byte{
		"fullchain.pem": certPEM,
		"privkey.pem":   keyPEM,
	})

	res, err := m.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan = %v", err)
	}
	if len(res.Adopted) != 1 || res.Adopted[0] != "handmade.example.com" {
		t.Fatalf("Adopted = %v, want the hand-made certificate", res.Adopted)
	}
	got, err := st.GetCertificate(ctx, "handmade.example.com")
	if err != nil {
		t.Fatalf("the adopted certificate is not in state: %v", err)
	}
	if got.Source != state.CertSourceLetsEncrypt {
		t.Errorf("source = %q", got.Source)
	}
	if len(got.SANs) != 1 {
		t.Errorf("SANs = %v", got.SANs)
	}

	// Rescanning is idempotent and does not re-report it as new.
	res, err = m.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Adopted) != 0 {
		t.Errorf("a rescan re-adopted %v", res.Adopted)
	}
}

func TestScanPreservesRenewalBookkeeping(t *testing.T) {
	m, st, _ := testManager(t)
	m.DryRun = false
	ctx := context.Background()

	certPEM, keyPEM, _ := generateCert(t, []string{"example.com"},
		time.Now().Add(-time.Hour), time.Now().Add(40*24*time.Hour), false)
	writeFiles(t, filepath.Join(m.Cfg.Paths.LetsEncryptDir, "live", "example.com"), map[string][]byte{
		"fullchain.pem": certPEM, "privkey.pem": keyPEM,
	})
	if _, err := m.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	// Bookkeeping a rescan cannot rediscover from the file must survive it.
	if err := st.RecordRenewal(ctx, "example.com", "failure", "DNS mismatch"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAutoRenew(ctx, "example.com", false); err != nil {
		t.Fatal(err)
	}
	if err := st.AttachCertificate(ctx, "example.com", "example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetCertificate(ctx, "example.com")
	if got.ConsecutiveFailures != 1 {
		t.Errorf("the failure count was lost: %d", got.ConsecutiveFailures)
	}
	if got.AutoRenew {
		t.Error("the auto-renew setting was reset by a rescan")
	}
	if len(got.Attached) != 1 {
		t.Errorf("the attachment was lost: %v", got.Attached)
	}
}

func TestScanMarksStagingFromTheIssuer(t *testing.T) {
	for issuer, wantStaging := range map[string]bool{
		"(STAGING) Artificial Apricot R3": true,
		"Pebble Intermediate CA":          true,
		"R11":                             false,
	} {
		if got := isStagingIssuer(issuer); got != wantStaging {
			t.Errorf("isStagingIssuer(%q) = %v, want %v", issuer, got, wantStaging)
		}
	}
}

func TestResolveDefaultsAndConflicts(t *testing.T) {
	m, _, _ := testManager(t)

	opts := IssueOptions{Domain: "example.com"}
	if err := m.Resolve(&opts); err != nil {
		t.Fatalf("Resolve = %v", err)
	}
	if opts.Challenge != "http" || opts.KeyType != "ecdsa" || opts.Email != "ops@example.com" {
		t.Errorf("defaults were not applied: %+v", opts)
	}

	// A wildcard cannot use HTTP-01, so the challenge is switched rather than
	// failing at the CA.
	wild := IssueOptions{Domain: "*.example.com", Challenge: "http",
		DNSProvider: "cloudflare", DNSCredentials: "/etc/ratline/dns/cloudflare.ini"}
	if err := m.Resolve(&wild); err != nil {
		t.Fatalf("Resolve on a wildcard = %v", err)
	}
	if wild.Challenge != "dns" {
		t.Errorf("a wildcard was left on %q", wild.Challenge)
	}

	for name, o := range map[string]IssueOptions{
		"dns without a provider":  {Domain: "example.com", Challenge: "dns"},
		"dns without credentials": {Domain: "example.com", Challenge: "dns", DNSProvider: "cloudflare"},
		"a provider on http":      {Domain: "example.com", Challenge: "http", DNSProvider: "cloudflare"},
		"an unknown challenge":    {Domain: "example.com", Challenge: "tls-alpn"},
		"an unknown key type":     {Domain: "example.com", KeyType: "dsa"},
	} {
		if err := m.Resolve(&o); err == nil {
			t.Errorf("Resolve accepted %s", name)
		}
	}
}

func TestResolveRequiresAcceptedTerms(t *testing.T) {
	m, _, _ := testManager(t)
	m.Cfg.ACME.TOSAgreed = false

	opts := IssueOptions{Domain: "example.com"}
	if err := m.Resolve(&opts); err == nil {
		t.Fatal("Resolve proceeded without the subscriber agreement accepted")
	}
	// Staging and dry runs do not need it, so testing is never blocked on it.
	staging := IssueOptions{Domain: "example.com", Staging: true}
	if err := m.Resolve(&staging); err != nil {
		t.Errorf("Resolve refused a staging request: %v", err)
	}
}

func TestNamesIncludesSiteAliases(t *testing.T) {
	m, st, _ := testManager(t)
	ctx := context.Background()
	if err := st.PutUser(ctx, &state.User{Name: "alice", Home: "/home/alice", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSite(ctx, &state.Site{
		Domain: "example.com", Owner: "alice", Runtime: "static", Slug: "alice-example_com",
		Aliases: []string{"www.example.com"},
	}); err != nil {
		t.Fatal(err)
	}

	// A certificate that does not cover the www host a site already serves is a
	// name mismatch waiting to happen, so aliases are included by default.
	opts := IssueOptions{Domain: "example.com"}
	names, err := m.Names(ctx, &opts)
	if err != nil {
		t.Fatalf("Names = %v", err)
	}
	if len(names) != 2 || names[0] != "example.com" || names[1] != "www.example.com" {
		t.Errorf("Names = %v, want the domain and its alias", names)
	}

	// An explicit --alias replaces the site's list rather than adding to it.
	opts = IssueOptions{Domain: "example.com", Aliases: []string{"cdn.example.com"}}
	names, _ = m.Names(ctx, &opts)
	if len(names) != 2 || names[1] != "cdn.example.com" {
		t.Errorf("Names with an explicit alias = %v", names)
	}

	// Extra SANs are additive and de-duplicated.
	opts = IssueOptions{Domain: "example.com", ExtraSANs: []string{"api.example.com", "example.com"}}
	names, _ = m.Names(ctx, &opts)
	if len(names) != 3 {
		t.Errorf("Names = %v, want three unique names", names)
	}
}

func TestImportValidation(t *testing.T) {
	m, _, root := testManager(t)
	m.DryRun = false
	ctx := context.Background()
	dir := filepath.Join(root, "in")

	valid, validKey, _ := generateCert(t, []string{"example.com"},
		time.Now().Add(-time.Hour), time.Now().Add(365*24*time.Hour), false)
	expired, expiredKey, _ := generateCert(t, []string{"example.com"},
		time.Now().Add(-400*24*time.Hour), time.Now().Add(-24*time.Hour), false)
	wrongName, wrongNameKey, _ := generateCert(t, []string{"other.example.org"},
		time.Now().Add(-time.Hour), time.Now().Add(365*24*time.Hour), false)
	_, otherKey, _ := generateCert(t, []string{"example.com"},
		time.Now().Add(-time.Hour), time.Now().Add(365*24*time.Hour), false)

	writeFiles(t, dir, map[string][]byte{
		"valid.pem": valid, "valid.key": validKey,
		"expired.pem": expired, "expired.key": expiredKey,
		"wrongname.pem": wrongName, "wrongname.key": wrongNameKey,
		"other.key":     otherKey,
		"garbage.pem":   []byte("not a certificate"),
		"encrypted.key": []byte("-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\n-----END RSA PRIVATE KEY-----\n"),
	})
	p := func(name string) string { return filepath.Join(dir, name) }

	// Each failure has to name its own reason: "import failed" tells an operator
	// nothing about which of their two files is wrong.
	cases := map[string]struct {
		opts ImportOptions
		want string
	}{
		"a mismatched key": {
			ImportOptions{Domain: "example.com", CertPath: p("valid.pem"), KeyPath: p("other.key")},
			"does not match"},
		"an expired certificate": {
			ImportOptions{Domain: "example.com", CertPath: p("expired.pem"), KeyPath: p("expired.key")},
			"expired"},
		"the wrong names": {
			ImportOptions{Domain: "example.com", CertPath: p("wrongname.pem"), KeyPath: p("wrongname.key")},
			"does not cover"},
		"an unparseable certificate": {
			ImportOptions{Domain: "example.com", CertPath: p("garbage.pem"), KeyPath: p("valid.key")},
			"no certificate"},
		"an encrypted key": {
			ImportOptions{Domain: "example.com", CertPath: p("valid.pem"), KeyPath: p("encrypted.key")},
			"encrypted"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := m.Import(ctx, tc.opts)
			if err == nil {
				t.Fatalf("Import accepted %s", name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Errorf("the error does not explain the problem (want %q): %v", tc.want, err)
			}
		})
	}

	// And the valid case works, with the right modes.
	cert, err := m.Import(ctx, ImportOptions{
		Domain: "example.com", CertPath: p("valid.pem"), KeyPath: p("valid.key"),
	})
	if err != nil {
		t.Fatalf("Import of a valid pair = %v", err)
	}
	if cert.Source != state.CertSourceImported {
		t.Errorf("source = %q", cert.Source)
	}
	// Nothing renews an imported certificate; claiming otherwise would be worse
	// than saying so.
	if cert.AutoRenew {
		t.Error("an imported certificate was marked auto-renewing")
	}
	// A private key must never be more readable than root.
	fi, err := os.Stat(cert.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("private key mode = %04o, want 0600", got)
	}
	if fi, err := os.Stat(cert.CertPath); err == nil {
		if got := fi.Mode().Perm(); got != 0o644 {
			t.Errorf("certificate mode = %04o, want 0644", got)
		}
	}
	dirInfo, err := os.Stat(filepath.Dir(cert.KeyPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("directory mode = %04o, want 0700", got)
	}
}

func TestImportAcceptsAnRSAKey(t *testing.T) {
	m, _, root := testManager(t)
	m.DryRun = false
	dir := filepath.Join(root, "rsa")
	certPEM, keyPEM, _ := generateCert(t, []string{"example.com"},
		time.Now().Add(-time.Hour), time.Now().Add(365*24*time.Hour), true)
	writeFiles(t, dir, map[string][]byte{"c.pem": certPEM, "k.pem": keyPEM})

	cert, err := m.Import(context.Background(), ImportOptions{
		Domain: "example.com", CertPath: filepath.Join(dir, "c.pem"), KeyPath: filepath.Join(dir, "k.pem"),
	})
	if err != nil {
		t.Fatalf("Import of an RSA pair = %v", err)
	}
	if cert.KeyType != "rsa" {
		t.Errorf("key type = %q, want rsa", cert.KeyType)
	}
}

func TestSelfSignIsMarkedUntrusted(t *testing.T) {
	m, st, _ := testManager(t)
	m.DryRun = false
	ctx := context.Background()

	cert, err := m.SelfSign(ctx, "example.com", 30, false)
	if err != nil {
		t.Fatalf("SelfSign = %v", err)
	}
	if cert.Source != state.CertSourceSelfSigned {
		t.Errorf("source = %q", cert.Source)
	}
	// The whole point of recording it distinctly: nothing must ever treat it as a
	// working certificate.
	if cert.Trusted() {
		t.Error("a self-signed certificate reports itself trusted")
	}
	if cert.AutoRenew {
		t.Error("a self-signed certificate was marked auto-renewing")
	}
	if StatusOf(cert, time.Now()) != StatusSelfSigned {
		t.Errorf("status = %q", StatusOf(cert, time.Now()))
	}
	stored, err := st.GetCertificate(ctx, "example.com")
	if err != nil {
		t.Fatalf("not recorded: %v", err)
	}
	if !stored.Covers("example.com") {
		t.Error("the generated certificate does not cover its own domain")
	}
	fi, err := os.Stat(cert.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("private key mode = %04o, want 0600", got)
	}
}

func TestAttachRefusesACertificateThatDoesNotCoverTheSite(t *testing.T) {
	m, st, _ := testManager(t)
	ctx := context.Background()
	if err := st.PutUser(ctx, &state.User{Name: "alice", Home: "/home/alice", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSite(ctx, &state.Site{
		Domain: "example.com", Owner: "alice", Runtime: "static", Slug: "alice-example_com",
		Aliases: []string{"www.example.com"}, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	// Covers the apex but not the www host the site also serves.
	if err := st.PutCertificate(ctx, &state.Certificate{
		Name: "partial", Source: state.CertSourceLetsEncrypt, CertPath: "/tmp/c.pem",
		NotAfter: time.Now().Add(60 * 24 * time.Hour), SANs: []string{"example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	err := m.Attach(ctx, "example.com", "partial")
	if err == nil {
		t.Fatal("Attach accepted a certificate that does not cover every name")
	}
	if !strings.Contains(err.Error(), "www.example.com") {
		t.Errorf("the error does not name the uncovered host: %v", err)
	}
}

func TestAttachRefusesHSTSWithAnUntrustedCertificate(t *testing.T) {
	m, st, _ := testManager(t)
	ctx := context.Background()
	if err := st.PutUser(ctx, &state.User{Name: "alice", Home: "/home/alice", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSite(ctx, &state.Site{
		Domain: "example.com", Owner: "alice", Runtime: "static", Slug: "alice-example_com",
		Enabled: true, HSTS: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutCertificate(ctx, &state.Certificate{
		Name: "example.com", Source: state.CertSourceSelfSigned, CertPath: "/tmp/c.pem",
		NotAfter: time.Now().Add(60 * 24 * time.Hour), SANs: []string{"example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	// A browser that has seen HSTS refuses plain HTTP afterwards, so pinning it to
	// an untrusted certificate locks the site out of its own domain.
	err := m.Attach(ctx, "example.com", "example.com")
	if err == nil {
		t.Fatal("Attach allowed HSTS with a self-signed certificate")
	}
	if !strings.Contains(err.Error(), "HSTS") {
		t.Errorf("the error does not mention HSTS: %v", err)
	}
}

func TestDeleteRefusesWhileAttached(t *testing.T) {
	m, st, _ := testManager(t)
	ctx := context.Background()
	if err := st.PutCertificate(ctx, &state.Certificate{
		Name: "example.com", Source: state.CertSourceLetsEncrypt,
		NotAfter: time.Now().Add(60 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.AttachCertificate(ctx, "example.com", "example.com"); err != nil {
		t.Fatal(err)
	}
	err := m.Delete(ctx, "example.com", true)
	if err == nil {
		t.Fatal("Delete removed a certificate a site is still using")
	}
	if !strings.Contains(err.Error(), "still used by") {
		t.Errorf("the error does not say why: %v", err)
	}
}

func TestRenewSkipsWhatCannotOrNeedNotBeRenewed(t *testing.T) {
	m, st, _ := testManager(t)
	ctx := context.Background()
	now := time.Now()

	for _, c := range []*state.Certificate{
		{Name: "selfsigned.example.com", Source: state.CertSourceSelfSigned, NotAfter: now.Add(300 * 24 * time.Hour)},
		{Name: "imported.example.com", Source: state.CertSourceImported, NotAfter: now.Add(10 * 24 * time.Hour)},
		{Name: "fresh.example.com", Source: state.CertSourceLetsEncrypt, AutoRenew: true, NotAfter: now.Add(60 * 24 * time.Hour)},
		{Name: "off.example.com", Source: state.CertSourceLetsEncrypt, AutoRenew: false, NotAfter: now.Add(5 * 24 * time.Hour)},
	} {
		if err := st.PutCertificate(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	outcomes, err := m.Renew(ctx, RenewOptions{All: true})
	if err != nil {
		t.Fatalf("Renew = %v", err)
	}
	if len(outcomes) != 4 {
		t.Fatalf("got %d outcomes, want 4", len(outcomes))
	}
	byName := map[string]RenewOutcome{}
	for _, o := range outcomes {
		byName[o.Name] = o
	}
	for name, wantDetail := range map[string]string{
		"selfsigned.example.com": "self-signed",
		"imported.example.com":   "imported",
		"fresh.example.com":      "more than",
		"off.example.com":        "automatic renewal is off",
	} {
		o := byName[name]
		if o.Action != "skipped" {
			t.Errorf("%s: action = %q, want skipped", name, o.Action)
		}
		if !strings.Contains(o.Detail, wantDetail) {
			t.Errorf("%s: detail = %q, want it to mention %q", name, o.Detail, wantDetail)
		}
	}
}

func TestRenewBacksOffAfterRepeatedFailures(t *testing.T) {
	m, st, _ := testManager(t)
	ctx := context.Background()
	if err := st.PutCertificate(ctx, &state.Certificate{
		Name: "broken.example.com", Source: state.CertSourceLetsEncrypt, AutoRenew: true,
		NotAfter: time.Now().Add(5 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	// Three failures in a row: the next attempt must wait rather than hammer the
	// CA twice a day and burn the failed-validation budget.
	for i := 0; i < 3; i++ {
		if err := st.RecordRenewal(ctx, "broken.example.com", "failure", "DNS mismatch"); err != nil {
			t.Fatal(err)
		}
	}
	outcomes, err := m.Renew(ctx, RenewOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("got %d outcomes", len(outcomes))
	}
	if outcomes[0].Action != "skipped" || !strings.Contains(outcomes[0].Detail, "backing off") {
		t.Errorf("outcome = %+v, want a backoff", outcomes[0])
	}

	// --force overrides the backoff, because an operator watching the terminal
	// knows better than the heuristic.
	forced, err := m.Renew(ctx, RenewOptions{All: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if forced[0].Action == "skipped" && strings.Contains(forced[0].Detail, "backing off") {
		t.Error("--force did not override the backoff")
	}
}

func TestSANSummary(t *testing.T) {
	if got := SANSummary([]string{"a.com", "b.com"}, 2); got != "a.com,b.com" {
		t.Errorf("SANSummary = %q", got)
	}
	if got := SANSummary([]string{"a.com", "b.com", "c.com", "d.com"}, 2); got != "a.com,b.com,+2 more" {
		t.Errorf("SANSummary = %q", got)
	}
}

func TestDetectProxyRecognisesCloudflare(t *testing.T) {
	// The single most common cause of a failed first issuance, and it looks like a
	// ratline bug rather than a DNS setting unless it is named.
	for addr, want := range map[string]string{
		"104.16.1.1":   "Cloudflare",
		"172.67.10.20": "Cloudflare",
		"151.101.1.1":  "Fastly",
		"203.0.113.10": "",
		"2606:4700::1": "Cloudflare",
	} {
		got := detectProxy(mustAddrs(t, addr))
		if got != want {
			t.Errorf("detectProxy(%s) = %q, want %q", addr, got, want)
		}
	}
}

func TestPreflightErrorNamesEveryProblem(t *testing.T) {
	results := []PreflightResult{
		{Check: "dns:example.com", OK: false, Fatal: true, Detail: "resolves elsewhere", Fix: "point the record here"},
		{Check: "reachability", OK: false, Fatal: true, Detail: "port 80 is closed", Fix: "open port 80"},
		{Check: "site", OK: true},
	}
	err := PreflightError("example.com", results)
	if err == nil {
		t.Fatal("PreflightError returned nil with two fatal results")
	}
	msg := err.Error()
	// All of them, in one message: fixing one per attempt wastes rate-limit budget.
	for _, want := range []string{"dns:example.com", "reachability", "resolves elsewhere", "port 80 is closed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not mention %q:\n%s", want, msg)
		}
	}

	if PreflightError("example.com", []PreflightResult{{Check: "site", OK: true}}) != nil {
		t.Error("PreflightError returned an error with nothing fatal")
	}
	// A non-fatal failure must not block an issuance.
	soft := []PreflightResult{{Check: "site", OK: false, Fatal: false, Detail: "the site is disabled"}}
	if PreflightError("example.com", soft) != nil {
		t.Error("a non-fatal result was treated as fatal")
	}
}

func TestResetsIn(t *testing.T) {
	// The countdown matters: an operator needs to know when they can try again.
	oldest := time.Now().Add(-6 * 24 * time.Hour)
	if got := resetsIn(oldest, 7*24*time.Hour); !strings.Contains(got, "hour") {
		t.Errorf("resetsIn = %q, want an hour count", got)
	}
	if got := resetsIn(time.Time{}, time.Hour); !strings.Contains(got, "rolls over") {
		t.Errorf("resetsIn with no data = %q", got)
	}
	if got := resetsIn(time.Now().Add(-2*time.Hour), time.Hour); got != "now" {
		t.Errorf("resetsIn on an elapsed window = %q, want now", got)
	}
}

func TestCheckKeyMatchesRejectsAnEncryptedKey(t *testing.T) {
	_, _, leaf := generateCert(t, []string{"example.com"}, time.Now(), time.Now().Add(time.Hour), false)
	encrypted := []byte("-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-256-CBC,X\n\nAAAA\n-----END RSA PRIVATE KEY-----\n")
	err := checkKeyMatches(leaf, encrypted)
	if err == nil {
		t.Fatal("checkKeyMatches accepted an encrypted key")
	}
	// The one failure with a single obvious remedy, so the remedy is in the hint.
	if !strings.Contains(err.Error(), "encrypted") {
		t.Errorf("the error does not say the key is encrypted: %v", err)
	}
}

// mustAddrs parses addresses for the proxy-detection test.
func mustAddrs(t *testing.T, s ...string) []netip.Addr {
	t.Helper()
	out := make([]netip.Addr, 0, len(s))
	for _, v := range s {
		a, err := netip.ParseAddr(v)
		if err != nil {
			t.Fatalf("ParseAddr(%q) = %v", v, err)
		}
		out = append(out, a)
	}
	return out
}

// A certificate issued against a private ACME CA has to keep renewing against it.
// Issuance points certbot at a trust store when the operator names a directory;
// renewal has no flags to read, so it reads the lineage's own config — and getting
// this wrong is invisible until the certificate expires.

func TestRenewalReadsTheServerFromTheLineageConfig(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{Cfg: config.Default(), Log: log.Discard()}
	m.Cfg.Paths.LetsEncryptDir = dir
	if err := os.MkdirAll(filepath.Join(dir, "renewal"), 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "renewal", name+".conf"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// certbot's own layout: a [renewalparams] section with the server in it.
	write("private.test", "version = 2.9.0\narchive_dir = /etc/letsencrypt/archive/private.test\n"+
		"[renewalparams]\nauthenticator = webroot\nserver = https://ca.internal:14000/dir\n")
	write("public.test", "[renewalparams]\nserver = https://acme-v02.api.letsencrypt.org/directory\n")
	write("commented.test", "[renewalparams]\n# server = https://ca.internal/dir\n")

	for _, tc := range []struct {
		name, want string
	}{
		{"private.test", "https://ca.internal:14000/dir"},
		{"public.test", "https://acme-v02.api.letsencrypt.org/directory"},
		{"commented.test", ""},
		{"absent.test", ""},
	} {
		if got := RenewalServer(m.Cfg, tc.name); got != tc.want {
			t.Errorf("renewalServer(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestOnlyAPrivateCAGetsTheSystemTrustStore(t *testing.T) {
	// Widening the trust store for Let's Encrypt would be a downgrade nobody asked
	// for: certifi is the correct store there.
	dir := t.TempDir()
	m := &Manager{Cfg: config.Default(), Log: log.Discard()}
	m.Cfg.Paths.LetsEncryptDir = dir
	if err := os.MkdirAll(filepath.Join(dir, "renewal"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, server string) {
		t.Helper()
		body := "[renewalparams]\nserver = " + server + "\n"
		if err := os.WriteFile(filepath.Join(dir, "renewal", name+".conf"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("le", "https://acme-v02.api.letsencrypt.org/directory")
	write("le-staging", "https://acme-staging-v02.api.letsencrypt.org/directory")
	write("private", "https://pebble:14000/dir")

	for _, name := range []string{"le", "le-staging", "unknown-lineage"} {
		if got := m.renewalCABundle(name); got != "" {
			t.Errorf("renewalCABundle(%s) = %q, want empty — certifi is right for a public CA", name, got)
		}
	}
	// The private one gets whatever system store exists. On a machine with none —
	// macOS, for instance — there is nothing to point at, and empty is correct.
	got := m.renewalCABundle("private")
	if want := systemTrustStore(); got != want {
		t.Errorf("renewalCABundle(private) = %q, want %q", got, want)
	}
}

func TestTheRenewalEnvironmentCarriesBothVariables(t *testing.T) {
	// certbot reads REQUESTS_CA_BUNDLE; urllib3 and some DNS plugins read
	// SSL_CERT_FILE instead. Setting one and not the other fails for a subset of
	// configurations, which is worse than failing for all of them.
	m := &Manager{Cfg: config.Default(), Log: log.Discard()}
	if env := m.certbotEnvForBundle(""); env != nil {
		t.Errorf("with no bundle the environment should be nil, got %v", env)
	}
	env := m.certbotEnvForBundle("/etc/ssl/certs/ca-certificates.crt")
	var sawRequests, sawSSL bool
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "REQUESTS_CA_BUNDLE="):
			sawRequests = true
		case strings.HasPrefix(kv, "SSL_CERT_FILE="):
			sawSSL = true
		}
	}
	if !sawRequests || !sawSSL {
		t.Errorf("REQUESTS_CA_BUNDLE=%v SSL_CERT_FILE=%v, want both: %v", sawRequests, sawSSL, env)
	}
	// It is a complete replacement, so PATH has to survive or certbot is unfindable.
	var sawPath bool
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			sawPath = true
		}
	}
	if !sawPath {
		t.Errorf("the environment replaces the process's own, so it must carry PATH: %v", env)
	}
}

func TestRenewDoesNotLetCertbotSleepPastTheTimeout(t *testing.T) {
	// certbot renew --non-interactive sleeps a random 0-480s before doing anything,
	// so that the world's cron jobs do not arrive at the CA together. ratline bounds
	// the call with acme.issue_timeout, five minutes by default — so every draw above
	// five minutes was killed and recorded as a renewal failure. Not a rare edge:
	// most of a third of all renewals, on every server, against Let's Encrypt as much
	// as against anything else. The recorded failures then fed the exponential
	// backoff, so losing the dice roll twice cost hours.
	//
	// ratline's own timer carries RandomizedDelaySec=3h, so the spreading certbot is
	// trying to provide is already there and this gives up nothing.
	m, st, _ := testManager(t)
	m.DryRun = false
	ctx := context.Background()

	due := &state.Certificate{
		Name: "due.example.com", Source: state.CertSourceLetsEncrypt, AutoRenew: true,
		NotBefore: time.Now().Add(-24 * time.Hour), NotAfter: time.Now().Add(5 * 24 * time.Hour),
	}
	if err := st.PutCertificate(ctx, due); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Renew(ctx, RenewOptions{All: true}); err != nil {
		t.Fatalf("Renew = %v", err)
	}

	runner, ok := m.Runner.(*systest.FakeRunner)
	if !ok {
		t.Fatalf("the fixture's runner is a %T, which cannot be inspected", m.Runner)
	}
	var renewArgs []string
	for _, c := range runner.Calls() {
		if c.Name == "certbot" && len(c.Args) > 0 && c.Args[0] == "renew" {
			renewArgs = c.Args
		}
	}
	if renewArgs == nil {
		t.Fatalf("certbot renew was never invoked; calls: %v", runner.Keys())
	}
	var sawFlag bool
	for _, a := range renewArgs {
		if a == "--no-random-sleep-on-renew" {
			sawFlag = true
		}
	}
	if !sawFlag {
		t.Errorf("certbot renew must pass --no-random-sleep-on-renew, got: %v", renewArgs)
	}
}

func TestRatlineOwnsTheRenewalWindowNotCertbot(t *testing.T) {
	// acme.renew_before_days is the operator's setting, and renewOne is reached only
	// for a certificate ratline has already judged due by it. certbot applies its own
	// 30-day window on top and answers "Certificate not yet due for renewal", which
	// came back as "skipped" — so any window wider than 30 days silently did nothing.
	// The default is 30, which is exactly why this stayed hidden.
	m, st, _ := testManager(t)
	m.DryRun = false
	m.Cfg.ACME.RenewBeforeDays = 45 // wider than certbot's own 30
	ctx := context.Background()

	// 40 days out: inside ratline's window, outside certbot's.
	cert := &state.Certificate{
		Name: "wide.example.com", Source: state.CertSourceLetsEncrypt, AutoRenew: true,
		NotBefore: time.Now().Add(-24 * time.Hour), NotAfter: time.Now().Add(40 * 24 * time.Hour),
	}
	if err := st.PutCertificate(ctx, cert); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Renew(ctx, RenewOptions{All: true}); err != nil {
		t.Fatalf("Renew = %v", err)
	}

	runner := m.Runner.(*systest.FakeRunner)
	var renewArgs []string
	for _, c := range runner.Calls() {
		if c.Name == "certbot" && len(c.Args) > 0 && c.Args[0] == "renew" {
			renewArgs = c.Args
		}
	}
	if renewArgs == nil {
		t.Fatalf("a certificate 40 days out with a 45-day window was never sent to certbot; calls: %v",
			runner.Keys())
	}
	var forced bool
	for _, a := range renewArgs {
		if a == "--force-renewal" {
			forced = true
		}
	}
	if !forced {
		t.Errorf("ratline decided this was due, so certbot must be told to act: %v", renewArgs)
	}
}
