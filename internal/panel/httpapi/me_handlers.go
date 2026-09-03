package httpapi

import (
	"net/http"

	"github.com/ALIRAZA47/ratline-cli/internal/panel/auth"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// meView is the signed-in account plus what the interface needs to decide what to
// draw. Capabilities are computed here rather than inferred from the role in the
// browser: one place decides what a role can do, and it is the same place that
// enforces it.
type meView struct {
	Account      *store.Account `json:"account"`
	Capabilities capabilities   `json:"capabilities"`
	Panel        panelView      `json:"panel"`
	// The session's CSRF token, so a reloaded tab can send state-changing
	// requests again. The page holds it in memory only; it is never written to a
	// cookie the page could read, which is the whole reason it is returned here
	// rather than set beside the session.
	CSRF string `json:"csrf"`
}

type capabilities struct {
	ManageTeam   bool `json:"manage_team"`
	Destructive  bool `json:"destructive"`
	RequireTOTP  bool `json:"require_totp"`
	NeedsTOTPNow bool `json:"needs_totp_now"`
}

type panelView struct {
	Domain  string `json:"domain,omitempty"`
	Version string `json:"version,omitempty"`
	Ratline string `json:"ratline_version,omitempty"`
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, c *Caller) {
	view := meView{
		Account: c.Account,
		Capabilities: capabilities{
			ManageTeam:   c.Account.Role == store.RoleSuperAdmin,
			Destructive:  c.Account.Role == store.RoleSuperAdmin,
			RequireTOTP:  s.Cfg.Security.RequireTOTP,
			NeedsTOTPNow: s.Cfg.Security.RequireTOTP && !c.Account.TOTPEnabled,
		},
		Panel: panelView{Domain: s.Cfg.Listen.Domain, Version: Version},
	}
	if c.Session != nil {
		view.CSRF = c.Session.CSRFToken
	}
	// Best effort: the panel is perfectly usable while ratline is being upgraded
	// underneath it, and a version string is not worth failing the page over.
	if cat, err := s.Client.Catalogue(r.Context()); err == nil {
		view.Panel.Ratline = cat.Version
	}
	ok(w, view)
}

// Version is stamped at build time, the same way ratline's is.
var Version = "dev"

type changePasswordRequest struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

// handleChangePassword requires the current password even though the caller is
// already signed in.
//
// Because "already signed in" is exactly the state an attacker who borrowed a laptop
// is in, and a password change is how they would keep the access after the screen is
// locked again. Every other session is ended, including this one, so a change is also
// the way somebody evicts a session they do not recognise.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, c *Caller) {
	var req changePasswordRequest
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	valid, err := auth.VerifyPassword(c.Account.PasswordHash, req.Current)
	if err != nil || !valid {
		failStatus(w, http.StatusForbidden, "invalid_credentials",
			"the current password is wrong", "")
		return
	}
	if err := auth.CheckPasswordStrength(req.New); err != nil {
		s.fail(w, err)
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.SetPassword(r.Context(), c.Account.ID, hash); err != nil {
		s.fail(w, err)
		return
	}
	s.clearSessionCookie(w, r)
	s.Log.Info("a password was changed", "account", c.Account.Email)
	ok(w, map[string]bool{"changed": true, "signed_out": true})
}

type totpStartView struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

// handleTOTPStart issues a secret without enabling it.
//
// Two steps, because enabling on the first call would let somebody scan a QR code
// that did not save, and lock themselves out of the panel they are administering.
// The secret is stored but inert until a code proves it arrived.
func (s *Server) handleTOTPStart(w http.ResponseWriter, r *http.Request, c *Caller) {
	if c.Account.TOTPEnabled {
		failStatus(w, http.StatusConflict, "already_enrolled",
			"this account already has a second factor",
			"remove the existing one first")
		return
	}
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.SetTOTP(r.Context(), c.Account.ID, secret, false); err != nil {
		s.fail(w, err)
		return
	}
	issuer := "ratline"
	if s.Cfg.Listen.Domain != "" {
		issuer = "ratline (" + s.Cfg.Listen.Domain + ")"
	}
	ok(w, totpStartView{Secret: secret, URI: auth.TOTPURI(secret, c.Account.Email, issuer)})
}

type totpConfirmRequest struct {
	Code string `json:"code"`
}

func (s *Server) handleTOTPConfirm(w http.ResponseWriter, r *http.Request, c *Caller) {
	var req totpConfirmRequest
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if c.Account.TOTPSecret == "" {
		s.fail(w, rlerr.Preconditionf("no second factor has been started for this account"))
		return
	}
	valid, err := auth.VerifyTOTP(c.Account.TOTPSecret, req.Code, s.now())
	if err != nil {
		s.fail(w, err)
		return
	}
	if !valid {
		failStatus(w, http.StatusForbidden, "invalid_code",
			"that code is not right", "check the clock on the device generating it")
		return
	}
	if err := s.Store.SetTOTP(r.Context(), c.Account.ID, c.Account.TOTPSecret, true); err != nil {
		s.fail(w, err)
		return
	}
	s.Log.Info("a second factor was enrolled", "account", c.Account.Email)
	ok(w, map[string]bool{"enabled": true})
}

type totpDisableRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

// handleTOTPDisable needs both factors, which is the point: removing the second
// factor is the single most useful thing for somebody who has stolen the first.
func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request, c *Caller) {
	var req totpDisableRequest
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if s.Cfg.Security.RequireTOTP {
		s.fail(w, rlerr.Preconditionf("this panel requires a second factor").
			WithHint("set security.require_totp to false in panel.yaml first"))
		return
	}
	valid, err := auth.VerifyPassword(c.Account.PasswordHash, req.Password)
	if err != nil || !valid {
		failStatus(w, http.StatusForbidden, "invalid_credentials", "the password is wrong", "")
		return
	}
	if c.Account.TOTPEnabled {
		valid, err := auth.VerifyTOTP(c.Account.TOTPSecret, req.Code, s.now())
		if err != nil || !valid {
			failStatus(w, http.StatusForbidden, "invalid_code", "that code is not right", "")
			return
		}
	}
	if err := s.Store.SetTOTP(r.Context(), c.Account.ID, "", false); err != nil {
		s.fail(w, err)
		return
	}
	s.Log.Warn("a second factor was removed", "account", c.Account.Email)
	ok(w, map[string]bool{"enabled": false})
}

// sessionSummary describes one live session without the token that would let
// somebody use it.
type sessionSummary struct {
	Current   bool   `json:"current"`
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	CreatedAt string `json:"created_at"`
	LastSeen  string `json:"last_seen"`
	ExpiresAt string `json:"expires_at"`
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request, c *Caller) {
	sessions, err := s.Store.ListSessionsFor(r.Context(), c.Account.ID, s.now())
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]sessionSummary, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, sessionSummary{
			Current:   c.Session != nil && sess.TokenHash == c.Session.TokenHash,
			IP:        sess.IP,
			UserAgent: sess.UserAgent,
			CreatedAt: sess.CreatedAt.Format(timeLayout),
			LastSeen:  sess.LastSeenAt.Format(timeLayout),
			ExpiresAt: sess.ExpiresAt.Format(timeLayout),
		})
	}
	ok(w, out)
}

const timeLayout = "2006-01-02T15:04:05Z07:00"
