package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

const jobColumns = `id, action, target, argv, actor_id, actor, state, queued_at, started_at,
	finished_at, exit_code, error, hint, output, dry_run`

// CreateJob queues one.
func (s *Store) CreateJob(ctx context.Context, j *Job) error {
	if j.QueuedAt.IsZero() {
		j.QueuedAt = time.Now().UTC()
	}
	if j.State == "" {
		j.State = JobQueued
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (`+jobColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.Action, j.Target, j.Argv, j.ActorID, j.Actor, j.State,
		formatTime(j.QueuedAt), formatTime(j.StartedAt), formatTime(j.FinishedAt),
		j.ExitCode, j.Error, j.Hint, j.Output, boolToInt(j.DryRun))
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "queueing the job")
	}
	return nil
}

func scanJob(sc interface{ Scan(...any) error }) (*Job, error) {
	var (
		j                         Job
		queued, started, finished string
		dryRun                    int
	)
	if err := sc.Scan(&j.ID, &j.Action, &j.Target, &j.Argv, &j.ActorID, &j.Actor, &j.State,
		&queued, &started, &finished, &j.ExitCode, &j.Error, &j.Hint, &j.Output, &dryRun); err != nil {
		return nil, err
	}
	j.QueuedAt = parseTime(queued)
	j.StartedAt = parseTime(started)
	j.FinishedAt = parseTime(finished)
	j.DryRun = dryRun == 1
	return &j, nil
}

// FindJob returns one job with its whole transcript.
func (s *Store) FindJob(ctx context.Context, id string) (*Job, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("job", id)
	}
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading the job")
	}
	return j, nil
}

// ListJobs returns jobs newest first, without their transcripts: a listing of fifty
// deploys should not carry fifty megabytes of npm output.
func (s *Store) ListJobs(ctx context.Context, limit int) ([]*Job, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, action, target, argv, actor_id, actor, state, queued_at, started_at,
			finished_at, exit_code, error, hint, '', dry_run
		 FROM jobs ORDER BY queued_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "listing jobs")
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeGeneric, "reading a job row")
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// StartJob marks a job running.
func (s *Store) StartJob(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET state = ?, started_at = ? WHERE id = ?`, JobRunning, formatTime(at), id)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "starting the job")
	}
	return nil
}

// FinishJob records the outcome and the transcript.
func (s *Store) FinishJob(ctx context.Context, j *Job) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET state = ?, finished_at = ?, exit_code = ?, error = ?, hint = ?,
			output = ? WHERE id = ?`,
		j.State, formatTime(j.FinishedAt), j.ExitCode, j.Error, j.Hint, j.Output, j.ID)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodeGeneric, "finishing the job")
	}
	return nil
}

// FailStrandedJobs marks jobs that were running when the panel stopped.
//
// A job is a child process of this daemon, so a restart kills it. Leaving the row
// saying "running" would mean a deploy that shows a spinner for ever; saying it failed
// is both true and the thing that tells somebody to look.
func (s *Store) FailStrandedJobs(ctx context.Context, at time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET state = ?, finished_at = ?, error = ?
		 WHERE state IN (?, ?)`,
		JobFailed, formatTime(at),
		"the panel restarted while this job was running; its effect on the server is unknown — "+
			"check the site and run it again if nothing changed",
		JobQueued, JobRunning)
	if err != nil {
		return 0, rlerr.Wrap(err, rlerr.CodeGeneric, "reconciling interrupted jobs")
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// PurgeJobs trims to the newest keep rows.
func (s *Store) PurgeJobs(ctx context.Context, keep int) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM jobs WHERE id NOT IN
			(SELECT id FROM jobs ORDER BY queued_at DESC LIMIT ?)`, keep)
	if err != nil {
		return 0, rlerr.Wrap(err, rlerr.CodeGeneric, "trimming the job history")
	}
	n, _ := res.RowsAffected()
	return n, nil
}
