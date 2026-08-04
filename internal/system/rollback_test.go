package system

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

func TestRollbackUnwindsInReverse(t *testing.T) {
	rb := NewRollback(log.Discard())
	var order []string
	for _, name := range []string{"user", "home", "vhost", "unit"} {
		n := name
		rb.Push("created "+n, func(context.Context) error {
			order = append(order, n)
			return nil
		})
	}
	if rb.Len() != 4 {
		t.Fatalf("Len = %d, want 4", rb.Len())
	}

	rep := rb.Unwind(context.Background())
	want := []string{"unit", "vhost", "home", "user"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("unwound %v, want %v", order, want)
	}
	if len(rep.Failed) != 0 {
		t.Errorf("Failed = %v, want none", rep.Failed)
	}
	if len(rep.Undone) != 4 {
		t.Errorf("Undone has %d entries, want 4", len(rep.Undone))
	}
	if rep.Err() != nil {
		t.Errorf("Err = %v, want nil", rep.Err())
	}
	if rb.Len() != 0 {
		t.Errorf("the stack still holds %d steps after unwinding", rb.Len())
	}
}

func TestRollbackKeepsGoingAfterAFailedStep(t *testing.T) {
	rb := NewRollback(log.Discard())
	var ran []string
	rb.Push("first", func(context.Context) error { ran = append(ran, "first"); return nil })
	rb.Push("second", func(context.Context) error { return errors.New("device busy") })
	rb.Push("third", func(context.Context) error { ran = append(ran, "third"); return nil })

	rep := rb.Unwind(context.Background())
	// Reversing two of three steps is much better than stopping at the first
	// problem, so the unwind continues.
	if len(ran) != 2 {
		t.Errorf("ran %v, want both of the succeeding steps", ran)
	}
	if len(rep.Failed) != 1 || rep.Failed[0].Desc != "second" {
		t.Errorf("Failed = %+v, want the second step", rep.Failed)
	}
	err := rep.Err()
	if err == nil {
		t.Fatal("Err = nil, want a rollback-failed error")
	}
	if !rlerr.Is(err, rlerr.CodeRollbackFailed) {
		t.Errorf("code = %v, want rollback_failed (exit 6)", rlerr.CodeOf(err))
	}
	if !strings.Contains(err.Error(), "device busy") {
		t.Errorf("error %q does not include the underlying cause", err)
	}
}

func TestRollbackCommitDiscardsTheStack(t *testing.T) {
	rb := NewRollback(log.Discard())
	called := false
	rb.Push("x", func(context.Context) error { called = true; return nil })
	rb.Commit()
	if rb.Len() != 0 {
		t.Errorf("Len after Commit = %d, want 0", rb.Len())
	}
	// A helper that registers cleanup after the fact must not resurrect it.
	rb.Push("late", func(context.Context) error { called = true; return nil })
	rb.Unwind(context.Background())
	if called {
		t.Error("an undo action ran after Commit")
	}
}

func TestUnwindOnCommitsOnSuccess(t *testing.T) {
	undone := false
	err := func() (err error) {
		rb := NewRollback(log.Discard())
		defer rb.UnwindOn(context.Background(), &err)
		rb.Push("x", func(context.Context) error { undone = true; return nil })
		return nil
	}()
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if undone {
		t.Error("UnwindOn rolled back a successful operation")
	}
}

func TestUnwindOnRollsBackOnFailure(t *testing.T) {
	undone := false
	sentinel := rlerr.Preconditionf("nginx -t failed")
	err := func() (err error) {
		rb := NewRollback(log.Discard())
		defer rb.UnwindOn(context.Background(), &err)
		rb.Push("wrote the vhost", func(context.Context) error { undone = true; return nil })
		return sentinel
	}()
	if !undone {
		t.Error("UnwindOn did not roll back a failed operation")
	}
	// A clean rollback keeps the original error and its exit code: the caller
	// needs to know what failed, not that a rollback happened.
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want the original error", err)
	}
	if !rlerr.Is(err, rlerr.CodePrecondition) {
		t.Errorf("code = %v, want precondition", rlerr.CodeOf(err))
	}
}

func TestUnwindOnEscalatesWhenRollbackAlsoFails(t *testing.T) {
	original := rlerr.Preconditionf("nginx -t failed")
	err := func() (err error) {
		rb := NewRollback(log.Discard())
		defer rb.UnwindOn(context.Background(), &err)
		rb.Push("wrote the vhost", func(context.Context) error { return errors.New("read-only filesystem") })
		return original
	}()
	// Exit 6 is the signal that a human has to look at the server.
	if !rlerr.Is(err, rlerr.CodeRollbackFailed) {
		t.Errorf("code = %v, want rollback_failed (exit 6)", rlerr.CodeOf(err))
	}
	if !errors.Is(err, original) {
		t.Error("the original cause was lost when the rollback failed")
	}
	if !strings.Contains(err.Error(), "read-only filesystem") {
		t.Errorf("error %q does not say why the rollback failed", err)
	}
}

func TestNilRollbackIsUsable(t *testing.T) {
	// A nil stack keeps call sites free of nil checks.
	var rb *Rollback
	rb.Push("x", func(context.Context) error { return nil })
	rb.Commit()
	if rb.Len() != 0 {
		t.Error("a nil Rollback reported steps")
	}
	if rep := rb.Unwind(context.Background()); rep == nil || rep.Err() != nil {
		t.Error("unwinding a nil Rollback misbehaved")
	}
}
