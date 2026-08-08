package state

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// The last health check on each site.
//
// One row per site rather than a history. What an operator needs from this is "is it up,
// and if not since when", and a table that grows a row per site per interval is a
// disk-space bug on a box where nothing rotates it. consecutive_failures and failing_since
// answer the question without keeping every sample.

// Health is the last check on one site.
type Health struct {
	Domain              string    `json:"domain"`
	CheckedAt           time.Time `json:"checked_at"`
	OK                  bool      `json:"ok"`
	StatusCode          int       `json:"status_code,omitempty"`
	LatencyMS           int       `json:"latency_ms,omitempty"`
	Detail              string    `json:"detail,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures,omitempty"`
	FailingSince        time.Time `json:"failing_since,omitempty"`
}

// RecordHealth stores a check, carrying the failure streak forward.
//
// The streak is computed here rather than by the caller so that every writer agrees about
// it: a check recorded by the timer and one recorded by `site health` have to produce the
// same answer, or "failing for three hours" depends on who asked.
func (s *Store) RecordHealth(ctx context.Context, h *Health) error {
	previous, err := s.GetHealth(ctx, h.Domain)
	switch {
	case err == nil && !h.OK:
		h.ConsecutiveFailures = previous.ConsecutiveFailures + 1
		h.FailingSince = previous.FailingSince
		if h.FailingSince.IsZero() {
			h.FailingSince = h.CheckedAt
		}
	case !h.OK:
		h.ConsecutiveFailures = 1
		h.FailingSince = h.CheckedAt
	default:
		// Recovered: the streak resets, and so does the since.
		h.ConsecutiveFailures = 0
		h.FailingSince = time.Time{}
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO site_health
			(domain, checked_at, ok, status_code, latency_ms, detail,
			 consecutive_failures, failing_since)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(domain) DO UPDATE SET
			checked_at=excluded.checked_at, ok=excluded.ok,
			status_code=excluded.status_code, latency_ms=excluded.latency_ms,
			detail=excluded.detail,
			consecutive_failures=excluded.consecutive_failures,
			failing_since=excluded.failing_since`,
		h.Domain, h.CheckedAt.UTC().Format(time.RFC3339), boolToInt(h.OK),
		h.StatusCode, h.LatencyMS, h.Detail, h.ConsecutiveFailures,
		formatTime(h.FailingSince))
	return err
}

// GetHealth reads one site's last check.
func (s *Store) GetHealth(ctx context.Context, domain string) (*Health, error) {
	row := s.db.QueryRowContext(ctx, healthColumns+` WHERE domain = ?`, domain)
	h, err := scanHealth(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return h, err
}

// ListHealth reads every site's last check, keyed by domain so a caller can look up a
// site's health without a query per site.
func (s *Store) ListHealth(ctx context.Context) (map[string]*Health, error) {
	rows, err := s.db.QueryContext(ctx, healthColumns+` ORDER BY domain`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]*Health{}
	for rows.Next() {
		h, err := scanHealth(rows)
		if err != nil {
			return nil, err
		}
		out[h.Domain] = h
	}
	return out, rows.Err()
}

const healthColumns = `SELECT domain, checked_at, ok, status_code, latency_ms, detail,
	consecutive_failures, failing_since FROM site_health`

func scanHealth(row scanner) (*Health, error) {
	var (
		h            Health
		ok           int
		checked      string
		failingSince string
	)
	if err := row.Scan(&h.Domain, &checked, &ok, &h.StatusCode, &h.LatencyMS,
		&h.Detail, &h.ConsecutiveFailures, &failingSince); err != nil {
		return nil, err
	}
	h.OK = ok != 0
	h.CheckedAt, _ = time.Parse(time.RFC3339, checked)
	if failingSince != "" {
		h.FailingSince, _ = time.Parse(time.RFC3339, failingSince)
	}
	return &h, nil
}
