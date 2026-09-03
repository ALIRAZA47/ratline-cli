package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// CreateInvite records an invitation. The caller holds the only copy of the token.
func (s *Store) CreateInvite(ctx context.Context, inv *Invite, tokenHash string) error {
	inv.Email = NormalizeEmail(inv.Email)
	if !ValidRole(inv.Role) {
		return rlerr.Usagef("%q is not a role", inv.Role)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO invites (id, token_hash, email, role, invited_by, created_at, expires_at,
			accepted_at, revoked_at) VALUES (?,?,?,?,?,?,?,'','')`,
		inv.ID, tokenHash, inv.Email, inv.Role, inv.InvitedBy,
		formatTime(inv.CreatedAt), formatTime(inv.ExpiresAt))
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "creating the invitation for %s", inv.Email)
	}
	return nil
}

const inviteColumns = `id, email, role, invited_by, created_at, expires_at, accepted_at, revoked_at`

func scanInvite(sc interface{ Scan(...any) error }) (*Invite, error) {
	var (
		inv                                 Invite
		created, expires, accepted, revoked string
	)
	if err := sc.Scan(&inv.ID, &inv.Email, &inv.Role, &inv.InvitedBy, &created, &expires,
		&accepted, &revoked); err != nil {
		return nil, err
	}
	inv.CreatedAt = parseTime(created)
	inv.ExpiresAt = parseTime(expires)
	inv.AcceptedAt = parseTime(accepted)
	inv.RevokedAt = parseTime(revoked)
	return &inv, nil
}

// FindInviteByToken looks an invitation up by the hash of its token.
func (s *Store) FindInviteByToken(ctx context.Context, tokenHash string) (*Invite, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+inviteColumns+` FROM invites WHERE token_hash = ?`, tokenHash)
	inv, err := scanInvite(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("invitation", "that link")
	}
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the invitation")
	}
	return inv, nil
}

// FindInvite looks an invitation up by id.
func (s *Store) FindInvite(ctx context.Context, id string) (*Invite, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+inviteColumns+` FROM invites WHERE id = ?`, id)
	inv, err := scanInvite(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("invitation", id)
	}
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the invitation")
	}
	return inv, nil
}

// ListInvites returns every invitation, newest first.
func (s *Store) ListInvites(ctx context.Context) ([]*Invite, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+inviteColumns+` FROM invites ORDER BY created_at DESC`)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing invitations")
	}
	defer rows.Close()
	var out []*Invite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading an invitation row")
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// AcceptInvite marks an invitation used. It is the same statement that checks the
// invitation was still open, so two people racing the same link cannot both win:
// the second update affects no rows.
func (s *Store) AcceptInvite(ctx context.Context, id string, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE invites SET accepted_at = ?
		 WHERE id = ? AND accepted_at = '' AND revoked_at = '' AND expires_at > ?`,
		formatTime(at), id, formatTime(at))
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "accepting the invitation")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return rlerr.Preconditionf("that invitation is no longer open").
			WithHint("ask a super admin for a new link")
	}
	return nil
}

// RevokeInvite withdraws an unaccepted invitation.
func (s *Store) RevokeInvite(ctx context.Context, id string, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE invites SET revoked_at = ? WHERE id = ? AND accepted_at = '' AND revoked_at = ''`,
		formatTime(at), id)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "revoking the invitation")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return rlerr.Preconditionf("that invitation has already been accepted or revoked")
	}
	return nil
}

// PurgeInvites removes invitations that expired more than a week ago, so the listing
// stays about who is waiting rather than about who never replied.
func (s *Store) PurgeInvites(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM invites WHERE accepted_at = '' AND expires_at <= ?`, formatTime(before))
	if err != nil {
		return 0, rlerr.Wrap(err, rlerr.CodeGeneric, "purging invitations")
	}
	n, _ := res.RowsAffected()
	return n, nil
}
