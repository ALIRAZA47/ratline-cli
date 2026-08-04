package state

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// AllocatePort reserves the lowest free port in the range for a domain.
//
// The caller must also verify the port is actually free on the host with a bind
// test: this table only knows what ratline allocated, and something else on the
// server may hold it.
func (s *Store) AllocatePort(ctx context.Context, domain string, start, end int, isFree func(int) bool) (int, error) {
	// An existing allocation is reused, so `site add` is idempotent.
	var existing int
	err := s.db.QueryRowContext(ctx, `SELECT port FROM ports WHERE domain = ? LIMIT 1`, domain).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, scanError(err, "ports")
	}

	taken := map[int]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT port FROM ports`)
	if err != nil {
		return 0, scanError(err, "ports")
	}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return 0, scanError(err, "ports")
		}
		taken[p] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, scanError(err, "ports")
	}

	for p := start; p <= end; p++ {
		if taken[p] {
			continue
		}
		if isFree != nil && !isFree(p) {
			continue
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO ports (port, domain, allocated_at) VALUES (?,?,?)`, p, domain, now()); err != nil {
			// Another allocation won the race; try the next port.
			continue
		}
		return p, nil
	}
	return 0, rlerr.Preconditionf("no free port in the range %d-%d", start, end).
		WithHint("widen ports.range_start and ports.range_end in /etc/ratline/config.yaml, " +
			"or move some sites to Unix sockets with 'ratline site runtime <domain> --listen socket'")
}

// ReleasePort frees a domain's allocation.
func (s *Store) ReleasePort(ctx context.Context, domain string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ports WHERE domain = ?`, domain); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "releasing the port for %s", domain)
	}
	return nil
}

// ListPorts returns every allocation.
func (s *Store) ListPorts(ctx context.Context) ([]*Port, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT port, domain, allocated_at FROM ports ORDER BY port`)
	if err != nil {
		return nil, scanError(err, "ports")
	}
	defer rows.Close()
	var out []*Port
	for rows.Next() {
		var (
			p         Port
			allocated string
		)
		if err := rows.Scan(&p.Port, &p.Domain, &allocated); err != nil {
			return nil, scanError(err, "ports")
		}
		p.AllocatedAt = parseTime(allocated)
		out = append(out, &p)
	}
	return out, rows.Err()
}

// StartDeployment opens a deployment record and returns its id.
func (s *Store) StartDeployment(ctx context.Context, domain string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO deployments (domain, started_at) VALUES (?,?)`, domain, now())
	if err != nil {
		return 0, rlerr.Wrap(err, rlerr.CodeGeneric, "opening a deployment record for %s", domain)
	}
	return res.LastInsertId()
}

// FinishDeployment closes a deployment record.
func (s *Store) FinishDeployment(ctx context.Context, d *Deployment) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE deployments
		SET finished_at = ?, git_sha = ?, steps = ?, ok = ?, health = ?, rolled_back = ?, error = ?
		WHERE id = ?`,
		now(), d.GitSHA, joinList(d.Steps), boolToInt(d.OK), d.Health, boolToInt(d.RolledBack), d.Error, d.ID)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "closing the deployment record for %s", d.Domain)
	}
	return nil
}

// ListDeployments returns a site's deployment history, newest first.
func (s *Store) ListDeployments(ctx context.Context, domain string, limit int) ([]*Deployment, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, domain, started_at, finished_at, git_sha, steps, ok, health, rolled_back, error
		FROM deployments WHERE domain = ? ORDER BY started_at DESC LIMIT ?`, domain, limit)
	if err != nil {
		return nil, scanError(err, "deployments")
	}
	defer rows.Close()
	var out []*Deployment
	for rows.Next() {
		var (
			d                 Deployment
			started, finished string
			steps             string
			ok, rolledBack    int
		)
		if err := rows.Scan(&d.ID, &d.Domain, &started, &finished, &d.GitSHA, &steps,
			&ok, &d.Health, &rolledBack, &d.Error); err != nil {
			return nil, scanError(err, "deployments")
		}
		d.StartedAt = parseTime(started)
		d.FinishedAt = parseTime(finished)
		d.Steps = splitList(steps)
		d.OK = ok == 1
		d.RolledBack = rolledBack == 1
		out = append(out, &d)
	}
	return out, rows.Err()
}

// LastDeployment returns a site's most recent deployment, if any.
func (s *Store) LastDeployment(ctx context.Context, domain string) (*Deployment, error) {
	list, err := s.ListDeployments(ctx, domain, 1)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, notFound("deployment for", domain)
	}
	return list[0], nil
}

// RecordEvent appends to the event log. This mirrors the audit file so that
// `export` and `site show` can report history without parsing logs.
func (s *Store) RecordEvent(ctx context.Context, e *Event) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO events (at, command, argv, uid, sudo_user, target, result, exit_code, duration_ms, detail)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		orNow(formatTime(e.At)), e.Command, e.Argv, e.UID, e.SudoUser, e.Target,
		e.Result, e.ExitCode, e.DurationMS, e.Detail)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "recording an event")
	}
	return nil
}

// ListEvents returns recent events, optionally for one target.
func (s *Store) ListEvents(ctx context.Context, target string, limit int) ([]*Event, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, at, command, argv, uid, sudo_user, target, result, exit_code, duration_ms, detail FROM events`
	args := []any{}
	if target != "" {
		q += ` WHERE target = ?`
		args = append(args, target)
	}
	q += ` ORDER BY at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, scanError(err, "events")
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		var (
			e  Event
			at string
		)
		if err := rows.Scan(&e.ID, &at, &e.Command, &e.Argv, &e.UID, &e.SudoUser,
			&e.Target, &e.Result, &e.ExitCode, &e.DurationMS, &e.Detail); err != nil {
			return nil, scanError(err, "events")
		}
		e.At = parseTime(at)
		out = append(out, &e)
	}
	return out, rows.Err()
}

// PruneEvents drops events older than the retention window, so the database does
// not grow without bound on a busy server.
func (s *Store) PruneEvents(ctx context.Context, keep time.Duration) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE at < ?`, formatTime(time.Now().Add(-keep)))
	if err != nil {
		return 0, rlerr.Wrap(err, rlerr.CodeGeneric, "pruning events")
	}
	return res.RowsAffected()
}

// Export is the whole database as a structure, for `ratline export --json` and
// for migrating a server. It deliberately contains no private key material:
// public blobs and fingerprints only.
type Export struct {
	SchemaVersion int            `json:"schema_version"`
	ExportedAt    time.Time      `json:"exported_at"`
	Users         []*User        `json:"users"`
	Sites         []*Site        `json:"sites"`
	Keys          []*Key         `json:"ssh_keys"`
	Certificates  []*Certificate `json:"certificates"`
	Ports         []*Port        `json:"ports"`
}

// Export dumps the state.
func (s *Store) Export(ctx context.Context) (*Export, error) {
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	e := &Export{SchemaVersion: version, ExportedAt: time.Now().UTC()}
	if e.Users, err = s.ListUsers(ctx); err != nil {
		return nil, err
	}
	if e.Sites, err = s.ListSites(ctx, SiteFilter{}); err != nil {
		return nil, err
	}
	if e.Keys, err = s.ListKeys(ctx, KeyFilter{IncludeRevoked: true}); err != nil {
		return nil, err
	}
	if e.Certificates, err = s.ListCertificates(ctx); err != nil {
		return nil, err
	}
	if e.Ports, err = s.ListPorts(ctx); err != nil {
		return nil, err
	}
	return e, nil
}

// Vacuum compacts the database, called by `doctor --fix`.
func (s *Store) Vacuum(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "compacting the state database")
	}
	return nil
}

var _ = strings.TrimSpace
