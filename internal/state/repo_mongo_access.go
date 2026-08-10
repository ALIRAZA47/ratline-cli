package state

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// The MongoDB access list. This is ratline's record of which addresses it has opened
// the server's port to; the ufw rules are the enforcement. Kept in state rather than
// parsed back out of `ufw status` because ufw's output is for humans and reshuffles
// across versions, and because "what did ratline allow" must stay answerable even when
// an operator has added unrelated rules of their own.

// PutMongoAccess records an allowed address, replacing the row if it exists.
func (s *Store) PutMongoAccess(ctx context.Context, a *MongoAccess) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO mongo_access (address, note, created_at, created_by)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(address) DO UPDATE SET note = excluded.note`,
		a.Address, a.Note, a.CreatedAt.UTC().Format(time.RFC3339), a.CreatedBy)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the allowed address %s", a.Address)
	}
	return nil
}

// GetMongoAccess returns one allowed address, or a not-found error naming it.
func (s *Store) GetMongoAccess(ctx context.Context, address string) (*MongoAccess, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT address, note, created_at, created_by FROM mongo_access WHERE address = ?`, address)
	var (
		a       MongoAccess
		created string
	)
	err := row.Scan(&a.Address, &a.Note, &created, &a.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("allowed address", address)
	}
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the allowed address %s", address)
	}
	a.CreatedAt = parseTime(created)
	return &a, nil
}

// DeleteMongoAccess removes an allowed address and reports whether it was there.
func (s *Store) DeleteMongoAccess(ctx context.Context, address string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM mongo_access WHERE address = ?`, address)
	if err != nil {
		return false, rlerr.Wrap(err, rlerr.CodeGeneric, "removing the allowed address %s", address)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListMongoAccess returns every allowed address, oldest first.
func (s *Store) ListMongoAccess(ctx context.Context) ([]*MongoAccess, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT address, note, created_at, created_by FROM mongo_access ORDER BY created_at, address`)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing the allowed addresses")
	}
	defer rows.Close()
	var out []*MongoAccess
	for rows.Next() {
		var (
			a       MongoAccess
			created string
		)
		if err := rows.Scan(&a.Address, &a.Note, &created, &a.CreatedBy); err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the allowed addresses")
		}
		a.CreatedAt = parseTime(created)
		out = append(out, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the allowed addresses")
	}
	return out, nil
}
