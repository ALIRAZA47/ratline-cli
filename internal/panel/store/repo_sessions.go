package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// CreateSession records a signed-in browser.
//
// The session token is stored only as a hash; the CSRF token is stored as itself,
// because the browser has to be able to ask for it again after a reload.
func (s *Store) CreateSession(ctx context.Context, sess *Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, account_id, csrf_token, created_at, last_seen_at,
			expires_at, ip, user_agent) VALUES (?,?,?,?,?,?,?,?)`,
		sess.TokenHash, sess.AccountID, sess.CSRFToken, formatTime(sess.CreatedAt),
		formatTime(sess.LastSeenAt), formatTime(sess.ExpiresAt), sess.IP, sess.UserAgent)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "creating the session")
	}
	return nil
}

// FindSession looks a session up by the hash of its token.
func (s *Store) FindSession(ctx context.Context, tokenHash string) (*Session, error) {
	var (
		sess                   Session
		created, seen, expires string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT token_hash, account_id, csrf_token, created_at, last_seen_at, expires_at,
			ip, user_agent FROM sessions WHERE token_hash = ?`, tokenHash).
		Scan(&sess.TokenHash, &sess.AccountID, &sess.CSRFToken, &created, &seen, &expires,
			&sess.IP, &sess.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("session", "the supplied cookie")
	}
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the session")
	}
	sess.CreatedAt = parseTime(created)
	sess.LastSeenAt = parseTime(seen)
	sess.ExpiresAt = parseTime(expires)
	return &sess, nil
}

// TouchSession moves a session's idle clock and its absolute expiry together.
//
// Both, not one. An idle timeout that slides a fixed expiry forward is not a maximum
// session lifetime at all — a browser polling every minute would stay signed in for
// ever — so the caller passes the new expiry it has already clamped.
func (s *Store) TouchSession(ctx context.Context, tokenHash string, seen, expires time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE token_hash = ?`,
		formatTime(seen), formatTime(expires), tokenHash)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "refreshing the session")
	}
	return nil
}

// DeleteSession signs one browser out.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "signing out")
	}
	return nil
}

// DeleteSessionsFor signs an account out everywhere.
func (s *Store) DeleteSessionsFor(ctx context.Context, accountID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE account_id = ?`, accountID)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "signing the account out")
	}
	return nil
}

// ListSessionsFor returns an account's live sessions, newest first.
func (s *Store) ListSessionsFor(ctx context.Context, accountID string, at time.Time) ([]*Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT token_hash, account_id, csrf_token, created_at, last_seen_at, expires_at,
			ip, user_agent FROM sessions WHERE account_id = ? AND expires_at > ?
		 ORDER BY last_seen_at DESC`, accountID, formatTime(at))
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing sessions")
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		var (
			sess                   Session
			created, seen, expires string
		)
		if err := rows.Scan(&sess.TokenHash, &sess.AccountID, &sess.CSRFToken, &created,
			&seen, &expires, &sess.IP, &sess.UserAgent); err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading a session row")
		}
		sess.CreatedAt = parseTime(created)
		sess.LastSeenAt = parseTime(seen)
		sess.ExpiresAt = parseTime(expires)
		out = append(out, &sess)
	}
	return out, rows.Err()
}

// PurgeExpiredSessions removes sessions past their expiry.
func (s *Store) PurgeExpiredSessions(ctx context.Context, at time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, formatTime(at))
	if err != nil {
		return 0, rlerr.Wrap(err, rlerr.CodeGeneric, "purging expired sessions")
	}
	n, _ := res.RowsAffected()
	return n, nil
}
