package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func account(t *testing.T, st *Store, email, role string) *Account {
	t.Helper()
	a := &Account{
		ID: email, Email: email, Role: role, PasswordHash: "hash",
		CreatedAt: time.Now().UTC(),
	}
	if err := st.CreateAccount(context.Background(), a); err != nil {
		t.Fatalf("CreateAccount(%s): %v", email, err)
	}
	return a
}

func TestAccountsAreUniqueByNormalisedEmail(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	account(t, st, "ops@example.com", RoleAdmin)

	// The same person capitalising differently must not get a second account, or
	// disabling one leaves the other signed in.
	err := st.CreateAccount(ctx, &Account{
		ID: "second", Email: "OPS@Example.COM", Role: RoleAdmin, PasswordHash: "hash",
	})
	if err == nil {
		t.Fatal("a second account was created for the same address in different case")
	}
	if rlerr.CodeOf(err) != rlerr.CodePrecondition {
		t.Errorf("code = %s, want precondition_failed", rlerr.CodeOf(err))
	}

	found, err := st.FindAccountByEmail(ctx, "  Ops@Example.com ")
	if err != nil {
		t.Fatalf("a padded, differently cased address did not find the account: %v", err)
	}
	if found.Email != "ops@example.com" {
		t.Errorf("stored email = %q, want the normalised form", found.Email)
	}
}

// The guard that keeps somebody able to grant access back. Every route to removing
// the last super admin is here, because they are four different statements and the
// mistake is the same one.
func TestTheLastSuperAdminCannotBeRemoved(t *testing.T) {
	ctx := context.Background()

	t.Run("demote", func(t *testing.T) {
		st := open(t)
		only := account(t, st, "boss@example.com", RoleSuperAdmin)
		account(t, st, "ops@example.com", RoleAdmin)
		if err := st.SetAccountRole(ctx, only.ID, RoleAdmin); err == nil {
			t.Fatal("the only super admin was demoted")
		}
	})

	t.Run("disable", func(t *testing.T) {
		st := open(t)
		only := account(t, st, "boss@example.com", RoleSuperAdmin)
		if err := st.SetAccountDisabled(ctx, only.ID, true); err == nil {
			t.Fatal("the only super admin was disabled")
		}
	})

	t.Run("delete", func(t *testing.T) {
		st := open(t)
		only := account(t, st, "boss@example.com", RoleSuperAdmin)
		if err := st.DeleteAccount(ctx, only.ID); err == nil {
			t.Fatal("the only super admin was deleted")
		}
	})

	// The negative case: with a second one, each of those is allowed. Without this
	// the three above would pass for an implementation that refused everything.
	t.Run("allowed once there are two", func(t *testing.T) {
		st := open(t)
		first := account(t, st, "boss@example.com", RoleSuperAdmin)
		account(t, st, "deputy@example.com", RoleSuperAdmin)
		if err := st.SetAccountRole(ctx, first.ID, RoleAdmin); err != nil {
			t.Fatalf("demoting one of two super admins was refused: %v", err)
		}
	})

	// A disabled super admin does not count towards the guard: two accounts where
	// one is switched off is one super admin, not two.
	t.Run("a disabled super admin does not count", func(t *testing.T) {
		st := open(t)
		active := account(t, st, "boss@example.com", RoleSuperAdmin)
		sleeping := account(t, st, "former@example.com", RoleSuperAdmin)
		if err := st.SetAccountDisabled(ctx, sleeping.ID, true); err != nil {
			t.Fatalf("disabling the second super admin: %v", err)
		}
		if err := st.SetAccountDisabled(ctx, active.ID, true); err == nil {
			t.Fatal("the last *active* super admin was disabled while a disabled one existed")
		}
	})
}

// Disabling somebody must end their sessions. Leaving them signed in means the
// account is disabled everywhere except where it matters.
func TestDisablingAnAccountEndsItsSessions(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	account(t, st, "boss@example.com", RoleSuperAdmin)
	target := account(t, st, "ops@example.com", RoleAdmin)

	now := time.Now().UTC()
	if err := st.CreateSession(ctx, &Session{
		TokenHash: "token-hash", CSRFToken: "csrf-token", AccountID: target.ID,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := st.FindSession(ctx, "token-hash"); err != nil {
		t.Fatalf("the session was not there to begin with: %v", err)
	}
	if err := st.SetAccountDisabled(ctx, target.ID, true); err != nil {
		t.Fatalf("SetAccountDisabled: %v", err)
	}
	if _, err := st.FindSession(ctx, "token-hash"); err == nil {
		t.Fatal("the session survived the account being disabled")
	}
}

func TestChangingAPasswordSignsEveryBrowserOut(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	a := account(t, st, "ops@example.com", RoleAdmin)
	now := time.Now().UTC()
	for _, hash := range []string{"one", "two"} {
		if err := st.CreateSession(ctx, &Session{
			TokenHash: hash, CSRFToken: hash, AccountID: a.ID,
			CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetPassword(ctx, a.ID, "new-hash"); err != nil {
		t.Fatal(err)
	}
	sessions, err := st.ListSessionsFor(ctx, a.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("%d sessions survived a password change", len(sessions))
	}
}

// An invitation link is a bearer credential that creates an administrator. It must
// work exactly once, and two people racing the same link must not both win.
func TestAnInvitationCanOnlyBeAcceptedOnce(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	inv := &Invite{
		ID: "inv-1", Email: "new@example.com", Role: RoleAdmin,
		InvitedBy: "boss@example.com", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := st.CreateInvite(ctx, inv, "token-hash"); err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if err := st.AcceptInvite(ctx, inv.ID, now); err != nil {
		t.Fatalf("the first acceptance failed: %v", err)
	}
	if err := st.AcceptInvite(ctx, inv.ID, now); err == nil {
		t.Fatal("the same invitation was accepted twice")
	}
}

func TestAnExpiredInvitationIsRefused(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	inv := &Invite{
		ID: "inv-1", Email: "new@example.com", Role: RoleAdmin,
		CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}
	if err := st.CreateInvite(ctx, inv, "token-hash"); err != nil {
		t.Fatal(err)
	}
	found, err := st.FindInviteByToken(ctx, "token-hash")
	if err != nil {
		t.Fatal(err)
	}
	if found.Pending(now) {
		t.Fatal("an expired invitation reports itself as still open")
	}
	if got := found.Status(now); got != "expired" {
		t.Errorf("Status = %q, want expired", got)
	}
	if err := st.AcceptInvite(ctx, inv.ID, now); err == nil {
		t.Fatal("an expired invitation was accepted")
	}
}

func TestRevokingAnInvitationClosesIt(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	inv := &Invite{ID: "inv-1", Email: "new@example.com", Role: RoleAdmin,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := st.CreateInvite(ctx, inv, "hash"); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeInvite(ctx, inv.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := st.AcceptInvite(ctx, inv.ID, now); err == nil {
		t.Fatal("a revoked invitation was accepted")
	}
	if err := st.RevokeInvite(ctx, inv.ID, now); err == nil {
		t.Fatal("revoking twice was allowed, which would hide a race")
	}
}

func TestFailedLoginsAreCountedByAddressAndBySource(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Three failures for one account from one address, and two more for other
	// accounts from the same address: the per-address count must see all five.
	for i := 0; i < 3; i++ {
		if err := st.RecordLoginAttempt(ctx, "ops@example.com", "203.0.113.9", false, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		if err := st.RecordLoginAttempt(ctx, email, "203.0.113.9", false, now); err != nil {
			t.Fatal(err)
		}
	}
	byEmail, byIP, err := st.FailedLoginsSince(ctx, "ops@example.com", "203.0.113.9", now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if byEmail != 3 {
		t.Errorf("byEmail = %d, want 3", byEmail)
	}
	if byIP != 5 {
		t.Errorf("byIP = %d, want 5 — an attacker spraying one password across accounts "+
			"must be counted by source", byIP)
	}

	// A success clears the account's failures, so somebody who mistyped four times
	// is not locked out afterwards.
	if err := st.ClearLoginAttempts(ctx, "ops@example.com"); err != nil {
		t.Fatal(err)
	}
	byEmail, _, err = st.FailedLoginsSince(ctx, "ops@example.com", "203.0.113.9", now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if byEmail != 0 {
		t.Errorf("byEmail after clearing = %d, want 0", byEmail)
	}
}

// A job is a child of the panel process, so a restart killed it. The row must not
// keep claiming it is running, or a deploy shows a spinner for ever.
func TestJobsInterruptedByARestartAreMarkedFailed(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	for _, id := range []string{"queued-one", "running-one"} {
		if err := st.CreateJob(ctx, &Job{ID: id, Action: "site deploy", State: JobQueued}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.StartJob(ctx, "running-one", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	n, err := st.FailStrandedJobs(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("FailStrandedJobs marked %d jobs, want 2", n)
	}
	for _, id := range []string{"queued-one", "running-one"} {
		job, err := st.FindJob(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != JobFailed {
			t.Errorf("job %s is %q after a restart, want failed", id, job.State)
		}
		if job.Error == "" {
			t.Errorf("job %s failed with no explanation", id)
		}
	}
}

// The listing must not carry transcripts: fifty deploys would be fifty megabytes of
// npm output for a page that shows five columns.
func TestListJobsOmitsTheTranscript(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	if err := st.CreateJob(ctx, &Job{ID: "one", Action: "site deploy", State: JobDone,
		Output: "a very long transcript"}); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d jobs, want 1", len(list))
	}
	if list[0].Output != "" {
		t.Error("the listing carried a transcript")
	}
	full, err := st.FindJob(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	if full.Output == "" {
		t.Error("the detail lost the transcript, which is where it is meant to be")
	}
}

func TestNotFoundIsTypedAsAPrecondition(t *testing.T) {
	st := open(t)
	_, err := st.FindAccount(context.Background(), "nobody")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error does not wrap ErrNotFound: %v", err)
	}
	if rlerr.CodeOf(err) != rlerr.CodePrecondition {
		t.Errorf("code = %s, want precondition_failed", rlerr.CodeOf(err))
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	before, err := st.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before != len(migrations) {
		t.Fatalf("schema version = %d, want %d", before, len(migrations))
	}
	// Running them again must be a no-op rather than an error, because that is
	// what every restart does.
	if err := st.migrate(ctx); err != nil {
		t.Fatalf("re-running the migrations failed: %v", err)
	}
	after, err := st.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("the schema version moved from %d to %d on a second run", before, after)
	}
}

func TestRoleOrdering(t *testing.T) {
	if !AtLeast(RoleSuperAdmin, RoleAdmin) {
		t.Error("a super admin does not satisfy an admin requirement")
	}
	if AtLeast(RoleAdmin, RoleSuperAdmin) {
		t.Error("an admin satisfies a super-admin requirement")
	}
	if AtLeast("nonsense", RoleAdmin) {
		t.Error("an unknown role satisfies an admin requirement, which is the wrong default")
	}
}
