package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Extra units belonging to a site.
//
// One table with a kind rather than two, because a scheduled job and a long-running worker
// differ only in what starts them. Everything else — the tenant, the working directory,
// the environment, the sandbox, the resource ceiling — is the site's, and duplicating all
// of that twice is how the two drift apart.

// Unit kinds.
const (
	UnitJob    = "job"
	UnitWorker = "worker"
)

// SiteUnit is one scheduled job or worker.
type SiteUnit struct {
	Domain      string `json:"domain"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Command     string `json:"command"`
	Schedule    string `json:"schedule,omitempty"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	// Persistent runs a job that was missed while the server was off, the moment it comes
	// back. systemd calls this Persistent=true; cron has no equivalent, which is why a
	// nightly job on a machine that sleeps silently never runs.
	Persistent bool   `json:"persistent,omitempty"`
	Timeout    string `json:"timeout,omitempty"`
	Instances  int    `json:"instances,omitempty"`
	MemoryMax  string `json:"memory_max,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	CreatedBy string    `json:"created_by,omitempty"`
	LastRunAt time.Time `json:"last_run_at,omitempty"`
}

// PutSiteUnit inserts or replaces one.
func (s *Store) PutSiteUnit(ctx context.Context, u *SiteUnit) error {
	now := time.Now().UTC()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO site_units
			(domain, name, kind, command, schedule, description, enabled, persistent,
			 timeout, instances, memory_max, created_at, updated_at, created_by, last_run_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(domain, name) DO UPDATE SET
			kind=excluded.kind, command=excluded.command, schedule=excluded.schedule,
			description=excluded.description, enabled=excluded.enabled,
			persistent=excluded.persistent, timeout=excluded.timeout,
			instances=excluded.instances, memory_max=excluded.memory_max,
			updated_at=excluded.updated_at`,
		u.Domain, u.Name, u.Kind, u.Command, u.Schedule, u.Description,
		boolToInt(u.Enabled), boolToInt(u.Persistent), u.Timeout, u.Instances, u.MemoryMax,
		u.CreatedAt.Format(time.RFC3339), u.UpdatedAt.Format(time.RFC3339),
		u.CreatedBy, formatTime(u.LastRunAt))
	return err
}

// GetSiteUnit looks one up.
func (s *Store) GetSiteUnit(ctx context.Context, domain, name string) (*SiteUnit, error) {
	row := s.db.QueryRowContext(ctx, siteUnitColumns+` WHERE domain = ? AND name = ?`, domain, name)
	u, err := scanSiteUnit(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no such unit on %s: %s: %w", domain, name, ErrNotFound)
	}
	return u, err
}

// ListSiteUnits lists them, optionally filtered by domain and kind. Both empty lists
// everything, which is what `reconcile` and `export` need.
func (s *Store) ListSiteUnits(ctx context.Context, domain, kind string) ([]*SiteUnit, error) {
	q := siteUnitColumns + ` WHERE 1=1`
	var args []any
	if domain != "" {
		q += ` AND domain = ?`
		args = append(args, domain)
	}
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY domain, kind, name`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*SiteUnit
	for rows.Next() {
		u, err := scanSiteUnit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// DeleteSiteUnit removes one.
func (s *Store) DeleteSiteUnit(ctx context.Context, domain, name string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM site_units WHERE domain = ? AND name = ?`, domain, name)
	return err
}

// RecordSiteUnitRun stamps the last run.
func (s *Store) RecordSiteUnitRun(ctx context.Context, domain, name string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE site_units SET last_run_at = ? WHERE domain = ? AND name = ?`,
		at.UTC().Format(time.RFC3339), domain, name)
	return err
}

const siteUnitColumns = `SELECT domain, name, kind, command, schedule, description, enabled,
	persistent, timeout, instances, memory_max, created_at, updated_at, created_by,
	last_run_at FROM site_units`

type scanner interface {
	Scan(dest ...any) error
}

func scanSiteUnit(row scanner) (*SiteUnit, error) {
	var (
		u                            SiteUnit
		enabled, persistent          int
		created, updated, lastRunStr string
	)
	if err := row.Scan(&u.Domain, &u.Name, &u.Kind, &u.Command, &u.Schedule, &u.Description,
		&enabled, &persistent, &u.Timeout, &u.Instances, &u.MemoryMax,
		&created, &updated, &u.CreatedBy, &lastRunStr); err != nil {
		return nil, err
	}
	u.Enabled = enabled != 0
	u.Persistent = persistent != 0
	u.CreatedAt, _ = time.Parse(time.RFC3339, created)
	u.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	if lastRunStr != "" {
		u.LastRunAt, _ = time.Parse(time.RFC3339, lastRunStr)
	}
	return &u, nil
}
