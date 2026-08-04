package diag

import (
	"context"
	"strings"
	"testing"
)

// fixed builds a check that always reports the same thing.
func fixed(id string, v Verdict, needs ...string) Check {
	return Check{
		ID:    id,
		Title: id,
		Needs: needs,
		Run:   func(context.Context) Result { return Result{Verdict: v, Detail: id + " detail"} },
	}
}

// counted builds a check that records whether it ran, which is how "skipped" is
// distinguished from "ran and passed".
func counted(id string, v Verdict, ran *bool, needs ...string) Check {
	return Check{
		ID:    id,
		Title: id,
		Needs: needs,
		Run: func(context.Context) Result {
			*ran = true
			return Result{Verdict: v}
		},
	}
}

func step(t *testing.T, r *Report, id string) Step {
	t.Helper()
	for _, s := range r.Steps {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no step %q in the report", id)
	return Step{}
}

func TestAFailedDependencyStopsTheDownstreamCheckFromRunning(t *testing.T) {
	// This is the whole point of the engine. A check whose precondition failed would
	// report a second symptom of the same cause, and the operator would then have to
	// work out which of the two to act on.
	var ran bool
	r := Run(context.Background(), "test", "subject", "", []Check{
		fixed("socket", Failed),
		counted("app-answers", OK, &ran, "socket"),
	})

	if ran {
		t.Error("the downstream check ran even though its dependency failed")
	}
	down := step(t, r, "app-answers")
	if down.Verdict != Skipped {
		t.Errorf("verdict = %q, want skipped", down.Verdict)
	}
	if down.Blocked != "socket" {
		t.Errorf("blocked_by = %q, want the failing dependency named", down.Blocked)
	}
	if !strings.Contains(down.Detail, "socket") {
		t.Errorf("the skip reason should name what has to pass first: %q", down.Detail)
	}
}

func TestTheFirstFailureIsTheCause(t *testing.T) {
	// Later failures are either consequences whose dependency was not declared, or
	// genuinely separate problems. Either way the first is the one to act on, so it
	// is the one promoted to the headline.
	r := Run(context.Background(), "test", "subject", "", []Check{
		fixed("first", OK),
		{
			ID: "second", Title: "second",
			Run: func(context.Context) Result {
				return Fail("the real cause").WithFix("do this").WithTopic("sockets")
			},
		},
		{
			ID: "third", Title: "third",
			Run: func(context.Context) Result { return Fail("a later, unrelated failure") },
		},
	})

	if r.Cause != "the real cause" {
		t.Errorf("cause = %q, want the first failure", r.Cause)
	}
	if r.Fix != "do this" || r.Topic != "sockets" {
		t.Errorf("the fix and topic should come from the same step: %q / %q", r.Fix, r.Topic)
	}
	if r.Failed != 2 {
		t.Errorf("failed = %d, want both counted even though one is the headline", r.Failed)
	}
}

func TestASkippedDependencyIsNotApplicableRatherThanBlocked(t *testing.T) {
	// A dependency that was itself skipped means this part of the subject does not
	// exist — a node site with no PM2, say. That is not a failure to blame anything
	// on, and reporting it as blocked would imply something is broken.
	var ran bool
	r := Run(context.Background(), "test", "subject", "", []Check{
		fixed("workers", Skipped),
		counted("worker-health", OK, &ran, "workers"),
	})

	if ran {
		t.Error("a check whose dependency was skipped must not run")
	}
	down := step(t, r, "worker-health")
	if down.Verdict != Skipped {
		t.Errorf("verdict = %q, want skipped", down.Verdict)
	}
	if !strings.Contains(down.Detail, "not applicable") {
		t.Errorf("detail = %q, want it distinguished from a blocked check", down.Detail)
	}
}

func TestWarningsDoNotMakeASubjectUnhealthy(t *testing.T) {
	// A certificate expiring in six days is worth saying and is not why the site is
	// down. Conflating the two makes every diagnosis look like a failure.
	r := Run(context.Background(), "test", "subject", "", []Check{
		fixed("a", OK),
		fixed("b", Warning),
	})
	if !r.Healthy() {
		t.Error("a warning must not make the subject unhealthy")
	}
	if r.Cause != "" {
		t.Errorf("cause = %q, want none — nothing failed", r.Cause)
	}
	if r.Warnings != 1 || r.OK != 1 {
		t.Errorf("counts = %d warnings, %d ok", r.Warnings, r.OK)
	}
}

func TestAWarningDoesNotBlockDownstreamChecks(t *testing.T) {
	// Only a failure blocks. A warning is information, and suppressing the rest of
	// the walk over one would hide the actual cause.
	var ran bool
	Run(context.Background(), "test", "subject", "", []Check{
		fixed("a", Warning),
		counted("b", OK, &ran, "a"),
	})
	if !ran {
		t.Error("a warning must not stop the checks that depend on it")
	}
}

func TestAZeroResultIsAPass(t *testing.T) {
	// Keeps the common case to `return Pass("")` without every check having to spell
	// out a verdict, and stops a check that forgets to from silently reading as a
	// failure.
	r := Run(context.Background(), "test", "subject", "", []Check{
		{ID: "a", Title: "a", Run: func(context.Context) Result { return Result{} }},
	})
	if step(t, r, "a").Verdict != OK {
		t.Errorf("a zero Result should be a pass, got %q", r.Steps[0].Verdict)
	}
}

func TestCancellationStopsFurtherChecksWithoutClaimingSuccess(t *testing.T) {
	// Ctrl-C during a diagnosis must not produce a report that looks like a clean
	// bill of health for the parts that never ran.
	ctx, cancel := context.WithCancel(context.Background())
	var ran bool
	r := Run(ctx, "test", "subject", "", []Check{
		{
			ID: "first", Title: "first",
			Run: func(context.Context) Result {
				cancel()
				return Pass("")
			},
		},
		counted("second", OK, &ran),
	})
	if ran {
		t.Error("a check ran after the context was cancelled")
	}
	second := step(t, r, "second")
	if second.Verdict != Skipped || !strings.Contains(second.Detail, "interrupted") {
		t.Errorf("got %+v, want a skip that says it was interrupted", second)
	}
}

func TestValidateCatchesTheMistakesThatWouldSilentlySkipEverything(t *testing.T) {
	for _, tc := range []struct {
		name   string
		checks []Check
		want   string
	}{
		{
			"a dependency that does not exist",
			[]Check{fixed("a", OK, "nonexistent")},
			"not a check in this list",
		},
		{
			"a dependency declared later",
			[]Check{fixed("a", OK, "b"), fixed("b", OK)},
			"runs later",
		},
		{
			"a duplicate id",
			[]Check{fixed("a", OK), fixed("a", OK)},
			"declared twice",
		},
		{
			"a check with no Run",
			[]Check{{ID: "a", Title: "a"}},
			"no Run function",
		},
		{
			"a check with no id",
			[]Check{{Title: "a", Run: func(context.Context) Result { return Pass("") }}},
			"has no ID",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.checks)
			if err == nil {
				t.Fatal("Validate accepted a check list that cannot work")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestValidateAcceptsAWellFormedList(t *testing.T) {
	if err := Validate([]Check{
		fixed("a", OK),
		fixed("b", OK, "a"),
		fixed("c", OK, "a", "b"),
	}); err != nil {
		t.Errorf("Validate = %v", err)
	}
}

func TestEveryFailureCarriesAFix(t *testing.T) {
	// A diagnosis with no way forward is half an answer. Enforced on the check
	// *lists* by the per-subject tests; this pins the contract itself so the rule is
	// stated in one place.
	r := Run(context.Background(), "test", "subject", "", []Check{
		{
			ID: "a", Title: "a",
			Run: func(context.Context) Result { return Fail("broken").WithFix("fix it") },
		},
	})
	if r.Fix == "" {
		t.Error("the report should surface the failing step's fix")
	}
}
