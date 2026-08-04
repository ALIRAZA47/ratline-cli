package state

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

const certColumns = `name, lineage, source, issuer, serial, fingerprint, key_type,
	not_before, not_after, challenge, dns_provider, auto_renew,
	cert_path, key_path, chain_path,
	last_renewal_at, last_renewal_status, last_renewal_error, consecutive_failures,
	created_at, updated_at`

func scanCert(row interface{ Scan(...any) error }) (*Certificate, error) {
	var (
		c                              Certificate
		autoRenew                      int
		notBefore, notAfter, renewedAt string
		created, updated               string
	)
	err := row.Scan(&c.Name, &c.Lineage, &c.Source, &c.Issuer, &c.Serial, &c.Fingerprint, &c.KeyType,
		&notBefore, &notAfter, &c.Challenge, &c.DNSProvider, &autoRenew,
		&c.CertPath, &c.KeyPath, &c.ChainPath,
		&renewedAt, &c.LastRenewalStatus, &c.LastRenewalError, &c.ConsecutiveFailures,
		&created, &updated)
	if err != nil {
		return nil, err
	}
	c.AutoRenew = autoRenew == 1
	c.NotBefore = parseTime(notBefore)
	c.NotAfter = parseTime(notAfter)
	c.LastRenewalAt = parseTime(renewedAt)
	c.CreatedAt = parseTime(created)
	c.UpdatedAt = parseTime(updated)
	return &c, nil
}

// PutCertificate inserts or updates a certificate and replaces its SAN list.
func (s *Store) PutCertificate(ctx context.Context, c *Certificate) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO certificates (`+certColumns+`)
			VALUES (?,?,?,?,?,?,?, ?,?,?,?,?, ?,?,?, ?,?,?,?, ?,?)
			ON CONFLICT(name) DO UPDATE SET
				lineage=excluded.lineage, source=excluded.source, issuer=excluded.issuer,
				serial=excluded.serial, fingerprint=excluded.fingerprint, key_type=excluded.key_type,
				not_before=excluded.not_before, not_after=excluded.not_after,
				challenge=excluded.challenge, dns_provider=excluded.dns_provider,
				auto_renew=excluded.auto_renew, cert_path=excluded.cert_path,
				key_path=excluded.key_path, chain_path=excluded.chain_path,
				last_renewal_at=excluded.last_renewal_at,
				last_renewal_status=excluded.last_renewal_status,
				last_renewal_error=excluded.last_renewal_error,
				consecutive_failures=excluded.consecutive_failures,
				updated_at=excluded.updated_at`,
			c.Name, c.Lineage, c.Source, c.Issuer, c.Serial, c.Fingerprint, c.KeyType,
			formatTime(c.NotBefore), formatTime(c.NotAfter), c.Challenge, c.DNSProvider, boolToInt(c.AutoRenew),
			c.CertPath, c.KeyPath, c.ChainPath,
			formatTime(c.LastRenewalAt), c.LastRenewalStatus, c.LastRenewalError, c.ConsecutiveFailures,
			orNow(formatTime(c.CreatedAt)), now())
		if err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the certificate %s", c.Name)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM cert_sans WHERE cert = ?`, c.Name); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "replacing the SANs for %s", c.Name)
		}
		for _, san := range c.SANs {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO cert_sans (cert, san) VALUES (?,?)`, c.Name, san); err != nil {
				return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the SAN %s", san)
			}
		}
		return nil
	})
}

// GetCertificate looks up one certificate with its SANs and attachments.
func (s *Store) GetCertificate(ctx context.Context, name string) (*Certificate, error) {
	c, err := scanCert(s.db.QueryRowContext(ctx, `SELECT `+certColumns+` FROM certificates WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("certificate", name)
	}
	if err != nil {
		return nil, scanError(err, "certificates")
	}
	if err := s.loadCertLists(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ListCertificates returns every certificate, soonest expiry first, so the ones
// that need attention are at the top.
func (s *Store) ListCertificates(ctx context.Context) ([]*Certificate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+certColumns+` FROM certificates ORDER BY not_after, name`)
	if err != nil {
		return nil, scanError(err, "certificates")
	}
	defer rows.Close()
	var out []*Certificate
	for rows.Next() {
		c, err := scanCert(rows)
		if err != nil {
			return nil, scanError(err, "certificates")
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, scanError(err, "certificates")
	}
	for _, c := range out {
		if err := s.loadCertLists(ctx, c); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) loadCertLists(ctx context.Context, c *Certificate) error {
	sans, err := s.queryStrings(ctx, `SELECT san FROM cert_sans WHERE cert = ? ORDER BY san`, c.Name)
	if err != nil {
		return err
	}
	c.SANs = sans
	attached, err := s.queryStrings(ctx, `SELECT domain FROM cert_attachments WHERE cert = ? ORDER BY domain`, c.Name)
	if err != nil {
		return err
	}
	c.Attached = attached
	return nil
}

func (s *Store) queryStrings(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "querying the state database")
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading a row")
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// AttachCertificate records that a site's vhost points at a certificate.
func (s *Store) AttachCertificate(ctx context.Context, certName, domain string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cert_attachments (cert, domain, attached_at) VALUES (?,?,?)
		ON CONFLICT(cert, domain) DO UPDATE SET attached_at = excluded.attached_at`,
		certName, domain, now())
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "attaching %s to %s", certName, domain)
	}
	return nil
}

// DetachCertificate removes every attachment for a domain.
func (s *Store) DetachCertificate(ctx context.Context, domain string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM cert_attachments WHERE domain = ?`, domain); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "detaching the certificate from %s", domain)
	}
	return nil
}

// CertificateForSite returns the certificate attached to a domain, if any.
func (s *Store) CertificateForSite(ctx context.Context, domain string) (*Certificate, error) {
	var name string
	err := s.db.QueryRowContext(ctx,
		`SELECT cert FROM cert_attachments WHERE domain = ? ORDER BY attached_at DESC LIMIT 1`, domain).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("certificate for site", domain)
	}
	if err != nil {
		return nil, scanError(err, "cert_attachments")
	}
	return s.GetCertificate(ctx, name)
}

// DeleteCertificate removes a certificate and its SANs and attachments.
func (s *Store) DeleteCertificate(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM certificates WHERE name = ?`, name); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the certificate %s", name)
	}
	return nil
}

// RecordRenewal updates the renewal bookkeeping. Consecutive failures drive the
// backoff and the `degraded` status in `cert list`.
func (s *Store) RecordRenewal(ctx context.Context, name, status, errMsg string) error {
	failures := 0
	if status != "success" {
		c, err := s.GetCertificate(ctx, name)
		if err == nil {
			failures = c.ConsecutiveFailures + 1
		} else {
			failures = 1
		}
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE certificates
		SET last_renewal_at = ?, last_renewal_status = ?, last_renewal_error = ?,
		    consecutive_failures = ?, updated_at = ?
		WHERE name = ?`, now(), status, errMsg, failures, now(), name)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the renewal of %s", name)
	}
	return nil
}

// SetAutoRenew turns automatic renewal on or off for one certificate.
func (s *Store) SetAutoRenew(ctx context.Context, name string, on bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE certificates SET auto_renew = ?, updated_at = ? WHERE name = ?`, boolToInt(on), now(), name)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "updating %s", name)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("certificate", name)
	}
	return nil
}

// RecordACMEAttempt stores one issuance attempt, successful or not. Failures
// matter as much as successes: the CA counts failed validations too.
func (s *Store) RecordACMEAttempt(ctx context.Context, a *ACMEAttempt) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO acme_attempts (registered_domain, domain, san_set, attempted_at, outcome, error_class, staging)
		VALUES (?,?,?,?,?,?,?)`,
		a.RegisteredDomain, a.Domain, a.SANSet, orNow(formatTime(a.AttemptedAt)),
		a.Outcome, a.ErrorClass, boolToInt(a.Staging))
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the ACME attempt for %s", a.Domain)
	}
	return nil
}

// ACMEUsage is the rate-limit picture for one registered domain.
type ACMEUsage struct {
	RegisteredDomain   string
	CertsThisWeek      int
	DuplicatesThisWeek int
	FailuresThisHour   int
	OrdersLast3Hours   int
	OldestThisWeek     time.Time
	OldestFailure      time.Time
	OldestOrder        time.Time
}

// ACMEUsageFor computes the attempts that count against each published limit.
//
// Staging attempts are excluded: they are rate-limited separately and much more
// generously, which is exactly why operators are pointed at them while testing.
func (s *Store) ACMEUsageFor(ctx context.Context, registeredDomain, sanSet string, at time.Time) (*ACMEUsage, error) {
	u := &ACMEUsage{RegisteredDomain: registeredDomain}
	weekAgo := formatTime(at.Add(-7 * 24 * time.Hour))
	hourAgo := formatTime(at.Add(-time.Hour))
	threeHoursAgo := formatTime(at.Add(-3 * time.Hour))

	q := func(dest *int, oldest *time.Time, query string, args ...any) error {
		var (
			count int
			min   sql.NullString
		)
		if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count, &min); err != nil {
			return scanError(err, "acme_attempts")
		}
		*dest = count
		if oldest != nil && min.Valid {
			*oldest = parseTime(min.String)
		}
		return nil
	}

	if err := q(&u.CertsThisWeek, &u.OldestThisWeek, `
		SELECT COUNT(*), MIN(attempted_at) FROM acme_attempts
		WHERE registered_domain = ? AND outcome = ? AND staging = 0 AND attempted_at > ?`,
		registeredDomain, ACMESuccess, weekAgo); err != nil {
		return nil, err
	}
	if sanSet != "" {
		if err := q(&u.DuplicatesThisWeek, nil, `
			SELECT COUNT(*), MIN(attempted_at) FROM acme_attempts
			WHERE registered_domain = ? AND san_set = ? AND outcome = ? AND staging = 0 AND attempted_at > ?`,
			registeredDomain, sanSet, ACMESuccess, weekAgo); err != nil {
			return nil, err
		}
	}
	if err := q(&u.FailuresThisHour, &u.OldestFailure, `
		SELECT COUNT(*), MIN(attempted_at) FROM acme_attempts
		WHERE registered_domain = ? AND outcome = ? AND staging = 0 AND attempted_at > ?`,
		registeredDomain, ACMEFailure, hourAgo); err != nil {
		return nil, err
	}
	if err := q(&u.OrdersLast3Hours, &u.OldestOrder, `
		SELECT COUNT(*), MIN(attempted_at) FROM acme_attempts
		WHERE staging = 0 AND attempted_at > ?`, threeHoursAgo); err != nil {
		return nil, err
	}
	return u, nil
}

// SANSetKey is the canonical identity of a certificate request, used to detect a
// duplicate: the same set of names in any order is one duplicate certificate as
// far as the CA is concerned.
func SANSetKey(names []string) string {
	lower := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		lower = append(lower, n)
	}
	sortStrings(lower)
	return strings.Join(lower, ",")
}

func sortStrings(v []string) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}
