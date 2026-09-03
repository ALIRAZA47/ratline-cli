package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/panel/rl"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// readCache holds the answer to a read for a couple of seconds.
//
// Every read is a process spawn: fork, exec a 20 MB static binary, open SQLite, print
// JSON, exit. That is a few tens of milliseconds, which is fine once and silly when a
// dashboard with six panels asks for the same `status` six times on one page load.
//
// The TTL is short on purpose. This is a cache against a stampede, not a cache
// against staleness — somebody who has just restarted a site should see it restarted
// on the next refresh, so any mutation clears the whole thing rather than trying to
// work out which keys it touched.
type readCache struct {
	mu      sync.RWMutex
	entries map[string]readEntry
	ttl     time.Duration
}

type readEntry struct {
	body json.RawMessage
	at   time.Time
}

func newReadCache(ttl time.Duration) *readCache {
	return &readCache{entries: map[string]readEntry{}, ttl: ttl}
}

func (c *readCache) get(key string) (json.RawMessage, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok || time.Since(e.at) > c.ttl {
		return nil, false
	}
	return e.body, true
}

func (c *readCache) put(key string, body json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = readEntry{body: body, at: time.Now()}
}

func (c *readCache) clear() {
	c.mu.Lock()
	c.entries = map[string]readEntry{}
	c.mu.Unlock()
}

func (s *Server) invalidateReads() {
	if s.reads != nil {
		s.reads.clear()
	}
}

// read runs a read-only ratline command and returns its data.
//
// The policy is looked up rather than assumed: a caller cannot reach a mutating
// command through here even by naming one, because the policy's role gate is applied
// the same way it is for an action.
func (s *Server) read(ctx context.Context, role, verb string, args ...string) (json.RawMessage, error) {
	cat, policy, err := s.resolveRead(ctx, role, verb)
	if err != nil {
		return nil, err
	}

	key := verb + "\x00" + strings.Join(args, "\x00")
	if body, hit := s.reads.get(key); hit {
		return body, nil
	}
	out, err := s.Client.Run(ctx, cat, policy, rl.Request{Verb: verb, Args: args})
	if err != nil {
		return nil, err
	}
	// A read that reports a problem is still a read. `doctor` exits non-zero when
	// it finds something wrong and its findings are in the envelope — treating the
	// exit code as a failure would hide exactly the output somebody opened the page
	// to see.
	if out.Envelope == nil {
		return nil, out.Err()
	}
	if !out.Envelope.OK {
		return nil, out.Envelope.Err()
	}
	body := out.Envelope.Data
	if body == nil {
		body = json.RawMessage("null")
	}
	s.reads.put(key, body)
	return body, nil
}

// serveRead is the handler shape every read endpoint uses.
func (s *Server) serveRead(w http.ResponseWriter, r *http.Request, c *Caller, verb string, args ...string) {
	body, err := s.read(r.Context(), c.Account.Role, verb, args...)
	if err != nil {
		s.fail(w, err)
		return
	}
	ok(w, body)
}

// ── the dashboard ───────────────────────────────────────────────────────────────

// overviewView is the front page: what ratline says about the server, plus the
// panel's own recent history, in one request.
//
// One request rather than four, because the dashboard would otherwise fork four
// ratline processes on every page load and render in whatever order they returned.
type overviewView struct {
	Status  json.RawMessage       `json:"status"`
	Jobs    []*store.Job          `json:"jobs"`
	Recent  []*store.ActionRecord `json:"recent"`
	Warning string                `json:"warning,omitempty"`
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, c *Caller) {
	view := overviewView{}
	status, err := s.read(r.Context(), c.Account.Role, "status")
	if err != nil {
		// The panel is still useful when ratline cannot answer — the activity log
		// and the job history are the panel's own — so this degrades rather than
		// failing the page, and says why.
		view.Warning = err.Error()
	} else {
		view.Status = status
	}
	if jobsList, err := s.Store.ListJobs(r.Context(), 8); err == nil {
		view.Jobs = jobsList
	}
	if recent, err := s.Store.ListActions(r.Context(), store.ActionFilter{Limit: 12}); err == nil {
		view.Recent = recent
	}
	ok(w, view)
}

// ── sites, tenants, keys, certificates, databases ───────────────────────────────

func (s *Server) handleSites(w http.ResponseWriter, r *http.Request, c *Caller) {
	s.serveRead(w, r, c, "site list")
}

func (s *Server) handleSite(w http.ResponseWriter, r *http.Request, c *Caller) {
	domain, err := s.pathDomain(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.serveRead(w, r, c, "site show", domain)
}

// handleSiteLogs reads the tail of a site's journal.
//
// --lines is bounded here rather than passed through: a browser asking for a million
// lines would make ratline read a million lines out of the journal and then try to
// send them, which is a request that can hurt the server it is asking about.
func (s *Server) handleSiteLogs(w http.ResponseWriter, r *http.Request, c *Caller) {
	domain, err := s.pathDomain(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	lines := 200
	if raw := r.URL.Query().Get("lines"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 2000 {
			s.fail(w, rlerr.Usagef("lines must be a number between 1 and 2000"))
			return
		}
		lines = n
	}
	text, err := s.readText(r.Context(), c.Account.Role, "site logs",
		[]string{domain}, map[string]any{"lines": lines})
	if err != nil {
		s.fail(w, err)
		return
	}
	ok(w, map[string]any{"domain": domain, "lines": lines, "text": text})
}

// handleSiteEnv lists a site's environment keys. Values are masked by ratline unless
// --reveal is passed, and the panel never passes it: reading a secret is a separate,
// deliberate act through the action surface, where it is recorded.
func (s *Server) handleSiteEnv(w http.ResponseWriter, r *http.Request, c *Caller) {
	domain, err := s.pathDomain(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.serveRead(w, r, c, "site env list", domain)
}

func (s *Server) handleTenants(w http.ResponseWriter, r *http.Request, c *Caller) {
	s.serveRead(w, r, c, "user list")
}

func (s *Server) handleTenant(w http.ResponseWriter, r *http.Request, c *Caller) {
	name := r.PathValue("name")
	if err := validate.Username(name); err != nil {
		s.fail(w, err)
		return
	}
	s.serveRead(w, r, c, "user show", name)
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request, c *Caller) {
	s.serveRead(w, r, c, "key list")
}

func (s *Server) handleCerts(w http.ResponseWriter, r *http.Request, c *Caller) {
	s.serveRead(w, r, c, "cert list")
}

func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request, c *Caller) {
	s.serveRead(w, r, c, "db list")
}

func (s *Server) handleRuntimes(w http.ResponseWriter, r *http.Request, c *Caller) {
	s.serveRead(w, r, c, "runtime list")
}

func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request, c *Caller) {
	s.serveRead(w, r, c, "doctor")
}

// readText serves a command whose output is a log rather than an envelope.
func (s *Server) readText(ctx context.Context, role, verb string, args []string, flags map[string]any) (string, error) {
	cat, policy, err := s.resolveRead(ctx, role, verb)
	if err != nil {
		return "", err
	}
	return s.Client.RunText(ctx, cat, policy, rl.Request{Verb: verb, Args: args, Flags: flags})
}

// resolveRead checks that a verb is a read this role may run.
func (s *Server) resolveRead(ctx context.Context, role, verb string) (*rl.Catalogue, rl.Policy, error) {
	cat, err := s.Client.Catalogue(ctx)
	if err != nil {
		return nil, rl.Policy{}, err
	}
	cmd, found := cat.Leaves[verb]
	if !found {
		return nil, rl.Policy{}, rlerr.Preconditionf("the installed ratline has no %q command", verb).
			WithHint("this panel may be newer than the ratline it is driving")
	}
	if cmd.Mutates {
		return nil, rl.Policy{}, rlerr.Genericf("internal error: %q mutates and cannot be served as a read", verb)
	}
	policy, _ := rl.PolicyFor(verb, cmd)
	if policy.Denied || !store.AtLeast(role, policy.MinRole) {
		return nil, rl.Policy{}, rlerr.Preconditionf("this account may not read %q", verb)
	}
	return cat, policy, nil
}

// pathDomain validates a domain out of the URL before it can become an argument.
//
// The first of the two checks it will get: ratline validates it again where it enters
// a manager. Doing it here means a path that is not a domain is a 400 with a sentence
// rather than a process spawn that fails somewhere less legible.
func (s *Server) pathDomain(r *http.Request) (string, error) {
	return validate.Domain(r.PathValue("domain"))
}
