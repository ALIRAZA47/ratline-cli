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
	disabled, created_at, created_by, updated_at, last_login_at, last_login_ip, totp_last_step`

// querier is what *sql.DB and *sql.Tx have in common, so that a guard can be written
// once and run either on its own or inside the transaction that acts on its answer.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// NormalizeEmail lowercases and trims an address so that one person cannot hold two
// accounts by capitalising differently. Stored normalised, compared normalised.
func NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// CreateAccount inserts an account. The caller has already hashed the password.
func (s *Store) CreateAccount(ctx context.Context, a *Account) error {
	return insertAccount(ctx, s.db, a)
}

// ErrAlreadySetUp is what CreateFirstAccount returns when it was not the first.
var ErrAlreadySetUp = errors.New("the panel already has an account")

// CreateFirstAccount inserts the panel's first account, and only if there is none.
//
// The count and the insert are one transaction, begun with a write lock (the store
// opens every transaction BEGIN IMMEDIATE), so two setup requests arriving together
// cannot both see an empty table: the second waits for the first to commit and then
// sees its row. UNIQUE(email) alone would let two different addresses both claim the
// panel, and the operator who won would not know they were not alone.
func (s *Store) CreateFirstAccount(ctx context.Context, a *Account) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&n); err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "counting accounts")
		}
		if n > 0 {
			return ErrAlreadySetUp
		}
		return insertAccount(ctx, tx, a)
	})
}

func insertAccount(ctx context.Context, q querier, a *Account) error {
	a.Email = NormalizeEmail(a.Email)
	if !ValidRole(a.Role) {
		return rlerr.Usagef("%q is not a role", a.Role)
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	a.UpdatedAt = a.CreatedAt
	_, err := q.ExecContext(ctx,
		`INSERT INTO accounts (`+accountColumns+`)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Email, a.Name, a.Role, a.PasswordHash, a.TOTPSecret, boolToInt(a.TOTPEnabled),
		boolToInt(a.Disabled), formatTime(a.CreatedAt), a.CreatedBy, formatTime(a.UpdatedAt),
		formatTime(a.LastLoginAt), a.LastLoginIP, a.TOTPLastStep)
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
		&disabled, &created, &a.CreatedBy, &updated, &lastLogin, &a.LastLoginIP, &a.TOTPLastStep)
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
	return findAccount(ctx, s.db, id)
}

func findAccount(ctx context.Context, q querier, id string) (*Account, error) {
	row := q.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM accounts WHERE id = ?`, id)
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
// person able to grant access back. It runs inside the transaction that acts on it,
// or two super admins demoting each other at the same moment would both pass.
func countActiveSuperAdmins(ctx context.Context, q querier, excluding string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx,
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
	return s.Tx(ctx, func(tx *sql.Tx) error {
		a, err := findAccount(ctx, tx, id)
		if err != nil {
			return err
		}
		if a.Role == RoleSuperAdmin && role != RoleSuperAdmin {
			n, err := countActiveSuperAdmins(ctx, tx, id)
			if err != nil {
				return err
			}
			if n == 0 {
				return errLastSuperAdmin("demoting " + a.Email)
			}
		}
		return touch(ctx, tx, id, `role = ?`, role)
	})
}

// SetAccountDisabled disables or re-enables an account, refusing to disable the last
// super admin.
func (s *Store) SetAccountDisabled(ctx context.Context, id string, disabled bool) error {
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		a, err := findAccount(ctx, tx, id)
		if err != nil {
			return err
		}
		if disabled && a.Role == RoleSuperAdmin {
			n, err := countActiveSuperAdmins(ctx, tx, id)
			if err != nil {
				return err
			}
			if n == 0 {
				return errLastSuperAdmin("disabling " + a.Email)
			}
		}
		return touch(ctx, tx, id, `disabled = ?`, boolToInt(disabled))
	})
	if err != nil {
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
	if err := touch(ctx, s.db, id, `password_hash = ?`, hash); err != nil {
		return err
	}
	return s.DeleteSessionsFor(ctx, id)
}

// SetTOTP stores a secret and whether it has been confirmed. A new secret starts the
// replay record over: its steps have nothing to do with the old one's.
func (s *Store) SetTOTP(ctx context.Context, id, secret string, enabled bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE accounts SET totp_secret = ?, totp_enabled = ?, totp_last_step = 0, updated_at = ? WHERE id = ?`,
		secret, boolToInt(enabled), now(), id)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "updating the second factor")
	}
	return affectedOne(res, "account", id)
}

// ConsumeTOTPStep records that a code for the given time step was accepted, and
// reports false if one for that step or a later one already had been.
//
// One conditional UPDATE rather than a read and a write, so two requests presenting
// the same code at the same moment cannot both succeed: whichever runs first changes
// the row and the other finds nothing to change. RFC 6238 §5.2 requires a verifier to
// refuse a second use of an OTP; refusing anything not newer than the last accepted
// step also closes the one-step-behind code the skew window would otherwise still take.
func (s *Store) ConsumeTOTPStep(ctx context.Context, id string, step uint64) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE accounts SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?`,
		int64(step), id, int64(step))
	if err != nil {
		return false, rlerr.Wrap(err, rlerr.CodeGeneric, "recording the second-factor step")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, rlerr.Wrap(err, rlerr.CodeGeneric, "checking the update")
	}
	return n == 1, nil
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
	return s.Tx(ctx, func(tx *sql.Tx) error {
		a, err := findAccount(ctx, tx, id)
		if err != nil {
			return err
		}
		if a.Role == RoleSuperAdmin {
			n, err := countActiveSuperAdmins(ctx, tx, id)
			if err != nil {
				return err
			}
			if n == 0 {
				return errLastSuperAdmin("deleting " + a.Email)
			}
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
		if err != nil {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "deleting the account")
		}
		return affectedOne(res, "account", id)
	})
}

// touch applies a single-column update and moves updated_at.
func touch(ctx context.Context, q querier, id, assignment string, value any) error {
	res, err := q.ExecContext(ctx,
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
