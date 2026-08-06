package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/sshkey"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

func newKeyCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "key",
		Short:   "Add, inspect and revoke SSH keys across the three scopes",
		GroupID: GroupKeys,
		Long: "Keys are a managed resource with three scopes:\n\n" +
			"  global  server administration — a shell as the administrator, and permission to run ratline\n" +
			"  user    one tenant — an interactive shell and every site that user owns\n" +
			"  site    one site directory — sftp, rsync and git, with no interactive shell\n\n" +
			"Site scope is a blast-radius boundary, not a kernel one: the session still runs as\n" +
			"the site owner's UID. Where real isolation is needed, use one user per site.\n" +
			"'ratline key test' spells out what any given key can reach.",
	}
	cmd.AddCommand(
		newKeyAddCommand(g),
		newKeyListCommand(g),
		newKeyShowCommand(g),
		newKeyRemoveCommand(g, false),
		newKeyRemoveCommand(g, true),
		newKeyMoveCommand(g),
		newKeyTestCommand(g),
		newKeyAuditCommand(g),
		newKeySyncCommand(g),
		newKeyPruneCommand(g),
	)
	return cmd
}

func (g *Globals) keyManager(ctx context.Context) (*sshkey.Manager, error) {
	st, err := g.Store(ctx)
	if err != nil {
		return nil, err
	}
	return &sshkey.Manager{
		Cfg:     g.Cfg,
		Log:     g.Log,
		Runner:  g.Runner,
		State:   st,
		Invoker: g.Invoked(),
		DryRun:  g.DryRun,
	}, nil
}

func newKeyAddCommand(g *Globals) *cobra.Command {
	var (
		label          string
		scope          string
		userName       string
		site           string
		keyRefs        []string
		fromGitHub     string
		fromGitLab     string
		sftpOnly       bool
		allowShell     bool
		from           []string
		expires        string
		noAgentForward bool
		noPortForward  bool
		noPTY          bool
		command        string
		allowDuplicate bool
		isolation      string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Install a public key at one of the three scopes",
		Args:  cobra.NoArgs,
		Example: "  # your own laptop, for server administration\n" +
			"  ratline key add --scope global --label \"Ali MacBook\" --key ~/.ssh/id_ed25519.pub\n\n" +
			"  # a client, who gets a shell and all of their sites\n" +
			"  ratline key add --scope user --user acme --label \"Acme ops\" --from-github acme-ops\n\n" +
			"  # a contractor, confined to one site, from one network, for 90 days\n" +
			"  ratline key add --scope site --site example.com --label \"Contractor\" \\\n" +
			"      --key contractor.pub --from 203.0.113.0/24 --expires 90d",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, err := g.keyManager(cmd.Context())
			if err != nil {
				return err
			}
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}

			// The wizard runs when asked for, and is offered rather than a usage
			// page when the two required flags are missing on a terminal.
			if g.Interactive || (g.CanPrompt() && (label == "" || scope == "")) {
				collected, werr := wizardKeyAdd(g, cmd.Context(), keyWizardResult{
					Label: label,
					Grant: sshkey.Grant{Scope: scope, User: userName, Site: site,
						AllowShell: allowShell, SFTPOnly: sftpOnly, CommandPreset: command},
					KeyRefs: keyRefs,
				})
				if werr != nil {
					return errCancelledToNil(werr)
				}
				label = collected.Label
				scope = collected.Grant.Scope
				userName = collected.Grant.User
				site = collected.Grant.Site
				from = collected.Grant.FromCIDR
				keyRefs = collected.KeyRefs
				if !collected.Grant.ExpiresAt.IsZero() {
					expires = collected.Grant.ExpiresAt.Format("2006-01-02")
				}
				// A pasted key arrives inline; hand it to the collector over stdin so
				// there is one code path for key material.
				for i, ref := range keyRefs {
					if strings.HasPrefix(ref, "literal:") {
						g.Stdin = strings.NewReader(strings.TrimPrefix(ref, "literal:") + "\n")
						keyRefs[i] = "-"
					}
					if strings.HasPrefix(ref, "github:") {
						fromGitHub = strings.TrimPrefix(ref, "github:")
						keyRefs = append(keyRefs[:i], keyRefs[i+1:]...)
						break
					}
				}
			}
			if err := RequireFlags(cmd, g, "label", "scope"); err != nil {
				return err
			}
			if err := validate.Label(label); err != nil {
				return err
			}

			grant := sshkey.Grant{
				Scope:          scope,
				User:           userName,
				Site:           site,
				AllowShell:     allowShell,
				SFTPOnly:       sftpOnly,
				FromCIDR:       from,
				CommandPreset:  command,
				NoAgentForward: noAgentForward,
				NoPortForward:  noPortForward,
				NoPTY:          noPTY,
				ShellWrapper:   g.Cfg.Paths.ShellWrapper,
			}
			var siteRow *state.Site
			if scope == state.ScopeSite {
				if site == "" {
					return rlerr.Usagef("--scope site requires --site")
				}
				normalised, err := validate.Domain(site)
				if err != nil {
					return err
				}
				grant.Site = normalised
				if siteRow, err = st.FindSiteByName(cmd.Context(), normalised); err != nil {
					return err
				}
				grant.SiteDir = g.Cfg.SiteDir(siteRow.Owner, siteRow.Domain)
			}
			if expires != "" {
				at, err := validate.ExpiryTime(expires, time.Now())
				if err != nil {
					return err
				}
				grant.ExpiresAt = at
				grant.ExpirySupported = mgr.DetectExpirySupport(cmd.Context())
			}
			if err := sshkey.ResolveScope(&grant, siteRow); err != nil {
				return err
			}
			if scope == state.ScopeGlobal && g.Cfg.Server.AdminUser == "" {
				g.Log.Warn("no administrator account is configured, so a global key is recorded but not installed anywhere sshd reads",
					"fix", "set server.admin_user in the configuration, or run 'ratline init'")
			}
			if grant.AllowShell {
				g.Log.Warn("--allow-shell removes most of what site scope confines: a shell can reach anything the owner's UID can",
					"site", grant.Site, "owner", grant.User)
			}
			switch isolation {
			case "", "default":
			case "strict":
				if scope != state.ScopeSite {
					return rlerr.Usagef("--isolation strict only applies to --scope site")
				}
				if !g.Cfg.Features.StrictIsolation {
					return rlerr.Preconditionf("strict isolation is turned off").
						WithHint("set features.strict_isolation: true in %s first. It adds a chroot, which is "+
							"stronger than the default confinement but produces a login that fails with "+
							"nothing useful in the log when it is misconfigured — one user per site is "+
							"simpler and kernel-enforced", g.Cfg.SourcePath)
				}
				if grant.AllowShell {
					return rlerr.Usagef("--isolation strict and --allow-shell contradict each other").
						WithHint("a chroot exists to prevent a shell reaching outside it")
				}
				grant.SFTPOnly = true
				g.Log.Warn("strict isolation adds a chroot; test the login before relying on it",
					"site", grant.Site)
			default:
				return rlerr.Usagef("--isolation must be default or strict, got %q", isolation)
			}

			opts := sshkey.AddOptions{
				Label:          label,
				Grant:          grant,
				AllowDuplicate: allowDuplicate,
				KeyRefs:        keyRefs,
				FromGitHub:     fromGitHub,
				FromGitLab:     fromGitLab,
				Stdin:          g.Stdin,
			}
			keys, warnings, err := mgr.Collect(cmd.Context(), opts)
			if err != nil {
				return err
			}
			for _, w := range warnings {
				g.Log.Warn(string(w))
			}

			// A fetched key list is unreviewed input: show every fingerprint and
			// get a yes before any of it is installed.
			if fromGitHub != "" || fromGitLab != "" {
				if !g.JSON {
					g.Printf("Fetched %d key(s):\n", len(keys))
					for _, k := range keys {
						g.Printf("  %s  %s  %s\n", k.Fingerprint, k.Algorithm, k.Comment)
					}
				}
				ok, err := g.Confirm(fmt.Sprintf("Install %d key(s) at %s scope?", len(keys), scope))
				if err != nil {
					return err
				}
				if !ok {
					g.Println("Nothing was installed.")
					return nil
				}
			}

			res, err := mgr.Add(cmd.Context(), opts, keys)
			if err != nil {
				return err
			}
			for _, w := range res.Warnings {
				g.Log.Warn(w)
			}
			// SFTP-only tenants need an sshd Match block, which is a separate,
			// verified change.
			if grant.SFTPOnly || scope == state.ScopeSite {
				if err := mgr.ApplyDropIn(cmd.Context()); err != nil {
					return err
				}
			}

			if g.JSON {
				return g.EmitJSON(res)
			}
			for _, k := range res.Keys {
				g.Printf("Added %s (%s) at %s scope", k.Label, k.Fingerprint, k.Scope)
				if t := k.Target(); t != "" && k.Scope != state.ScopeGlobal {
					g.Printf(" → %s", t)
				}
				g.Printf("\n")
			}
			g.Printf("\nWhat this key can reach:  ratline key test %s\n", res.Keys[0].Fingerprint)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&label, "label", "", "Human label, so this key can be recognised later (required)")
	f.StringVar(&scope, "scope", "", "global, user or site (required)")
	f.StringVar(&userName, "user", "", "Tenant, for --scope user")
	f.StringVar(&site, "site", "", "Domain, for --scope site")
	f.StringArrayVar(&keyRefs, "key", nil, "Public key: a path, an https URL, or - for stdin (repeatable)")
	f.StringVar(&fromGitHub, "from-github", "", "Fetch a user's public keys from github.com")
	f.StringVar(&fromGitLab, "from-gitlab", "", "Fetch a user's public keys from gitlab.com")
	f.BoolVar(&sftpOnly, "sftp-only", false, "Force SFTP with no shell")
	f.BoolVar(&allowShell, "allow-shell", false, "Site scope only: permit an interactive shell (weakens the confinement)")
	f.StringSliceVar(&from, "from", nil, "Restrict to these source addresses, e.g. 203.0.113.0/24")
	f.StringVar(&expires, "expires", "", "Expiry as a date (2026-12-31) or a duration (90d)")
	f.BoolVar(&noAgentForward, "no-agent-forwarding", false, "Refuse agent forwarding (the default outside global scope)")
	f.BoolVar(&noPortForward, "no-port-forwarding", false, "Refuse port forwarding (already the default)")
	f.BoolVar(&noPTY, "no-pty", false, "Refuse a PTY")
	f.StringVar(&command, "command", "", "Named preset from config: sftp-only, rsync-only or git-only")
	f.BoolVar(&allowDuplicate, "allow-duplicate", false, "Permit a fingerprint that is already installed elsewhere")
	f.StringVar(&isolation, "isolation", "", "Site scope only: default, or strict to add a chroot (needs features.strict_isolation)")
	return OwnWizard(Mutating(cmd))
}

// addUserKeys installs the keys given to `user add` at user scope.
func (g *Globals) addUserKeys(ctx context.Context, owner string, refs []string) (int, error) {
	mgr, err := g.keyManager(ctx)
	if err != nil {
		return 0, err
	}
	grant := sshkey.Grant{Scope: state.ScopeUser, User: owner, ShellWrapper: g.Cfg.Paths.ShellWrapper}
	if err := sshkey.ResolveScope(&grant, nil); err != nil {
		return 0, err
	}
	opts := sshkey.AddOptions{
		Label:   "added with user " + owner,
		Grant:   grant,
		KeyRefs: refs,
		Stdin:   g.Stdin,
	}
	keys, warnings, err := mgr.Collect(ctx, opts)
	if err != nil {
		return 0, err
	}
	for _, w := range warnings {
		g.Log.Warn(string(w))
	}
	res, err := mgr.Add(ctx, opts, keys)
	if err != nil {
		return 0, err
	}
	return len(res.Keys), nil
}

func newKeyListCommand(g *Globals) *cobra.Command {
	var (
		scope    string
		userName string
		site     string
		unused   int
		expiring int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List keys, optionally filtered by scope, staleness or expiry",
		Args:  cobra.NoArgs,
		Example: "  ratline key list\n" +
			"  ratline key list --scope site --site example.com\n" +
			"  ratline key list --unused 90        # stale contractor access",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, err := g.keyManager(cmd.Context())
			if err != nil {
				return err
			}
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			// Opportunistic, so last-used data survives log rotation.
			since, _ := st.LastKeyUsageScan(cmd.Context())
			if _, err := mgr.ScanUsage(cmd.Context(), since); err != nil {
				g.Log.Debug("usage scan failed", "err", err)
			}

			keys, err := st.ListKeys(cmd.Context(), state.KeyFilter{Scope: scope, Owner: userName, Site: site})
			if err != nil {
				return err
			}
			now := time.Now()
			var filtered []*state.Key
			for _, k := range keys {
				if unused > 0 {
					if !k.LastUsedAt.IsZero() && now.Sub(k.LastUsedAt) < time.Duration(unused)*24*time.Hour {
						continue
					}
				}
				if expiring > 0 {
					if k.ExpiresAt.IsZero() || k.ExpiresAt.After(now.Add(time.Duration(expiring)*24*time.Hour)) {
						continue
					}
				}
				filtered = append(filtered, k)
			}

			if g.JSON {
				return g.EmitJSON(map[string]any{"keys": filtered})
			}
			if len(filtered) == 0 {
				g.Println("No keys match.")
				return nil
			}
			tbl := g.Table("fingerprint", "label", "algo", "scope", "target", "last used", "expires")
			for _, k := range filtered {
				tbl.Row(shortFingerprint(k.Fingerprint), k.Label, k.Algorithm, k.Scope, k.Target(),
					relativeTime(k.LastUsedAt, now), expiryColumn(k, now))
			}
			return tbl.Render()
		},
	}
	f := cmd.Flags()
	f.StringVar(&scope, "scope", "", "Only this scope")
	f.StringVar(&userName, "user", "", "Only this tenant")
	f.StringVar(&site, "site", "", "Only this site")
	f.IntVar(&unused, "unused", 0, "Only keys not seen in this many days")
	f.IntVar(&expiring, "expiring", 0, "Only keys expiring within this many days")
	return cmd
}

func newKeyShowCommand(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "show <fingerprint|label|id>",
		Short: "Show one key in full",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			keys, err := st.FindKeys(cmd.Context(), args[0], state.KeyFilter{IncludeRevoked: true})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"keys": keys})
			}
			for i, k := range keys {
				if i > 0 {
					g.Println()
				}
				pairs := [][2]string{
					{"id", k.ID},
					{"label", k.Label},
					{"fingerprint", k.Fingerprint},
					{"algorithm", fmt.Sprintf("%s (%d bits)", k.Algorithm, k.Bits)},
					{"scope", k.Scope + " → " + k.Target()},
					{"options", k.Options},
					{"source", k.Source},
					{"added", k.AddedAt.Format("2006-01-02 15:04") + " by " + k.AddedBy},
				}
				if k.Comment != "" {
					pairs = append(pairs, [2]string{"comment", k.Comment})
				}
				if len(k.FromCIDR) > 0 {
					pairs = append(pairs, [2]string{"from", strings.Join(k.FromCIDR, ", ")})
				}
				if !k.ExpiresAt.IsZero() {
					pairs = append(pairs, [2]string{"expires", expiryColumn(k, time.Now())})
				}
				if !k.RevokedAt.IsZero() {
					pairs = append(pairs, [2]string{"revoked", k.RevokedAt.Format("2006-01-02")})
				}
				if err := g.Fields(pairs...); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func newKeyRemoveCommand(g *Globals, revoke bool) *cobra.Command {
	var (
		scope      string
		userName   string
		site       string
		everywhere bool
		force      bool
	)
	use, short := "remove <fingerprint|label|id>", "Remove a key from one scope"
	if revoke {
		use, short = "revoke <fingerprint|label|id>", "Remove a key from every scope and add it to the revoked list"
	}
	cmd := &cobra.Command{
		Use:     use,
		Aliases: aliasesFor(revoke),
		Short:   short,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := g.keyManager(cmd.Context())
			if err != nil {
				return err
			}
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}

			// Show what is about to go, and warn before the last global key.
			filter := state.KeyFilter{Scope: scope, Owner: userName, Site: site}
			if revoke || everywhere {
				filter = state.KeyFilter{}
			}
			matches, err := st.FindKeys(cmd.Context(), args[0], filter)
			if err != nil {
				return err
			}
			if !g.JSON {
				g.Printf("This will remove:\n")
				for _, k := range matches {
					g.Printf("  %s  %s  (%s → %s)\n", shortFingerprint(k.Fingerprint), k.Label, k.Scope, k.Target())
				}
			}
			for _, k := range matches {
				if k.Scope != state.ScopeGlobal {
					continue
				}
				remaining, err := st.CountKeysInScope(cmd.Context(), state.ScopeGlobal, "", "")
				if err != nil {
					return err
				}
				if remaining <= 1 {
					g.Printf("\nThis is the last global key. Afterwards the only way in is a console\n" +
						"session or a password login, if you have either.\n")
					if err := g.ConfirmTyped(k.Fingerprint, "Remove the last key able to administer this server?"); err != nil {
						return err
					}
					force = true
				}
			}

			removed, err := mgr.Remove(cmd.Context(), sshkey.RemoveOptions{
				Needle: args[0], Scope: scope, User: userName, Site: site,
				Everywhere: everywhere || revoke, Revoke: revoke, Force: force,
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"removed": removed, "revoked": revoke})
			}
			verb := "Removed"
			if revoke {
				verb = "Revoked"
			}
			g.Printf("%s %d key(s)\n", verb, len(removed))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&scope, "scope", "", "Only this scope")
	f.StringVar(&userName, "user", "", "Only this tenant")
	f.StringVar(&site, "site", "", "Only this site")
	f.BoolVar(&force, "force", false, "Proceed even if this is the last credential for a scope")
	if !revoke {
		f.BoolVar(&everywhere, "everywhere", false, "Remove the key from every scope on this server")
	} else {
		everywhere = true
		f.Bool("everywhere", true, "Always true for revoke")
		_ = f.MarkHidden("everywhere")
	}
	return Mutating(cmd)
}

func aliasesFor(revoke bool) []string {
	if revoke {
		return nil
	}
	return []string{"rm"}
}

func newKeyMoveCommand(g *Globals) *cobra.Command {
	var (
		toScope string
		toUser  string
		toSite  string
	)
	cmd := &cobra.Command{
		Use:   "move <fingerprint|label|id>",
		Short: "Move a key to a different scope",
		Args:  cobra.ExactArgs(1),
		Example: "  # narrow a contractor's access to one site\n" +
			"  ratline key move SHA256:x9K… --to-scope site --site example.com",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := RequireFlags(cmd, g, "to-scope"); err != nil {
				return err
			}
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			mgr, err := g.keyManager(cmd.Context())
			if err != nil {
				return err
			}
			matches, err := st.FindKeys(cmd.Context(), args[0], state.KeyFilter{})
			if err != nil {
				return err
			}
			if len(matches) != 1 {
				return rlerr.Usagef("%q matches %d keys; move one at a time using its id", args[0], len(matches))
			}
			old := matches[0]

			grant := sshkey.Grant{
				Scope: toScope, User: toUser, Site: toSite,
				FromCIDR: old.FromCIDR, ExpiresAt: old.ExpiresAt,
				AllowShell: old.AllowShell, SFTPOnly: old.SFTPOnly,
				CommandPreset: old.Command, ShellWrapper: g.Cfg.Paths.ShellWrapper,
			}
			var siteRow *state.Site
			if toScope == state.ScopeSite {
				if siteRow, err = st.FindSiteByName(cmd.Context(), toSite); err != nil {
					return err
				}
				grant.Site = siteRow.Domain
			}
			if err := sshkey.ResolveScope(&grant, siteRow); err != nil {
				return err
			}
			if !grant.ExpiresAt.IsZero() {
				grant.ExpirySupported = mgr.DetectExpirySupport(cmd.Context())
			}

			moved := *old
			moved.ID = state.NewKeyID()
			moved.Scope = grant.Scope
			moved.Owner = grant.User
			moved.Site = grant.Site
			moved.Options = sshkey.Options(&grant)
			moved.AllowShell = grant.AllowShell
			if err := st.PutKey(cmd.Context(), &moved); err != nil {
				return err
			}
			if err := st.DeleteKey(cmd.Context(), old.ID); err != nil {
				return err
			}
			for _, s := range [][2]string{{old.Scope, old.Owner}, {moved.Scope, moved.Owner}} {
				if err := mgr.SyncScope(cmd.Context(), s[0], s[1]); err != nil {
					return err
				}
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"key": moved, "from_scope": old.Scope})
			}
			g.Printf("Moved %s from %s → %s to %s → %s\n",
				old.Label, old.Scope, old.Target(), moved.Scope, moved.Target())
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&toScope, "to-scope", "", "New scope: global, user or site")
	f.StringVar(&toUser, "user", "", "Tenant, for --to-scope user")
	f.StringVar(&toSite, "site", "", "Domain, for --to-scope site")
	return Mutating(cmd)
}

func newKeyTestCommand(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "test <fingerprint|label|id>",
		Short: "Explain in plain English exactly what a key can reach",
		Args:  cobra.ExactArgs(1),
		Long: "Answers the question that matters before someone finds out the hard way:\n" +
			"what can this key actually do on this server?",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := g.Store(cmd.Context())
			if err != nil {
				return err
			}
			keys, err := st.FindKeys(cmd.Context(), args[0], state.KeyFilter{IncludeRevoked: true})
			if err != nil {
				return err
			}
			now := time.Now()
			var caps []*sshkey.Capability
			for _, k := range keys {
				siteDir := ""
				if k.Scope == state.ScopeSite {
					siteDir = g.Cfg.SiteDir(k.Owner, k.Site)
				}
				caps = append(caps, sshkey.Describe(k, siteDir, now))
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"capabilities": caps})
			}
			for i, c := range caps {
				if i > 0 {
					g.Println()
				}
				pairs := [][2]string{
					{"Key", fmt.Sprintf("%s   %q   %s", shortFingerprint(c.Fingerprint), c.Label, c.Algorithm)},
					{"Scope", scopeLine(c)},
					{"Login", c.Login},
					{"Allowed", strings.Join(c.Allowed, ", ")},
				}
				if c.ConfinedTo != "" {
					pairs = append(pairs, [2]string{"", "confined to " + c.ConfinedTo})
				}
				pairs = append(pairs, [2]string{"Denied", strings.Join(c.Denied, ", ")})
				if len(c.Source) > 0 {
					pairs = append(pairs, [2]string{"Source", strings.Join(c.Source, ", ") + " only"})
				} else {
					pairs = append(pairs, [2]string{"Source", "any address"})
				}
				if c.Expires != "" {
					pairs = append(pairs, [2]string{"Expires", c.Expires})
				} else {
					pairs = append(pairs, [2]string{"Expires", "never"})
				}
				pairs = append(pairs,
					[2]string{"Last use", c.LastUse},
					[2]string{"Note", c.Note},
				)
				if err := g.Fields(pairs...); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func scopeLine(c *sshkey.Capability) string {
	if c.Target == "" || c.Scope == state.ScopeGlobal {
		return c.Scope
	}
	s := c.Scope + " → " + c.Target
	if c.LoginAs != "" && c.Scope == state.ScopeSite {
		s += "  (owner: " + c.LoginAs + ")"
	}
	return s
}

func newKeyAuditCommand(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "audit",
		Short: "Report duplicate, weak, stale, expired and unmanaged keys",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			findings, err := g.auditKeys(cmd.Context())
			if err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"findings": findings})
			}
			if len(findings) == 0 {
				g.Println("No key problems found.")
				return nil
			}
			tbl := g.Table("severity", "kind", "key", "detail")
			for _, f := range findings {
				tbl.Row(f.Severity, f.Kind, f.Label, f.Detail)
			}
			if err := tbl.Render(); err != nil {
				return err
			}
			g.Println()
			for _, f := range findings {
				if f.Fix != "" {
					g.Printf("  %s: %s\n", f.Kind, f.Fix)
				}
			}
			return nil
		},
	}
}

func newKeySyncCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Re-render every authorized_keys file from state",
		Args:  cobra.NoArgs,
		Long: "Rewrites the managed block in each file. Keys an operator added by hand outside\n" +
			"the markers are preserved exactly as they are.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mgr, err := g.keyManager(cmd.Context())
			if err != nil {
				return err
			}
			n, err := mgr.SyncAll(cmd.Context())
			if err != nil {
				return err
			}
			if err := mgr.ApplyDropIn(cmd.Context()); err != nil {
				return err
			}
			if g.JSON {
				return g.EmitJSON(map[string]any{"files_written": n})
			}
			g.Printf("Re-rendered %d authorized_keys file(s)\n", n)
			return nil
		},
	}
	return Mutating(cmd)
}

func shortFingerprint(fp string) string {
	const width = 22
	if len(fp) <= width {
		return fp
	}
	return fp[:width] + "…"
}

func relativeTime(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < time.Hour:
		return "just now"
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func expiryColumn(k *state.Key, now time.Time) string {
	if k.ExpiresAt.IsZero() {
		return "never"
	}
	days := int(k.ExpiresAt.Sub(now).Hours() / 24)
	if days < 0 {
		return k.ExpiresAt.Format("2006-01-02") + " (expired)"
	}
	return fmt.Sprintf("%s (%dd)", k.ExpiresAt.Format("2006-01-02"), days)
}
