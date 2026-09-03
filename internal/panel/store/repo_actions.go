package store

import (
	"context"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// RecordAction appends to the panel's own trail.
//
// ratline writes its own audit entry for the command that ran, but every panel
// invocation reaches it as root — so ratline can say what happened and this says who
// asked. Read together they are the whole record; either alone is half of one.
func (s *Store) RecordAction(ctx context.Context, rec *ActionRecord) error {
	if rec.At.IsZero() {
		rec.At = time.Now().UTC()
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO actions (at, actor_id, actor, action, argv, target, dry_run, ok,
			exit_code, error, duration_ms, ip) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		formatTime(rec.At), rec.ActorID, rec.Actor, rec.Action, rec.Argv, rec.Target,
		boolToInt(rec.DryRun), boolToInt(rec.OK), rec.ExitCode, rec.Error,
		rec.DurationMS, rec.IP)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the action")
	}
	if id, err := res.LastInsertId(); err == nil {
		rec.ID = id
	}
	return nil
}

// ActionFilter narrows a listing.
type ActionFilter struct {
	ActorID string
	Action  string
	Target  string
	// FailedOnly is what somebody actually wants when they open the log after
	// something went wrong.
	FailedOnly bool
	Limit      int
	Before     int64
}

// ListActions returns the trail, newest first.
func (s *Store) ListActions(ctx context.Context, f ActionFilter) ([]*ActionRecord, error) {
	q := `SELECT id, at, actor_id, actor, action, argv, target, dry_run, ok, exit_code,
		error, duration_ms, ip FROM actions WHERE 1=1`
	var args []any
	if f.ActorID != "" {
		q += ` AND actor_id = ?`
		args = append(args, f.ActorID)
	}
	if f.Action != "" {
		q += ` AND action = ?`
		args = append(args, f.Action)
	}
	if f.Target != "" {
		q += ` AND target = ?`
		args = append(args, f.Target)
	}
	if f.FailedOnly {
		q += ` AND ok = 0`
	}
	if f.Before > 0 {
		q += ` AND id < ?`
		args = append(args, f.Before)
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing the action log")
	}
	defer rows.Close()
	var out []*ActionRecord
	for rows.Next() {
		var (
			r          ActionRecord
			at         string
			dryRun, ok int
		)
		if err := rows.Scan(&r.ID, &at, &r.ActorID, &r.Actor, &r.Action, &r.Argv, &r.Target,
			&dryRun, &ok, &r.ExitCode, &r.Error, &r.DurationMS, &r.IP); err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading an action row")
		}
		r.At = parseTime(at)
		r.DryRun = dryRun == 1
		r.OK = ok == 1
		out = append(out, &r)
	}
	return out, rows.Err()
}

// PurgeActions trims the trail to the newest keep rows.
func (s *Store) PurgeActions(ctx context.Context, keep int) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM actions WHERE id NOT IN (SELECT id FROM actions ORDER BY id DESC LIMIT ?)`,
		keep)
	if err != nil {
		return 0, rlerr.Wrap(err, rlerr.CodeGeneric, "trimming the action log")
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// RecordLoginAttempt appends to the rate-limiting window.
func (s *Store) RecordLoginAttempt(ctx context.Context, email, ip string, ok bool, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO login_attempts (at, email, ip, ok) VALUES (?,?,?,?)`,
		formatTime(at), NormalizeEmail(email), ip, boolToInt(ok))
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the sign-in attempt")
	}
	return nil
}

// FailedLoginsSince counts recent failures for an address and for a source together.
//
// Both, because either alone is trivially defeated: counting per account lets a
// password-spraying run try one password against every account, and counting per
// address lets a distributed one through. The caller refuses if either is over.
func (s *Store) FailedLoginsSince(ctx context.Context, email, ip string, since time.Time) (byEmail, byIP int, err error) {
	ts := formatTime(since)
	if email != "" {
		if err = s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM login_attempts WHERE ok = 0 AND email = ? AND at > ?`,
			NormalizeEmail(email), ts).Scan(&byEmail); err != nil {
			return 0, 0, rlerr.Wrap(err, rlerr.CodeGeneric, "counting sign-in attempts")
		}
	}
	if ip != "" {
		if err = s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM login_attempts WHERE ok = 0 AND ip = ? AND at > ?`,
			ip, ts).Scan(&byIP); err != nil {
			return 0, 0, rlerr.Wrap(err, rlerr.CodeGeneric, "counting sign-in attempts")
		}
	}
	return byEmail, byIP, nil
}

// ClearLoginAttempts forgets the failures for an address, called on a success so that
// somebody who mistyped their password four times is not locked out afterwards.
func (s *Store) ClearLoginAttempts(ctx context.Context, email string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM login_attempts WHERE email = ?`,
		NormalizeEmail(email))
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "clearing the sign-in attempts")
	}
	return nil
}

// PurgeLoginAttempts drops attempts older than the rate-limiting window.
func (s *Store) PurgeLoginAttempts(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM login_attempts WHERE at <= ?`,
		formatTime(before))
	if err != nil {
		return 0, rlerr.Wrap(err, rlerr.CodeGeneric, "purging sign-in attempts")
	}
	n, _ := res.RowsAffected()
	return n, nil
}
