package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

const keyColumns = `id, label, fingerprint, algorithm, bits, blob, comment,
	scope, owner, site, options, source, allow_shell, sftp_only, from_cidr, command,
	added_at, added_by, expires_at, last_used_at, last_used_ip, revoked_at`

// NewKeyID mints the short identifier that appears in authorized_keys comments,
// so a line in the file can be traced back to a state row.
func NewKeyID() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "k_000000"
	}
	return "k_" + hex.EncodeToString(b[:])
}

func scanKey(row interface{ Scan(...any) error }) (*Key, error) {
	var (
		k                                 Key
		allowShell, sftpOnly              int
		fromCIDR                          string
		added, expires, lastUsed, revoked string
	)
	err := row.Scan(&k.ID, &k.Label, &k.Fingerprint, &k.Algorithm, &k.Bits, &k.Blob, &k.Comment,
		&k.Scope, &k.Owner, &k.Site, &k.Options, &k.Source, &allowShell, &sftpOnly, &fromCIDR, &k.Command,
		&added, &k.AddedBy, &expires, &lastUsed, &k.LastUsedIP, &revoked)
	if err != nil {
		return nil, err
	}
	k.AllowShell = allowShell == 1
	k.SFTPOnly = sftpOnly == 1
	k.FromCIDR = splitList(fromCIDR)
	k.AddedAt = parseTime(added)
	k.ExpiresAt = parseTime(expires)
	k.LastUsedAt = parseTime(lastUsed)
	k.RevokedAt = parseTime(revoked)
	return &k, nil
}

// PutKey inserts or updates a key row.
func (s *Store) PutKey(ctx context.Context, k *Key) error {
	if k.ID == "" {
		k.ID = NewKeyID()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ssh_keys (`+keyColumns+`)
		VALUES (?,?,?,?,?,?,?, ?,?,?,?,?,?,?,?,?, ?,?,?,?,?,?)
		ON CONFLICT(fingerprint, scope, owner, site) DO UPDATE SET
			label=excluded.label, options=excluded.options, allow_shell=excluded.allow_shell,
			sftp_only=excluded.sftp_only, from_cidr=excluded.from_cidr, command=excluded.command,
			expires_at=excluded.expires_at, revoked_at=excluded.revoked_at`,
		k.ID, k.Label, k.Fingerprint, k.Algorithm, k.Bits, k.Blob, k.Comment,
		k.Scope, k.Owner, k.Site, k.Options, k.Source, boolToInt(k.AllowShell), boolToInt(k.SFTPOnly),
		joinList(k.FromCIDR), k.Command,
		orNow(formatTime(k.AddedAt)), k.AddedBy, formatTime(k.ExpiresAt),
		formatTime(k.LastUsedAt), k.LastUsedIP, formatTime(k.RevokedAt))
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the key %s", k.Fingerprint)
	}
	return nil
}

// KeyFilter narrows a key listing.
type KeyFilter struct {
	Scope          string
	Owner          string
	Site           string
	Fingerprint    string
	IncludeRevoked bool
}

// ListKeys returns keys matching the filter, newest first.
func (s *Store) ListKeys(ctx context.Context, f KeyFilter) ([]*Key, error) {
	q := `SELECT ` + keyColumns + ` FROM ssh_keys`
	var (
		where []string
		args  []any
	)
	if f.Scope != "" {
		where = append(where, "scope = ?")
		args = append(args, f.Scope)
	}
	if f.Owner != "" {
		where = append(where, "owner = ?")
		args = append(args, f.Owner)
	}
	if f.Site != "" {
		where = append(where, "site = ?")
		args = append(args, f.Site)
	}
	if f.Fingerprint != "" {
		where = append(where, "fingerprint = ?")
		args = append(args, f.Fingerprint)
	}
	if !f.IncludeRevoked {
		where = append(where, "revoked_at = ''")
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY added_at DESC, label"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, scanError(err, "ssh_keys")
	}
	defer rows.Close()
	var out []*Key
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, scanError(err, "ssh_keys")
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// FindKeys resolves an operator-supplied fingerprint, key id or label into
// however many keys match. A label may legitimately match several rows, because
// the same laptop key can be added at more than one scope, so callers decide
// whether an ambiguous match is an error.
func (s *Store) FindKeys(ctx context.Context, needle string, f KeyFilter) ([]*Key, error) {
	f.Fingerprint = ""
	all, err := s.ListKeys(ctx, f)
	if err != nil {
		return nil, err
	}
	var out []*Key
	for _, k := range all {
		if k.Fingerprint == needle || k.ID == needle || strings.EqualFold(k.Label, needle) {
			out = append(out, k)
			continue
		}
		// A fingerprint prefix is enough, since operators copy the first few
		// characters out of a listing.
		if len(needle) >= 8 && strings.HasPrefix(k.Fingerprint, needle) {
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		return nil, notFound("key", needle)
	}
	return out, nil
}

// DeleteKey removes one key row by id.
func (s *Store) DeleteKey(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM ssh_keys WHERE id = ?`, id)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the key %s", id)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("key", id)
	}
	return nil
}

// RevokeKey marks a key revoked without deleting the record, so the audit trail
// keeps showing that it once existed.
func (s *Store) RevokeKey(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ssh_keys SET revoked_at = ? WHERE id = ?`, now(), id)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "revoking the key %s", id)
	}
	return nil
}

// FingerprintLocations lists every scope a fingerprint already appears in. The
// same key in two scopes makes the audit trail meaningless, so `key add` reports
// these before refusing.
func (s *Store) FingerprintLocations(ctx context.Context, fingerprint string) ([]*Key, error) {
	return s.ListKeys(ctx, KeyFilter{Fingerprint: fingerprint, IncludeRevoked: true})
}

// CountKeysInScope counts live keys in a scope, which is how the last global
// credential is protected.
func (s *Store) CountKeysInScope(ctx context.Context, scope, owner, site string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM ssh_keys
		WHERE scope = ? AND owner = ? AND site = ? AND revoked_at = ''
		  AND (expires_at = '' OR expires_at > ?)`,
		scope, owner, site, now()).Scan(&n)
	if err != nil {
		return 0, scanError(err, "ssh_keys")
	}
	return n, nil
}

// RecordKeyUsage stores an accepted-publickey observation and advances the key's
// last-used fields. Usage is scraped from the auth log opportunistically, so the
// same line may be seen twice; the unique constraint absorbs that.
func (s *Store) RecordKeyUsage(ctx context.Context, fingerprint string, at time.Time, ip, method string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO key_usage (fingerprint, used_at, remote_ip, method)
			VALUES (?,?,?,?)`, fingerprint, formatTime(at), ip, method)
		if err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "recording key usage")
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE ssh_keys SET last_used_at = ?, last_used_ip = ?
			WHERE fingerprint = ? AND (last_used_at = '' OR last_used_at < ?)`,
			formatTime(at), ip, fingerprint, formatTime(at))
		if err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "updating the last-used time")
		}
		return nil
	})
}

// LastKeyUsageScan reports when the auth log was last read, so a scan can pick
// up where it left off instead of re-reading rotated logs.
func (s *Store) LastKeyUsageScan(ctx context.Context) (time.Time, error) {
	v, err := s.GetServerValue(ctx, "last_key_usage_scan")
	if err != nil {
		return time.Time{}, err
	}
	return parseTime(v), nil
}

// SetLastKeyUsageScan records the scan watermark.
func (s *Store) SetLastKeyUsageScan(ctx context.Context, at time.Time) error {
	return s.SetServerValue(ctx, "last_key_usage_scan", formatTime(at))
}

// GetServerValue reads a cached host fact.
func (s *Store) GetServerValue(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM server WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", scanError(err, "server")
	}
	return v, nil
}

// SetServerValue caches a host fact, such as the detected public addresses.
func (s *Store) SetServerValue(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO server (key, value) VALUES (?,?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "caching the server value %s", key)
	}
	return nil
}
