// Package diag is the diagnostic engine behind `ratline troubleshoot`.
//
// It exists because a list of findings is the wrong answer to "why is this
// broken". `ratline doctor` sweeps the whole server and reports everything it
// notices, in whatever order the checks happen to run — which is right for a cron
// job and leaves a human holding a list they then have to rank themselves.
//
// The engine here inverts that. Checks declare what they depend on, they run in
// dependency order, and a check whose dependency failed is not run at all: it is
// reported as skipped, naming the step that has to pass first. So the first failure
// in the output is the cause, and the things it broke do not appear as separate
// problems competing for attention.
//
// The subject is deliberately not a site. A tenant, an SSH key, a certificate,
// nginx, sshd and the host itself all have the same shape of question — an ordered
// chain of preconditions where the first broken link explains the rest — so they
// share one engine, one output format and one JSON schema rather than accumulating
// a bespoke diagnostic per resource.
package diag

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Verdict is the outcome of one check.
type Verdict string

const (
	// OK — the check passed.
	OK Verdict = "ok"
	// Failed — the check found something that is definitely wrong.
	Failed Verdict = "failed"
	// Warning — worth knowing, but not why the subject is broken. An expiring
	// certificate on a site that is down is a warning; the down-ness is the failure.
	Warning Verdict = "warning"
	// Skipped — not run, either because a dependency failed or because the check
	// does not apply to this subject. Both carry a reason.
	Skipped Verdict = "skipped"
)

// Result is what a check reports.
//
// A zero Result is a pass with nothing to say, which keeps the common case to
// `return diag.Pass()`.
type Result struct {
	Verdict Verdict `json:"verdict"`
	// Detail is what was observed — the mode, the path, the status code. Present on
	// a pass as well as a failure, because "ok, mode 0660" is more useful than "ok".
	Detail string `json:"detail,omitempty"`
	// Fix is the command or action that addresses it. Required on a failure: a
	// diagnosis with no way forward is half an answer.
	Fix string `json:"fix,omitempty"`
	// Topic names a `ratline explain` page that covers this in depth.
	Topic string `json:"topic,omitempty"`
}

// Pass, Fail, Warn and Skip build the four outcomes.
func Pass(detail string, a ...any) Result {
	return Result{Verdict: OK, Detail: sprint(detail, a...)}
}

func Fail(detail string, a ...any) Result {
	return Result{Verdict: Failed, Detail: sprint(detail, a...)}
}

func Warn(detail string, a ...any) Result {
	return Result{Verdict: Warning, Detail: sprint(detail, a...)}
}

func Skip(reason string, a ...any) Result {
	return Result{Verdict: Skipped, Detail: sprint(reason, a...)}
}

// WithFix attaches the way forward.
func (r Result) WithFix(fix string, a ...any) Result {
	r.Fix = sprint(fix, a...)
	return r
}

// WithTopic attaches the `ratline explain` page that covers this.
func (r Result) WithTopic(topic string) Result {
	r.Topic = topic
	return r
}

func sprint(format string, a ...any) string {
	if len(a) == 0 {
		return format
	}
	return fmt.Sprintf(format, a...)
}

// Check is one question about the subject.
type Check struct {
	// ID is stable and machine-readable: JSON consumers and the --only flag match
	// on it, so renaming one is a breaking change.
	ID string
	// Title is the one-line human name, phrased as the thing that should be true
	// rather than as a question — "the socket has the permissions nginx needs".
	Title string
	// Needs lists the IDs that must pass first. A check whose dependency failed is
	// reported as skipped rather than run, because running it would produce a second
	// symptom of the same cause.
	Needs []string
	// Run does the work. It is never called if a dependency failed.
	Run func(ctx context.Context) Result
}

// Step is a check and what it reported.
type Step struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Verdict Verdict `json:"verdict"`
	Detail  string  `json:"detail,omitempty"`
	Fix     string  `json:"fix,omitempty"`
	Topic   string  `json:"topic,omitempty"`
	// Blocked names the failed dependency, when that is why this was skipped.
	Blocked string `json:"blocked_by,omitempty"`
}

// Report is a whole diagnosis.
type Report struct {
	// Kind and Subject identify what was looked at: "site", "app.example.com".
	Kind    string `json:"kind"`
	Subject string `json:"subject,omitempty"`
	// Summary is the one-line description of the subject, for the header.
	Summary string `json:"summary,omitempty"`
	Steps   []Step `json:"steps"`

	// Cause is the first failure — the one worth acting on.
	Cause string `json:"likely_cause,omitempty"`
	Fix   string `json:"fix,omitempty"`
	Topic string `json:"topic,omitempty"`

	Failed   int `json:"failed"`
	Warnings int `json:"warnings"`
	OK       int `json:"passed"`

	// Related is other subjects worth looking at next, when this one is healthy but
	// something it depends on is not.
	Related []string `json:"related,omitempty"`
}

// Healthy reports whether nothing failed. Warnings do not make a subject unhealthy:
// a certificate expiring in six days is worth saying and is not why the site is
// down.
func (r *Report) Healthy() bool { return r.Failed == 0 }

// Run executes a check list in dependency order.
//
// Checks are given in the order they should be *reported*, which is the order a
// request or a login travels. Dependencies are resolved within that order rather
// than by sorting, so the output reads as a walk rather than as a topological
// listing — and a Needs entry pointing forward is a programming error the caller
// finds immediately rather than a subtle reordering.
func Run(ctx context.Context, kind, subject, summary string, checks []Check) *Report {
	r := &Report{Kind: kind, Subject: subject, Summary: summary}

	// The verdict of everything that has already run, so a dependency can be
	// resolved without re-scanning the report.
	seen := map[string]Verdict{}

	for _, c := range checks {
		step := Step{ID: c.ID, Title: c.Title}

		if blocker, ok := blockedBy(c.Needs, seen); ok {
			step.Verdict = Skipped
			step.Blocked = blocker
			step.Detail = "not checked: " + blocker + " has to pass first"
			r.append(step, seen)
			continue
		}
		// A dependency that was itself skipped means this cannot be evaluated
		// either, but there is no failure to blame it on — the subject simply does
		// not have that part.
		if blocker, ok := unresolved(c.Needs, seen); ok {
			step.Verdict = Skipped
			step.Blocked = blocker
			step.Detail = "not applicable: " + blocker + " was not checked"
			r.append(step, seen)
			continue
		}

		if ctx.Err() != nil {
			step.Verdict = Skipped
			step.Detail = "not checked: the diagnosis was interrupted"
			r.append(step, seen)
			continue
		}

		res := c.Run(ctx)
		step.Verdict = res.Verdict
		if step.Verdict == "" {
			step.Verdict = OK
		}
		step.Detail, step.Fix, step.Topic = res.Detail, res.Fix, res.Topic
		r.append(step, seen)
	}

	return r
}

// append records a step and updates the counters and the cause.
func (r *Report) append(s Step, seen map[string]Verdict) {
	seen[s.ID] = s.Verdict
	r.Steps = append(r.Steps, s)

	switch s.Verdict {
	case Failed:
		r.Failed++
		// The first failure is the cause. Later ones are either consequences whose
		// dependencies were not declared, or genuinely separate problems — either
		// way the first is the one to act on.
		if r.Cause == "" {
			r.Cause, r.Fix, r.Topic = s.Detail, s.Fix, s.Topic
			if r.Cause == "" {
				r.Cause = s.Title
			}
		}
	case Warning:
		r.Warnings++
	case OK:
		r.OK++
	}
}

// blockedBy finds the first dependency that failed.
func blockedBy(needs []string, seen map[string]Verdict) (string, bool) {
	for _, id := range needs {
		if seen[id] == Failed {
			return id, true
		}
	}
	return "", false
}

// unresolved finds the first dependency that was skipped or never ran.
func unresolved(needs []string, seen map[string]Verdict) (string, bool) {
	for _, id := range needs {
		v, ran := seen[id]
		if !ran || v == Skipped {
			return id, true
		}
	}
	return "", false
}

// Validate reports a check list whose dependencies cannot be satisfied.
//
// Called by the tests rather than at run time: a Needs entry naming a check that
// does not exist, or one that runs later, would silently skip everything downstream
// and produce a diagnosis that looked complete and was not. That is exactly the
// failure a diagnostic tool must not have, and it is a property of the code rather
// than of any particular server — so a test is the right place to enforce it.
func Validate(checks []Check) error {
	var problems []string
	position := map[string]int{}
	for i, c := range checks {
		if c.ID == "" {
			problems = append(problems, fmt.Sprintf("check %d has no ID", i))
			continue
		}
		if _, dup := position[c.ID]; dup {
			problems = append(problems, fmt.Sprintf("%s is declared twice", c.ID))
		}
		if c.Run == nil {
			problems = append(problems, fmt.Sprintf("%s has no Run function", c.ID))
		}
		position[c.ID] = i
	}
	for i, c := range checks {
		for _, need := range c.Needs {
			at, ok := position[need]
			switch {
			case !ok:
				problems = append(problems,
					fmt.Sprintf("%s needs %q, which is not a check in this list", c.ID, need))
			case at >= i:
				problems = append(problems,
					fmt.Sprintf("%s needs %q, which runs later — dependencies must come first", c.ID, need))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("invalid check list:\n  %s", strings.Join(problems, "\n  "))
}
