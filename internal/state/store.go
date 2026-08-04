// Package state is ratline's index and audit history.
//
// The filesystem, the systemd units and /etc/passwd are the source of truth.
// This database records what ratline believes it created, why, and when, so that
// `reconcile` can compare the two and `doctor` can report drift. Losing it is
// recoverable — `reconcile` rebuilds it by scanning the system — which is a
// deliberate design choice, because a provisioning tool that cannot survive the
// loss of its own database is a liability.
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
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

// Open opens (creating if needed) the state database and applies migrations.
//
// The file is 0600 root:root: it holds no private keys, but it does hold the
// complete map of who owns what on this server, and there is no reason for a
// tenant to read it.
func Open(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if _, err := system.EnsureDir(dir, 0o750, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return nil, err
	}
	fresh := !system.Exists(path)

	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "opening the state database at %s", path)
	}
	// One connection: every mutating command already holds the global lock, and
	// a single writer avoids SQLITE_BUSY entirely.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "opening the state database at %s", path)
	}
	if fresh {
		if err := os.Chmod(path, 0o600); err != nil {
			db.Close()
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "setting the mode on %s", path)
		}
	}

	s := &Store{db: db, path: path}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// OpenMemory returns an in-memory store, for tests.
func OpenMemory() (*Store, error) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: ":memory:"}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
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

// DB exposes the handle for the few callers that need raw access (export).
func (s *Store) DB() *sql.DB { return s.db }

// Tx runs fn inside a transaction, rolling back on error.
//
// Every multi-row mutation goes through here so that a failure part way through
// leaves no partial rows to confuse the next reconcile.
func (s *Store) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "starting a database transaction")
	}
	if err := fn(tx); err != nil {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "database transaction failed, and the rollback also failed (%v)", rerr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "committing a database transaction")
	}
	return nil
}

// migrate applies every migration newer than the recorded version.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_version (
			version    INTEGER NOT NULL,
			applied_at TEXT    NOT NULL
		)`); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "creating the schema_version table")
	}

	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&current); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "reading the schema version")
	}
	if current > len(migrations) {
		return rlerr.Preconditionf("the state database is at schema version %d but this ratline understands %d", current, len(migrations)).
			WithHint("a newer ratline created this database; upgrade rather than downgrade")
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
				`INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`,
				version, now())
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
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&v)
	if err != nil {
		return 0, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the schema version")
	}
	return v, nil
}

// ErrNotFound is returned by every lookup that finds nothing.
var ErrNotFound = errors.New("not found")

// notFound builds the operator-facing form of a missing row.
func notFound(kind, name string) error {
	return rlerr.Wrap(ErrNotFound, rlerr.CodePrecondition, "no such %s: %s", kind, name)
}

// Timestamps are stored as RFC3339 in UTC: sortable as text, unambiguous across
// timezone changes, and readable when someone opens the database by hand.
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

// scanError wraps a row-scan failure, which is always a schema mismatch.
func scanError(err error, table string) error {
	return rlerr.Wrap(err, rlerr.CodeGeneric, "reading a row from %s", table)
}

var _ = fmt.Sprintf
