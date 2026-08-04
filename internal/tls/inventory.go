package tls

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// maxCertBytes bounds a PEM file. A certificate chain is a few kilobytes; a
// megabyte means something is wrong with the file, not with the limit.
const maxCertBytes = 1 << 20

// ParsePEM reads a certificate file and returns the leaf plus the rest of the
// chain, in the order they appear.
func ParsePEM(path string) (*x509.Certificate, []*x509.Certificate, error) {
	data, err := system.ReadFileLimit(path, maxCertBytes)
	if err != nil {
		return nil, nil, err
	}
	var certs []*x509.Certificate
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, nil, rlerr.Wrap(err, rlerr.CodeUsage, "%s contains a certificate that does not parse", path)
		}
		certs = append(certs, c)
	}
	if len(certs) == 0 {
		return nil, nil, rlerr.Usagef("%s contains no certificate", path).
			WithHint("expected a PEM file beginning with -----BEGIN CERTIFICATE-----")
	}
	return certs[0], certs[1:], nil
}

// Fingerprint is the SHA-256 of the DER form, which is what browsers and
// certificate viewers display.
func Fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return "SHA256:" + hex.EncodeToString(sum[:])
}

// KeyTypeOf names a certificate's public key algorithm.
func KeyTypeOf(c *x509.Certificate) string {
	switch c.PublicKeyAlgorithm {
	case x509.RSA:
		return "rsa"
	case x509.ECDSA:
		return "ecdsa"
	case x509.Ed25519:
		return "ed25519"
	default:
		return strings.ToLower(c.PublicKeyAlgorithm.String())
	}
}

// SANsOf collects every name a certificate covers.
func SANsOf(c *x509.Certificate) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	for _, n := range c.DNSNames {
		add(n)
	}
	// A certificate with no SAN extension is ancient, but the CN is still what a
	// client would check.
	if len(out) == 0 && c.Subject.CommonName != "" {
		add(c.Subject.CommonName)
	}
	for _, ip := range c.IPAddresses {
		add(ip.String())
	}
	return out
}

// FromX509 builds a state row from a parsed certificate.
func FromX509(name, source string, leaf *x509.Certificate, certPath, keyPath, chainPath string) *state.Certificate {
	issuer := leaf.Issuer.CommonName
	if issuer == "" && len(leaf.Issuer.Organization) > 0 {
		issuer = leaf.Issuer.Organization[0]
	}
	if leaf.Issuer.String() == leaf.Subject.String() {
		issuer = "self-signed"
	}
	return &state.Certificate{
		Name:        name,
		Source:      source,
		Issuer:      issuer,
		Serial:      leaf.SerialNumber.String(),
		Fingerprint: Fingerprint(leaf),
		KeyType:     KeyTypeOf(leaf),
		NotBefore:   leaf.NotBefore.UTC(),
		NotAfter:    leaf.NotAfter.UTC(),
		CertPath:    certPath,
		KeyPath:     keyPath,
		ChainPath:   chainPath,
		SANs:        SANsOf(leaf),
		AutoRenew:   source == state.CertSourceLetsEncrypt || source == state.CertSourceStaging,
	}
}

// ScanResult is what a filesystem scan found.
type ScanResult struct {
	Discovered []*state.Certificate `json:"discovered"`
	Adopted    []string             `json:"adopted,omitempty"`
	Missing    []string             `json:"missing,omitempty"`
}

// Scan reads every certificate on disk and reconciles it with state.
//
// Certificates issued by hand with certbot, outside ratline, are exactly the
// residue an operator leaves behind, and pretending they are not there does not
// help anyone. They are adopted into state so that `cert list` shows the whole
// truth about the machine and expiry warnings cover them too.
func (m *Manager) Scan(ctx context.Context) (*ScanResult, error) {
	res := &ScanResult{}
	known := map[string]bool{}
	existing, err := m.State.ListCertificates(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range existing {
		known[c.Name] = true
	}

	for _, found := range append(m.scanLetsEncrypt(), m.scanImported()...) {
		res.Discovered = append(res.Discovered, found)
		if !known[found.Name] {
			res.Adopted = append(res.Adopted, found.Name)
		}
		// Preserve the renewal bookkeeping and attachments a rescan cannot know
		// about, while refreshing everything read from the file itself.
		if prior, err := m.State.GetCertificate(ctx, found.Name); err == nil {
			found.AutoRenew = prior.AutoRenew
			found.Challenge = prior.Challenge
			found.DNSProvider = prior.DNSProvider
			found.LastRenewalAt = prior.LastRenewalAt
			found.LastRenewalStatus = prior.LastRenewalStatus
			found.LastRenewalError = prior.LastRenewalError
			found.ConsecutiveFailures = prior.ConsecutiveFailures
			found.CreatedAt = prior.CreatedAt
			// A staging certificate stays marked as staging even after a rescan:
			// the file cannot tell you which endpoint issued it.
			if prior.Source == state.CertSourceStaging && found.Source == state.CertSourceLetsEncrypt {
				found.Source = state.CertSourceStaging
			}
		}
		if m.DryRun {
			continue
		}
		if err := m.State.PutCertificate(ctx, found); err != nil {
			return nil, err
		}
		delete(known, found.Name)
	}

	// Whatever is left in state has no files, so it was removed behind ratline's
	// back. Reported rather than deleted: an unmounted volume looks the same.
	for name := range known {
		c, err := m.State.GetCertificate(ctx, name)
		if err != nil || c.Source == state.CertSourceSelfSigned {
			continue
		}
		if c.CertPath != "" && !system.Exists(c.CertPath) {
			res.Missing = append(res.Missing, name)
		}
	}
	return res, nil
}

// scanLetsEncrypt reads /etc/letsencrypt/live.
func (m *Manager) scanLetsEncrypt() []*state.Certificate {
	liveDir := filepath.Join(m.Cfg.Paths.LetsEncryptDir, "live")
	entries, err := os.ReadDir(liveDir)
	if err != nil {
		return nil
	}
	var out []*state.Certificate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(liveDir, e.Name())
		certPath := filepath.Join(dir, "fullchain.pem")
		if !system.Exists(certPath) {
			continue
		}
		leaf, _, err := ParsePEM(certPath)
		if err != nil {
			m.Log.Debug("skipping an unreadable certificate", "path", certPath, "err", err)
			continue
		}
		source := state.CertSourceLetsEncrypt
		// A staging certificate's issuer says so, which is the only signal
		// available from the file alone.
		if isStagingIssuer(leaf.Issuer.CommonName) {
			source = state.CertSourceStaging
		}
		out = append(out, FromX509(e.Name(), source, leaf, certPath,
			filepath.Join(dir, "privkey.pem"), filepath.Join(dir, "chain.pem")))
	}
	return out
}

// scanImported reads /etc/ratline/certs.
func (m *Manager) scanImported() []*state.Certificate {
	entries, err := os.ReadDir(m.Cfg.Paths.ImportedCerts)
	if err != nil {
		return nil
	}
	var out []*state.Certificate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(m.Cfg.Paths.ImportedCerts, e.Name())
		certPath := filepath.Join(dir, "fullchain.pem")
		if !system.Exists(certPath) {
			continue
		}
		leaf, _, err := ParsePEM(certPath)
		if err != nil {
			continue
		}
		source := state.CertSourceImported
		if leaf.Issuer.String() == leaf.Subject.String() {
			source = state.CertSourceSelfSigned
		}
		chain := filepath.Join(dir, "chain.pem")
		if !system.Exists(chain) {
			chain = ""
		}
		out = append(out, FromX509(e.Name(), source, leaf, certPath,
			filepath.Join(dir, "privkey.pem"), chain))
	}
	return out
}

func isStagingIssuer(cn string) bool {
	lower := strings.ToLower(cn)
	for _, marker := range []string{"staging", "fake le", "pebble", "happy hacker"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// ListRow is one line of `cert list`.
type ListRow struct {
	*state.Certificate
	Status Status `json:"status"`
	Days   int    `json:"days_remaining"`
}

// List returns every certificate with its computed status, refreshing from disk
// first so the answer reflects the machine rather than the index.
func (m *Manager) List(ctx context.Context, expiringWithin int, orphanedOnly bool) ([]*ListRow, error) {
	if _, err := m.Scan(ctx); err != nil {
		m.Log.Debug("the certificate scan failed; reporting from state alone", "err", err)
	}
	certs, err := m.State.ListCertificates(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var out []*ListRow
	for _, c := range certs {
		row := &ListRow{Certificate: c, Status: StatusOf(c, now), Days: c.DaysRemaining(now)}
		if expiringWithin > 0 && row.Days > expiringWithin {
			continue
		}
		if orphanedOnly && len(c.Attached) > 0 {
			continue
		}
		// A certificate attached to a site whose names it no longer covers is the
		// failure an operator would otherwise find in a browser.
		for _, domain := range c.Attached {
			if site, err := m.State.FindSiteByName(ctx, domain); err == nil {
				for _, name := range site.ServerNames() {
					if !c.Covers(name) {
						row.Status = StatusMismatch
					}
				}
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// Show is the detail behind `cert show`.
type Show struct {
	*ListRow
	RenewalLog string `json:"renewal_log,omitempty"`
	NextRenew  string `json:"next_renewal,omitempty"`
	Trusted    bool   `json:"trusted"`
}

// Show gathers everything about one certificate.
func (m *Manager) Show(ctx context.Context, name string) (*Show, error) {
	if _, err := m.Scan(ctx); err != nil {
		m.Log.Debug("the certificate scan failed", "err", err)
	}
	cert, err := m.State.GetCertificate(ctx, name)
	if err != nil {
		// An operator types a domain, which may be an alias or a SAN rather than
		// the lineage name.
		if resolved, rerr := m.resolveByName(ctx, name); rerr == nil {
			cert = resolved
		} else {
			return nil, err
		}
	}
	now := time.Now()
	s := &Show{
		ListRow: &ListRow{Certificate: cert, Status: StatusOf(cert, now), Days: cert.DaysRemaining(now)},
		Trusted: cert.Trusted(),
	}
	if cert.AutoRenew && !cert.NotAfter.IsZero() {
		renewAt := cert.NotAfter.AddDate(0, 0, -m.Cfg.ACME.RenewBeforeDays)
		if renewAt.Before(now) {
			s.NextRenew = "due now"
		} else {
			s.NextRenew = renewAt.Format("2006-01-02")
		}
	}
	if cert.LastRenewalError != "" {
		s.RenewalLog = cert.LastRenewalError
	}
	return s, nil
}

// resolveByName finds a certificate by a name it covers.
func (m *Manager) resolveByName(ctx context.Context, name string) (*state.Certificate, error) {
	certs, err := m.State.ListCertificates(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range certs {
		if c.Covers(name) {
			return c, nil
		}
		for _, d := range c.Attached {
			if d == name {
				return c, nil
			}
		}
	}
	return nil, rlerr.Wrap(state.ErrNotFound, rlerr.CodePrecondition, "no certificate covers %s", name)
}

// SANSummary shortens a SAN list for a table column.
func SANSummary(sans []string, max int) string {
	if len(sans) <= max {
		return strings.Join(sans, ",")
	}
	return fmt.Sprintf("%s,+%d more", strings.Join(sans[:max], ","), len(sans)-max)
}
