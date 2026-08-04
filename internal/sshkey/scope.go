package sshkey

import (
	"fmt"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// Grant is everything that decides what a key can reach. It is resolved once
// from the flags and then drives both the options string and `key test`.
type Grant struct {
	Scope    string
	User     string
	Site     string
	SiteDir  string
	SiteUser string

	AllowShell     bool
	SFTPOnly       bool
	FromCIDR       []string
	ExpiresAt      time.Time
	CommandPreset  string
	NoAgentForward bool
	NoPortForward  bool
	NoPTY          bool

	// ShellWrapper is the forced command for site scope.
	ShellWrapper string
	// ExpirySupported reports whether sshd understands expiry-time=.
	ExpirySupported bool
}

// ResolveScope checks that the scope's required arguments are present and
// consistent, and fills in the derived fields.
func ResolveScope(g *Grant, site *state.Site) error {
	switch g.Scope {
	case state.ScopeGlobal:
		if g.User != "" || g.Site != "" {
			return rlerr.Usagef("--scope global takes neither --user nor --site").
				WithHint("a global key grants server administration, not access to one tenant")
		}
	case state.ScopeUser:
		if g.User == "" {
			return rlerr.Usagef("--scope user requires --user")
		}
		if g.Site != "" {
			return rlerr.Usagef("--scope user does not take --site").
				WithHint("a user-scoped key already reaches every site that user owns; use --scope site to confine it to one")
		}
	case state.ScopeSite:
		if g.Site == "" {
			return rlerr.Usagef("--scope site requires --site")
		}
		if site == nil {
			return rlerr.Preconditionf("no such site: %s", g.Site)
		}
		g.SiteUser = site.Owner
		if g.User != "" && g.User != site.Owner {
			return rlerr.Usagef("%s is owned by %s, not %s", g.Site, site.Owner, g.User).
				WithHint("omit --user; the site's owner is used")
		}
		g.User = site.Owner
	default:
		return rlerr.Usagef("unknown scope %q", g.Scope).
			WithHint("choose global (server administration), user (one tenant) or site (one directory)")
	}

	if g.AllowShell && g.Scope != state.ScopeSite {
		return rlerr.Usagef("--allow-shell only applies to --scope site").
			WithHint("global and user scoped keys already get a shell")
	}
	if len(g.FromCIDR) > 0 {
		normalised, err := validate.CIDRList(strings.Join(g.FromCIDR, ","))
		if err != nil {
			return err
		}
		g.FromCIDR = normalised
	}
	return nil
}

// Options renders the authorized_keys option string for a grant.
//
// Every scope starts from OpenSSH's `restrict`, which turns off port forwarding,
// agent forwarding, X11, PTY allocation and the user rc file. Anything a scope
// needs is then re-enabled explicitly. Opting in to permissiveness rather than
// out of it means a future OpenSSH option that `restrict` covers is safe by
// default rather than newly exposed.
func Options(g *Grant) string {
	var opts []string
	opts = append(opts, "restrict")

	needsShell := g.Scope == state.ScopeGlobal || g.Scope == state.ScopeUser || g.AllowShell
	if needsShell && !g.NoPTY {
		// Without a pty an interactive shell is close to unusable.
		opts = append(opts, "pty")
	}
	if !g.NoAgentForward && g.Scope == state.ScopeGlobal {
		// Administrators legitimately hop onward from the box; tenants do not.
		opts = append(opts, "agent-forwarding")
	}

	if len(g.FromCIDR) > 0 {
		opts = append(opts, `from="`+strings.Join(g.FromCIDR, ",")+`"`)
	}
	// expiry-time= needs OpenSSH 8.2. On anything older the option would be a
	// parse error that breaks the whole file, so the daily pruning timer is the
	// fallback and the caller is told which one is in force.
	if !g.ExpiresAt.IsZero() && g.ExpirySupported {
		opts = append(opts, `expiry-time="`+g.ExpiresAt.UTC().Format("20060102150405")+`"`)
	}
	if cmd := forcedCommand(g); cmd != "" {
		opts = append(opts, `command="`+cmd+`"`)
	}
	return strings.Join(opts, ",")
}

// forcedCommand is the wrapper invocation for a confined key.
func forcedCommand(g *Grant) string {
	if g.Scope != state.ScopeSite {
		return ""
	}
	wrapper := g.ShellWrapper
	if wrapper == "" {
		wrapper = "/usr/local/lib/ratline/ratline-shell"
	}
	cmd := wrapper + " --site " + g.Site
	if g.AllowShell {
		cmd += " --allow-shell"
	}
	if g.CommandPreset != "" {
		cmd += " --only " + g.CommandPreset
	}
	return cmd
}

// Capability describes, in plain English, what a key can do. This is the data
// behind `ratline key test`, whose whole purpose is to answer "what can this key
// actually reach?" before someone finds out the hard way.
type Capability struct {
	Fingerprint string   `json:"fingerprint"`
	Label       string   `json:"label"`
	Algorithm   string   `json:"algorithm"`
	Scope       string   `json:"scope"`
	Target      string   `json:"target,omitempty"`
	LoginAs     string   `json:"login_as,omitempty"`
	Login       string   `json:"login"`
	Allowed     []string `json:"allowed"`
	Denied      []string `json:"denied"`
	ConfinedTo  string   `json:"confined_to,omitempty"`
	Source      []string `json:"source_addresses,omitempty"`
	Expires     string   `json:"expires,omitempty"`
	LastUse     string   `json:"last_use,omitempty"`
	Note        string   `json:"note"`
}

// Describe explains a stored key's reach.
func Describe(k *state.Key, siteDir string, now time.Time) *Capability {
	c := &Capability{
		Fingerprint: k.Fingerprint,
		Label:       k.Label,
		Algorithm:   k.Algorithm,
		Scope:       k.Scope,
		Target:      k.Target(),
		LoginAs:     k.Owner,
	}
	switch k.Scope {
	case state.ScopeGlobal:
		c.Login = "interactive shell as the administrator"
		c.Allowed = []string{"shell", "sftp", "rsync", "git", "running ratline"}
		c.Denied = []string{"port forwarding", "X11"}
		c.Note = "This key administers the whole server."
	case state.ScopeUser:
		c.Login = k.Owner + "@server — interactive shell"
		c.Allowed = []string{"shell", "sftp", "rsync", "git", "every site " + k.Owner + " owns"}
		c.Denied = []string{"port forwarding", "agent forwarding", "X11", "other tenants' files"}
		c.Note = "Runs as UID " + k.Owner + ". Reaches every site that user owns."
	case state.ScopeSite:
		if k.AllowShell {
			c.Login = k.Owner + "@server — shell, opened in the site directory"
			c.Allowed = []string{"shell", "sftp", "rsync", "git-upload-pack", "git-receive-pack"}
			c.Denied = []string{"port forwarding", "agent forwarding", "X11"}
			c.Note = "--allow-shell was used, which removes most of the confinement: a shell can " +
				"reach anything the owner's UID can. Not a kernel boundary — see SECURITY.md."
		} else {
			c.Login = k.Owner + "@server — forced command only, no interactive shell"
			c.Allowed = []string{"sftp", "rsync", "git-upload-pack", "git-receive-pack"}
			c.Denied = []string{"shell", "port forwarding", "agent forwarding", "X11", "PTY"}
			c.Note = "Runs as UID " + k.Owner + ". Not a kernel boundary — see SECURITY.md."
		}
		c.ConfinedTo = siteDir + " (symlinks resolved)"
	}
	if k.Command != "" {
		c.Allowed = []string{k.Command + " only"}
	}
	if len(k.FromCIDR) > 0 {
		c.Source = k.FromCIDR
	}
	if !k.ExpiresAt.IsZero() {
		days := int(k.ExpiresAt.Sub(now).Hours() / 24)
		switch {
		case days < 0:
			c.Expires = k.ExpiresAt.Format("2006-01-02") + " (expired)"
		default:
			c.Expires = fmt.Sprintf("%s (%d days)", k.ExpiresAt.Format("2006-01-02"), days)
		}
	}
	if !k.LastUsedAt.IsZero() {
		c.LastUse = k.LastUsedAt.Format("2006-01-02 15:04")
		if k.LastUsedIP != "" {
			c.LastUse += " from " + k.LastUsedIP
		}
	} else {
		c.LastUse = "never observed"
	}
	return c
}
