package state

import (
	"context"
	"database/sql"
	"errors"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

const userColumns = `name, uid, gid, home, shell, comment, quota, memory_max,
	sftp_only, password_login, disabled, created_at, updated_at, created_by`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var (
		u                              User
		sftpOnly, passwordLogin, disab int
		created, updated               string
	)
	err := row.Scan(&u.Name, &u.UID, &u.GID, &u.Home, &u.Shell, &u.Comment, &u.Quota,
		&u.MemoryMax, &sftpOnly, &passwordLogin, &disab, &created, &updated, &u.CreatedBy)
	if err != nil {
		return nil, err
	}
	u.SFTPOnly = sftpOnly == 1
	u.PasswordLogin = passwordLogin == 1
	u.Disabled = disab == 1
	u.CreatedAt = parseTime(created)
	u.UpdatedAt = parseTime(updated)
	return &u, nil
}

// PutUser inserts or updates a user row.
func (s *Store) PutUser(ctx context.Context, u *User) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (`+userColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET
			uid=excluded.uid, gid=excluded.gid, home=excluded.home, shell=excluded.shell,
			comment=excluded.comment, quota=excluded.quota, memory_max=excluded.memory_max,
			sftp_only=excluded.sftp_only, password_login=excluded.password_login,
			disabled=excluded.disabled, updated_at=excluded.updated_at`,
		u.Name, u.UID, u.GID, u.Home, u.Shell, u.Comment, u.Quota, u.MemoryMax,
		boolToInt(u.SFTPOnly), boolToInt(u.PasswordLogin), boolToInt(u.Disabled),
		orNow(formatTime(u.CreatedAt)), now(), u.CreatedBy)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the user %s", u.Name)
	}
	return nil
}

// GetUser looks up one user.
func (s *Store) GetUser(ctx context.Context, name string) (*User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("user", name)
	}
	if err != nil {
		return nil, scanError(err, "users")
	}
	return u, nil
}

// HasUser reports whether a user is recorded.
func (s *Store) HasUser(ctx context.Context, name string) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE name = ?`, name).Scan(&n); err != nil {
		return false, scanError(err, "users")
	}
	return n > 0, nil
}

// ListUsers returns every user, ordered by name.
func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY name`)
	if err != nil {
		return nil, scanError(err, "users")
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, scanError(err, "users")
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetUserDisabled records a user being disabled or re-enabled.
func (s *Store) SetUserDisabled(ctx context.Context, name string, disabled bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET disabled = ?, updated_at = ? WHERE name = ?`,
		boolToInt(disabled), now(), name)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "updating the user %s", name)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("user", name)
	}
	return nil
}

// DeleteUser removes a user. Its sites cascade, so callers must have torn those
// down on the system first.
func (s *Store) DeleteUser(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE name = ?`, name); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "removing the user %s", name)
	}
	return nil
}

// CountSitesForUser reports how many sites a user owns.
func (s *Store) CountSitesForUser(ctx context.Context, name string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites WHERE owner = ?`, name).Scan(&n); err != nil {
		return 0, scanError(err, "sites")
	}
	return n, nil
}

func orNow(s string) string {
	if s == "" {
		return now()
	}
	return s
}
