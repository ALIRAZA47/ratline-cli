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
//
// A stack that is deliberately not unwound declares itself, in the comment directly
// above it:
//
//	// rollback-exception: reconcile --fix logs and moves to the next site
//	rb := system.NewRollback(g.Log)
//
// The declaration is the point — an exception is a decision somebody defends in review,
// not something that quietly accumulates — and it lives beside the code it excuses so
// that the next reader of *that* function can see why the stack has no unwind. This was
// a map in here keyed by file:line, which had the same intent and a worse key: any edit
// higher up the file shifted the numbers, failing this test in another package with a
// message about a line the author never touched. Three times in one change.
//
// `go test -v ./internal/system -run TestEveryRollbackStackIsUnwound` lists every
// exception being honoured, which is the roll-call the map used to provide. A marker on
// a stack that *is* unwound fails too: an exception nobody needs is a rule that has
// quietly loosened.
func TestEveryRollbackStackIsUnwound(t *testing.T) {
	root := repoRoot(t)
	create := regexp.MustCompile(`(\w+)\s*:=\s*(?:system\.)?NewRollback\(`)

	found, excused := 0, 0
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
		// Every marker in the file, struck off as a stack claims it. What is left over
		// is a marker excusing nothing — the stack it was written for has been deleted
		// or moved away, and the comment now tells the next reader something untrue.
		// This is what the old file:line map's staleness check was for.
		dangling := map[int]string{}
		for i, line := range lines {
			if reason, ok := markerReason(line); ok {
				dangling[i] = reason
			}
		}
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

			next := ""
			if i+1 < len(lines) {
				next = strings.TrimSpace(lines[i+1])
			}
			// The explicit shape may be anywhere in the enclosing function, bounded
			// by the next top-level declaration so it cannot borrow another's.
			unwound := next == "defer "+name+".UnwindOn(ctx, &err)" ||
				hasExplicitUnwind(lines[i+1:], name)

			reason, at, marked := rollbackException(lines, i)
			delete(dangling, at)
			switch {
			case marked && unwound:
				t.Errorf("%s is marked rollback-exception but the stack is unwound.\n"+
					"  remove the marker: an exception nobody needs is a rule that has quietly loosened",
					where)
			case marked && reason == "":
				t.Errorf("%s is marked rollback-exception with no reason.\n"+
					"  write why after the colon; the marker exists to be defended, not to silence the check",
					where)
			case marked:
				excused++
				t.Logf("%s: excused, %s", where, reason)
			case !unwound:
				t.Errorf("%s builds a rollback stack that is never unwound.\n"+
					"  expected the next line to be: defer %s.UnwindOn(ctx, &err)\n"+
					"  got:                         %s\n"+
					"  or an explicit %s.Unwind(ctx) in the same function.\n"+
					"  a stack that is never unwound leaves a failed command half applied.\n"+
					"  if not unwinding is deliberate, say so above it: // rollback-exception: <why>",
					where, name, next, name)
			}
		}
		for i, reason := range dangling {
			t.Errorf("%s:%d carries a rollback-exception marker (%s) with no rollback stack "+
				"under it.\n  remove it: it excuses nothing and tells the next reader "+
				"something untrue", rel, i+1, reason)
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
	t.Logf("%d rollback stacks, %d excused", found, excused)
}

// rollbackException reads the comment block directly above line i and returns the reason
// given by a `rollback-exception:` marker there, and the line the marker itself is on.
//
// Directly above, and contiguous: the same adjacency the deferred unwind is held to, so
// that a marker cannot drift away from the stack it excuses and end up excusing the next
// one somebody writes underneath it. The reason may wrap onto the following comment
// lines, but only the marker line's own text is returned and logged — so write a
// complete clause on that line and elaborate underneath, or the roll-call reads as a
// sentence cut in half.
func rollbackException(lines []string, i int) (reason string, at int, marked bool) {
	for j := i - 1; j >= 0; j-- {
		t := strings.TrimSpace(lines[j])
		if !strings.HasPrefix(t, "//") {
			return "", -1, false
		}
		if rest, ok := markerReason(t); ok {
			return rest, j, true
		}
	}
	return "", -1, false
}

// markerReason returns the text after `rollback-exception:` on a comment line.
func markerReason(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "//") {
		return "", false
	}
	t = strings.TrimSpace(strings.TrimPrefix(t, "//"))
	rest, ok := strings.CutPrefix(t, "rollback-exception:")
	return strings.TrimSpace(rest), ok
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

// The marker's own rules, pinned here because the guard above cannot check them on the
// repository's own two exceptions: those are both well-formed, so a parser that quietly
// stopped honouring adjacency — or started honouring an empty reason — would go on
// passing while the rule it enforces had changed underneath it.
func TestRollbackExceptionMarker(t *testing.T) {
	for _, tc := range []struct {
		name   string
		lines  []string
		marked bool
		reason string
	}{
		{
			name:   "directly above",
			lines:  []string{"\t// rollback-exception: the loop continues", "\trb := system.NewRollback(log)"},
			marked: true, reason: "the loop continues",
		},
		{
			name: "further up a comment block that reaches the stack",
			lines: []string{
				"\t// rollback-exception: the loop continues",
				"\t// past a site that could not be written, so there is nothing to undo.",
				"\trb := system.NewRollback(log)",
			},
			marked: true, reason: "the loop continues",
		},
		{
			// The same adjacency the deferred unwind is held to. A marker that may float
			// upwards ends up excusing whatever stack is written under it next.
			name: "separated by a blank line",
			lines: []string{
				"\t// rollback-exception: the loop continues", "",
				"\trb := system.NewRollback(log)",
			},
			marked: false,
		},
		{
			name: "separated by code",
			lines: []string{
				"\t// rollback-exception: the loop continues",
				"\tcert, _ := st.CertificateForSite(ctx, domain)",
				"\trb := system.NewRollback(log)",
			},
			marked: false,
		},
		{
			name:   "no reason given",
			lines:  []string{"\t// rollback-exception:", "\trb := system.NewRollback(log)"},
			marked: true, reason: "",
		},
		{
			name:   "an ordinary comment is not a marker",
			lines:  []string{"\t// the stack for this site's vhost", "\trb := system.NewRollback(log)"},
			marked: false,
		},
		{
			name:   "nothing above it at all",
			lines:  []string{"\trb := system.NewRollback(log)"},
			marked: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason, at, marked := rollbackException(tc.lines, len(tc.lines)-1)
			if marked != tc.marked {
				t.Fatalf("marked = %v, want %v", marked, tc.marked)
			}
			if reason != tc.reason {
				t.Errorf("reason = %q, want %q", reason, tc.reason)
			}
			// The line the marker is on, so the walk can strike it off and report
			// whatever is left as excusing nothing.
			if marked && (at < 0 || !strings.Contains(tc.lines[at], "rollback-exception:")) {
				t.Errorf("at = %d, which is not the marker's line", at)
			}
			if !marked && at != -1 {
				t.Errorf("at = %d with no marker, want -1", at)
			}
		})
	}
}
