package state

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// Persistence for the engine-scoped database records (MySQL now, Redis later). This
// mirrors repo_databases.go but every row is keyed by engine, so the SQL and key-value
// engines share one set of tables without colliding with each other or with MongoDB.

const engineDBColumns = `engine, name, owner, server, created_at, created_by, notes`

func scanEngineDatabase(row interface{ Scan(...any) error }) (*EngineDatabase, error) {
	var (
		d       EngineDatabase
		created string
	)
	if err := row.Scan(&d.Engine, &d.Name, &d.Owner, &d.Server, &created, &d.CreatedBy, &d.Notes); err != nil {
		return nil, err
	}
	d.CreatedAt = parseTime(created)
	return &d, nil
}

// PutEngineDatabase records a database, replacing the row if it exists.
func (s *Store) PutEngineDatabase(ctx context.Context, d *EngineDatabase) error {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO db_databases (engine, name, owner, server, created_at, created_by, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(engine, name) DO UPDATE SET
			owner = excluded.owner, server = excluded.server, notes = excluded.notes`,
		d.Engine, d.Name, d.Owner, d.Server, d.CreatedAt.UTC().Format(time.RFC3339), d.CreatedBy, d.Notes)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the %s database %s", d.Engine, d.Name)
	}
	return nil
}

// GetEngineDatabase returns one database (with its users), or a not-found error.
func (s *Store) GetEngineDatabase(ctx context.Context, engine, name string) (*EngineDatabase, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+engineDBColumns+` FROM db_databases WHERE engine = ? AND name = ?`, engine, name)
	d, err := scanEngineDatabase(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound(engine+" database", name)
	}
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the %s database %s", engine, name)
	}
	if d.Users, err = s.ListEngineUsers(ctx, engine, name); err != nil {
		return nil, err
	}
	return d, nil
}

// ListEngineDatabases returns every recorded database for an engine, with its users.
// Owner filters to one tenant; empty returns all.
func (s *Store) ListEngineDatabases(ctx context.Context, engine, owner string) ([]*EngineDatabase, error) {
	q := `SELECT ` + engineDBColumns + ` FROM db_databases WHERE engine = ?`
	args := []any{engine}
	if owner != "" {
		q += ` AND owner = ?`
		args = append(args, owner)
	}
	q += ` ORDER BY name`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing %s databases", engine)
	}
	defer rows.Close()

	var out []*EngineDatabase
	for rows.Next() {
		d, err := scanEngineDatabase(rows)
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading a database row")
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing %s databases", engine)
	}
	// Users fetched after the cursor is closed: sqlite holds a read lock for an open
	// cursor, and a nested query on the same connection deadlocks against it.
	for _, d := range out {
		if d.Users, err = s.ListEngineUsers(ctx, engine, d.Name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// DeleteEngineDatabase removes the row. Its users and attachments are removed by the
// caller (there is no cross-engine cascade on these tables).
func (s *Store) DeleteEngineDatabase(ctx context.Context, engine, name string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM db_databases WHERE engine = ? AND name = ?`, engine, name); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the %s database %s", engine, name)
	}
	return nil
}

const engineUserColumns = `engine, username, scope, database, role, created_at, created_by, rotated_at`

func scanEngineUser(row interface{ Scan(...any) error }) (*EngineUser, error) {
	var (
		u                EngineUser
		created, rotated string
	)
	if err := row.Scan(&u.Engine, &u.Username, &u.Scope, &u.Database, &u.Role, &created, &u.CreatedBy, &rotated); err != nil {
		return nil, err
	}
	u.CreatedAt = parseTime(created)
	u.RotatedAt = parseTime(rotated)
	return &u, nil
}

// PutEngineUser records a user, replacing the row if it exists.
func (s *Store) PutEngineUser(ctx context.Context, u *EngineUser) error {
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	rotated := ""
	if !u.RotatedAt.IsZero() {
		rotated = u.RotatedAt.UTC().Format(time.RFC3339)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO db_users (engine, username, scope, database, role, created_at, created_by, rotated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(engine, username, scope) DO UPDATE SET
			database = excluded.database, role = excluded.role, rotated_at = excluded.rotated_at`,
		u.Engine, u.Username, u.Scope, u.Database, u.Role,
		u.CreatedAt.UTC().Format(time.RFC3339), u.CreatedBy, rotated)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the %s user %s", u.Engine, u.Username)
	}
	return nil
}

// GetEngineUser returns one user by name and scope. Scope may be empty, which matches a
// user with exactly one row — the normal case; two rows are reported as an ambiguity
// rather than guessed at.
func (s *Store) GetEngineUser(ctx context.Context, engine, username, scope string) (*EngineUser, error) {
	if scope != "" {
		row := s.db.QueryRowContext(ctx,
			`SELECT `+engineUserColumns+` FROM db_users WHERE engine = ? AND username = ? AND scope = ?`,
			engine, username, scope)
		u, err := scanEngineUser(row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound(engine+" user", username+" ("+scope+")")
		}
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the %s user %s", engine, username)
		}
		if u.Attachments, err = s.ListEngineAttachmentsForUser(ctx, engine, u.Username, u.Scope); err != nil {
			return nil, err
		}
		return u, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+engineUserColumns+` FROM db_users WHERE engine = ? AND username = ? ORDER BY scope`,
		engine, username)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the %s user %s", engine, username)
	}
	defer rows.Close()
	var found []*EngineUser
	for rows.Next() {
		u, err := scanEngineUser(rows)
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading a user row")
		}
		found = append(found, u)
	}
	if err := rows.Err(); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the %s user %s", engine, username)
	}
	switch len(found) {
	case 0:
		return nil, notFound(engine+" user", username)
	case 1:
		u := found[0]
		if u.Attachments, err = s.ListEngineAttachmentsForUser(ctx, engine, u.Username, u.Scope); err != nil {
			return nil, err
		}
		return u, nil
	default:
		var scopes []string
		for _, u := range found {
			scopes = append(scopes, u.Scope)
		}
		return nil, rlerr.Usagef("%s exists under more than one scope: %s", username, joinComma(scopes)).
			WithHint("name the one you mean with --scope")
	}
}

// ListEngineUsers returns the users of one database, or of all when database is empty.
func (s *Store) ListEngineUsers(ctx context.Context, engine, database string) ([]*EngineUser, error) {
	q := `SELECT ` + engineUserColumns + ` FROM db_users WHERE engine = ?`
	args := []any{engine}
	if database != "" {
		q += ` AND database = ?`
		args = append(args, database)
	}
	q += ` ORDER BY username`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing %s users", engine)
	}
	defer rows.Close()
	var out []*EngineUser
	for rows.Next() {
		u, err := scanEngineUser(rows)
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading a user row")
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing %s users", engine)
	}
	for _, u := range out {
		if u.Attachments, err = s.ListEngineAttachmentsForUser(ctx, engine, u.Username, u.Scope); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// DeleteEngineUser removes the row and any attachments referencing it.
func (s *Store) DeleteEngineUser(ctx context.Context, engine, username, scope string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the %s user %s", engine, username)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM db_attachments WHERE engine = ? AND username = ? AND scope = ?`,
		engine, username, scope); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the attachments for %s", username)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM db_users WHERE engine = ? AND username = ? AND scope = ?`,
		engine, username, scope); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the %s user %s", engine, username)
	}
	if err := tx.Commit(); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the %s user %s", engine, username)
	}
	return nil
}

// PutEngineAttachment records that a site was given a user's connection string.
func (s *Store) PutEngineAttachment(ctx context.Context, a *EngineAttachment) error {
	if a.AttachedAt.IsZero() {
		a.AttachedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO db_attachments (engine, domain, username, scope, env_key, attached_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(domain, env_key) DO UPDATE SET
			engine = excluded.engine, username = excluded.username,
			scope = excluded.scope, attached_at = excluded.attached_at`,
		a.Engine, a.Domain, a.Username, a.Scope, a.EnvKey, a.AttachedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the attachment to %s", a.Domain)
	}
	return nil
}

// DeleteEngineAttachment removes one site's attachment under one environment key.
func (s *Store) DeleteEngineAttachment(ctx context.Context, domain, envKey string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM db_attachments WHERE domain = ? AND env_key = ?`, domain, envKey); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the attachment from %s", domain)
	}
	return nil
}

func scanEngineAttachment(row interface{ Scan(...any) error }) (*EngineAttachment, error) {
	var (
		a        EngineAttachment
		attached string
	)
	if err := row.Scan(&a.Engine, &a.Domain, &a.Username, &a.Scope, &a.EnvKey, &attached); err != nil {
		return nil, err
	}
	a.AttachedAt = parseTime(attached)
	return &a, nil
}

const engineAttachmentColumns = `engine, domain, username, scope, env_key, attached_at`

// ListEngineAttachmentsForUser returns the sites holding one user's credentials.
func (s *Store) ListEngineAttachmentsForUser(ctx context.Context, engine, username, scope string) ([]*EngineAttachment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+engineAttachmentColumns+` FROM db_attachments
		 WHERE engine = ? AND username = ? AND scope = ? ORDER BY domain, env_key`,
		engine, username, scope)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing the attachments for %s", username)
	}
	defer rows.Close()
	var out []*EngineAttachment
	for rows.Next() {
		a, err := scanEngineAttachment(rows)
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading an attachment row")
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListEngineAttachmentsForSite returns the engine database credentials one site holds.
func (s *Store) ListEngineAttachmentsForSite(ctx context.Context, domain string) ([]*EngineAttachment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+engineAttachmentColumns+` FROM db_attachments WHERE domain = ? ORDER BY env_key`, domain)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing the attachments for %s", domain)
	}
	defer rows.Close()
	var out []*EngineAttachment
	for rows.Next() {
		a, err := scanEngineAttachment(rows)
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading an attachment row")
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- the access list -------------------------------------------------------------

// PutEngineAccess records an allowed address, replacing the row if it exists.
func (s *Store) PutEngineAccess(ctx context.Context, a *EngineAccess) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO db_access (engine, address, note, created_at, created_by)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(engine, address) DO UPDATE SET note = excluded.note`,
		a.Engine, a.Address, a.Note, a.CreatedAt.UTC().Format(time.RFC3339), a.CreatedBy)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the allowed address %s", a.Address)
	}
	return nil
}

// GetEngineAccess returns one allowed address, or a not-found error naming it.
func (s *Store) GetEngineAccess(ctx context.Context, engine, address string) (*EngineAccess, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT engine, address, note, created_at, created_by FROM db_access WHERE engine = ? AND address = ?`,
		engine, address)
	var (
		a       EngineAccess
		created string
	)
	err := row.Scan(&a.Engine, &a.Address, &a.Note, &created, &a.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("allowed address", address)
	}
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the allowed address %s", address)
	}
	a.CreatedAt = parseTime(created)
	return &a, nil
}

// DeleteEngineAccess removes an allowed address and reports whether it was there.
func (s *Store) DeleteEngineAccess(ctx context.Context, engine, address string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM db_access WHERE engine = ? AND address = ?`, engine, address)
	if err != nil {
		return false, rlerr.Wrap(err, rlerr.CodeGeneric, "removing the allowed address %s", address)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListEngineAccess returns every allowed address for an engine, oldest first.
func (s *Store) ListEngineAccess(ctx context.Context, engine string) ([]*EngineAccess, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT engine, address, note, created_at, created_by FROM db_access
		 WHERE engine = ? ORDER BY created_at, address`, engine)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing the allowed addresses")
	}
	defer rows.Close()
	var out []*EngineAccess
	for rows.Next() {
		var (
			a       EngineAccess
			created string
		)
		if err := rows.Scan(&a.Engine, &a.Address, &a.Note, &created, &a.CreatedBy); err != nil {
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
