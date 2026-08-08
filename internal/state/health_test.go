package state

import (
	"testing"
	"time"
)

func aSiteForHealth(t *testing.T) *Store {
	t.Helper()
	s := testStore(t)
	ctx := t.Context()
	if err := s.PutUser(ctx, &User{Name: "alice", Home: "/home/alice", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSite(ctx, &Site{
		Domain: "app.test", Owner: "alice", Runtime: "node", Slug: "alice-app_test", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

// "Failing since" is the whole value of recording these: anyone can see a site is down
// now, and the useful question is how long it has been. The streak has to survive being
// written by two different callers — the timer and somebody typing the command — or the
// answer depends on who asked last.
func TestAFailureStreakAccumulatesAndFailingSinceHolds(t *testing.T) {
	s := aSiteForHealth(t)
	ctx := t.Context()

	first := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := s.RecordHealth(ctx, &Health{
			Domain: "app.test", CheckedAt: first.Add(time.Duration(i) * 5 * time.Minute),
			OK: false, Detail: "HTTP 500",
		}); err != nil {
			t.Fatal(err)
		}
	}
	h, err := s.GetHealth(ctx, "app.test")
	if err != nil {
		t.Fatal(err)
	}
	if h.ConsecutiveFailures != 3 {
		t.Errorf("consecutive failures = %d after three failures, want 3", h.ConsecutiveFailures)
	}
	// The since must be the *first* failure, not the most recent check. Moving it forward
	// each time would make a site that has been down for a day report as down for five
	// minutes, which is the opposite of useful.
	if !h.FailingSince.Equal(first) {
		t.Errorf("failing since = %v, want the first failure at %v", h.FailingSince, first)
	}
}

// Recovery has to clear both, or a site that came back still reads as broken.
func TestRecoveryResetsTheStreak(t *testing.T) {
	s := aSiteForHealth(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 2; i++ {
		if err := s.RecordHealth(ctx, &Health{
			Domain: "app.test", CheckedAt: now, OK: false, Detail: "refused"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordHealth(ctx, &Health{
		Domain: "app.test", CheckedAt: now.Add(time.Hour), OK: true, StatusCode: 200}); err != nil {
		t.Fatal(err)
	}
	h, err := s.GetHealth(ctx, "app.test")
	if err != nil {
		t.Fatal(err)
	}
	if h.ConsecutiveFailures != 0 {
		t.Errorf("a recovered site still reports %d consecutive failures", h.ConsecutiveFailures)
	}
	if !h.FailingSince.IsZero() {
		t.Errorf("a recovered site still reports failing since %v", h.FailingSince)
	}
	if !h.OK {
		t.Error("a recovered site is still recorded as failing")
	}
}

// Deleting a site must take its health row: otherwise the next site with that domain
// inherits a failure streak from a predecessor it has nothing to do with.
func TestDeletingASiteTakesItsHealthRow(t *testing.T) {
	s := aSiteForHealth(t)
	ctx := t.Context()
	if err := s.RecordHealth(ctx, &Health{
		Domain: "app.test", CheckedAt: time.Now().UTC(), OK: false, Detail: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSite(ctx, "app.test"); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, still := all["app.test"]; still {
		t.Error("the health row outlived the site, so a new site on this domain would " +
			"inherit its failure streak")
	}
}
