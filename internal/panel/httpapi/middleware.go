package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/panel"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/auth"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
)

type ctxKey int

const (
	ctxAccount ctxKey = iota
	ctxSession
)

// Caller is the authenticated account and the session it arrived on.
type Caller struct {
	Account *store.Account
	Session *store.Session
	IP      string
}

// authed wraps a handler with authentication, CSRF and the second-factor gate.
//
// One wrapper rather than three, because the order matters and an order that lives
// in a route table is an order somebody will get wrong: authenticate, then check the
// request is not cross-site, then check the account is allowed to be doing anything
// at all yet.
func (s *Server) authed(h func(http.ResponseWriter, *http.Request, *Caller)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, err := s.authenticate(r)
		if err != nil {
			s.clearSessionCookie(w, r)
			failStatus(w, http.StatusUnauthorized, "unauthenticated",
				"sign in to continue", "")
			return
		}
		if err := s.checkCSRF(r, caller); err != nil {
			failStatus(w, http.StatusForbidden, "csrf",
				err.Error(), "reload the page and try again")
			return
		}
		// An account that must enrol a second factor can reach exactly the
		// endpoints that let it do so, and nothing else. Refusing everything
		// including enrolment would be a locked door with the key inside.
		if s.Cfg.Security.RequireTOTP && !caller.Account.TOTPEnabled && !isEnrolmentPath(r.URL.Path) {
			failStatus(w, http.StatusForbidden, "totp_required",
				"this panel requires a second factor before anything else",
				"set one up under your account")
			return
		}
		caller.IP = panel.ClientIP(r, s.Cfg.Listen.TrustProxy)
		ctx := context.WithValue(r.Context(), ctxAccount, caller.Account)
		ctx = context.WithValue(ctx, ctxSession, caller.Session)
		h(w, r.WithContext(ctx), caller)
	})
}

func isEnrolmentPath(p string) bool {
	switch p {
	case "/api/me", "/api/me/totp/start", "/api/me/totp/confirm", "/api/auth/logout":
		return true
	}
	return false
}

// authenticate resolves the cookie to a live session and an enabled account.
//
// The idle timeout and the absolute lifetime are enforced together: a session ends
// when it has been quiet for too long *or* when it is simply too old, and refreshing
// the first never extends the second past its original ceiling. Sliding a fixed
// expiry forward on every request is the common version of this and it means a
// browser that polls has an unbounded session.
func (s *Server) authenticate(r *http.Request) (*Caller, error) {
	c, err := r.Cookie(s.Cfg.Session.CookieName)
	if err != nil || c.Value == "" {
		return nil, errors.New("no session")
	}
	hash := auth.HashToken(c.Value)
	sess, err := s.Store.FindSession(r.Context(), hash)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if !sess.ExpiresAt.After(now) {
		_ = s.Store.DeleteSession(r.Context(), hash)
		return nil, errors.New("the session has expired")
	}
	if now.Sub(sess.LastSeenAt) > s.Cfg.Session.IdleTimeout.D() {
		_ = s.Store.DeleteSession(r.Context(), hash)
		return nil, errors.New("the session went idle")
	}
	account, err := s.Store.FindAccount(r.Context(), sess.AccountID)
	if err != nil {
		return nil, err
	}
	if account.Disabled {
		_ = s.Store.DeleteSessionsFor(r.Context(), account.ID)
		return nil, errors.New("the account is disabled")
	}
	// Never past the ceiling the session started with.
	ceiling := sess.CreatedAt.Add(s.Cfg.Session.TTL.D())
	expires := now.Add(s.Cfg.Session.TTL.D())
	if expires.After(ceiling) {
		expires = ceiling
	}
	if err := s.Store.TouchSession(r.Context(), hash, now, expires); err != nil {
		s.Log.Debug("could not refresh the session", "err", err)
	}
	sess.LastSeenAt, sess.ExpiresAt = now, expires
	return &Caller{Account: account, Session: sess}, nil
}

// checkCSRF refuses a state-changing request that did not come from the panel.
//
// Two independent checks, because each one alone has a known hole. The header token
// is the double-submit: an attacker's page can make the browser send the cookie but
// cannot read it to echo it back. The Origin check catches the case where it could —
// a subdomain that can write cookies for the parent domain — and costs nothing.
//
// GET is exempt because it must be: an SSE stream cannot carry a header the browser
// will not let EventSource set. That is safe only because no GET here changes
// anything, which is a property to keep rather than a coincidence.
func (s *Server) checkCSRF(r *http.Request, caller *Caller) error {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}
	if caller.Session == nil {
		return errors.New("this request needs a session")
	}
	token := r.Header.Get("X-Ratline-CSRF")
	if token == "" || caller.Session.CSRFToken == "" ||
		!auth.ConstantTimeEqualString(token, caller.Session.CSRFToken) {
		return errors.New("the request did not carry this session's token")
	}
	if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(origin, r) {
		return errors.New("the request came from another origin")
	}
	return nil
}

// originAllowed compares the Origin against the panel's own name.
func (s *Server) originAllowed(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if s.Cfg.Listen.Domain != "" && strings.EqualFold(u.Hostname(), s.Cfg.Listen.Domain) {
		return true
	}
	// Before a domain is set the panel is reached through a tunnel or a port, so
	// the Host header is the only name it has. Comparing against it is weaker than
	// comparing against a configured name, which is precisely why setting one is
	// part of the install.
	return strings.EqualFold(u.Host, r.Host)
}

// withAllowList refuses an address outside security.allow_from before anything else
// happens, including reading a body.
func (s *Server) withAllowList(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !panel.AllowedFrom(s.allowFrom, panel.ClientIP(r, s.Cfg.Listen.TrustProxy)) {
			failStatus(w, http.StatusForbidden, "not_allowed",
				"this address may not reach the panel", "")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withSecurityHeaders sets the headers a panel serving root-equivalent operations
// should be serving.
//
// The Content-Security-Policy is the load-bearing one: 'self' for scripts and styles
// with no inline anything, so a value that somehow reaches the DOM as markup cannot
// execute. The bundle is built to comply rather than the policy relaxed to fit the
// bundle, which is the usual direction and the wrong one.
func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self'; " +
		"img-src 'self' data:; " +
		"font-src 'self'; " +
		"connect-src 'self'; " +
		"form-action 'self'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'none'; " +
		"object-src 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		// Only over HTTPS. Sending HSTS from a panel somebody is reaching on
		// http://localhost through an SSH tunnel would pin that name to HTTPS in
		// their browser and lock them out of it.
		if panel.RequestIsSecure(r, s.Cfg.Listen.TrustProxy) {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		// An API response must never be cached: it is per-account, and a shared
		// cache holding one is a way for the next person to read it.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the status for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush and Unwrap keep SSE working through the wrapper: without them the stream
// buffers until the response ends, which for a job that is being watched means it
// shows nothing until it is over.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *Server) withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// The path only, never the query and never a body: this log is read by
		// whoever is debugging, and a panel that writes secrets into its own log
		// has moved the problem rather than solved it.
		s.Log.Debug("request",
			"method", r.Method, "path", r.URL.Path, "status", rec.status,
			"ms", time.Since(start).Milliseconds(),
			"ip", panel.ClientIP(r, s.Cfg.Listen.TrustProxy))
	})
}

// withRecovery turns a panic into a 500 rather than a dropped connection and a dead
// server. A panel that dies on one bad request takes every session with it.
func (s *Server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.Log.Error("panic serving a request", "path", r.URL.Path, "panic", v)
				failStatus(w, http.StatusInternalServerError, "internal",
					"the panel hit an internal error", "check the panel's journal: journalctl -u ratline-panel")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
