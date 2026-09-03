package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/cli"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/panel"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/jobs"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/rl"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// fakeRunner stands in for the ratline binary.
//
// It answers `schema` with the *real* schema — built by walking the actual cobra tree
// — so the catalogue, the forms and the role gate under test are the ones a server
// would have. Everything else returns a success envelope and records what it was
// asked to run, which is what the assertions are about: the panel's job is to build
// the right argv and hand back what came out.
type fakeRunner struct {
	calls  [][]string
	stdins []string
	// reply, when set, is returned instead of a generic success envelope.
	reply    string
	exitCode int
}

func (f *fakeRunner) Run(_ context.Context, c system.Cmd) (*system.Result, error) {
	f.calls = append(f.calls, c.Args)
	in := ""
	if c.Stdin != nil {
		b, _ := io.ReadAll(c.Stdin)
		in = string(b)
	}
	f.stdins = append(f.stdins, in)

	if len(c.Args) > 0 && c.Args[0] == "schema" {
		raw, err := json.Marshal(cli.BuildSchema(cli.NewRootCommand(&cli.Globals{})))
		if err != nil {
			return nil, err
		}
		return &system.Result{Args: c.Args, Stdout: string(raw)}, nil
	}
	body := f.reply
	if body == "" {
		body = `{"ok":true,"command":"ratline","version":"test","data":{"ok":true}}`
	}
	return &system.Result{Args: c.Args, Stdout: body, ExitCode: f.exitCode}, nil
}

// lastCall returns the argv of the most recent non-schema invocation.
func (f *fakeRunner) lastCall(t *testing.T) []string {
	t.Helper()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if len(f.calls[i]) > 0 && f.calls[i][0] != "schema" {
			return f.calls[i]
		}
	}
	t.Fatal("ratline was never invoked")
	return nil
}

func (f *fakeRunner) lastStdin(t *testing.T) string {
	t.Helper()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if len(f.calls[i]) > 0 && f.calls[i][0] != "schema" {
			return f.stdins[i]
		}
	}
	t.Fatal("ratline was never invoked")
	return ""
}

type harness struct {
	t      *testing.T
	server *Server
	http   http.Handler
	store  *store.Store
	runner *fakeRunner

	cookie string
	csrf   string
}

func newHarness(t *testing.T, tweak func(*panel.Config)) *harness {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := panel.Default()
	cfg.SourcePath = "test"
	// Never in a test: the requests below are plain HTTP, and a Secure cookie
	// would be set and then not sent back, which looks exactly like a broken
	// session and is not what is under test.
	cfg.Session.SecureCookie = "never"
	if tweak != nil {
		tweak(cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the test configuration is invalid: %v", err)
	}

	runner := &fakeRunner{}
	client := &rl.Client{
		Binary: "/usr/bin/true", Runner: runner, Log: log.Discard(),
		ReadTimeout: time.Second, WriteTimeout: time.Second, JobTimeout: time.Second,
	}
	jm := jobs.New(st, client, log.Discard(), 1<<16, 50)
	if err := jm.Start(context.Background(), 1); err != nil {
		t.Fatalf("starting the job workers: %v", err)
	}
	t.Cleanup(jm.Stop)

	srv, err := New(cfg, st, client, jm, log.Discard())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &harness{t: t, server: srv, http: srv.Handler(), store: st, runner: runner}
}

// do issues a request carrying whatever session the harness holds.
func (h *harness) do(method, path string, body any, headers ...[2]string) *httptest.ResponseRecorder {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.cookie != "" {
		req.Header.Set("Cookie", h.cookie)
	}
	if h.csrf != "" && method != http.MethodGet {
		req.Header.Set("X-Ratline-CSRF", h.csrf)
	}
	for _, kv := range headers {
		req.Header.Set(kv[0], kv[1])
	}
	rec := httptest.NewRecorder()
	h.http.ServeHTTP(rec, req)
	return rec
}

// data unwraps a successful reply.
func (h *harness) data(rec *httptest.ResponseRecorder) map[string]any {
	h.t.Helper()
	var body struct {
		OK    bool           `json:"ok"`
		Data  map[string]any `json:"data"`
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		h.t.Fatalf("the reply is not JSON: %s", rec.Body.String())
	}
	if !body.OK {
		h.t.Fatalf("the request failed: %v", body.Error)
	}
	return body.Data
}

// setup claims the panel and keeps the session.
func (h *harness) setup(email, password string) {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/api/auth/setup",
		map[string]string{"email": email, "name": "Test", "password": password})
	if rec.Code != http.StatusOK {
		h.t.Fatalf("setup failed: %d %s", rec.Code, rec.Body.String())
	}
	h.keep(rec)
}

func (h *harness) keep(rec *httptest.ResponseRecorder) {
	h.t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == h.server.Cfg.Session.CookieName && c.Value != "" {
			h.cookie = c.Name + "=" + c.Value
		}
	}
	data := h.data(rec)
	if csrf, ok := data["csrf"].(string); ok {
		h.csrf = csrf
	}
}

const goodPassword = "a perfectly reasonable passphrase"

// ── the front door ──────────────────────────────────────────────────────────────

// An unauthenticated browser learns whether the panel has been claimed and nothing
// else. A sign-in page that fingerprints its host does an attacker's first step.
func TestBootstrapSaysLittle(t *testing.T) {
	h := newHarness(t, nil)
	data := h.data(h.do(http.MethodGet, "/api/bootstrap", nil))
	if data["needs_setup"] != true {
		t.Error("a fresh panel does not report that it needs setting up")
	}
	for _, leaked := range []string{"hostname", "version", "ratline_version", "accounts"} {
		if _, present := data[leaked]; present {
			t.Errorf("the bootstrap reply leaks %q to anybody who can reach the port", leaked)
		}
	}
}

// The claim window is the empty accounts table, and it closes on the first use.
func TestThePanelCanOnlyBeClaimedOnce(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("first@example.com", goodPassword)

	rec := h.do(http.MethodPost, "/api/auth/setup",
		map[string]string{"email": "second@example.com", "password": goodPassword})
	if rec.Code != http.StatusConflict {
		t.Fatalf("a second setup returned %d, want 409", rec.Code)
	}
	accounts, err := h.store.ListAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("%d accounts exist after a refused second setup", len(accounts))
	}
	if accounts[0].Role != store.RoleSuperAdmin {
		t.Errorf("the first account is %q, want superadmin", accounts[0].Role)
	}
}

func TestSetupRefusesAWeakPassword(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.do(http.MethodPost, "/api/auth/setup",
		map[string]string{"email": "ops@example.com", "password": "short"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a five-character password returned %d, want 400", rec.Code)
	}
}

// Every wrong sign-in gets the same sentence. A different message for an unknown
// address enumerates the accounts as surely as a directory would.
func TestSignInFailuresAreIndistinguishable(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("ops@example.com", goodPassword)
	h.cookie, h.csrf = "", ""

	wrongPassword := h.do(http.MethodPost, "/api/auth/login",
		map[string]string{"email": "ops@example.com", "password": "not the password"})
	noSuchAccount := h.do(http.MethodPost, "/api/auth/login",
		map[string]string{"email": "nobody@example.com", "password": "not the password"})

	if wrongPassword.Code != http.StatusUnauthorized || noSuchAccount.Code != http.StatusUnauthorized {
		t.Fatalf("statuses differ: %d and %d", wrongPassword.Code, noSuchAccount.Code)
	}
	if wrongPassword.Body.String() != noSuchAccount.Body.String() {
		t.Errorf("the two refusals differ, which enumerates accounts:\n  %s\n  %s",
			wrongPassword.Body.String(), noSuchAccount.Body.String())
	}
}

func TestRepeatedFailuresAreRateLimited(t *testing.T) {
	h := newHarness(t, func(c *panel.Config) { c.Security.MaxFailedLogins = 3 })
	h.setup("ops@example.com", goodPassword)
	h.cookie, h.csrf = "", ""

	for i := 0; i < 3; i++ {
		if rec := h.do(http.MethodPost, "/api/auth/login",
			map[string]string{"email": "ops@example.com", "password": "wrong"}); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d returned %d, want 401", i+1, rec.Code)
		}
	}
	rec := h.do(http.MethodPost, "/api/auth/login",
		map[string]string{"email": "ops@example.com", "password": goodPassword})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the fourth attempt returned %d, want 429 — and note it used the *right* "+
			"password, so the limit is on attempts rather than on failures alone", rec.Code)
	}
}

func TestADisabledAccountCannotSignIn(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("boss@example.com", goodPassword)
	ctx := context.Background()
	other := &store.Account{ID: "other", Email: "ops@example.com", Role: store.RoleAdmin}
	hash, err := hashFor(goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	other.PasswordHash = hash
	if err := h.store.CreateAccount(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetAccountDisabled(ctx, other.ID, true); err != nil {
		t.Fatal(err)
	}
	h.cookie, h.csrf = "", ""
	rec := h.do(http.MethodPost, "/api/auth/login",
		map[string]string{"email": "ops@example.com", "password": goodPassword})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a disabled account signed in: %d", rec.Code)
	}
}

// ── CSRF and sessions ───────────────────────────────────────────────────────────

// A state-changing request without this session's token must be refused, or any page
// on the internet can make a signed-in browser deploy something.
func TestAMutationWithoutTheCSRFTokenIsRefused(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("ops@example.com", goodPassword)

	held := h.csrf
	h.csrf = ""
	rec := h.do(http.MethodPost, "/api/actions/site.restart/run",
		map[string]any{"args": []string{"example.com"}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a request with no token returned %d, want 403", rec.Code)
	}

	h.csrf = "a token from somewhere else"
	rec = h.do(http.MethodPost, "/api/actions/site.restart/run",
		map[string]any{"args": []string{"example.com"}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a request with a wrong token returned %d, want 403", rec.Code)
	}

	// The negative case: with the right token it goes through, or the two above
	// would pass for a handler that refused everything.
	h.csrf = held
	if rec := h.do(http.MethodPost, "/api/actions/site.restart/run",
		map[string]any{"args": []string{"example.com"}}); rec.Code != http.StatusOK {
		t.Fatalf("the right token was refused: %d %s", rec.Code, rec.Body.String())
	}
}

// A reloaded tab keeps nothing in memory, so it asks /api/me who it is. If the CSRF
// token did not come back with the answer, the tab would be signed in and unable to
// change anything — every mutation refused, with nothing on screen to say why.
//
// This is a regression test: that is exactly what the first version did.
func TestAReloadedTabCanStillMakeChanges(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("ops@example.com", goodPassword)

	// Everything a reload discards. The cookie survives; the token does not.
	h.csrf = ""

	data := h.data(h.do(http.MethodGet, "/api/me", nil))
	csrf, _ := data["csrf"].(string)
	if csrf == "" {
		t.Fatal("/api/me did not return the session's CSRF token, so a reloaded tab is read-only")
	}
	h.csrf = csrf
	if rec := h.do(http.MethodPost, "/api/actions/site.restart/run",
		map[string]any{"args": []string{"example.com"}}); rec.Code != http.StatusOK {
		t.Fatalf("a mutation after a reload returned %d: %s", rec.Code, rec.Body.String())
	}

	// And the token is still a real check: another session's does not work.
	other := newHarness(t, nil)
	other.setup("someone@example.com", goodPassword)
	h.csrf = other.csrf
	if rec := h.do(http.MethodPost, "/api/actions/site.restart/run",
		map[string]any{"args": []string{"example.com"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("another session's token was accepted: %d", rec.Code)
	}
}

func TestARequestFromAnotherOriginIsRefused(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("ops@example.com", goodPassword)
	rec := h.do(http.MethodPost, "/api/actions/site.restart/run",
		map[string]any{"args": []string{"example.com"}},
		[2]string{"Origin", "https://evil.example.net"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a cross-origin request returned %d, want 403", rec.Code)
	}
}

func TestAnExpiredSessionIsRefusedAndTheCookieCleared(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("ops@example.com", goodPassword)
	if rec := h.do(http.MethodGet, "/api/me", nil); rec.Code != http.StatusOK {
		t.Fatalf("the session did not work to begin with: %d", rec.Code)
	}
	// Thirteen hours on, past the twelve-hour ceiling.
	h.server.now = func() time.Time { return time.Now().UTC().Add(13 * time.Hour) }
	rec := h.do(http.MethodGet, "/api/me", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("an expired session returned %d, want 401", rec.Code)
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == h.server.Cfg.Session.CookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("the stale cookie was not cleared, so the browser will keep sending it")
	}
}

func TestAnIdleSessionEnds(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("ops@example.com", goodPassword)
	// Three hours, past the two-hour idle timeout but inside the twelve-hour
	// ceiling — so this proves the idle rule specifically.
	h.server.now = func() time.Time { return time.Now().UTC().Add(3 * time.Hour) }
	if rec := h.do(http.MethodGet, "/api/me", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an idle session returned %d, want 401", rec.Code)
	}
}

// ── the action surface ──────────────────────────────────────────────────────────

func TestRunningAnActionInvokesRatlineWithTheRightArgv(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("ops@example.com", goodPassword)

	rec := h.do(http.MethodPost, "/api/actions/site.restart/run",
		map[string]any{"args": []string{"example.com"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("run returned %d: %s", rec.Code, rec.Body.String())
	}
	argv := h.runner.lastCall(t)
	joined := strings.Join(argv, " ")
	for _, want := range []string{"site", "restart", "--json", "--no-input", "--yes", "-- example.com"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv is missing %q: %v", want, argv)
		}
	}
}

func TestPreviewPassesDryRunAndNotYes(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("ops@example.com", goodPassword)

	rec := h.do(http.MethodPost, "/api/actions/site.restart/preview",
		map[string]any{"args": []string{"example.com"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("preview returned %d: %s", rec.Code, rec.Body.String())
	}
	joined := strings.Join(h.runner.lastCall(t), " ")
	if !strings.Contains(joined, "--dry-run") {
		t.Errorf("--dry-run was not passed: %s", joined)
	}
	if strings.Contains(joined, "--yes") {
		t.Errorf("--yes was passed for a rehearsal: %s", joined)
	}
}

// The typed confirmation is enforced on the server. A UI that asks for it and a
// server that does not is a UI, not a control.
func TestADestructiveActionNeedsTheNameTypedBack(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("boss@example.com", goodPassword)

	rec := h.do(http.MethodPost, "/api/actions/site.delete/run",
		map[string]any{"args": []string{"example.com"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an unconfirmed deletion returned %d, want 400", rec.Code)
	}
	rec = h.do(http.MethodPost, "/api/actions/site.delete/run",
		map[string]any{"args": []string{"example.com"}, "confirm": "example.co"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a near-miss confirmation returned %d, want 400", rec.Code)
	}
	rec = h.do(http.MethodPost, "/api/actions/site.delete/run",
		map[string]any{"args": []string{"example.com"}, "confirm": "example.com"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("a correct confirmation returned %d, want 202 (site delete is a job): %s",
			rec.Code, rec.Body.String())
	}
}

// The invariant, end to end through the HTTP layer: a value submitted as a secret
// reaches ratline on stdin and appears nowhere in the process's arguments.
func TestASecretReachesRatlineOnStdinAndNotInArgv(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("boss@example.com", goodPassword)

	const secret = "postgres://user:hunter2@db.internal/app"
	rec := h.do(http.MethodPost, "/api/actions/site.env.set/run", map[string]any{
		"args":       []string{"example.com"},
		"secret":     secret,
		"secret_key": "DATABASE_URL",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("run returned %d: %s", rec.Code, rec.Body.String())
	}
	argv := h.runner.lastCall(t)
	for _, a := range argv {
		if strings.Contains(a, "hunter2") {
			t.Fatalf("the secret is in argv, and so in /proc/PID/cmdline: %v", argv)
		}
	}
	if got := h.runner.lastStdin(t); got != "DATABASE_URL="+secret+"\n" {
		t.Errorf("stdin = %q, want the composed assignment", got)
	}
	// And the audit record must not carry it either: the whole argv is stored.
	records, err := h.store.ListActions(context.Background(), store.ActionFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range records {
		if strings.Contains(rec.Argv, "hunter2") {
			t.Fatalf("the secret was written into the activity log: %s", rec.Argv)
		}
	}
}

// A ratline failure is a 200 with ok:false, not a 5xx: the panel did its job, and the
// exit code, the hint and the log are what the browser needs to show.
func TestARatlineFailureKeepsItsCodeAndHint(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("ops@example.com", goodPassword)
	h.runner.reply = `{"ok":false,"command":"ratline site restart","version":"test",` +
		`"error":{"code":5,"name":"locked","message":"another invocation holds the lock",` +
		`"hint":"retry shortly"}}`
	h.runner.exitCode = 5

	rec := h.do(http.MethodPost, "/api/actions/site.restart/run",
		map[string]any{"args": []string{"example.com"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with ok:false", rec.Code)
	}
	var body struct {
		OK    bool `json:"ok"`
		Error struct {
			Code int    `json:"code"`
			Name string `json:"name"`
			Hint string `json:"hint"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.OK {
		t.Fatal("a failed command was reported as ok")
	}
	if body.Error.Code != 5 || body.Error.Name != "locked" {
		t.Errorf("the exit code was lost: %+v", body.Error)
	}
	if body.Error.Hint != "retry shortly" {
		t.Errorf("the hint was lost: %q", body.Error.Hint)
	}
}

func TestALongActionBecomesAJob(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("ops@example.com", goodPassword)

	rec := h.do(http.MethodPost, "/api/actions/site.deploy/run",
		map[string]any{"args": []string{"example.com"}})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("a deploy returned %d, want 202: %s", rec.Code, rec.Body.String())
	}
	data := h.data(rec)
	id, _ := data["job_id"].(string)
	if id == "" {
		t.Fatal("no job id was returned")
	}
	// The worker runs it out of band, so wait for the row rather than assuming.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := h.store.FindJob(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Terminal() {
			if job.State != store.JobDone {
				t.Fatalf("the job ended %q: %s", job.State, job.Error)
			}
			if !strings.Contains(job.Argv, "site deploy") {
				t.Errorf("the job did not record what it ran: %q", job.Argv)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the job never finished")
}

// ── roles ───────────────────────────────────────────────────────────────────────

func (h *harness) addAdmin(t *testing.T, email string) *store.Account {
	t.Helper()
	hash, err := hashFor(goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	a := &store.Account{ID: email, Email: email, Role: store.RoleAdmin, PasswordHash: hash}
	if err := h.store.CreateAccount(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return a
}

// signInAs replaces the harness's session with a fresh one for this account.
func (h *harness) signInAs(t *testing.T, email string) {
	t.Helper()
	h.cookie, h.csrf = "", ""
	rec := h.do(http.MethodPost, "/api/auth/login",
		map[string]string{"email": email, "password": goodPassword})
	if rec.Code != http.StatusOK {
		t.Fatalf("signing in as %s returned %d: %s", email, rec.Code, rec.Body.String())
	}
	h.keep(rec)
}

func TestAnAdminCannotReachASuperAdminAction(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("boss@example.com", goodPassword)
	h.addAdmin(t, "ops@example.com")
	h.signInAs(t, "ops@example.com")

	// 404, not 403: telling somebody an action exists but is not theirs maps out
	// the surface above them.
	if rec := h.do(http.MethodGet, "/api/actions/user.delete", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET a super-admin action returned %d, want 404", rec.Code)
	}
	rec := h.do(http.MethodPost, "/api/actions/user.delete/run",
		map[string]any{"args": []string{"acme"}, "confirm": "acme"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("running a super-admin action as an admin returned %d, want 404", rec.Code)
	}
	for _, call := range h.runner.calls {
		if len(call) > 1 && call[0] == "user" && call[1] == "delete" {
			t.Fatal("ratline was invoked despite the refusal")
		}
	}
}

func TestAnAdminIsNotSentTheSuperAdminCatalogue(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("boss@example.com", goodPassword)
	h.addAdmin(t, "ops@example.com")
	h.signInAs(t, "ops@example.com")

	rec := h.do(http.MethodGet, "/api/actions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing actions returned %d", rec.Code)
	}
	var body struct {
		Data []struct {
			Verb    string `json:"verb"`
			MinRole string `json:"min_role"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) == 0 {
		t.Fatal("an admin was sent no actions at all")
	}
	for _, a := range body.Data {
		if a.MinRole == store.RoleSuperAdmin {
			t.Errorf("%q was sent to an admin's browser", a.Verb)
		}
	}
}

func TestOnlyASuperAdminCanManageTheTeam(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("boss@example.com", goodPassword)
	h.addAdmin(t, "ops@example.com")
	h.signInAs(t, "ops@example.com")

	for _, req := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/team", nil},
		{http.MethodPost, "/api/team/invites", map[string]string{"email": "x@example.com", "role": "admin"}},
		{http.MethodPost, "/api/team/boss@example.com/role", map[string]string{"role": "admin"}},
		{http.MethodDelete, "/api/team/boss@example.com", nil},
	} {
		if rec := h.do(req.method, req.path, req.body); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s returned %d, want 403", req.method, req.path, rec.Code)
		}
	}
}

// ── invitations ─────────────────────────────────────────────────────────────────

func TestTheInvitationRoundTrip(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("boss@example.com", goodPassword)

	data := h.data(h.do(http.MethodPost, "/api/team/invites",
		map[string]string{"email": "new@example.com", "role": "admin"}))
	link, _ := data["link"].(string)
	if link == "" {
		t.Fatal("no link was returned")
	}
	token := link[strings.Index(link, "token=")+len("token="):]

	// The stored row must not contain the token, or the invitations table is a set
	// of working links for anybody who reads it.
	invites, err := h.store.ListInvites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(invites)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("the invitation token is recoverable from the stored row")
	}

	h.cookie, h.csrf = "", ""
	rec := h.do(http.MethodGet, "/api/auth/invite?token="+token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("looking the invitation up returned %d", rec.Code)
	}

	rec = h.do(http.MethodPost, "/api/auth/accept",
		map[string]string{"token": token, "name": "New", "password": goodPassword})
	if rec.Code != http.StatusOK {
		t.Fatalf("accepting returned %d: %s", rec.Code, rec.Body.String())
	}

	// Once. A second use must fail, or the link is a standing invitation.
	rec = h.do(http.MethodPost, "/api/auth/accept",
		map[string]string{"token": token, "name": "Impostor", "password": goodPassword})
	if rec.Code == http.StatusOK {
		t.Fatal("the same invitation was accepted twice")
	}
}

// The role comes from the invitation, never from the request body — otherwise
// anybody holding an admin link could create themselves a super admin.
func TestAcceptingAnInvitationCannotChooseItsRole(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("boss@example.com", goodPassword)
	data := h.data(h.do(http.MethodPost, "/api/team/invites",
		map[string]string{"email": "new@example.com", "role": "admin"}))
	link := data["link"].(string)
	token := link[strings.Index(link, "token=")+len("token="):]

	h.cookie, h.csrf = "", ""
	// DisallowUnknownFields means an attempt to smuggle a role is refused
	// outright rather than ignored, which is the louder and better failure.
	rec := h.do(http.MethodPost, "/api/auth/accept", map[string]string{
		"token": token, "name": "New", "password": goodPassword, "role": "superadmin",
	})
	if rec.Code == http.StatusOK {
		t.Fatal("a body naming its own role was accepted")
	}

	rec = h.do(http.MethodPost, "/api/auth/accept",
		map[string]string{"token": token, "name": "New", "password": goodPassword})
	if rec.Code != http.StatusOK {
		t.Fatalf("the ordinary acceptance returned %d: %s", rec.Code, rec.Body.String())
	}
	account, err := h.store.FindAccountByEmail(context.Background(), "new@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if account.Role != store.RoleAdmin {
		t.Fatalf("the new account is %q, want the invited role admin", account.Role)
	}
}

// ── headers and the allow list ──────────────────────────────────────────────────

func TestSecurityHeadersAreServed(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.do(http.MethodGet, "/api/bootstrap", nil)
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no Content-Security-Policy")
	}
	// The whole point of building the bundle without inline scripts or styles.
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("the policy allows inline code: %s", csp)
	}
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Cache-Control":          "no-store",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	// HSTS must not be sent over plain HTTP: it would pin localhost to HTTPS in
	// the browser of somebody reaching the panel through an SSH tunnel.
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS was sent over plain HTTP: %q", got)
	}
}

func TestTheAllowListRefusesBeforeAnythingElse(t *testing.T) {
	h := newHarness(t, func(c *panel.Config) {
		c.Security.AllowFrom = []string{"10.0.0.0/8"}
		// Otherwise the forwarded header would decide, and httptest's RemoteAddr
		// is what is under test.
		c.Listen.TrustProxy = false
	})
	if rec := h.do(http.MethodGet, "/api/bootstrap", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("an address outside the allow list returned %d, want 403", rec.Code)
	}
	// httptest uses 192.0.2.1; widening the list must let it through, or the test
	// above would pass for a handler that refused everything.
	h2 := newHarness(t, func(c *panel.Config) {
		c.Security.AllowFrom = []string{"192.0.2.0/24"}
		c.Listen.TrustProxy = false
	})
	if rec := h2.do(http.MethodGet, "/api/bootstrap", nil); rec.Code != http.StatusOK {
		t.Fatalf("an allowed address returned %d, want 200", rec.Code)
	}
}

// ── the second factor ───────────────────────────────────────────────────────────

// An account that must enrol can reach enrolment and nothing else. Refusing
// everything would be a locked door with the key inside.
func TestRequireTOTPGatesEverythingButEnrolment(t *testing.T) {
	h := newHarness(t, func(c *panel.Config) { c.Security.RequireTOTP = true })
	h.setup("ops@example.com", goodPassword)

	if rec := h.do(http.MethodGet, "/api/sites", nil); rec.Code != http.StatusForbidden {
		t.Errorf("a site listing without a second factor returned %d, want 403", rec.Code)
	}
	if rec := h.do(http.MethodGet, "/api/me", nil); rec.Code != http.StatusOK {
		t.Errorf("/api/me returned %d; an account that must enrol still has to see itself", rec.Code)
	}
	start := h.do(http.MethodPost, "/api/me/totp/start", nil)
	if start.Code != http.StatusOK {
		t.Fatalf("starting enrolment returned %d, want 200", start.Code)
	}
	secret, _ := h.data(start)["secret"].(string)
	if secret == "" {
		t.Fatal("no secret was issued")
	}

	// Inert until confirmed: the account is still gated.
	if rec := h.do(http.MethodGet, "/api/sites", nil); rec.Code != http.StatusForbidden {
		t.Errorf("an unconfirmed secret unlocked the panel: %d", rec.Code)
	}

	code, err := codeFor(secret)
	if err != nil {
		t.Fatal(err)
	}
	if rec := h.do(http.MethodPost, "/api/me/totp/confirm",
		map[string]string{"code": code}); rec.Code != http.StatusOK {
		t.Fatalf("confirming returned %d: %s", rec.Code, rec.Body.String())
	}
	if rec := h.do(http.MethodGet, "/api/sites", nil); rec.Code != http.StatusOK {
		t.Errorf("the panel is still gated after enrolment: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSignInRequiresTheCodeOnceEnrolled(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("ops@example.com", goodPassword)
	secret, _ := h.data(h.do(http.MethodPost, "/api/me/totp/start", nil))["secret"].(string)
	code, err := codeFor(secret)
	if err != nil {
		t.Fatal(err)
	}
	if rec := h.do(http.MethodPost, "/api/me/totp/confirm",
		map[string]string{"code": code}); rec.Code != http.StatusOK {
		t.Fatal("could not enrol")
	}

	h.cookie, h.csrf = "", ""
	if rec := h.do(http.MethodPost, "/api/auth/login",
		map[string]string{"email": "ops@example.com", "password": goodPassword}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("signing in without a code returned %d, want 401", rec.Code)
	}
	code, err = codeFor(secret)
	if err != nil {
		t.Fatal(err)
	}
	if rec := h.do(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "ops@example.com", "password": goodPassword, "code": code,
	}); rec.Code != http.StatusOK {
		t.Fatalf("signing in with the right code returned %d", rec.Code)
	}
}

func TestUnknownFieldsInABodyAreRefused(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("ops@example.com", goodPassword)
	req := httptest.NewRequest(http.MethodPost, "/api/actions/site.restart/run",
		strings.NewReader(`{"args":["example.com"],"dryrun":true}`))
	req.Header.Set("Cookie", h.cookie)
	req.Header.Set("X-Ratline-CSRF", h.csrf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.http.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a misspelled field returned %d, want 400 — silently ignoring it would "+
			"turn an intended rehearsal into a real mutation", rec.Code)
	}
}

func TestReadsAreServedFromRatline(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("ops@example.com", goodPassword)
	h.runner.reply = `{"ok":true,"command":"ratline site list","version":"test",` +
		`"data":{"sites":[{"domain":"example.com","user":"acme","runtime":"static","enabled":true}]}}`

	data := h.data(h.do(http.MethodGet, "/api/sites", nil))
	sites, ok := data["sites"].([]any)
	if !ok || len(sites) != 1 {
		t.Fatalf("the site listing did not pass ratline's data through: %v", data)
	}
	joined := strings.Join(h.runner.lastCall(t), " ")
	if !strings.Contains(joined, "site list") || strings.Contains(joined, "--yes") {
		t.Errorf("a read was invoked as %q; it must not carry --yes", joined)
	}
}

func TestUnauthenticatedRequestsAreRefused(t *testing.T) {
	h := newHarness(t, nil)
	h.setup("ops@example.com", goodPassword)
	h.cookie, h.csrf = "", ""
	for _, path := range []string{
		"/api/me", "/api/sites", "/api/actions", "/api/jobs", "/api/activity", "/api/team",
	} {
		if rec := h.do(http.MethodGet, path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session returned %d, want 401", path, rec.Code)
		}
	}
}
