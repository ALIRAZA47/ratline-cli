package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/sshkey"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

// auditKeys is the analysis behind `ratline key audit` and part of `doctor`.
//
// Each finding answers "why does this matter" and "what do I type", because a
// finding an operator cannot act on is noise.
func (g *Globals) auditKeys(ctx context.Context) ([]sshkey.AuditFinding, error) {
	st, err := g.Store(ctx)
	if err != nil {
		return nil, err
	}
	mgr, err := g.keyManager(ctx)
	if err != nil {
		return nil, err
	}
	since, _ := st.LastKeyUsageScan(ctx)
	if _, err := mgr.ScanUsage(ctx, since); err != nil {
		g.Log.Debug("usage scan failed", "err", err)
	}

	keys, err := st.ListKeys(ctx, state.KeyFilter{})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var findings []sshkey.AuditFinding

	// The same key in two scopes means a usage record can no longer be
	// attributed to one grant.
	byFingerprint := map[string][]*state.Key{}
	for _, k := range keys {
		byFingerprint[k.Fingerprint] = append(byFingerprint[k.Fingerprint], k)
	}
	for fp, group := range byFingerprint {
		if len(group) < 2 {
			continue
		}
		var where []string
		for _, k := range group {
			where = append(where, k.Scope+" → "+k.Target())
		}
		findings = append(findings, sshkey.AuditFinding{
			Severity: "warning", Kind: "duplicate", Key: fp, Label: group[0].Label,
			Detail: "installed at " + strings.Join(where, " and ") + ", so usage cannot be attributed to one grant",
			Fix:    "keep one: ratline key remove " + fp + " --scope " + group[0].Scope,
		})
	}

	for _, k := range keys {
		switch {
		case k.Algorithm == "ssh-dss":
			findings = append(findings, sshkey.AuditFinding{
				Severity: "problem", Kind: "weak-algorithm", Key: k.Fingerprint, Label: k.Label,
				Detail: "DSA keys are fixed at 1024 bits",
				Fix:    "replace it: ratline key remove " + k.Fingerprint + " and add an ed25519 key",
			})
		case strings.Contains(k.Algorithm, "rsa") && k.Bits > 0 && k.Bits < g.Cfg.SSH.WarnRSABits:
			findings = append(findings, sshkey.AuditFinding{
				Severity: "warning", Kind: "weak-algorithm", Key: k.Fingerprint, Label: k.Label,
				Detail: fmt.Sprintf("RSA %d bits, under the warning threshold of %d", k.Bits, g.Cfg.SSH.WarnRSABits),
				Fix:    "ask the holder for an ed25519 key",
			})
		}

		if k.Expired(now) {
			findings = append(findings, sshkey.AuditFinding{
				Severity: "problem", Kind: "expired", Key: k.Fingerprint, Label: k.Label,
				Detail: "expired on " + k.ExpiresAt.Format("2006-01-02") + " but is still installed",
				Fix:    "remove it: ratline key remove " + k.Fingerprint,
			})
		}

		// Stale access is the most common real finding: a contractor finished
		// months ago and nobody took the key away.
		if k.LastUsedAt.IsZero() && now.Sub(k.AddedAt) > 90*24*time.Hour {
			findings = append(findings, sshkey.AuditFinding{
				Severity: "warning", Kind: "never-used", Key: k.Fingerprint, Label: k.Label,
				Detail: fmt.Sprintf("added %d days ago and never observed in use", int(now.Sub(k.AddedAt).Hours()/24)),
				Fix:    "if it is not needed: ratline key remove " + k.Fingerprint,
			})
		} else if !k.LastUsedAt.IsZero() && now.Sub(k.LastUsedAt) > 90*24*time.Hour {
			findings = append(findings, sshkey.AuditFinding{
				Severity: "warning", Kind: "stale", Key: k.Fingerprint, Label: k.Label,
				Detail: fmt.Sprintf("last used %d days ago", int(now.Sub(k.LastUsedAt).Hours()/24)),
				Fix:    "if it is not needed: ratline key remove " + k.Fingerprint,
			})
		}

		// A label that promises less access than the key grants is how an
		// operator ends up trusting the wrong thing.
		if k.Scope == state.ScopeGlobal && looksSiteScoped(k.Label) {
			findings = append(findings, sshkey.AuditFinding{
				Severity: "problem", Kind: "misleading-label", Key: k.Fingerprint, Label: k.Label,
				Detail: "the label suggests limited access but the key administers the whole server",
				Fix:    "narrow it: ratline key move " + k.Fingerprint + " --to-scope site --site <domain>",
			})
		}
		if k.Scope == state.ScopeSite && k.AllowShell {
			findings = append(findings, sshkey.AuditFinding{
				Severity: "warning", Kind: "allow-shell", Key: k.Fingerprint, Label: k.Label,
				Detail: "site scope with --allow-shell: a shell can reach anything the owner's UID can",
				Fix:    "for real isolation, give this site its own user",
			})
		}
	}

	// Keys outside the managed markers. These are reported, never removed:
	// deleting something an operator put there by hand would be worse than
	// leaving it.
	users, err := st.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	paths := map[string]string{}
	if admin := g.Cfg.Server.AdminUser; admin != "" {
		paths[admin] = filepath.Join(g.Cfg.HomeDir(admin), ".ssh", "authorized_keys")
	}
	for _, u := range users {
		paths[u.Name] = filepath.Join(u.Home, ".ssh", "authorized_keys")
	}
	for owner, path := range paths {
		f, err := sshkey.ReadFile(path, g.Cfg.SSH.MaxAuthKeysBytes)
		if err != nil {
			g.Log.Debug("could not read an authorized_keys file", "path", path, "err", err)
			continue
		}
		for _, u := range f.Unmanaged {
			findings = append(findings, sshkey.AuditFinding{
				Severity: "warning", Kind: "unmanaged", Key: u.Fingerprint, Label: u.Comment,
				Detail: fmt.Sprintf("a key in %s sits outside ratline's managed block, so ratline cannot account for it", path),
				Fix: "adopt it with 'ratline key add --scope user --user " + owner +
					" --label \"…\"' and then remove the hand-written line",
			})
		}
		for _, p := range sshkey.CheckPermissions(filepath.Dir(filepath.Dir(path)), path) {
			findings = append(findings, sshkey.AuditFinding{
				Severity: "problem", Kind: "permissions", Label: owner, Detail: p,
				Fix: "sshd silently ignores keys when the modes are too open; fix them and try logging in again",
			})
		}
	}
	return findings, nil
}

// looksSiteScoped spots a label that promises less than the key grants.
func looksSiteScoped(label string) bool {
	l := strings.ToLower(label)
	for _, hint := range []string{"deploy", "ci", "contractor", "readonly", "read-only", "sftp", "backup", "upload"} {
		if strings.Contains(l, hint) {
			return true
		}
	}
	return false
}
