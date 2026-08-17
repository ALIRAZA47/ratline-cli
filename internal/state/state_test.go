package state

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenAppliesMigrationsAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lib", "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	v, err := s.SchemaVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v != len(migrations) {
		t.Errorf("schema version = %d, want %d", v, len(migrations))
	}
	// The database holds the full map of who owns what; a tenant has no reason
	// to read it.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %04o, want 0600", got)
	}
	s.Close()

	// Re-opening must not re-run migrations.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open = %v", err)
	}
	defer s2.Close()
	if v2, _ := s2.SchemaVersion(context.Background()); v2 != v {
		t.Errorf("schema version changed on reopen: %d then %d", v, v2)
	}
}

func TestUserRoundTrip(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	u := &User{Name: "alice", UID: 1001, GID: 1001, Home: "/home/alice", Shell: "/bin/bash", CreatedBy: "ali"}
	if err := s.PutUser(ctx, u); err != nil {
		t.Fatalf("PutUser = %v", err)
	}
	got, err := s.GetUser(ctx, "alice")
	if err != nil {
		t.Fatalf("GetUser = %v", err)
	}
	if got.UID != 1001 || got.Home != "/home/alice" || got.CreatedBy != "ali" {
		t.Errorf("round trip = %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at was not set")
	}

	// PutUser is an upsert, so re-running a provisioning step is safe.
	u.Shell = "/usr/sbin/nologin"
	if err := s.PutUser(ctx, u); err != nil {
		t.Fatalf("second PutUser = %v", err)
	}
	got, _ = s.GetUser(ctx, "alice")
	if got.Shell != "/usr/sbin/nologin" {
		t.Errorf("shell = %q after update", got.Shell)
	}
	users, err := s.ListUsers(ctx)
	if err != nil || len(users) != 1 {
		t.Errorf("ListUsers = %d users, %v", len(users), err)
	}

	if _, err := s.GetUser(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUser on a missing user = %v, want ErrNotFound", err)
	}
}

// The site row is written and read through one long positional placeholder list, so a
// column inserted in the middle is inserted in four places that all have to agree. Get
// one of them out of step and the values simply shift along by one — no error, no
// constraint violation, just a site whose bun version is stored as its package manager.
// Distinct values in every neighbouring field are what makes that visible.
func TestEveryEngineFieldSurvivesTheRoundTripInItsOwnColumn(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	if err := s.PutUser(ctx, &User{Name: "alice", Home: "/home/alice", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	want := &Site{
		Domain: "edge.example.com", Owner: "alice", Runtime: "bun", Slug: "alice-edge_example_com",
		Enabled: true,
		Entry:   "src/index.tsx", NodeVersion: "22", BunVersion: "1.2",
		PackageManager: "pnpm", Listen: "port", ProcessManager: "direct",
		Port: 41007, Instances: 3, PythonVersion: "3.12",
	}
	if err := s.PutSite(ctx, want); err != nil {
		t.Fatalf("PutSite = %v", err)
	}
	got, err := s.GetSite(ctx, want.Domain)
	if err != nil {
		t.Fatalf("GetSite = %v", err)
	}
	for _, f := range [][3]any{
		{"entry", want.Entry, got.Entry},
		{"node_version", want.NodeVersion, got.NodeVersion},
		{"bun_version", want.BunVersion, got.BunVersion},
		{"package_manager", want.PackageManager, got.PackageManager},
		{"listen", want.Listen, got.Listen},
		{"process_manager", want.ProcessManager, got.ProcessManager},
		{"port", want.Port, got.Port},
		{"instances", want.Instances, got.Instances},
		{"python_version", want.PythonVersion, got.PythonVersion},
	} {
		if f[1] != f[2] {
			t.Errorf("%v: wrote %v, read back %v", f[0], f[1], f[2])
		}
	}
	if !got.Dynamic() || !got.JavaScript() {
		t.Error("a bun site must report itself both dynamic and JavaScript, " +
			"or the unit, the nginx proxy block and the asset shortcuts all skip it")
	}
}

func TestSiteRoundTripWithAliases(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	if err := s.PutUser(ctx, &User{Name: "alice", Home: "/home/alice", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	site := &Site{
		Domain: "example.com", Owner: "alice", Runtime: "python", Slug: "alice-example_com",
		Enabled: true, AppModule: "app.main:app", ASGI: true, Workers: 3,
		Aliases: []string{"www.example.com", "cdn.example.com"},
	}
	if err := s.PutSite(ctx, site); err != nil {
		t.Fatalf("PutSite = %v", err)
	}
	got, err := s.GetSite(ctx, "example.com")
	if err != nil {
		t.Fatalf("GetSite = %v", err)
	}
	if got.AppModule != "app.main:app" || !got.ASGI || got.Workers != 3 {
		t.Errorf("round trip = %+v", got)
	}
	if len(got.Aliases) != 2 {
		t.Errorf("aliases = %v", got.Aliases)
	}
	if !got.Dynamic() {
		t.Error("a python site does not report itself dynamic")
	}

	// An operator who types the www name should still find the site.
	byAlias, err := s.FindSiteByName(ctx, "www.example.com")
	if err != nil || byAlias.Domain != "example.com" {
		t.Errorf("FindSiteByName(www) = %v, %v", byAlias, err)
	}

	// Replacing the alias list removes the old ones.
	site.Aliases = []string{"www.example.com"}
	if err := s.PutSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSite(ctx, "example.com")
	if len(got.Aliases) != 1 {
		t.Errorf("aliases after update = %v", got.Aliases)
	}
	if _, found, _ := s.NameInUse(ctx, "cdn.example.com"); found {
		t.Error("a removed alias is still recorded as in use")
	}
}

func TestAliasCannotBeClaimedTwice(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	if err := s.PutUser(ctx, &User{Name: "alice", Home: "/home/alice", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	first := &Site{Domain: "a.example.com", Owner: "alice", Runtime: "static", Slug: "alice-a_example_com",
		Aliases: []string{"shared.example.com"}}
	if err := s.PutSite(ctx, first); err != nil {
		t.Fatal(err)
	}
	// Two vhosts claiming one server_name is a configuration nginx would accept
	// and then behave unpredictably about, so it is refused here.
	second := &Site{Domain: "b.example.com", Owner: "alice", Runtime: "static", Slug: "alice-b_example_com",
		Aliases: []string{"shared.example.com"}}
	err := s.PutSite(ctx, second)
	if err == nil {
		t.Fatal("PutSite allowed two sites to claim one alias")
	}
	// The failed insert must leave nothing behind.
	if _, gerr := s.GetSite(ctx, "b.example.com"); !errors.Is(gerr, ErrNotFound) {
		t.Errorf("a partial site row survived a failed PutSite: %v", gerr)
	}
}

func TestSiteFilters(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	for _, u := range []string{"alice", "bob"} {
		if err := s.PutUser(ctx, &User{Name: u, Home: "/home/" + u, Shell: "/bin/bash"}); err != nil {
			t.Fatal(err)
		}
	}
	sites := []*Site{
		{Domain: "a.com", Owner: "alice", Runtime: "static", Slug: "alice-a_com"},
		{Domain: "b.com", Owner: "alice", Runtime: "python", Slug: "alice-b_com"},
		{Domain: "c.com", Owner: "bob", Runtime: "node", Slug: "bob-c_com"},
	}
	for _, site := range sites {
		if err := s.PutSite(ctx, site); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.ListSites(ctx, SiteFilter{Owner: "alice"}); len(got) != 2 {
		t.Errorf("filter by owner returned %d sites", len(got))
	}
	if got, _ := s.ListSites(ctx, SiteFilter{Runtime: "node"}); len(got) != 1 {
		t.Errorf("filter by runtime returned %d sites", len(got))
	}
	if n, _ := s.CountSitesForUser(ctx, "alice"); n != 2 {
		t.Errorf("CountSitesForUser = %d, want 2", n)
	}

	// Deleting a user cascades to their sites, so no orphan rows remain to
	// confuse the next reconcile.
	if err := s.DeleteUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ListSites(ctx, SiteFilter{}); len(got) != 1 {
		t.Errorf("after deleting alice, %d sites remain", len(got))
	}
}

func TestKeyRoundTripAndScopes(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	fp := "SHA256:abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"

	// The same laptop key at two scopes is two rows, which is what the unique
	// constraint on (fingerprint, scope, owner, site) allows.
	global := &Key{Label: "Ali MacBook", Fingerprint: fp, Algorithm: "ssh-ed25519", Blob: "AAAA", Scope: ScopeGlobal, Source: "manual"}
	perUser := &Key{Label: "Ali MacBook", Fingerprint: fp, Algorithm: "ssh-ed25519", Blob: "AAAA", Scope: ScopeUser, Owner: "alice", Source: "manual"}
	for _, k := range []*Key{global, perUser} {
		if err := s.PutKey(ctx, k); err != nil {
			t.Fatalf("PutKey = %v", err)
		}
	}
	locations, err := s.FingerprintLocations(ctx, fp)
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) != 2 {
		t.Errorf("the fingerprint appears in %d places, want 2", len(locations))
	}

	if got, _ := s.ListKeys(ctx, KeyFilter{Scope: ScopeUser, Owner: "alice"}); len(got) != 1 {
		t.Errorf("filter by scope returned %d keys", len(got))
	}
	if n, _ := s.CountKeysInScope(ctx, ScopeGlobal, "", ""); n != 1 {
		t.Errorf("CountKeysInScope = %d, want 1", n)
	}

	// Lookup by label, id and fingerprint prefix, because that is what an
	// operator has to hand from a listing.
	for _, needle := range []string{"Ali MacBook", global.ID, fp, fp[:16]} {
		if got, err := s.FindKeys(ctx, needle, KeyFilter{}); err != nil || len(got) == 0 {
			t.Errorf("FindKeys(%q) = %d keys, %v", needle, len(got), err)
		}
	}

	// Revoking keeps the record so the audit trail still shows it existed.
	if err := s.RevokeKey(ctx, global.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ListKeys(ctx, KeyFilter{Scope: ScopeGlobal}); len(got) != 0 {
		t.Error("a revoked key is still listed")
	}
	if got, _ := s.ListKeys(ctx, KeyFilter{Scope: ScopeGlobal, IncludeRevoked: true}); len(got) != 1 {
		t.Error("a revoked key was deleted rather than marked")
	}
	if n, _ := s.CountKeysInScope(ctx, ScopeGlobal, "", ""); n != 0 {
		t.Errorf("a revoked key still counts towards the scope: %d", n)
	}
}

func TestKeyExpiryExcludedFromCount(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	if err := s.PutKey(ctx, &Key{
		Label: "old contractor", Fingerprint: "SHA256:x", Algorithm: "ssh-ed25519", Blob: "A",
		Scope: ScopeGlobal, Source: "manual", ExpiresAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	// An expired key must not be mistaken for a working credential when the
	// last-global-key guard runs.
	if n, _ := s.CountKeysInScope(ctx, ScopeGlobal, "", ""); n != 0 {
		t.Errorf("an expired key counts as live: %d", n)
	}
}

func TestKeyUsageTracking(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	fp := "SHA256:usage"
	if err := s.PutKey(ctx, &Key{Label: "CI", Fingerprint: fp, Algorithm: "ssh-ed25519", Blob: "A", Scope: ScopeGlobal, Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	older := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 2, 14, 11, 0, 0, time.UTC)

	if err := s.RecordKeyUsage(ctx, fp, newer, "203.0.113.19", "publickey"); err != nil {
		t.Fatal(err)
	}
	// Re-reading the same log line must not double count or move the watermark
	// backwards.
	if err := s.RecordKeyUsage(ctx, fp, newer, "203.0.113.19", "publickey"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordKeyUsage(ctx, fp, older, "198.51.100.1", "publickey"); err != nil {
		t.Fatal(err)
	}
	keys, _ := s.ListKeys(ctx, KeyFilter{Fingerprint: fp})
	if len(keys) != 1 {
		t.Fatalf("expected one key, got %d", len(keys))
	}
	if !keys[0].LastUsedAt.Equal(newer) {
		t.Errorf("last used = %v, want the most recent observation %v", keys[0].LastUsedAt, newer)
	}
	if keys[0].LastUsedIP != "203.0.113.19" {
		t.Errorf("last used ip = %q", keys[0].LastUsedIP)
	}
}

func TestPortAllocation(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	free := func(int) bool { return true }

	p1, err := s.AllocatePort(ctx, "a.com", 20000, 20003, free)
	if err != nil || p1 != 20000 {
		t.Fatalf("AllocatePort = %d, %v", p1, err)
	}
	// Re-allocating for the same site returns the same port, so `site add` is
	// idempotent.
	again, err := s.AllocatePort(ctx, "a.com", 20000, 20003, free)
	if err != nil || again != p1 {
		t.Errorf("re-allocation returned %d, want %d", again, p1)
	}
	p2, _ := s.AllocatePort(ctx, "b.com", 20000, 20003, free)
	if p2 == p1 {
		t.Error("two sites were given the same port")
	}

	// A port held by something else on the host is skipped.
	p3, err := s.AllocatePort(ctx, "c.com", 20000, 20003, func(p int) bool { return p != 20002 })
	if err != nil || p3 == 20002 {
		t.Errorf("AllocatePort = %d, %v; want it to skip the busy port", p3, err)
	}

	if err := s.ReleasePort(ctx, "a.com"); err != nil {
		t.Fatal(err)
	}
	reused, _ := s.AllocatePort(ctx, "d.com", 20000, 20003, free)
	if reused != 20000 {
		t.Errorf("a released port was not reused: got %d", reused)
	}

	// Exhaustion is a precondition failure with a hint, not a silent zero.
	if _, err := s.AllocatePort(ctx, "e.com", 20000, 20003, func(int) bool { return false }); err == nil {
		t.Error("AllocatePort succeeded with no free ports")
	}
}

func TestCertificateRoundTripAndCoverage(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	c := &Certificate{
		Name: "example.com", Source: CertSourceLetsEncrypt, Issuer: "R11",
		NotAfter: time.Now().Add(45 * 24 * time.Hour), AutoRenew: true,
		SANs: []string{"example.com", "www.example.com"},
	}
	if err := s.PutCertificate(ctx, c); err != nil {
		t.Fatalf("PutCertificate = %v", err)
	}
	got, err := s.GetCertificate(ctx, "example.com")
	if err != nil {
		t.Fatalf("GetCertificate = %v", err)
	}
	if len(got.SANs) != 2 || !got.AutoRenew {
		t.Errorf("round trip = %+v", got)
	}
	if !got.Trusted() {
		t.Error("a Let's Encrypt certificate is not reported trusted")
	}
	if d := got.DaysRemaining(time.Now()); d < 44 || d > 45 {
		t.Errorf("DaysRemaining = %d, want about 45", d)
	}

	// One SAN certificate can serve several vhosts.
	for _, d := range []string{"example.com", "www.example.com"} {
		if err := s.AttachCertificate(ctx, "example.com", d); err != nil {
			t.Fatal(err)
		}
	}
	got, _ = s.GetCertificate(ctx, "example.com")
	if len(got.Attached) != 2 {
		t.Errorf("attachments = %v", got.Attached)
	}
	if forSite, err := s.CertificateForSite(ctx, "www.example.com"); err != nil || forSite.Name != "example.com" {
		t.Errorf("CertificateForSite = %v, %v", forSite, err)
	}
}

func TestCertificateCoversWildcard(t *testing.T) {
	c := &Certificate{SANs: []string{"example.com", "*.example.com"}}
	for _, name := range []string{"example.com", "www.example.com", "api.example.com", "WWW.EXAMPLE.COM"} {
		if !c.Covers(name) {
			t.Errorf("Covers(%q) = false, want true", name)
		}
	}
	// A wildcard matches exactly one label, as a TLS client would enforce.
	for _, name := range []string{"a.b.example.com", "example.org", "notexample.com", ".example.com"} {
		if c.Covers(name) {
			t.Errorf("Covers(%q) = true, want false", name)
		}
	}
}

func TestStagingAndSelfSignedAreNotTrusted(t *testing.T) {
	for _, source := range []string{CertSourceStaging, CertSourceSelfSigned} {
		c := &Certificate{Source: source}
		if c.Trusted() {
			t.Errorf("a %s certificate reports itself trusted", source)
		}
	}
}

func TestRenewalBookkeepingCountsConsecutiveFailures(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	if err := s.PutCertificate(ctx, &Certificate{Name: "example.com", Source: CertSourceLetsEncrypt, AutoRenew: true}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if err := s.RecordRenewal(ctx, "example.com", "failure", "DNS mismatch"); err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetCertificate(ctx, "example.com")
		if got.ConsecutiveFailures != i {
			t.Errorf("after %d failures, consecutive_failures = %d", i, got.ConsecutiveFailures)
		}
	}
	if err := s.RecordRenewal(ctx, "example.com", "success", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetCertificate(ctx, "example.com")
	if got.ConsecutiveFailures != 0 {
		t.Errorf("a success did not reset the failure count: %d", got.ConsecutiveFailures)
	}
}

func TestACMERateLimitBudget(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	at := time.Now().UTC()
	sanSet := SANSetKey([]string{"example.com", "www.example.com"})

	// Five duplicates of the same SAN set this week, plus a failure.
	for i := 0; i < 5; i++ {
		if err := s.RecordACMEAttempt(ctx, &ACMEAttempt{
			RegisteredDomain: "example.com", Domain: "example.com", SANSet: sanSet,
			AttemptedAt: at.Add(-time.Duration(i) * time.Hour), Outcome: ACMESuccess,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordACMEAttempt(ctx, &ACMEAttempt{
		RegisteredDomain: "example.com", Domain: "example.com", SANSet: sanSet,
		AttemptedAt: at.Add(-10 * time.Minute), Outcome: ACMEFailure, ErrorClass: "dns",
	}); err != nil {
		t.Fatal(err)
	}
	// Staging attempts are rate-limited separately and must not count.
	if err := s.RecordACMEAttempt(ctx, &ACMEAttempt{
		RegisteredDomain: "example.com", Domain: "example.com", SANSet: sanSet,
		AttemptedAt: at, Outcome: ACMESuccess, Staging: true,
	}); err != nil {
		t.Fatal(err)
	}

	u, err := s.ACMEUsageFor(ctx, "example.com", sanSet, at)
	if err != nil {
		t.Fatalf("ACMEUsageFor = %v", err)
	}
	if u.CertsThisWeek != 5 {
		t.Errorf("certs this week = %d, want 5 (staging excluded)", u.CertsThisWeek)
	}
	if u.DuplicatesThisWeek != 5 {
		t.Errorf("duplicates this week = %d, want 5", u.DuplicatesThisWeek)
	}
	if u.FailuresThisHour != 1 {
		t.Errorf("failures this hour = %d, want 1", u.FailuresThisHour)
	}
	if u.OldestThisWeek.IsZero() {
		t.Error("the oldest attempt was not reported, so no countdown can be shown")
	}
}

func TestSANSetKeyIsOrderIndependent(t *testing.T) {
	a := SANSetKey([]string{"example.com", "www.example.com"})
	b := SANSetKey([]string{"WWW.example.com", "example.com", "example.com"})
	if a != b {
		t.Errorf("SANSetKey is order- or case-sensitive: %q vs %q", a, b)
	}
}

func TestDeploymentHistory(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	if err := s.PutUser(ctx, &User{Name: "alice", Home: "/home/alice", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSite(ctx, &Site{Domain: "example.com", Owner: "alice", Runtime: "node", Slug: "alice-example_com"}); err != nil {
		t.Fatal(err)
	}
	id, err := s.StartDeployment(ctx, "example.com")
	if err != nil {
		t.Fatalf("StartDeployment = %v", err)
	}
	if err := s.FinishDeployment(ctx, &Deployment{
		ID: id, Domain: "example.com", GitSHA: "deadbeef",
		Steps: []string{"pull", "install", "build", "restart"}, OK: true, Health: "200 in 1.2s",
	}); err != nil {
		t.Fatalf("FinishDeployment = %v", err)
	}
	last, err := s.LastDeployment(ctx, "example.com")
	if err != nil {
		t.Fatalf("LastDeployment = %v", err)
	}
	if !last.OK || last.GitSHA != "deadbeef" || len(last.Steps) != 4 {
		t.Errorf("deployment = %+v", last)
	}
}

func TestExportCarriesNoPrivateKeyMaterial(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	if err := s.PutUser(ctx, &User{Name: "alice", Home: "/home/alice", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutKey(ctx, &Key{Label: "k", Fingerprint: "SHA256:x", Algorithm: "ssh-ed25519",
		Blob: "AAAAC3NzaC1lZDI1NTE5", Scope: ScopeUser, Owner: "alice", Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	e, err := s.Export(ctx)
	if err != nil {
		t.Fatalf("Export = %v", err)
	}
	if len(e.Users) != 1 || len(e.Keys) != 1 {
		t.Errorf("export = %+v", e)
	}
	// Only public blobs are ever stored, so there is nothing private to leak;
	// this asserts the schema keeps it that way.
	if e.Keys[0].Blob == "" {
		t.Error("the public blob is missing from the export")
	}
	if e.SchemaVersion != len(migrations) {
		t.Errorf("schema version = %d", e.SchemaVersion)
	}
}

func TestTxRollsBackOnError(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	sentinel := errors.New("boom")

	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO users (name, uid, gid, home, shell, created_at, updated_at)
			 VALUES ('ghost', 0, 0, '/home/ghost', '/bin/bash', '2026-08-04T00:00:00Z', '2026-08-04T00:00:00Z')`); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Tx = %v, want the callback's error", err)
	}
	// A failure part way through must leave no rows behind to confuse the next
	// reconcile.
	if _, err := s.GetUser(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the row survived a rolled-back transaction: %v", err)
	}
}

func TestUpgradeFromAnOlderSchemaReachesTheSameShape(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")

	// Apply only migration 1, which is what a server installed before the process
	// manager column existed has on disk.
	full := migrations
	migrations = full[:1]
	old, err := Open(path)
	if err != nil {
		t.Fatalf("Open at version 1 = %v", err)
	}
	if v, _ := old.SchemaVersion(ctx); v != 1 {
		t.Fatalf("schema version = %d, want 1", v)
	}
	if err := old.PutUser(ctx, &User{Name: "alice", Home: "/home/alice", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	old.Close()
	migrations = full

	// Upgrading has to carry the existing row forward rather than start over.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("upgrading = %v", err)
	}
	defer s.Close()
	if v, _ := s.SchemaVersion(ctx); v != len(migrations) {
		t.Errorf("schema version = %d, want %d", v, len(migrations))
	}
	if _, err := s.GetUser(ctx, "alice"); err != nil {
		t.Errorf("the upgrade lost an existing row: %v", err)
	}

	// And the added column has to be usable, not merely present.
	site := &Site{Domain: "app.example.com", Owner: "alice", Runtime: "node",
		Slug: "alice-app_example_com", Entry: "server.js", ProcessManager: "pm2"}
	if err := s.PutSite(ctx, site); err != nil {
		t.Fatalf("PutSite after the upgrade = %v", err)
	}
	got, err := s.GetSite(ctx, "app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.ProcessManager != "pm2" {
		t.Errorf("process_manager = %q, want pm2", got.ProcessManager)
	}
}

func TestProcessManagerDefaultsToEmptyMeaningTheConfiguredDefault(t *testing.T) {
	s, ctx := testStore(t), context.Background()
	if err := s.PutUser(ctx, &User{Name: "alice", Home: "/home/alice", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	// Empty rather than "pm2": a site that never chose is meant to follow the
	// server's configured default, so writing the default into the row would
	// silently pin it and make a configuration change a no-op.
	site := &Site{Domain: "b.example.com", Owner: "alice", Runtime: "node",
		Slug: "alice-b_example_com", Entry: "server.js"}
	if err := s.PutSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSite(ctx, "b.example.com")
	if got.ProcessManager != "" {
		t.Errorf("process_manager = %q, want it left empty", got.ProcessManager)
	}
}
