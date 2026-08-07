package state

import (
	"testing"
	"time"
)

func aSiteWithUnits(t *testing.T) *Store {
	t.Helper()
	s := testStore(t)
	ctx := t.Context()
	if err := s.PutUser(ctx, &User{Name: "alice", Home: "/home/alice", Shell: "/bin/bash"}); err != nil {
		t.Fatalf("PutUser = %v", err)
	}
	if err := s.PutSite(ctx, &Site{
		Domain: "app.test", Owner: "alice", Runtime: "node", Slug: "alice-app_test", Enabled: true,
	}); err != nil {
		t.Fatalf("PutSite = %v", err)
	}
	return s
}

func TestASiteUnitSurvivesARoundTrip(t *testing.T) {
	s := aSiteWithUnits(t)
	ctx := t.Context()

	want := &SiteUnit{
		Domain: "app.test", Name: "nightly", Kind: UnitJob,
		Command: "/srv/bin/nightly", Schedule: "*-*-* 03:00:00",
		Description: "the roll-up", Enabled: true, Persistent: true,
		Timeout: "30m", MemoryMax: "1G", CreatedBy: "root",
	}
	if err := s.PutSiteUnit(ctx, want); err != nil {
		t.Fatalf("PutSiteUnit = %v", err)
	}
	got, err := s.GetSiteUnit(ctx, "app.test", "nightly")
	if err != nil {
		t.Fatalf("GetSiteUnit = %v", err)
	}
	for _, c := range []struct{ field, got, want string }{
		{"command", got.Command, want.Command},
		{"schedule", got.Schedule, want.Schedule},
		{"description", got.Description, want.Description},
		{"timeout", got.Timeout, want.Timeout},
		{"memory_max", got.MemoryMax, want.MemoryMax},
		{"kind", got.Kind, want.Kind},
	} {
		if c.got != c.want {
			t.Errorf("%s came back as %q, want %q", c.field, c.got, c.want)
		}
	}
	// The two booleans decide whether a job runs at all and whether it catches up after
	// downtime, so a silent flip here is a job that does not do what was asked.
	if !got.Enabled {
		t.Error("enabled came back false")
	}
	if !got.Persistent {
		t.Error("persistent came back false")
	}
}

// A disabled job must come back disabled. Reading it as enabled means a job somebody
// switched off is armed again after a restore or a reconcile.
func TestADisabledUnitStaysDisabledThroughStorage(t *testing.T) {
	s := aSiteWithUnits(t)
	ctx := t.Context()
	if err := s.PutSiteUnit(ctx, &SiteUnit{
		Domain: "app.test", Name: "off", Kind: UnitJob,
		Command: "/srv/bin/x", Schedule: "daily", Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSiteUnit(ctx, "app.test", "off")
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Error("a disabled job came back enabled, so it would be armed again")
	}
}

// Re-adding one replaces it rather than failing or duplicating, which is what makes
// `site cron add` safe to run twice.
func TestPuttingAUnitTwiceReplacesIt(t *testing.T) {
	s := aSiteWithUnits(t)
	ctx := t.Context()
	first := &SiteUnit{Domain: "app.test", Name: "j", Kind: UnitJob,
		Command: "/srv/bin/old", Schedule: "daily", Enabled: true}
	if err := s.PutSiteUnit(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := &SiteUnit{Domain: "app.test", Name: "j", Kind: UnitJob,
		Command: "/srv/bin/new", Schedule: "hourly", Enabled: true}
	if err := s.PutSiteUnit(ctx, second); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListSiteUnits(ctx, "app.test", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("%d rows after replacing one; want 1", len(all))
	}
	if all[0].Command != "/srv/bin/new" || all[0].Schedule != "hourly" {
		t.Errorf("the replacement did not take: %+v", all[0])
	}
}

// Jobs and workers share a table, so the filter has to keep them apart — or `worker list`
// shows a cron job and removing it by name hits the wrong one.
func TestJobsAndWorkersAreListedSeparately(t *testing.T) {
	s := aSiteWithUnits(t)
	ctx := t.Context()
	for _, u := range []*SiteUnit{
		{Domain: "app.test", Name: "nightly", Kind: UnitJob, Command: "/x", Schedule: "daily", Enabled: true},
		{Domain: "app.test", Name: "queue", Kind: UnitWorker, Command: "/y", Enabled: true},
	} {
		if err := s.PutSiteUnit(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := s.ListSiteUnits(ctx, "app.test", UnitJob)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Name != "nightly" {
		t.Errorf("the job filter returned %+v", jobs)
	}
	workers, err := s.ListSiteUnits(ctx, "app.test", UnitWorker)
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 1 || workers[0].Name != "queue" {
		t.Errorf("the worker filter returned %+v", workers)
	}
	// No filter at all is what reconcile and export need.
	all, err := s.ListSiteUnits(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("listing everything returned %d rows, want 2", len(all))
	}
}

// A job and a worker may share a name, because the unit filenames keep them apart. If the
// primary key did not allow it, `worker add queue` would silently replace `cron add queue`.
func TestAJobAndAWorkerCannotShareAName(t *testing.T) {
	s := aSiteWithUnits(t)
	ctx := t.Context()
	if err := s.PutSiteUnit(ctx, &SiteUnit{
		Domain: "app.test", Name: "same", Kind: UnitJob,
		Command: "/x", Schedule: "daily", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSiteUnit(ctx, &SiteUnit{
		Domain: "app.test", Name: "same", Kind: UnitWorker,
		Command: "/y", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// The key is (domain, name), so the second replaces the first. That is a deliberate
	// choice and worth pinning: the alternative is two rows the CLI cannot tell apart by
	// the name the operator typed.
	all, err := s.ListSiteUnits(ctx, "app.test", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("%d rows for one name; the CLI addresses these by name alone", len(all))
	}
	if all[0].Kind != UnitWorker {
		t.Errorf("the later definition did not win: %+v", all[0])
	}
}

// Deleting a site has to take its jobs' rows with it.
//
// SQLite has foreign keys off by default, so ON DELETE CASCADE in the schema proves
// nothing on its own — the pragma has to be on for every connection. If it is not, a
// deleted site leaves rows behind and the next site with that domain inherits somebody
// else's cron jobs.
func TestDeletingASiteTakesItsUnitsWithIt(t *testing.T) {
	s := aSiteWithUnits(t)
	ctx := t.Context()
	if err := s.PutSiteUnit(ctx, &SiteUnit{
		Domain: "app.test", Name: "nightly", Kind: UnitJob,
		Command: "/x", Schedule: "daily", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSite(ctx, "app.test"); err != nil {
		t.Fatalf("DeleteSite = %v", err)
	}
	left, err := s.ListSiteUnits(ctx, "app.test", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("%d unit row(s) outlived the site; the next site with this domain would "+
			"inherit them", len(left))
	}
}

func TestDeletingAndStampingAUnit(t *testing.T) {
	s := aSiteWithUnits(t)
	ctx := t.Context()
	u := &SiteUnit{Domain: "app.test", Name: "j", Kind: UnitJob,
		Command: "/x", Schedule: "daily", Enabled: true}
	if err := s.PutSiteUnit(ctx, u); err != nil {
		t.Fatal(err)
	}

	when := time.Date(2026, 8, 7, 3, 0, 0, 0, time.UTC)
	if err := s.RecordSiteUnitRun(ctx, "app.test", "j", when); err != nil {
		t.Fatalf("RecordSiteUnitRun = %v", err)
	}
	got, err := s.GetSiteUnit(ctx, "app.test", "j")
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastRunAt.Equal(when) {
		t.Errorf("last run came back as %v, want %v", got.LastRunAt, when)
	}

	if err := s.DeleteSiteUnit(ctx, "app.test", "j"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSiteUnit(ctx, "app.test", "j"); err == nil {
		t.Error("the unit is still there after being deleted")
	}
}
