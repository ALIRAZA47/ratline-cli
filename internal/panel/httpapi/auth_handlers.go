package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/panel"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/auth"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// bootstrapView is what an unauthenticated browser is allowed to know.
//
// Deliberately almost nothing: whether anybody has set the panel up, and whether a
// second factor will be wanted. Not the server's name, not the ratline version, not
// how many accounts exist. A sign-in page that fingerprints the host for anybody who
// loads it is a sign-in page doing an attacker's first step for them.
type bootstrapView struct {
	NeedsSetup  bool   `json:"needs_setup"`
	RequireTOTP bool   `json:"require_totp"`
	Product     string `json:"product"`
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountAccounts(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	ok(w, bootstrapView{
		NeedsSetup:  n == 0,
		RequireTOTP: s.Cfg.Security.RequireTOTP,
		Product:     "ratline panel",
	})
}

type setupRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// handleSetup creates the first super admin, and only the first.
//
// Normally nothing reaches this: `ratline-panel install` creates the account, so a
// panel that is answering already has one and this returns 409. It exists for the
// paths where the database is genuinely empty — an install run with --no-admin, an
// uninstall --purge, or an operator who deleted the last account by hand.
//
// The window is the empty accounts table, and it closes on the first successful call.
// No default password, no printed token, no "admin/admin until you change it". A
// panel left in this state on a public port belongs to whoever finds it, which is why
// the installer closes the window itself and `doctor` reports it as fatal.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	n, err := s.Store.CountAccounts(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	if n > 0 {
		failStatus(w, http.StatusConflict, "already_set_up",
			"this panel already has an account",
			"ask a super admin to invite you")
		return
	}
	account, err := s.newAccount(req.Email, req.Name, req.Password, store.RoleSuperAdmin, "setup")
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.CreateAccount(r.Context(), account); err != nil {
		s.fail(w, err)
		return
	}
	s.Log.Info("the panel was set up", "email", account.Email)
	s.issueSession(w, r, account)
}

// newAccount validates and hashes, so the three places that create one — setup, an
// accepted invitation and the command line — cannot disagree about what is allowed.
func (s *Server) newAccount(email, name, password, role, by string) (*store.Account, error) {
	email = store.NormalizeEmail(email)
	if err := validate.Email(email); err != nil {
		return nil, err
	}
	if err := validate.NoControlChars("name", name); err != nil {
		return nil, err
	}
	if len(name) > 128 {
		return nil, rlerr.Usagef("a name may be at most 128 characters")
	}
	if err := auth.CheckPasswordStrength(password); err != nil {
		return nil, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	id, err := auth.NewID()
	if err != nil {
		return nil, err
	}
	return &store.Account{
		ID: id, Email: email, Name: name, Role: role,
		PasswordHash: hash, CreatedBy: by, CreatedAt: s.now(),
	}, nil
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

// handleLogin is the front door, and the only endpoint an unauthenticated caller can
// use repeatedly. Everything defensive about it is here.
//
// The refusal is the same sentence whatever went wrong — no account, wrong password,
// disabled, bad code — and the timing is the same too, because a response that is
// fast for an unknown address and slow for a known one enumerates the accounts as
// surely as a different message would. Failures are counted per account and per
// source address independently: counting only per account lets one password be tried
// against every address, and counting only per address lets a distributed attempt
// through.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	ctx := r.Context()
	ip := panel.ClientIP(r, s.Cfg.Listen.TrustProxy)
	now := s.now()
	email := store.NormalizeEmail(req.Email)

	since := now.Add(-s.Cfg.Security.LoginWindow.D())
	byEmail, byIP, err := s.Store.FailedLoginsSince(ctx, email, ip, since)
	if err != nil {
		s.fail(w, err)
		return
	}
	limit := s.Cfg.Security.MaxFailedLogins
	if byEmail >= limit || byIP >= limit {
		// Told plainly, because an operator locked out by their own typo needs to
		// know to wait rather than to keep trying, and an attacker learns nothing
		// they could not measure anyway.
		w.Header().Set("Retry-After", "60")
		failStatus(w, http.StatusTooManyRequests, "rate_limited",
			"too many failed sign-in attempts; wait a few minutes",
			"the window is "+s.Cfg.Security.LoginWindow.String())
		return
	}

	account, lookupErr := s.Store.FindAccountByEmail(ctx, email)
	if lookupErr != nil {
		// The same work a real verification would do, so that a wrong address and
		// a wrong password take the same time.
		auth.EqualiseTiming(req.Password)
		s.rejectLogin(w, ctx, email, ip, now)
		return
	}
	valid, verr := auth.VerifyPassword(account.PasswordHash, req.Password)
	if verr != nil {
		s.Log.Error("a stored password hash could not be read", "account", account.Email, "err", verr)
	}
	if !valid || account.Disabled {
		s.rejectLogin(w, ctx, email, ip, now)
		return
	}
	if account.TOTPEnabled {
		valid, err := auth.VerifyTOTP(account.TOTPSecret, req.Code, now)
		if err != nil || !valid {
			s.rejectLogin(w, ctx, email, ip, now)
			return
		}
	} else if s.Cfg.Security.RequireTOTP {
		// Allowed through to enrol, and nothing else: authed() holds the rest of
		// the panel shut until the factor exists.
		s.Log.Info("signing in without a second factor to enrol one", "account", account.Email)
	}

	if err := s.Store.RecordLoginAttempt(ctx, email, ip, true, now); err != nil {
		s.Log.Debug("could not record the sign-in", "err", err)
	}
	if err := s.Store.ClearLoginAttempts(ctx, email); err != nil {
		s.Log.Debug("could not clear the failed attempts", "err", err)
	}
	if err := s.Store.RecordLogin(ctx, account.ID, ip, now); err != nil {
		s.Log.Debug("could not stamp the sign-in", "err", err)
	}
	s.Log.Info("signed in", "account", account.Email, "ip", ip)
	s.issueSession(w, r, account)
}

func (s *Server) rejectLogin(w http.ResponseWriter, ctx context.Context, email, ip string, now time.Time) {
	if err := s.Store.RecordLoginAttempt(ctx, email, ip, false, now); err != nil {
		s.Log.Debug("could not record the failed sign-in", "err", err)
	}
	failStatus(w, http.StatusUnauthorized, "invalid_credentials",
		"that email address and password do not match an account", "")
}

// sessionView is what a signed-in browser is told about itself.
type sessionView struct {
	Account *store.Account `json:"account"`
	// CSRF is handed over in the body rather than in a readable cookie, so the
	// token exists only in the page's memory. A cookie the page can read is a
	// cookie an injected script can read.
	//
	// /api/me returns it too, which is what makes a reloaded tab work: the page
	// keeps nothing across a load, so on the way back it asks who it is and gets
	// the token with the answer.
	CSRF      string    `json:"csrf"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, account *store.Account) {
	token, tokenHash, err := auth.NewToken()
	if err != nil {
		s.fail(w, err)
		return
	}
	csrf, _, err := auth.NewToken()
	if err != nil {
		s.fail(w, err)
		return
	}
	now := s.now()
	sess := &store.Session{
		TokenHash:  tokenHash,
		CSRFToken:  csrf,
		AccountID:  account.ID,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(s.Cfg.Session.TTL.D()),
		IP:         panel.ClientIP(r, s.Cfg.Listen.TrustProxy),
		UserAgent:  truncate(r.UserAgent(), 200),
	}
	if err := s.Store.CreateSession(r.Context(), sess); err != nil {
		s.fail(w, err)
		return
	}
	s.setSessionCookie(w, r, token, sess.ExpiresAt)
	ok(w, sessionView{Account: account, CSRF: csrf, ExpiresAt: sess.ExpiresAt})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	//nolint:gosec // G124: Secure is set from cookieSecure(r), which the analyser cannot follow
	http.SetCookie(w, &http.Cookie{
		Name:     s.Cfg.Session.CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.cookieSecure(r),
		// Strict rather than Lax. Lax lets the cookie ride a top-level GET from
		// another site, and the reason that is usually tolerated — sign-in links
		// arriving from email — does not apply to a panel nobody links to.
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	//nolint:gosec // G124: Secure is set from cookieSecure(r), which the analyser cannot follow
	http.SetCookie(w, &http.Cookie{
		Name:     s.Cfg.Session.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cookieSecure(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// cookieSecure decides whether the cookie may only travel over HTTPS.
//
// "auto" is the default because both fixed answers are wrong somewhere. Always would
// make the panel unusable through an SSH tunnel on http://localhost, which is exactly
// how the first sign-in happens. Never would ship a root-equivalent session in the
// clear the moment somebody puts a domain on it.
func (s *Server) cookieSecure(r *http.Request) bool {
	switch s.Cfg.Session.SecureCookie {
	case "always":
		return true
	case "never":
		return false
	default:
		return panel.RequestIsSecure(r, s.Cfg.Listen.TrustProxy)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(s.Cfg.Session.CookieName); err == nil && c.Value != "" {
		if derr := s.Store.DeleteSession(r.Context(), auth.HashToken(c.Value)); derr != nil {
			s.Log.Debug("could not delete the session", "err", derr)
		}
	}
	s.clearSessionCookie(w, r)
	ok(w, map[string]bool{"signed_out": true})
}

// inviteView describes an invitation to somebody holding the link, before they have
// an account. The address is included because they are about to type it; nothing
// else about the panel is.
type inviteView struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (s *Server) handleInviteLookup(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		failStatus(w, http.StatusBadRequest, "usage", "no invitation token", "")
		return
	}
	inv, err := s.Store.FindInviteByToken(r.Context(), auth.HashToken(token))
	if err != nil || !inv.Pending(s.now()) {
		// One answer for "no such invitation" and "that one is used up", so the
		// endpoint cannot be used to test tokens.
		failStatus(w, http.StatusNotFound, "invalid_invite",
			"that invitation is not valid any more",
			"ask a super admin for a new link")
		return
	}
	ok(w, inviteView{Email: inv.Email, Role: inv.Role})
}

type acceptRequest struct {
	Token    string `json:"token"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// handleAcceptInvite turns a link into an account.
//
// The role comes from the invitation, never from the request: a body that could name
// its own role would let anybody holding an admin invitation become a super admin.
func (s *Server) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	var req acceptRequest
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	ctx := r.Context()
	inv, err := s.Store.FindInviteByToken(ctx, auth.HashToken(req.Token))
	if err != nil || !inv.Pending(s.now()) {
		failStatus(w, http.StatusNotFound, "invalid_invite",
			"that invitation is not valid any more",
			"ask a super admin for a new link")
		return
	}
	account, err := s.newAccount(inv.Email, req.Name, req.Password, inv.Role, "invitation from "+inv.InvitedBy)
	if err != nil {
		s.fail(w, err)
		return
	}
	// Marked used first. If the account creation then fails the invitation is
	// spent, which is the safe direction: the alternative is a link that survives
	// a partial failure and can be replayed.
	if err := s.Store.AcceptInvite(ctx, inv.ID, s.now()); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.CreateAccount(ctx, account); err != nil {
		s.fail(w, err)
		return
	}
	s.Log.Info("an invitation was accepted", "email", account.Email, "role", account.Role)
	s.issueSession(w, r, account)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
