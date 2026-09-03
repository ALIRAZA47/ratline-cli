package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

const accountColumns = `id, email, name, role, password_hash, totp_secret, totp_enabled,
	disabled, created_at, created_by, updated_at, last_login_at, last_login_ip`

// NormalizeEmail lowercases and trims an address so that one person cannot hold two
// accounts by capitalising differently. Stored normalised, compared normalised.
func NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// CreateAccount inserts an account. The caller has already hashed the password.
func (s *Store) CreateAccount(ctx context.Context, a *Account) error {
	a.Email = NormalizeEmail(a.Email)
	if !ValidRole(a.Role) {
		return rlerr.Usagef("%q is not a role", a.Role)
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	a.UpdatedAt = a.CreatedAt
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO accounts (`+accountColumns+`)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Email, a.Name, a.Role, a.PasswordHash, a.TOTPSecret, boolToInt(a.TOTPEnabled),
		boolToInt(a.Disabled), formatTime(a.CreatedAt), a.CreatedBy, formatTime(a.UpdatedAt),
		formatTime(a.LastLoginAt), a.LastLoginIP)
	if err != nil {
		if isUniqueViolation(err) {
			return rlerr.Preconditionf("an account already exists for %s", a.Email).
				WithHint("invite a different address, or change the existing account's role")
		}
		return rlerr.Wrap(err, rlerr.CodeGeneric, "creating the account for %s", a.Email)
	}
	return nil
}

func scanAccount(sc interface{ Scan(...any) error }) (*Account, error) {
	var (
		a                           Account
		totp, disabled              int
		created, updated, lastLogin string
	)
	err := sc.Scan(&a.ID, &a.Email, &a.Name, &a.Role, &a.PasswordHash, &a.TOTPSecret, &totp,
		&disabled, &created, &a.CreatedBy, &updated, &lastLogin, &a.LastLoginIP)
	if err != nil {
		return nil, err
	}
	a.TOTPEnabled = totp == 1
	a.Disabled = disabled == 1
	a.CreatedAt = parseTime(created)
	a.UpdatedAt = parseTime(updated)
	a.LastLoginAt = parseTime(lastLogin)
	return &a, nil
}

// FindAccountByEmail looks an account up by address.
func (s *Store) FindAccountByEmail(ctx context.Context, email string) (*Account, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+accountColumns+` FROM accounts WHERE email = ?`, NormalizeEmail(email))
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("account", email)
	}
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the account for %s", email)
	}
	return a, nil
}

// FindAccount looks an account up by id.
func (s *Store) FindAccount(ctx context.Context, id string) (*Account, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM accounts WHERE id = ?`, id)
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("account", id)
	}
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the account %s", id)
	}
	return a, nil
}

// ListAccounts returns every account, newest last.
func (s *Store) ListAccounts(ctx context.Context) ([]*Account, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+accountColumns+` FROM accounts ORDER BY created_at, email`)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing accounts")
	}
	defer rows.Close()
	var out []*Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading an account row")
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountAccounts reports how many accounts exist, which is what tells the panel
// whether it has ever been set up.
func (s *Store) CountAccounts(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&n); err != nil {
		return 0, rlerr.Wrap(err, rlerr.CodeGeneric, "counting accounts")
	}
	return n, nil
}

// countActiveSuperAdmins is the guard behind every change that could remove the last
// person able to grant access back.
func (s *Store) countActiveSuperAdmins(ctx context.Context, excluding string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM accounts WHERE role = ? AND disabled = 0 AND id <> ?`,
		RoleSuperAdmin, excluding).Scan(&n)
	if err != nil {
		return 0, rlerr.Wrap(err, rlerr.CodeGeneric, "counting super admins")
	}
	return n, nil
}

// errLastSuperAdmin is one message, raised from four places, because the mistake is
// the same one however it is spelled: demote yourself, disable yourself, delete
// yourself, or demote the only other super admin, and nobody can invite anyone again.
// Recovering means SSH and `ratline-panel account promote`, which is exactly the
// situation the panel exists to avoid.
func errLastSuperAdmin(what string) error {
	return rlerr.Preconditionf("%s would leave the panel with no active super admin", what).
		WithHint("promote another account first; a panel with no super admin cannot invite one")
}

// SetAccountRole changes a role, refusing to remove the last super admin.
func (s *Store) SetAccountRole(ctx context.Context, id, role string) error {
	if !ValidRole(role) {
		return rlerr.Usagef("%q is not a role", role)
	}
	a, err := s.FindAccount(ctx, id)
	if err != nil {
		return err
	}
	if a.Role == RoleSuperAdmin && role != RoleSuperAdmin {
		n, err := s.countActiveSuperAdmins(ctx, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return errLastSuperAdmin("demoting " + a.Email)
		}
	}
	return s.touch(ctx, id, `role = ?`, role)
}

// SetAccountDisabled disables or re-enables an account, refusing to disable the last
// super admin.
func (s *Store) SetAccountDisabled(ctx context.Context, id string, disabled bool) error {
	a, err := s.FindAccount(ctx, id)
	if err != nil {
		return err
	}
	if disabled && a.Role == RoleSuperAdmin {
		n, err := s.countActiveSuperAdmins(ctx, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return errLastSuperAdmin("disabling " + a.Email)
		}
	}
	if err := s.touch(ctx, id, `disabled = ?`, boolToInt(disabled)); err != nil {
		return err
	}
	if disabled {
		// A disabled account with a live session is still signed in. Every check that
		// matters happens per request, so the session is also cut here rather than
		// relying on it expiring.
		return s.DeleteSessionsFor(ctx, id)
	}
	return nil
}

// SetPassword replaces the stored hash and signs every other browser out.
func (s *Store) SetPassword(ctx context.Context, id, hash string) error {
	if err := s.touch(ctx, id, `password_hash = ?`, hash); err != nil {
		return err
	}
	return s.DeleteSessionsFor(ctx, id)
}

// SetTOTP stores a secret and whether it has been confirmed.
func (s *Store) SetTOTP(ctx context.Context, id, secret string, enabled bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE accounts SET totp_secret = ?, totp_enabled = ?, updated_at = ? WHERE id = ?`,
		secret, boolToInt(enabled), now(), id)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "updating the second factor")
	}
	return affectedOne(res, "account", id)
}

// RecordLogin stamps a successful sign-in.
func (s *Store) RecordLogin(ctx context.Context, id, ip string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE accounts SET last_login_at = ?, last_login_ip = ? WHERE id = ?`,
		formatTime(at), ip, id)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording the sign-in")
	}
	return nil
}

// DeleteAccount removes an account and everything that referenced it.
func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	a, err := s.FindAccount(ctx, id)
	if err != nil {
		return err
	}
	if a.Role == RoleSuperAdmin {
		n, err := s.countActiveSuperAdmins(ctx, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return errLastSuperAdmin("deleting " + a.Email)
		}
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "deleting the account")
	}
	return affectedOne(res, "account", id)
}

// touch applies a single-column update and moves updated_at.
func (s *Store) touch(ctx context.Context, id, assignment string, value any) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE accounts SET `+assignment+`, updated_at = ? WHERE id = ?`, value, now(), id)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "updating the account")
	}
	return affectedOne(res, "account", id)
}

func affectedOne(res sql.Result, kind, name string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "checking the update")
	}
	if n == 0 {
		return notFound(kind, name)
	}
	return nil
}

// isUniqueViolation recognises SQLite's constraint failure without depending on the
// driver's error type, which is not exported by modernc's driver.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
