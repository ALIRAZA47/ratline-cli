// Package httpapi is the panel's front door.
//
// Everything is one of three things: a session endpoint under /api/auth, a read that
// runs a ratline query, or an action that runs a ratline command. There is no fourth
// kind, and in particular there is no endpoint that touches the system directly —
// every effect this package has on the server goes through internal/panel/rl, which
// goes through the ratline binary.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/panel"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/jobs"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/rl"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// Server holds everything a handler needs.
type Server struct {
	Cfg    *panel.Config
	Store  *store.Store
	Client *rl.Client
	Jobs   *jobs.Manager
	Log    *log.Logger

	// UI serves the built single-page application. Nil in tests, where the API is
	// the thing under test and a missing bundle should not be a compile error.
	UI http.Handler

	allowFrom []*net.IPNet
	reads     *readCache
	// now is time.Now, replaced in tests so that an expiry can be reached without
	// waiting twelve hours for it.
	now func() time.Time
}

// New builds a server.
func New(cfg *panel.Config, st *store.Store, client *rl.Client, jm *jobs.Manager, lg *log.Logger) (*Server, error) {
	nets, err := panel.ParseAllowFrom(cfg.Security.AllowFrom)
	if err != nil {
		return nil, err
	}
	if lg == nil {
		lg = log.Discard()
	}
	return &Server{
		Cfg: cfg, Store: st, Client: client, Jobs: jm, Log: lg,
		allowFrom: nets,
		// Two seconds: long enough that one page load does not fork six ratline
		// processes for the same answer, short enough that a refresh after a
		// restart shows the restart.
		reads: newReadCache(2 * time.Second),
		now:   func() time.Time { return time.Now().UTC() },
	}, nil
}

// Handler returns the routed, wrapped handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Setup and sign-in. Reachable without a session, which is the whole point,
	// so each one does its own rate limiting and its own refusal.
	mux.HandleFunc("GET /api/bootstrap", s.handleBootstrap)
	mux.HandleFunc("POST /api/auth/setup", s.handleSetup)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/invite", s.handleInviteLookup)
	mux.HandleFunc("POST /api/auth/accept", s.handleAcceptInvite)

	// Everything below needs a session.
	mux.Handle("GET /api/me", s.authed(s.handleMe))
	mux.Handle("POST /api/me/password", s.authed(s.handleChangePassword))
	mux.Handle("POST /api/me/totp/start", s.authed(s.handleTOTPStart))
	mux.Handle("POST /api/me/totp/confirm", s.authed(s.handleTOTPConfirm))
	mux.Handle("POST /api/me/totp/disable", s.authed(s.handleTOTPDisable))
	mux.Handle("GET /api/me/sessions", s.authed(s.handleListSessions))

	// The catalogue and the two ways of running something from it.
	mux.Handle("GET /api/actions", s.authed(s.handleActions))
	mux.Handle("GET /api/actions/{id}", s.authed(s.handleAction))
	mux.Handle("POST /api/actions/{id}/preview", s.authed(s.handlePreview))
	mux.Handle("POST /api/actions/{id}/run", s.authed(s.handleRun))

	// Reads, named for what they are about rather than for the command behind
	// them: the browser should not have to know that the dashboard is `status`.
	mux.Handle("GET /api/overview", s.authed(s.handleOverview))
	mux.Handle("GET /api/sites", s.authed(s.handleSites))
	mux.Handle("GET /api/sites/{domain}", s.authed(s.handleSite))
	mux.Handle("GET /api/sites/{domain}/logs", s.authed(s.handleSiteLogs))
	mux.Handle("GET /api/sites/{domain}/env", s.authed(s.handleSiteEnv))
	mux.Handle("GET /api/tenants", s.authed(s.handleTenants))
	mux.Handle("GET /api/tenants/{name}", s.authed(s.handleTenant))
	mux.Handle("GET /api/keys", s.authed(s.handleKeys))
	mux.Handle("GET /api/certs", s.authed(s.handleCerts))
	mux.Handle("GET /api/databases", s.authed(s.handleDatabases))
	mux.Handle("GET /api/runtimes", s.authed(s.handleRuntimes))
	mux.Handle("GET /api/doctor", s.authed(s.handleDoctor))

	// Jobs.
	mux.Handle("GET /api/jobs", s.authed(s.handleJobs))
	mux.Handle("GET /api/jobs/{id}", s.authed(s.handleJob))
	mux.Handle("GET /api/jobs/{id}/stream", s.authed(s.handleJobStream))

	// The trail.
	mux.Handle("GET /api/activity", s.authed(s.handleActivity))

	// The team. Every one of these is a super admin's, enforced in the handler
	// rather than by route so that the refusal is one code path.
	mux.Handle("GET /api/team", s.authed(s.handleTeam))
	mux.Handle("POST /api/team/invites", s.authed(s.handleInvite))
	mux.Handle("DELETE /api/team/invites/{id}", s.authed(s.handleRevokeInvite))
	mux.Handle("POST /api/team/{id}/role", s.authed(s.handleSetRole))
	mux.Handle("POST /api/team/{id}/disable", s.authed(s.handleSetDisabled))
	mux.Handle("DELETE /api/team/{id}", s.authed(s.handleDeleteAccount))

	// Anything not under /api is the application: React owns its own routing, so
	// a deep link has to reach index.html rather than a 404.
	if s.UI != nil {
		mux.Handle("/", s.UI)
	}

	// Outermost first: an address that is not allowed to talk to the panel should
	// be refused before anything reads its body.
	return s.withRecovery(s.withAllowList(s.withSecurityHeaders(s.withRequestLog(mux))))
}

// Serve runs until the context is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	addr := net.JoinHostPort(s.Cfg.Listen.Address, strconv.Itoa(s.Cfg.Listen.Port))
	srv := &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
		// A slow-loris client must not be able to hold a connection open for
		// ever. The write timeout is generous because a job stream is a long
		// response by design; the read timeout is not, because no legitimate
		// request to this API takes thirty seconds to arrive.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          nil,
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return rlerr.Wrap(err, rlerr.CodePrecondition, "listening on %s", addr).
			WithHint("another process may already hold that port: ss -ltnp | grep %d", s.Cfg.Listen.Port)
	}
	s.Log.Info("panel listening", "addr", addr, "domain", s.Cfg.Listen.Domain)

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return rlerr.Wrap(err, rlerr.CodeGeneric, "serving")
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}

// ── replies ─────────────────────────────────────────────────────────────────────

// reply is the panel's own envelope.
//
// Shaped like ratline's on purpose: ok, data, error with a code, a name and a hint.
// A caller that has written something against `ratline --json` can read the panel's
// API without learning a second convention, and a ratline error passing through
// keeps its code the whole way rather than being flattened into a 500.
type reply struct {
	OK    bool          `json:"ok"`
	Data  any           `json:"data,omitempty"`
	Error *errorPayload `json:"error,omitempty"`
}

type errorPayload struct {
	Code    int               `json:"code"`
	Name    string            `json:"name"`
	Message string            `json:"message"`
	Hint    string            `json:"hint,omitempty"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(body)
}

func ok(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, reply{OK: true, Data: data})
}

// fail maps a ratline error onto an HTTP status without losing its code.
//
// The mapping is the interesting part. A precondition failure is a 409, not a 500:
// the server is fine, the request asked for something the system is not ready for,
// and a monitoring system that pages on 5xx should not be woken by somebody trying to
// issue a certificate before DNS points here. `locked` is a 409 with a Retry-After,
// because retrying shortly is exactly the right response.
func (s *Server) fail(w http.ResponseWriter, err error) {
	code := rlerr.CodeOf(err)
	status := http.StatusInternalServerError
	switch code {
	case rlerr.CodeUsage:
		status = http.StatusBadRequest
	case rlerr.CodePrecondition, rlerr.CodeUnhealthy, rlerr.CodeACME:
		status = http.StatusConflict
	case rlerr.CodeLocked:
		status = http.StatusConflict
		w.Header().Set("Retry-After", "5")
	case rlerr.CodeRateLimited:
		status = http.StatusTooManyRequests
	case rlerr.CodeInputRequired:
		status = http.StatusBadRequest
	case rlerr.CodeExternal, rlerr.CodeRollbackFailed, rlerr.CodeGeneric:
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, reply{Error: &errorPayload{
		Code:    int(code),
		Name:    code.Name(),
		Message: err.Error(),
		Hint:    rlerr.Hint(err),
		Fields:  rlerr.Fields(err),
	}})
}

// failStatus is for the failures that are the panel's own rather than ratline's:
// unauthenticated, forbidden, not found.
func failStatus(w http.ResponseWriter, status int, name, message, hint string) {
	writeJSON(w, status, reply{Error: &errorPayload{
		Code: status, Name: name, Message: message, Hint: hint,
	}})
}

// decode reads a JSON body with a size limit.
//
// Bounded because the body reaches a form that becomes an argv, and an unbounded
// read is a way to make the panel allocate a gigabyte from one request. Unknown
// fields are refused rather than ignored: a client sending `dryrun` and meaning
// `dry_run` should be told, not silently given a real mutation.
func decode(w http.ResponseWriter, r *http.Request, into any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return rlerr.Usagef("the request body could not be read: %s", err.Error())
	}
	return nil
}
