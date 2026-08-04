package tls

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// ImportOptions is the resolved form of `ratline cert import`.
type ImportOptions struct {
	Domain    string
	CertPath  string
	KeyPath   string
	ChainPath string
	Attach    bool
}

// Import installs a third-party certificate.
//
// Every check runs before anything is written, and each failure names its own
// specific reason. "Import failed" is useless; "the private key does not match
// this certificate" tells the operator they have the wrong pair of files.
func (m *Manager) Import(ctx context.Context, opts ImportOptions) (*state.Certificate, error) {
	domain, err := validate.Domain(opts.Domain)
	if err != nil {
		return nil, err
	}

	leaf, chain, err := ParsePEM(opts.CertPath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := system.ReadFileLimit(opts.KeyPath, maxCertBytes)
	if err != nil {
		return nil, err
	}
	if err := checkKeyMatches(leaf, keyPEM); err != nil {
		return nil, err
	}

	if opts.ChainPath != "" {
		extra, _, err := ParsePEM(opts.ChainPath)
		if err != nil {
			return nil, err
		}
		chain = append(chain, extra)
	}

	now := time.Now()
	if leaf.NotAfter.Before(now) {
		return nil, rlerr.Usagef("this certificate expired on %s", leaf.NotAfter.Format("2006-01-02")).
			WithHint("import a current one; ratline will not install an expired certificate")
	}
	if leaf.NotBefore.After(now) {
		return nil, rlerr.Usagef("this certificate is not valid until %s", leaf.NotBefore.Format("2006-01-02"))
	}

	// The SANs have to cover every name the site serves, or browsers will show a
	// name mismatch on some of them.
	sans := SANsOf(leaf)
	wanted := []string{domain}
	if site, err := m.State.FindSiteByName(ctx, domain); err == nil {
		wanted = site.ServerNames()
	}
	var uncovered []string
	for _, name := range wanted {
		if !coveredBy(sans, name) {
			uncovered = append(uncovered, name)
		}
	}
	if len(uncovered) > 0 {
		return nil, rlerr.Usagef("this certificate does not cover %s", strings.Join(uncovered, ", ")).
			WithHint("it covers %s; ask the issuer for a certificate including the missing names",
				strings.Join(sans, ", "))
	}

	// Chain order and trust. A private CA is a legitimate reason to fail this, so
	// it warns rather than refusing.
	pool := x509.NewCertPool()
	for _, c := range chain {
		pool.AddCert(c)
	}
	if _, verr := leaf.Verify(x509.VerifyOptions{
		DNSName: domain, Intermediates: pool, CurrentTime: now,
	}); verr != nil {
		if leaf.Issuer.String() == leaf.Subject.String() {
			m.Log.Warn("this certificate is self-signed, so browsers will not trust it",
				"domain", domain)
		} else {
			m.Log.Warn("the chain does not build to a trusted root; browsers may reject it",
				"reason", firstLine(verr.Error()),
				"note", "this is expected for a private CA or a Cloudflare Origin certificate")
		}
	}
	if len(chain) == 0 && leaf.Issuer.String() != leaf.Subject.String() {
		m.Log.Warn("no intermediate certificates were supplied, which breaks some clients",
			"fix", "pass the full chain as --cert, or the intermediates as --chain")
	}

	// Write only after everything has passed.
	dir := m.ImportedDir(domain)
	if m.DryRun {
		m.Log.Info("would install the imported certificate", "dir", dir)
		return FromX509(domain, state.CertSourceImported, leaf, filepath.Join(dir, "fullchain.pem"),
			filepath.Join(dir, "privkey.pem"), ""), nil
	}
	if _, err := system.EnsureDir(m.Cfg.Paths.ImportedCerts, 0o700, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return nil, err
	}
	if _, err := system.EnsureDir(dir, 0o700, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return nil, err
	}

	// fullchain.pem is the leaf plus the intermediates, which is what nginx wants
	// in ssl_certificate.
	var full bytes.Buffer
	for _, c := range append([]*x509.Certificate{leaf}, chain...) {
		if err := pem.Encode(&full, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}); err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "encoding the certificate chain")
		}
	}
	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	if err := system.WriteFileAtomic(certPath, full.Bytes(), 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return nil, err
	}
	// 0600 root:root. A private key never goes anywhere nginx could serve it and
	// never into a tenant's home.
	if err := system.WriteFileAtomic(keyPath, keyPEM, 0o600, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return nil, err
	}
	chainPath := ""
	if len(chain) > 0 {
		var chainBuf bytes.Buffer
		for _, c := range chain {
			if err := pem.Encode(&chainBuf, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}); err != nil {
				return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "encoding the chain")
			}
		}
		chainPath = filepath.Join(dir, "chain.pem")
		if err := system.WriteFileAtomic(chainPath, chainBuf.Bytes(), 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
			return nil, err
		}
	}
	meta := fmt.Sprintf("{\n  \"source\": \"imported\",\n  \"imported_at\": %q,\n  \"fingerprint\": %q,\n  \"imported_by\": %q\n}\n",
		now.UTC().Format(time.RFC3339), Fingerprint(leaf), m.Invoker)
	if err := system.WriteFileAtomic(filepath.Join(dir, "meta.json"), []byte(meta), 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return nil, err
	}

	cert := FromX509(domain, state.CertSourceImported, leaf, certPath, keyPath, chainPath)
	// Nothing renews an imported certificate, so auto-renew is off and doctor
	// warns as expiry approaches. Pretending otherwise would be worse.
	cert.AutoRenew = false
	if err := m.State.PutCertificate(ctx, cert); err != nil {
		return nil, err
	}
	m.Log.Info("certificate imported", "domain", domain, "issuer", cert.Issuer,
		"expires", cert.NotAfter.Format("2006-01-02"),
		"note", "nothing will renew this automatically")

	if opts.Attach {
		if err := m.Attach(ctx, domain, domain); err != nil {
			return cert, err
		}
	}
	return cert, nil
}

// checkKeyMatches proves the private key belongs to the certificate.
func checkKeyMatches(leaf *x509.Certificate, keyPEM []byte) error {
	// Checked on the raw bytes, before decoding: an encrypted key's body often
	// fails to decode, and "the file is not PEM" would send the operator looking
	// for the wrong problem. Encryption is the one failure with a single obvious
	// remedy, so it is reported first.
	if isEncryptedKey(keyPEM) {
		return rlerr.Usagef("the private key is passphrase-encrypted").
			WithHint("decrypt it first: openssl rsa -in privkey.pem -out privkey-decrypted.pem " +
				"(or 'openssl ec' for an EC key), then import the decrypted file")
	}

	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return rlerr.Usagef("the private key file does not contain PEM data").
			WithHint("expected a file beginning with -----BEGIN PRIVATE KEY----- or similar")
	}

	var priv any
	var err error
	switch block.Type {
	case "RSA PRIVATE KEY":
		priv, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		priv, err = x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		priv, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	default:
		return rlerr.Usagef("unsupported private key type %q", block.Type)
	}
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeUsage, "the private key does not parse")
	}

	mismatch := rlerr.Usagef("the private key does not match this certificate").
		WithHint("these are two unrelated files; check that both came from the same issuance")

	switch pub := leaf.PublicKey.(type) {
	case *rsa.PublicKey:
		k, ok := priv.(*rsa.PrivateKey)
		if !ok {
			return rlerr.Usagef("the certificate holds an RSA key but the private key is not RSA")
		}
		if k.PublicKey.N.Cmp(pub.N) != 0 || k.PublicKey.E != pub.E {
			return mismatch
		}
	case *ecdsa.PublicKey:
		k, ok := priv.(*ecdsa.PrivateKey)
		if !ok {
			return rlerr.Usagef("the certificate holds an ECDSA key but the private key is not ECDSA")
		}
		if !k.PublicKey.Equal(pub) {
			return mismatch
		}
	case ed25519.PublicKey:
		k, ok := priv.(ed25519.PrivateKey)
		if !ok {
			return rlerr.Usagef("the certificate holds an Ed25519 key but the private key is not Ed25519")
		}
		if !k.Public().(ed25519.PublicKey).Equal(pub) {
			return mismatch
		}
	default:
		return rlerr.Usagef("unsupported public key algorithm in the certificate")
	}
	return nil
}

// isEncryptedKey spots the two ways a PEM private key says it is encrypted:
// the PKCS#8 header type, and OpenSSL's legacy Proc-Type/DEK-Info headers.
func isEncryptedKey(keyPEM []byte) bool {
	head := string(keyPEM)
	if len(head) > 4096 {
		head = head[:4096]
	}
	for _, marker := range []string{"ENCRYPTED PRIVATE KEY", "Proc-Type:", "DEK-Info:"} {
		if strings.Contains(head, marker) {
			return true
		}
	}
	return false
}

func coveredBy(sans []string, name string) bool {
	name = strings.ToLower(name)
	for _, san := range sans {
		san = strings.ToLower(san)
		if san == name {
			return true
		}
		if strings.HasPrefix(san, "*.") {
			host, rest, found := strings.Cut(name, ".")
			if found && rest == san[2:] && host != "" && !strings.Contains(host, ".") {
				return true
			}
		}
	}
	return false
}

// SelfSign creates a placeholder certificate.
//
// This exists so a site can serve HTTPS the moment it is created, before DNS is
// pointed. It is recorded distinctly, never counted as valid, always flagged, and
// replaced cleanly by a real issuance later.
func (m *Manager) SelfSign(ctx context.Context, domain string, days int, attach bool) (*state.Certificate, error) {
	name, err := validate.Domain(domain)
	if err != nil {
		return nil, err
	}
	if days <= 0 {
		days = 365
	}
	names := []string{name}
	if site, err := m.State.FindSiteByName(ctx, name); err == nil {
		names = site.ServerNames()
	}

	if m.DryRun {
		m.Log.Info("would generate a self-signed certificate", "domain", name, "days", days)
		return &state.Certificate{Name: name, Source: state.CertSourceSelfSigned, SANs: names}, nil
	}

	// P-256 rather than RSA: faster, smaller, and universally supported for a
	// placeholder nobody trusts anyway.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "generating a key")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "generating a serial number")
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: name,
			// Named so that anyone who sees it in a browser warning knows exactly
			// what it is and that it was not meant for production.
			Organization: []string{"ratline self-signed placeholder"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(0, 0, days),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              names,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "creating the certificate")
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "parsing the generated certificate")
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "encoding the private key")
	}

	dir := m.ImportedDir(name)
	if _, err := system.EnsureDir(m.Cfg.Paths.ImportedCerts, 0o700, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return nil, err
	}
	if _, err := system.EnsureDir(dir, 0o700, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return nil, err
	}
	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	if err := system.WriteFileAtomic(certPath,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return nil, err
	}
	if err := system.WriteFileAtomic(keyPath,
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return nil, err
	}

	cert := FromX509(name, state.CertSourceSelfSigned, leaf, certPath, keyPath, "")
	cert.AutoRenew = false
	if err := m.State.PutCertificate(ctx, cert); err != nil {
		return nil, err
	}
	m.Log.Warn("this certificate is self-signed and browsers will reject it",
		"domain", name, "expires", cert.NotAfter.Format("2006-01-02"),
		"next", "ratline cert issue "+name+" once DNS points at this server")

	if attach {
		if err := m.Attach(ctx, name, name); err != nil {
			return cert, err
		}
	}
	return cert, nil
}
