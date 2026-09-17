package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// Every rollback stack in the repository is unwound when the operation fails.
//
// The whole promise of the stack is that a command failing halfway leaves the server
// as it was, and that promise is void the moment a caller builds one, pushes undo
// steps into it and returns an error without running them. Five methods in
// internal/site did exactly that, and internal/tls did it inside a loop. Neither was
// exotic to reach: nginx.Apply puts the previous vhost back itself when `nginx -t`
// fails, but not when the *reload* fails — there it returns with the new
// configuration already written and the undo sitting unrun in the caller's stack.
//
// Checked structurally, across packages, because the defect is an omission. A
// behavioural test of the call sites that exist today says nothing about the one
// added tomorrow, and the omission is invisible at review precisely because the code
// that is there looks complete.
//
// Two shapes count as unwinding, and both are in use:
//
//   - `defer <rb>.UnwindOn(ctx, &err)` on the line after the stack is created, which
//     is what almost everything does. It has to be the next line: a stack that is
//     created, pushed to, and only then guarded has a window, and that window is
//     where the bug lived.
//   - an explicit `<rb>.Unwind(ctx)`, for a caller that decides on a collected error
//     rather than a returned one. internal/selfupdate swaps a list of files and acts
//     on swapErr, so a deferred unwind keyed to the return value would be the wrong
//     shape.
func TestEveryRollbackStackIsUnwound(t *testing.T) {
	root := repoRoot(t)
	create := regexp.MustCompile(`(\w+)\s*:=\s*(?:system\.)?NewRollback\(`)

	// Deliberate exceptions, each with the reason it is one. A new entry here is a
	// decision someone has to defend in review, which is the point of listing them
	// rather than loosening the rule.
	allowed := map[string]string{
		// reconcile's --fix loop logs and moves to the next site rather than
		// returning. Unwinding would put the drifted vhost back, which is arguably
		// the opposite of what reconcile is for — a judgement worth making
		// deliberately rather than by inheriting this rule.
		"internal/cli/cmd_doctor.go:747": "reconcile --fix continues past a failed site",
		"internal/cli/cmd_doctor.go:777": "reconcile --fix continues past a failed unit",
	}

	found, seen := 0, map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Vendored or generated trees are not ours to hold to this.
			if name := info.Name(); name == "vendor" || name == "node_modules" ||
				name == ".git" || name == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			// Not a comment: Rollback's own doc comment shows the intended usage,
			// and matching the example would be matching the documentation.
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			m := create.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			found++
			name := m[1]
			where := rel + ":" + strconv.Itoa(i+1)
			if reason, ok := allowed[where]; ok {
				seen[where] = true
				t.Logf("%s: allowed, %s", where, reason)
				continue
			}
			next := ""
			if i+1 < len(lines) {
				next = strings.TrimSpace(lines[i+1])
			}
			if next == "defer "+name+".UnwindOn(ctx, &err)" {
				continue
			}
			// The explicit shape, anywhere in the enclosing function. Bounded by
			// the next top-level declaration so it cannot match a different one.
			if hasExplicitUnwind(lines[i+1:], name) {
				continue
			}
			t.Errorf("%s builds a rollback stack that is never unwound.\n"+
				"  expected the next line to be: defer %s.UnwindOn(ctx, &err)\n"+
				"  got:                         %s\n"+
				"  or an explicit %s.Unwind(ctx) in the same function.\n"+
				"  a stack that is never unwound leaves a failed command half applied",
				where, name, next, name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// The scan has to be finding things, or this passes on a regex that quietly
	// stopped matching — which is the failure mode of every source-reading test.
	if found < 25 {
		t.Fatalf("only found %d rollback stacks in the repository, so the scan is broken, not the code", found)
	}
	// A stale exception is a rule that has quietly loosened.
	for where, reason := range allowed {
		if !seen[where] {
			t.Errorf("the exception for %s (%s) no longer matches a rollback stack; "+
				"remove it or correct the line number", where, reason)
		}
	}
}

// hasExplicitUnwind reports whether the rest of the enclosing function calls
// <name>.Unwind(ctx) directly. It stops at the next top-level declaration so it
// cannot borrow a different function's unwind.
func hasExplicitUnwind(rest []string, name string) bool {
	want := name + ".Unwind(ctx)"
	for _, l := range rest {
		if strings.HasPrefix(l, "func ") || strings.HasPrefix(l, "type ") {
			return false
		}
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

// repoRoot walks up from the test's directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find the module root from the test's working directory")
	return ""
}
