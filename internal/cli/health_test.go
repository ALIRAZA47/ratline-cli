package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

// A stale check must not read as current.
//
// A recorded "healthy" from four days ago, on a server whose timer stopped, is worse than
// no answer at all: somebody looks at doctor, sees nothing wrong, and believes they have
// monitoring. The staleness cutoff is what turns that into a warning.
func TestAStaleHealthCheckIsNotBelieved(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	fresh := &state.Health{CheckedAt: now.Add(-5 * time.Minute), OK: true}
	if staleHealth(fresh, now) {
		t.Error("a check from five minutes ago was called stale")
	}
	old := &state.Health{CheckedAt: now.Add(-4 * 24 * time.Hour), OK: true}
	if !staleHealth(old, now) {
		t.Error("a check from four days ago was treated as current, which is how somebody " +
			"believes they have monitoring when the timer has stopped")
	}
	// Never checked at all is stale by definition — there is nothing to believe.
	if !staleHealth(nil, now) {
		t.Error("a site that was never checked was treated as current")
	}
	if !staleHealth(&state.Health{OK: true}, now) {
		t.Error("a row with no timestamp was treated as current")
	}
}

// The summary is what appears in the status table, so it has to say how long as well as
// what: one failure and forty are different situations.
func TestTheHealthSummarySaysHowBadItIs(t *testing.T) {
	if got := healthSummary(&state.Health{OK: true, LatencyMS: 12}); !strings.Contains(got, "healthy") {
		t.Errorf("a healthy site summarised as %q", got)
	}
	once := healthSummary(&state.Health{OK: false, Detail: "HTTP 500", ConsecutiveFailures: 1})
	if !strings.Contains(once, "FAILING") || !strings.Contains(once, "HTTP 500") {
		t.Errorf("a single failure summarised as %q", once)
	}
	many := healthSummary(&state.Health{OK: false, Detail: "HTTP 500", ConsecutiveFailures: 40})
	if !strings.Contains(many, "40") {
		t.Errorf("forty consecutive failures summarised as %q, which reads the same as one", many)
	}
	if healthSummary(nil) != "" {
		t.Error("a site with no health row produced a summary")
	}
}
