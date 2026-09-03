package store

import "time"

// Roles. Two, deliberately.
//
// A third read-only role is the obvious next thing to want, and it is not here because
// "read-only" is not a property of the panel — it is a property of each action, and the
// catalogue already carries it. Adding a role is a row in the capability table below;
// adding one before anybody has asked what it should *not* be able to do produces a role
// nobody can describe.
const (
	// RoleSuperAdmin can do everything an admin can, plus the two things that are
	// not day-to-day work: changing who else has access, and the operations that
	// cannot be undone by running another command.
	RoleSuperAdmin = "superadmin"
	// RoleAdmin runs the server: sites, deploys, certificates, keys, databases,
	// environment. Everything Ploi calls "managing a server".
	RoleAdmin = "admin"
)

// ValidRole reports whether a string names a role.
func ValidRole(r string) bool { return r == RoleSuperAdmin || r == RoleAdmin }

// RoleRank orders the roles for comparison. Higher outranks lower.
func RoleRank(r string) int {
	switch r {
	case RoleSuperAdmin:
		return 2
	case RoleAdmin:
		return 1
	default:
		return 0
	}
}

// AtLeast reports whether have is the same role as want or outranks it.
func AtLeast(have, want string) bool { return RoleRank(have) >= RoleRank(want) }

// Account is somebody who may sign in to the panel.
//
// PasswordHash and TOTPSecret are never marshalled. The struct is handed straight to
// the JSON encoder by the handlers, so the tag is the control, not a convention the
// next handler has to remember.
type Account struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name,omitempty"`
	Role         string    `json:"role"`
	PasswordHash string    `json:"-"`
	TOTPSecret   string    `json:"-"`
	TOTPEnabled  bool      `json:"totp_enabled"`
	Disabled     bool      `json:"disabled"`
	CreatedAt    time.Time `json:"created_at"`
	CreatedBy    string    `json:"created_by,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
	LastLoginAt  time.Time `json:"last_login_at,omitempty"`
	LastLoginIP  string    `json:"last_login_ip,omitempty"`
}

// Session is one signed-in browser.
type Session struct {
	TokenHash  string    `json:"-"`
	CSRFToken  string    `json:"-"`
	AccountID  string    `json:"account_id"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	IP         string    `json:"ip,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
}

// Invite is an unaccepted invitation to create an account.
//
// The token itself exists once, in the link handed to the person invited. What is
// stored is its hash, so the invitations table cannot be turned into a set of working
// links by anybody who reads it.
type Invite struct {
	ID         string    `json:"id"`
	Email      string    `json:"email"`
	Role       string    `json:"role"`
	InvitedBy  string    `json:"invited_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	AcceptedAt time.Time `json:"accepted_at,omitempty"`
	RevokedAt  time.Time `json:"revoked_at,omitempty"`
}

// Pending reports whether the invitation can still be accepted at the given moment.
func (i *Invite) Pending(at time.Time) bool {
	return i.AcceptedAt.IsZero() && i.RevokedAt.IsZero() && i.ExpiresAt.After(at)
}

// Status names the invitation's state for a listing.
func (i *Invite) Status(at time.Time) string {
	switch {
	case !i.AcceptedAt.IsZero():
		return "accepted"
	case !i.RevokedAt.IsZero():
		return "revoked"
	case !i.ExpiresAt.After(at):
		return "expired"
	default:
		return "pending"
	}
}

// ActionRecord is one thing somebody asked the panel to do.
type ActionRecord struct {
	ID         int64     `json:"id"`
	At         time.Time `json:"at"`
	ActorID    string    `json:"actor_id,omitempty"`
	Actor      string    `json:"actor,omitempty"`
	Action     string    `json:"action"`
	Argv       string    `json:"argv,omitempty"`
	Target     string    `json:"target,omitempty"`
	DryRun     bool      `json:"dry_run"`
	OK         bool      `json:"ok"`
	ExitCode   int       `json:"exit_code"`
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	IP         string    `json:"ip,omitempty"`
}

// Job states.
const (
	JobQueued  = "queued"
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"
)

// Job is one long-running invocation with a transcript.
type Job struct {
	ID         string    `json:"id"`
	Action     string    `json:"action"`
	Target     string    `json:"target,omitempty"`
	Argv       string    `json:"argv,omitempty"`
	ActorID    string    `json:"actor_id,omitempty"`
	Actor      string    `json:"actor,omitempty"`
	State      string    `json:"state"`
	QueuedAt   time.Time `json:"queued_at"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	ExitCode   int       `json:"exit_code"`
	Error      string    `json:"error,omitempty"`
	Hint       string    `json:"hint,omitempty"`
	Output     string    `json:"output,omitempty"`
	DryRun     bool      `json:"dry_run"`
}

// Terminal reports whether the job has finished, one way or the other.
func (j *Job) Terminal() bool { return j.State == JobDone || j.State == JobFailed }
