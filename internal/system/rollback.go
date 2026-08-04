package system

import (
	"context"
	"strings"
	"sync"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// UndoFunc reverses one step of a multi-step mutation.
type UndoFunc func(context.Context) error

type step struct {
	desc string
	undo UndoFunc
}

// Rollback is the undo stack behind ratline's transactional guarantee: every
// created file, user, directory, symlink, unit, venv and port allocation
// registers how to reverse itself, and a failure unwinds them in reverse order.
//
// The alternative — a half-configured server after a failed `site add` — is the
// single worst outcome for a provisioning tool, because the operator has to
// work out what happened before they can retry.
type Rollback struct {
	mu        sync.Mutex
	steps     []step
	log       *log.Logger
	committed bool
}

// NewRollback returns an empty stack.
func NewRollback(lg *log.Logger) *Rollback {
	if lg == nil {
		lg = log.Discard()
	}
	return &Rollback{log: lg}
}

// Push records an undo action. Calls after Commit are ignored, so a helper that
// registers cleanup cannot resurrect a completed operation.
func (r *Rollback) Push(desc string, undo UndoFunc) {
	if r == nil || undo == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.committed {
		return
	}
	r.steps = append(r.steps, step{desc: desc, undo: undo})
}

// Len reports how many undo actions are pending.
func (r *Rollback) Len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.steps)
}

// Commit marks the operation successful and discards the stack.
func (r *Rollback) Commit() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.committed = true
	r.steps = nil
}

// UnwindFailure is a step whose undo action itself failed. These are what the
// operator has to clean up by hand, so they are reported individually.
type UnwindFailure struct {
	Desc string
	Err  error
}

// UnwindReport is the outcome of an unwind.
type UnwindReport struct {
	Undone []string
	Failed []UnwindFailure
}

// Err returns a rollback-failed error when any undo action failed.
func (u *UnwindReport) Err() error {
	if u == nil || len(u.Failed) == 0 {
		return nil
	}
	parts := make([]string, 0, len(u.Failed))
	for _, f := range u.Failed {
		parts = append(parts, f.Desc+" ("+f.Err.Error()+")")
	}
	return rlerr.RollbackFailedf("rollback did not complete: %s", strings.Join(parts, "; ")).
		WithHint("the server is in a partial state and needs a human; run 'ratline doctor' and 'ratline reconcile' to see what is left")
}

// Unwind runs every undo action in reverse order. It keeps going after a
// failure: reversing eight of nine steps is much better than stopping at the
// first problem.
//
// The context passed in is used for undo actions that shell out. A cancelled
// context is deliberately not honoured here — the operator pressing Ctrl-C is
// what triggered the unwind, and abandoning it half way is the worst outcome.
func (r *Rollback) Unwind(ctx context.Context) *UnwindReport {
	if r == nil {
		return &UnwindReport{}
	}
	r.mu.Lock()
	steps := r.steps
	r.steps = nil
	r.mu.Unlock()

	rep := &UnwindReport{}
	if len(steps) == 0 {
		return rep
	}
	r.log.Warn("rolling back", "steps", len(steps))
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		if err := s.undo(ctx); err != nil {
			r.log.Error("rollback step failed", "step", s.desc, "err", err)
			rep.Failed = append(rep.Failed, UnwindFailure{Desc: s.desc, Err: err})
			continue
		}
		r.log.Debug("rolled back", "step", s.desc)
		rep.Undone = append(rep.Undone, s.desc)
	}
	if len(rep.Failed) == 0 {
		r.log.Info("rolled back cleanly", "steps", len(rep.Undone))
	}
	return rep
}

// UnwindOn is the intended usage:
//
//	rb := system.NewRollback(lg)
//	defer rb.UnwindOn(ctx, &err)
//
// On a nil error it commits. Otherwise it unwinds and, if any undo action
// failed, replaces the error with a rollback-failed error (exit code 6) so that
// automation can tell "nothing happened" apart from "a human is needed".
func (r *Rollback) UnwindOn(ctx context.Context, errp *error) {
	if r == nil || errp == nil {
		return
	}
	if *errp == nil {
		r.Commit()
		return
	}
	rep := r.Unwind(ctx)
	if rollbackErr := rep.Err(); rollbackErr != nil {
		var e *rlerr.Error
		if ok := asRatlineError(rollbackErr, &e); ok {
			e.Err = *errp
			*errp = e
			return
		}
		*errp = rollbackErr
	}
}

func asRatlineError(err error, target **rlerr.Error) bool {
	if e, ok := err.(*rlerr.Error); ok {
		*target = e
		return true
	}
	return false
}
