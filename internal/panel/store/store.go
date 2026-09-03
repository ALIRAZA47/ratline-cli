// Package store is the panel's own database: who may sign in, what they did, and the
// jobs they started.
//
// It is deliberately a *second* database, beside ratline's. ratline owns
// /var/lib/ratline/state.db and is the only writer of it; the panel is a caller of the
// ratline binary, not a co-owner of its state. Anything the panel wants to know about
// sites, users, certificates or databases it asks the CLI for, so there is exactly one
// answer to "what exists on this server" and it is the one ratline gives.
//
// What lives here is the part ratline cannot know: that a human called Dana, signing in
// from an address, asked for the deploy that ratline recorded as root.
package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, so the binary stays static

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// Store wraps the database.
type Store struct {
	db   *sql.DB
	path string
}

// ErrNotFound is returned by every lookup that finds nothing.
var ErrNotFound = errors.New("not found")

// Open opens (creating if needed) the panel database and applies migrations.
//
// 0600 root:root. It holds password hashes, TOTP secrets and live session hashes —
// the only file on the server that, read, lets somebody become an administrator of it.
func Open(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if _, err := system.EnsureDir(dir, 0o750, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return nil, err
	}
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "opening the panel database at %s", path)
	}
	// Unlike ratline's store, the panel serves concurrent requests, so reads must not
	// queue behind each other. One writer, several readers: WAL makes that safe, and
	// the busy timeout covers the moment a write is in flight.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "opening the panel database at %s", path)
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	// After the file exists. A database holding password hashes must not be
	// group-readable even for the moment between creation and the chmod.
	if err := system.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// OpenMemory returns an ephemeral store, for tests.
func OpenMemory() (*Store, error) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: ":memory:"}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// Path reports where the database lives.
func (s *Store) Path() string { return s.path }

// Tx runs fn inside a transaction, rolling back on error.
func (s *Store) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "starting a database transaction")
	}
	if err := fn(tx); err != nil {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
			return rlerr.Wrap(err, rlerr.CodeGeneric,
				"database transaction failed, and the rollback also failed (%v)", rerr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "committing a database transaction")
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_version (
			version    INTEGER NOT NULL,
			applied_at TEXT    NOT NULL
		)`); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "creating the schema_version table")
	}
	var current int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&current); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "reading the schema version")
	}
	if current > len(migrations) {
		return rlerr.Preconditionf(
			"the panel database is at schema version %d but this ratline-panel understands %d",
			current, len(migrations)).
			WithHint("a newer ratline-panel created this database; upgrade rather than downgrade")
	}
	for i, m := range migrations {
		version := i + 1
		if version <= current {
			continue
		}
		err := s.Tx(ctx, func(tx *sql.Tx) error {
			for _, stmt := range m {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return rlerr.Wrap(err, rlerr.CodeGeneric, "applying migration %d", version)
				}
			}
			_, err := tx.ExecContext(ctx,
				`INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`, version, now())
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// SchemaVersion reports the applied schema version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&v); err != nil {
		return 0, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the schema version")
	}
	return v, nil
}

// Setting reads a stored setting, returning "" when it has never been set.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "reading the setting %q", key)
	}
	return v, nil
}

// SetSetting writes a setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now())
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "writing the setting %q", key)
	}
	return nil
}

// Timestamps are RFC3339 in UTC: sortable as text and readable by hand, the same
// convention ratline's own database uses.
const timeFormat = time.RFC3339

func now() string { return time.Now().UTC().Format(timeFormat) }

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeFormat)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func notFound(kind, name string) error {
	return rlerr.Wrap(ErrNotFound, rlerr.CodePrecondition, "no such %s: %s", kind, name)
}
