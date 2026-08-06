package sshkey

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// Manager applies key changes to the filesystem and to state.
type Manager struct {
	Cfg     *config.Config
	Log     *log.Logger
	Runner  system.Runner
	State   *state.Store
	Invoker string
	DryRun  bool
}

// AddOptions is the resolved form of `ratline key add`.
type AddOptions struct {
	Label          string
	Grant          Grant
	AllowDuplicate bool

	// Exactly one source of key material.
	KeyRefs    []string // paths, https URLs, or "-" for stdin
	FromGitHub string
	FromGitLab string

	Stdin io.Reader
}

// AddResult reports what was added.
type AddResult struct {
	Keys     []*state.Key `json:"keys"`
	Warnings []string     `json:"warnings,omitempty"`
}

// Policy builds the validation policy from config.
func (m *Manager) Policy() Policy {
	return Policy{
		MinRSABits:         m.Cfg.SSH.MinRSABits,
		WarnRSABits:        m.Cfg.SSH.WarnRSABits,
		AllowedAlgorithms:  m.Cfg.SSH.AllowedAlgorithms,
		RejectedAlgorithms: m.Cfg.SSH.RejectedAlgorithms,
		MaxLineBytes:       m.Cfg.SSH.MaxKeyLineBytes,
	}
}

// Collect gathers and validates key material without installing it, so the
// interactive path and `--from-github` can show fingerprints for confirmation
// before anything is written.
func (m *Manager) Collect(ctx context.Context, opts AddOptions) ([]*PublicKey, []Warning, error) {
	var (
		blobs    [][]byte
		warnings []Warning
	)
	switch {
	case opts.FromGitHub != "":
		b, err := m.fetchKeys(ctx, "https://github.com/"+urlPathSegment(opts.FromGitHub)+".keys")
		if err != nil {
			return nil, nil, err
		}
		blobs = append(blobs, b)
	case opts.FromGitLab != "":
		b, err := m.fetchKeys(ctx, "https://gitlab.com/"+urlPathSegment(opts.FromGitLab)+".keys")
		if err != nil {
			return nil, nil, err
		}
		blobs = append(blobs, b)
	}
	for _, ref := range opts.KeyRefs {
		b, err := m.readKeyRef(ctx, ref, opts.Stdin)
		if err != nil {
			return nil, nil, err
		}
		blobs = append(blobs, b)
	}
	if len(blobs) == 0 {
		return nil, nil, rlerr.Usagef("no key material was supplied").
			WithHint("pass --key <path|url|->, --from-github <user> or --from-gitlab <user>")
	}

	var keys []*PublicKey
	seen := map[string]bool{}
	for _, b := range blobs {
		parsed, w, err := ParseMany(b, m.Policy())
		if err != nil {
			return nil, nil, err
		}
		warnings = append(warnings, w...)
		for _, k := range parsed {
			if seen[k.Fingerprint] {
				continue
			}
			seen[k.Fingerprint] = true
			keys = append(keys, k)
		}
	}
	return keys, warnings, nil
}

func (m *Manager) readKeyRef(ctx context.Context, ref string, stdin io.Reader) ([]byte, error) {
	switch {
	case ref == "-":
		if stdin == nil {
			return nil, rlerr.Usagef("--key - was given but there is nothing on stdin")
		}
		b, err := io.ReadAll(io.LimitReader(stdin, int64(m.Cfg.SSH.MaxFetchedKeyBytes)))
		if err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeUsage, "reading the key from stdin")
		}
		return b, nil
	case strings.HasPrefix(ref, "https://"):
		return m.fetchKeys(ctx, ref)
	case strings.HasPrefix(ref, "http://"):
		return nil, rlerr.Usagef("refusing to fetch a key over plain HTTP").
			WithHint("use an https URL, so the key cannot be swapped in transit")
	case looksLikeAKey(ref):
		// The key itself, pasted. Asked for a key and handed a key, taking it is the
		// only sensible reading — and at a prompt, pasting is what everybody does.
		// Treating it as a filename produced "no such file: /root/ssh-ed25519 AAAAC3Nz…
		// ark@ark", which is a dead end dressed as a path.
		//
		// A public key is not a secret, so unlike a password there is no reason to keep
		// it out of argv. It is still parsed and validated exactly like one read from a
		// file — this only decides where the bytes come from.
		return []byte(ref), nil
	default:
		path, err := expandPath(ref)
		if err != nil {
			return nil, err
		}
		b, err := system.ReadFileLimit(path, int64(m.Cfg.SSH.MaxFetchedKeyBytes))
		if err != nil {
			if os.IsNotExist(err) {
				return nil, rlerr.Preconditionf("no such file: %s", path).
					WithHint("public keys usually live in ~/.ssh and end in .pub")
			}
			return nil, err
		}
		return b, nil
	}
}

// fetchKeys retrieves a key list over HTTPS with full certificate verification.
func (m *Manager) fetchKeys(ctx context.Context, url string) ([]byte, error) {
	timeout := m.Cfg.SSH.KeyFetchTimeout.D()
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeUsage, "building a request for %s", url)
	}
	req.Header.Set("User-Agent", "ratline")
	// The default transport verifies certificates against the system roots.
	// A redirect to a different host would defeat the point of naming GitHub, so
	// they are refused rather than followed.
	client := &http.Client{
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) > 0 && r.URL.Host != via[0].URL.Host {
				return fmt.Errorf("refusing a redirect from %s to %s", via[0].URL.Host, r.URL.Host)
			}
			if len(via) > 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	m.Log.Info("fetching keys", "url", url)
	resp, err := client.Do(req)
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeExternal, "fetching %s", url).
			WithHint("check the server's outbound network access and the username you passed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, rlerr.Externalf("%s returned HTTP %d", url, resp.StatusCode).
			WithHint("check the username; a user with no public keys returns an empty list, not an error")
	}
	max := int64(m.Cfg.SSH.MaxFetchedKeyBytes)
	if max <= 0 {
		max = 64 << 10
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max))
	if err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeExternal, "reading the response from %s", url)
	}
	if strings.TrimSpace(string(b)) == "" {
		return nil, rlerr.Preconditionf("%s returned no keys", url).
			WithHint("that account has no public keys published")
	}
	return b, nil
}

// Add installs validated keys into the right file and records them.
func (m *Manager) Add(ctx context.Context, opts AddOptions, keys []*PublicKey) (res *AddResult, err error) {
	if err := validate.Label(opts.Label); err != nil {
		return nil, err
	}
	res = &AddResult{}

	for _, pk := range keys {
		// The same fingerprint in two scopes makes the audit trail ambiguous:
		// a usage record can no longer be attributed to one grant.
		existing, err := m.State.FingerprintLocations(ctx, pk.Fingerprint)
		if err != nil {
			return nil, err
		}
		if len(existing) > 0 && !opts.AllowDuplicate {
			where := make([]string, 0, len(existing))
			for _, e := range existing {
				where = append(where, e.Scope+" → "+e.Target()+" as "+e.Label)
			}
			return nil, rlerr.Preconditionf("this key is already installed: %s", strings.Join(where, "; ")).
				WithHint("use 'ratline key move' to change its scope, or --allow-duplicate if you really want it twice")
		}

		grant := opts.Grant
		key := &state.Key{
			ID:          state.NewKeyID(),
			Label:       opts.Label,
			Fingerprint: pk.Fingerprint,
			Algorithm:   pk.Algorithm,
			Bits:        pk.Bits,
			Blob:        pk.Blob,
			Comment:     pk.Comment,
			Scope:       grant.Scope,
			Owner:       grant.User,
			Site:        grant.Site,
			Source:      sourceOf(opts),
			AllowShell:  grant.AllowShell,
			SFTPOnly:    grant.SFTPOnly,
			FromCIDR:    grant.FromCIDR,
			Command:     grant.CommandPreset,
			AddedAt:     time.Now().UTC(),
			AddedBy:     m.Invoker,
			ExpiresAt:   grant.ExpiresAt,
		}
		key.Options = Options(&grant)
		if err := m.State.PutKey(ctx, key); err != nil {
			return nil, err
		}
		res.Keys = append(res.Keys, key)
	}

	// One render per affected file, from state, so the file always matches the
	// database rather than accumulating appends.
	if err := m.SyncScope(ctx, opts.Grant.Scope, opts.Grant.User); err != nil {
		return nil, err
	}
	if !opts.Grant.ExpiresAt.IsZero() && !opts.Grant.ExpirySupported {
		res.Warnings = append(res.Warnings,
			"this sshd does not support expiry-time=, so the key was installed without it; "+
				"the daily pruning timer will remove it at the expiry instead")
	}
	return res, nil
}

func sourceOf(opts AddOptions) string {
	switch {
	case opts.FromGitHub != "":
		return "github"
	case opts.FromGitLab != "":
		return "gitlab"
	default:
		return "manual"
	}
}

// SyncScope re-renders the authorized_keys file backing one scope.
func (m *Manager) SyncScope(ctx context.Context, scope, owner string) error {
	switch scope {
	case state.ScopeGlobal:
		return m.syncGlobal(ctx)
	default:
		if owner == "" {
			return rlerr.Genericf("internal error: a user is required to sync the %s scope", scope)
		}
		return m.syncUser(ctx, owner)
	}
}

// SyncAll re-renders every authorized_keys file from state. This is `key sync`,
// and it is also how `reconcile --fix` repairs a hand-edited file.
func (m *Manager) SyncAll(ctx context.Context) (int, error) {
	if err := m.syncGlobal(ctx); err != nil {
		return 0, err
	}
	count := 1
	users, err := m.State.ListUsers(ctx)
	if err != nil {
		return count, err
	}
	for _, u := range users {
		if err := m.syncUser(ctx, u.Name); err != nil {
			return count, err
		}
		count++
	}
	if err := m.syncRevoked(ctx); err != nil {
		return count, err
	}
	return count, nil
}

func (m *Manager) syncGlobal(ctx context.Context) error {
	keys, err := m.State.ListKeys(ctx, state.KeyFilter{Scope: state.ScopeGlobal})
	if err != nil {
		return err
	}
	// The canonical copy lives under /etc so that global access does not depend
	// on any tenant's home directory surviving.
	path := m.Cfg.SSH.GlobalKeysFile
	if _, err := system.EnsureDir(filepath.Dir(path), 0o700, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}
	if err := m.renderInto(path, keys, 0o600, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}

	// Then mirrored into the administrator's own file, which is what sshd reads.
	admin := m.Cfg.Server.AdminUser
	if admin == "" {
		m.Log.Debug("no administrator account is configured, so global keys were only written to the canonical file",
			"path", path, "fix", "set server.admin_user in the configuration, or run 'ratline init'")
		return nil
	}
	return m.renderForUser(ctx, admin, keys)
}

func (m *Manager) syncUser(ctx context.Context, owner string) error {
	// A user's file carries both their user-scoped keys and the site-scoped keys
	// for the sites they own: all of them authenticate as that UID, and sshd only
	// reads one file per account.
	userKeys, err := m.State.ListKeys(ctx, state.KeyFilter{Scope: state.ScopeUser, Owner: owner})
	if err != nil {
		return err
	}
	siteKeys, err := m.State.ListKeys(ctx, state.KeyFilter{Scope: state.ScopeSite, Owner: owner})
	if err != nil {
		return err
	}
	return m.renderForUser(ctx, owner, append(userKeys, siteKeys...))
}

func (m *Manager) renderForUser(ctx context.Context, owner string, keys []*state.Key) error {
	u, err := m.State.GetUser(ctx, owner)
	home := m.Cfg.HomeDir(owner)
	uid, gid := system.KeepUnchanged, system.KeepUnchanged
	if err == nil {
		home = u.Home
	}
	if id, lerr := system.LookupIdentity(owner); lerr == nil {
		uid, gid = id.UID, id.GID
	} else if !m.DryRun {
		return rlerr.Wrap(lerr, rlerr.CodePrecondition, "cannot write keys for %s", owner)
	}

	sshDir := filepath.Join(home, ".ssh")
	if _, err := system.EnsureDir(sshDir, 0o700, uid, gid); err != nil {
		return err
	}
	return m.renderInto(filepath.Join(sshDir, "authorized_keys"), keys, 0o600, uid, gid)
}

// renderInto reads the existing file, replaces only the managed block and writes
// it back atomically.
func (m *Manager) renderInto(path string, keys []*state.Key, mode os.FileMode, uid, gid int) error {
	f, err := ReadFile(path, m.Cfg.SSH.MaxAuthKeysBytes)
	if err != nil {
		return err
	}
	data := f.Render(keys)
	if m.DryRun {
		m.Log.Info("would write", "path", path, "managed_keys", len(keys), "preserved_lines", len(f.Before)+len(f.After))
		return nil
	}
	if err := Write(path, data, mode, uid, gid); err != nil {
		return err
	}
	m.Log.Debug("wrote authorized_keys", "path", path, "keys", len(keys), "unmanaged", len(f.Unmanaged))
	return nil
}

func (m *Manager) syncRevoked(ctx context.Context) error {
	keys, err := m.State.ListKeys(ctx, state.KeyFilter{IncludeRevoked: true})
	if err != nil {
		return err
	}
	path := m.Cfg.SSH.RevokedKeys
	if _, err := system.EnsureDir(filepath.Dir(path), 0o700, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return err
	}
	if m.DryRun {
		m.Log.Info("would write the revoked key list", "path", path)
		return nil
	}
	return system.WriteFileAtomic(path, RenderRevoked(keys), 0o644, system.KeepUnchanged, system.KeepUnchanged)
}

// RemoveOptions is the resolved form of `ratline key remove`.
type RemoveOptions struct {
	Needle     string
	Scope      string
	User       string
	Site       string
	Everywhere bool
	Revoke     bool
	Force      bool
}

// Remove deletes matching keys, refusing to remove the last credential that can
// still administer the server.
func (m *Manager) Remove(ctx context.Context, opts RemoveOptions) ([]*state.Key, error) {
	filter := state.KeyFilter{Scope: opts.Scope, Owner: opts.User, Site: opts.Site}
	if opts.Everywhere {
		filter = state.KeyFilter{}
	}
	matches, err := m.State.FindKeys(ctx, opts.Needle, filter)
	if err != nil {
		return nil, err
	}
	if len(matches) > 1 && !opts.Everywhere {
		var where []string
		for _, k := range matches {
			where = append(where, k.ID+" ("+k.Scope+" → "+k.Target()+")")
		}
		return nil, rlerr.Usagef("%q matches %d keys: %s", opts.Needle, len(matches), strings.Join(where, ", ")).
			WithHint("narrow it with --scope, --user or --site, use the key id, or pass --everywhere to remove all of them")
	}

	if err := m.guardLastGlobalKey(ctx, matches, opts.Force); err != nil {
		return nil, err
	}

	scopes := map[string]string{}
	for _, k := range matches {
		if opts.Revoke {
			if err := m.State.RevokeKey(ctx, k.ID); err != nil {
				return nil, err
			}
		} else if err := m.State.DeleteKey(ctx, k.ID); err != nil {
			return nil, err
		}
		scopes[k.Scope+"\x00"+k.Owner] = k.Scope
	}
	for combined, scope := range scopes {
		owner := strings.SplitN(combined, "\x00", 2)[1]
		if err := m.SyncScope(ctx, scope, owner); err != nil {
			return nil, err
		}
	}
	if opts.Revoke {
		if err := m.syncRevoked(ctx); err != nil {
			return nil, err
		}
	}
	return matches, nil
}

// guardLastGlobalKey refuses to remove the last working administrative
// credential.
//
// On a remote server with no console, removing the last global key means no way
// back in. The check names exactly what access would remain so the operator can
// judge whether --force is safe.
func (m *Manager) guardLastGlobalKey(ctx context.Context, removing []*state.Key, force bool) error {
	removingGlobal := 0
	for _, k := range removing {
		if k.Scope == state.ScopeGlobal && k.RevokedAt.IsZero() {
			removingGlobal++
		}
	}
	if removingGlobal == 0 {
		return nil
	}
	total, err := m.State.CountKeysInScope(ctx, state.ScopeGlobal, "", "")
	if err != nil {
		return err
	}
	if total-removingGlobal > 0 {
		return nil
	}
	if force {
		m.Log.Warn("removing the last global key; the only remaining access is a console or a password login")
		return nil
	}
	return rlerr.Preconditionf("that is the last global key, and removing it would leave no key able to administer this server").
		WithHint("if you have console access or another way in, pass --force and confirm; " +
			"otherwise add the replacement key first with 'ratline key add --scope global'")
}

// Prune removes expired keys. Run by the daily timer, and unconditionally safe:
// an expired key is not a credential.
func (m *Manager) Prune(ctx context.Context, at time.Time) ([]*state.Key, error) {
	all, err := m.State.ListKeys(ctx, state.KeyFilter{})
	if err != nil {
		return nil, err
	}
	var pruned []*state.Key
	scopes := map[string]string{}
	for _, k := range all {
		if !k.Expired(at) {
			continue
		}
		if err := m.State.DeleteKey(ctx, k.ID); err != nil {
			return pruned, err
		}
		m.Log.Info("removed an expired key", "label", k.Label, "scope", k.Scope,
			"target", k.Target(), "expired", k.ExpiresAt.Format("2006-01-02"))
		pruned = append(pruned, k)
		scopes[k.Scope+"\x00"+k.Owner] = k.Scope
	}
	for combined, scope := range scopes {
		owner := strings.SplitN(combined, "\x00", 2)[1]
		if err := m.SyncScope(ctx, scope, owner); err != nil {
			return pruned, err
		}
	}
	return pruned, nil
}

func expandPath(ref string) (string, error) {
	if strings.HasPrefix(ref, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			ref = filepath.Join(home, ref[2:])
		}
		// Under sudo, HOME is often still the operator's, which is what they
		// meant by ~. If it is not resolvable, fall through and let the open fail
		// with a clear path.
	}
	abs, err := filepath.Abs(ref)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeUsage, "resolving %s", ref)
	}
	return abs, nil
}

// urlPathSegment keeps a username from escaping the URL path it is interpolated
// into.
func urlPathSegment(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// keyTypePrefixes are the algorithm names an OpenSSH public key line begins with.
//
// Deliberately a prefix test rather than a full parse: this only chooses between "read a
// file at this path" and "these are the bytes", and anything that gets it wrong is caught
// immediately afterwards by Parse, which is the real validator. A filename that begins
// with "ssh-ed25519 " and contains a space is not a thing anybody has.
var keyTypePrefixes = []string{
	"ssh-ed25519 ", "ssh-rsa ", "ssh-dss ",
	"ecdsa-sha2-nistp256 ", "ecdsa-sha2-nistp384 ", "ecdsa-sha2-nistp521 ",
	"sk-ssh-ed25519@openssh.com ", "sk-ecdsa-sha2-nistp256@openssh.com ",
}

// looksLikeAKey reports whether a reference is the key material itself.
func looksLikeAKey(ref string) bool {
	trimmed := strings.TrimSpace(ref)
	for _, p := range keyTypePrefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	// A private key pasted by mistake must reach Parse, which says so in those words,
	// rather than being read as a path and reported as a missing file.
	return strings.HasPrefix(trimmed, "-----BEGIN")
}
