package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/panel/auth"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// requireSuperAdmin is the gate on everything in this file.
//
// Managing who else can administer the server is the one power a super admin has
// that an admin does not, and it is the power that can create the other one — so it
// is checked in one place, by one function, rather than by remembering to add a route
// to a list.
func (s *Server) requireSuperAdmin(w http.ResponseWriter, c *Caller) bool {
	if c.Account.Role == store.RoleSuperAdmin {
		return true
	}
	failStatus(w, http.StatusForbidden, "forbidden",
		"only a super admin can manage who has access",
		"ask a super admin to make the change")
	return false
}

// teamView is the panel's people: accounts, and invitations not yet accepted.
type teamView struct {
	Accounts []*store.Account `json:"accounts"`
	Invites  []inviteSummary  `json:"invites"`
}

type inviteSummary struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	InvitedBy string    `json:"invited_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Server) handleTeam(w http.ResponseWriter, r *http.Request, c *Caller) {
	if !s.requireSuperAdmin(w, c) {
		return
	}
	accounts, err := s.Store.ListAccounts(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	invites, err := s.Store.ListInvites(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	now := s.now()
	summaries := make([]inviteSummary, 0, len(invites))
	for _, inv := range invites {
		summaries = append(summaries, inviteSummary{
			ID: inv.ID, Email: inv.Email, Role: inv.Role, Status: inv.Status(now),
			InvitedBy: inv.InvitedBy, CreatedAt: inv.CreatedAt, ExpiresAt: inv.ExpiresAt,
		})
	}
	ok(w, teamView{Accounts: accounts, Invites: summaries})
}

type inviteRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// invitedView carries the link back exactly once.
//
// The panel does not send email. It could — and then it would own an SMTP
// configuration, a queue, a bounce problem and a new way to leak an invitation to
// whoever reads the mail server's logs. Handing the link to the person who created it
// keeps the panel's dependencies at zero and puts the decision about how to deliver
// it with the human who knows which channel is safe.
type invitedView struct {
	Invite inviteSummary `json:"invite"`
	Link   string        `json:"link"`
	Note   string        `json:"note"`
}

func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request, c *Caller) {
	if !s.requireSuperAdmin(w, c) {
		return
	}
	var req inviteRequest
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	email := store.NormalizeEmail(req.Email)
	if err := validate.Email(email); err != nil {
		s.fail(w, err)
		return
	}
	if !store.ValidRole(req.Role) {
		s.fail(w, rlerr.Usagef("a role must be %q or %q", store.RoleAdmin, store.RoleSuperAdmin))
		return
	}
	if _, err := s.Store.FindAccountByEmail(r.Context(), email); err == nil {
		s.fail(w, rlerr.Preconditionf("%s already has an account", email).
			WithHint("change their role instead of inviting them again"))
		return
	}
	token, hash, err := auth.NewToken()
	if err != nil {
		s.fail(w, err)
		return
	}
	id, err := auth.NewID()
	if err != nil {
		s.fail(w, err)
		return
	}
	now := s.now()
	inv := &store.Invite{
		ID: id, Email: email, Role: req.Role, InvitedBy: c.Account.Email,
		CreatedAt: now, ExpiresAt: now.Add(s.Cfg.Security.InviteTTL.D()),
	}
	if err := s.Store.CreateInvite(r.Context(), inv, hash); err != nil {
		s.fail(w, err)
		return
	}
	s.Log.Info("invited", "email", email, "role", req.Role, "by", c.Account.Email)
	ok(w, invitedView{
		Invite: inviteSummary{
			ID: inv.ID, Email: inv.Email, Role: inv.Role, Status: inv.Status(now),
			InvitedBy: inv.InvitedBy, CreatedAt: inv.CreatedAt, ExpiresAt: inv.ExpiresAt,
		},
		Link: s.Cfg.PublicURL() + "/accept?token=" + token,
		Note: "This link is shown once and is not stored. Send it over a channel you " +
			"trust — it is worth as much as the account it creates.",
	})
}

func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request, c *Caller) {
	if !s.requireSuperAdmin(w, c) {
		return
	}
	if err := s.Store.RevokeInvite(r.Context(), r.PathValue("id"), s.now()); err != nil {
		s.fail(w, err)
		return
	}
	ok(w, map[string]bool{"revoked": true})
}

type roleRequest struct {
	Role string `json:"role"`
}

// handleSetRole changes somebody's role.
//
// Refusing to change your own is not paternalism: a super admin who demotes
// themselves while alone in the panel has locked the door and posted the key through
// it, and getting back in means SSH and `ratline-panel account promote`. The store
// refuses to leave the panel with no super admin for the same reason; this refuses
// the near miss that is only survivable because somebody else happens to exist.
func (s *Server) handleSetRole(w http.ResponseWriter, r *http.Request, c *Caller) {
	if !s.requireSuperAdmin(w, c) {
		return
	}
	var req roleRequest
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	id := r.PathValue("id")
	if id == c.Account.ID {
		s.fail(w, rlerr.Preconditionf("you cannot change your own role").
			WithHint("ask another super admin, or use 'ratline-panel account role' over SSH"))
		return
	}
	if err := s.Store.SetAccountRole(r.Context(), id, req.Role); err != nil {
		s.fail(w, err)
		return
	}
	s.Log.Warn("a role was changed", "account", id, "role", req.Role, "by", c.Account.Email)
	ok(w, map[string]string{"role": req.Role})
}

type disableRequest struct {
	Disabled bool `json:"disabled"`
}

func (s *Server) handleSetDisabled(w http.ResponseWriter, r *http.Request, c *Caller) {
	if !s.requireSuperAdmin(w, c) {
		return
	}
	var req disableRequest
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	id := r.PathValue("id")
	if id == c.Account.ID {
		s.fail(w, rlerr.Preconditionf("you cannot disable your own account"))
		return
	}
	if err := s.Store.SetAccountDisabled(r.Context(), id, req.Disabled); err != nil {
		s.fail(w, err)
		return
	}
	s.Log.Warn("an account was enabled or disabled", "account", id,
		"disabled", req.Disabled, "by", c.Account.Email)
	ok(w, map[string]bool{"disabled": req.Disabled})
}

// handleDeleteAccount removes somebody from the panel.
//
// It removes their access to the panel and nothing else. The tenants, sites and keys
// ratline holds are the server's, not this person's, and deleting an administrator
// must never cascade into deleting what they administered — that would make removing
// a departing colleague the most dangerous button in the product.
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request, c *Caller) {
	if !s.requireSuperAdmin(w, c) {
		return
	}
	id := r.PathValue("id")
	if id == c.Account.ID {
		s.fail(w, rlerr.Preconditionf("you cannot delete your own account"))
		return
	}
	// Typed back, like every other irreversible thing in this product. The header
	// rather than a body so that a DELETE stays a DELETE.
	target, err := s.Store.FindAccount(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	confirm := strings.TrimSpace(r.Header.Get("X-Ratline-Confirm"))
	if !auth.ConstantTimeEqualString(confirm, target.Email) {
		failStatus(w, http.StatusBadRequest, "confirmation_required",
			"type "+target.Email+" to confirm",
			"this removes their access to the panel; it changes nothing on the server")
		return
	}
	if err := s.Store.DeleteAccount(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	s.Log.Warn("an account was deleted", "account", target.Email, "by", c.Account.Email)
	ok(w, map[string]bool{"deleted": true})
}
