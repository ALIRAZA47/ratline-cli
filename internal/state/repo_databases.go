package state

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// The database index. Nothing here holds a password: MongoDB stores a hash and will not
// give it back, so a password ratline set is unknowable afterwards. That is the right
// shape — a lost password is rotated, not recovered — and it means this table is not a
// secret store and does not have to be treated as one.

const dbColumns = `name, owner, server, created_at, created_by, notes`

func scanDatabase(row interface{ Scan(...any) error }) (*Database, error) {
	var (
		d       Database
		created string
	)
	if err := row.Scan(&d.Name, &d.Owner, &d.Server, &created, &d.CreatedBy, &d.Notes); err != nil {
		return nil, err
	}
	d.CreatedAt = parseTime(created)
	return &d, nil
}

// PutDatabase records a database, replacing the row if it exists.
func (s *Store) PutDatabase(ctx context.Context, d *Database) error {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO databases (name, owner, server, created_at, created_by, notes)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			owner = excluded.owner, server = excluded.server, notes = excluded.notes`,
		d.Name, d.Owner, d.Server, d.CreatedAt.UTC().Format(time.RFC3339), d.CreatedBy, d.Notes)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the database %s", d.Name)
	}
	return nil
}

// GetDatabase returns one database, or a not-found error naming it.
func (s *Store) GetDatabase(ctx context.Context, name string) (*Database, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+dbColumns+` FROM databases WHERE name = ?`, name)
	d, err := scanDatabase(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("database", name)
	}
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the database %s", name)
	}
	if d.Users, err = s.ListDatabaseUsers(ctx, name); err != nil {
		return nil, err
	}
	return d, nil
}

// ListDatabases returns every recorded database, with its users.
//
// Owner filters to one tenant; empty returns all.
func (s *Store) ListDatabases(ctx context.Context, owner string) ([]*Database, error) {
	q := `SELECT ` + dbColumns + ` FROM databases`
	var args []any
	if owner != "" {
		q += ` WHERE owner = ?`
		args = append(args, owner)
	}
	q += ` ORDER BY name`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing databases")
	}
	defer rows.Close()

	var out []*Database
	for rows.Next() {
		d, err := scanDatabase(rows)
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading a database row")
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing databases")
	}
	// The users are fetched after the rows are closed rather than inside the loop:
	// sqlite holds a read lock for an open cursor, and a nested query on the same
	// connection deadlocks against it.
	for _, d := range out {
		if d.Users, err = s.ListDatabaseUsers(ctx, d.Name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// DeleteDatabase removes the row. Its users and attachments go with it, by cascade.
func (s *Store) DeleteDatabase(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM databases WHERE name = ?`, name); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the database %s", name)
	}
	return nil
}

const dbUserColumns = `username, auth_db, database, role, created_at, created_by, rotated_at`

func scanDatabaseUser(row interface{ Scan(...any) error }) (*DatabaseUser, error) {
	var (
		u                DatabaseUser
		created, rotated string
	)
	if err := row.Scan(&u.Username, &u.AuthDB, &u.Database, &u.Role, &created, &u.CreatedBy, &rotated); err != nil {
		return nil, err
	}
	u.CreatedAt = parseTime(created)
	u.RotatedAt = parseTime(rotated)
	return &u, nil
}

// PutDatabaseUser records a user, replacing the row if it exists.
func (s *Store) PutDatabaseUser(ctx context.Context, u *DatabaseUser) error {
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	rotated := ""
	if !u.RotatedAt.IsZero() {
		rotated = u.RotatedAt.UTC().Format(time.RFC3339)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO database_users (username, auth_db, database, role, created_at, created_by, rotated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(username, auth_db) DO UPDATE SET
			database = excluded.database, role = excluded.role, rotated_at = excluded.rotated_at`,
		u.Username, u.AuthDB, u.Database, u.Role,
		u.CreatedAt.UTC().Format(time.RFC3339), u.CreatedBy, rotated)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the database user %s", u.Username)
	}
	return nil
}

// GetDatabaseUser returns one user by name and authentication database.
//
// authDB may be empty, which matches a user with exactly one row — the normal case,
// and it means an operator does not have to know the concept to delete a user. Two
// users with the same name in different databases is legal in MongoDB, so the
// ambiguity is reported rather than guessed at.
func (s *Store) GetDatabaseUser(ctx context.Context, username, authDB string) (*DatabaseUser, error) {
	if authDB != "" {
		row := s.db.QueryRowContext(ctx,
			`SELECT `+dbUserColumns+` FROM database_users WHERE username = ? AND auth_db = ?`,
			username, authDB)
		u, err := scanDatabaseUser(row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("database user", username+" in "+authDB)
		}
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the database user %s", username)
		}
		if u.Attachments, err = s.ListDatabaseAttachmentsForUser(ctx, u.Username, u.AuthDB); err != nil {
			return nil, err
		}
		return u, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+dbUserColumns+` FROM database_users WHERE username = ? ORDER BY auth_db`, username)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the database user %s", username)
	}
	defer rows.Close()
	var found []*DatabaseUser
	for rows.Next() {
		u, err := scanDatabaseUser(rows)
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading a database user row")
		}
		found = append(found, u)
	}
	if err := rows.Err(); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the database user %s", username)
	}
	switch len(found) {
	case 0:
		return nil, notFound("database user", username)
	case 1:
		u := found[0]
		if u.Attachments, err = s.ListDatabaseAttachmentsForUser(ctx, u.Username, u.AuthDB); err != nil {
			return nil, err
		}
		return u, nil
	default:
		var dbs []string
		for _, u := range found {
			dbs = append(dbs, u.AuthDB)
		}
		return nil, rlerr.Usagef("%s exists in more than one database: %s", username, joinComma(dbs)).
			WithHint("name the one you mean with --auth-db")
	}
}

// ListDatabaseUsers returns the users of one database, or of all when database is empty.
func (s *Store) ListDatabaseUsers(ctx context.Context, database string) ([]*DatabaseUser, error) {
	q := `SELECT ` + dbUserColumns + ` FROM database_users`
	var args []any
	if database != "" {
		q += ` WHERE database = ?`
		args = append(args, database)
	}
	q += ` ORDER BY username`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing database users")
	}
	defer rows.Close()
	var out []*DatabaseUser
	for rows.Next() {
		u, err := scanDatabaseUser(rows)
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading a database user row")
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing database users")
	}
	for _, u := range out {
		if u.Attachments, err = s.ListDatabaseAttachmentsForUser(ctx, u.Username, u.AuthDB); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// DeleteDatabaseUser removes the row and any attachments referencing it.
func (s *Store) DeleteDatabaseUser(ctx context.Context, username, authDB string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the database user %s", username)
	}
	defer func() { _ = tx.Rollback() }()

	// The attachments are removed explicitly rather than by cascade: they reference the
	// user by (username, auth_db), and a foreign key to a composite key that also
	// cascades from sites would delete a site's row when a user went away.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM database_attachments WHERE username = ? AND auth_db = ?`, username, authDB); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the attachments for %s", username)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM database_users WHERE username = ? AND auth_db = ?`, username, authDB); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the database user %s", username)
	}
	if err := tx.Commit(); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the database user %s", username)
	}
	return nil
}

// PutDatabaseAttachment records that a site was given a user's connection string.
func (s *Store) PutDatabaseAttachment(ctx context.Context, a *DatabaseAttachment) error {
	if a.AttachedAt.IsZero() {
		a.AttachedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO database_attachments (domain, username, auth_db, env_key, attached_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(domain, env_key) DO UPDATE SET
			username = excluded.username, auth_db = excluded.auth_db,
			attached_at = excluded.attached_at`,
		a.Domain, a.Username, a.AuthDB, a.EnvKey, a.AttachedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the attachment to %s", a.Domain)
	}
	return nil
}

// DeleteDatabaseAttachment removes one site's attachment under one environment key.
func (s *Store) DeleteDatabaseAttachment(ctx context.Context, domain, envKey string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM database_attachments WHERE domain = ? AND env_key = ?`, domain, envKey); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the attachment from %s", domain)
	}
	return nil
}

func scanAttachment(row interface{ Scan(...any) error }) (*DatabaseAttachment, error) {
	var (
		a        DatabaseAttachment
		attached string
	)
	if err := row.Scan(&a.Domain, &a.Username, &a.AuthDB, &a.EnvKey, &attached); err != nil {
		return nil, err
	}
	a.AttachedAt = parseTime(attached)
	return &a, nil
}

const attachmentColumns = `domain, username, auth_db, env_key, attached_at`

// ListDatabaseAttachmentsForUser returns the sites holding one user's credentials.
func (s *Store) ListDatabaseAttachmentsForUser(ctx context.Context, username, authDB string) ([]*DatabaseAttachment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+attachmentColumns+` FROM database_attachments
		 WHERE username = ? AND auth_db = ? ORDER BY domain, env_key`, username, authDB)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing the attachments for %s", username)
	}
	defer rows.Close()
	var out []*DatabaseAttachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading an attachment row")
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListDatabaseAttachmentsForSite returns the database credentials one site holds.
func (s *Store) ListDatabaseAttachmentsForSite(ctx context.Context, domain string) ([]*DatabaseAttachment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+attachmentColumns+` FROM database_attachments
		 WHERE domain = ? ORDER BY env_key`, domain)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing the attachments for %s", domain)
	}
	defer rows.Close()
	var out []*DatabaseAttachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading an attachment row")
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// joinComma is a local helper so this file does not import strings for one use.
func joinComma(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}
