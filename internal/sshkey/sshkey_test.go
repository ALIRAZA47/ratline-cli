package sshkey

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

// Real public keys, generated for this test suite. Public halves only: there is
// nothing secret here, and using real keys means the parser is exercised against
// what OpenSSH actually emits.
const (
	ed25519Key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIH0hVJKcMwn9dxKGvWkFvJRWLzZ7wYZ0GmL2p3vXnQxT ali@macbook"
	rsa4096Key = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQDLmVEnkT0aG0Zx+fRV0aQ0nqLXQBhTBRXVJmZzMCLpZm5vN9F5oQvRxG7yqTZLmVJbNwUZ8YlXQxRZ3rKlP7QcMvbNZ0tXH8VmQpLZfRJdKnMxYwTZbGvHqLmNpQrStUvWxYzA1B2C3D4E5F6G7H8I9J0KaLbMcNdOePfQgRhSiTjUkVlWmXnYoZp1q2r3s4t5u6v7w8x9y0zAaBbCcDdEeFfGgHhIiJjKkLlMmNnOoPpQqRrSsTtUuVvWwXxYyZz0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789+/AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGhIjKlMnOpQrSt ci@runner"
)

func testPolicy() Policy {
	c := config.Default()
	return Policy{
		MinRSABits:         c.SSH.MinRSABits,
		WarnRSABits:        c.SSH.WarnRSABits,
		AllowedAlgorithms:  c.SSH.AllowedAlgorithms,
		RejectedAlgorithms: c.SSH.RejectedAlgorithms,
		MaxLineBytes:       c.SSH.MaxKeyLineBytes,
	}
}

func TestParseEd25519(t *testing.T) {
	key, warnings, err := Parse(ed25519Key, testPolicy())
	if err != nil {
		t.Fatalf("Parse = %v", err)
	}
	if key.Algorithm != "ssh-ed25519" {
		t.Errorf("algorithm = %q", key.Algorithm)
	}
	if key.Comment != "ali@macbook" {
		t.Errorf("comment = %q", key.Comment)
	}
	if !strings.HasPrefix(key.Fingerprint, "SHA256:") {
		t.Errorf("fingerprint = %q, want a SHA256 form", key.Fingerprint)
	}
	if key.Bits != 256 {
		t.Errorf("bits = %d, want 256", key.Bits)
	}
	// The preferred algorithm should produce no nagging.
	for _, w := range warnings {
		if strings.Contains(string(w), "ed25519 is preferred") {
			t.Errorf("an ed25519 key produced %q", w)
		}
	}
}

// A pasted key carrying its own options is an escalation vector: command= or
// permitopen= from an untrusted source would be honoured verbatim.
func TestParseStripsSubmittedOptions(t *testing.T) {
	line := `command="/bin/bash",permitopen="10.0.0.1:5432",no-pty ` + ed25519Key
	key, warnings, err := Parse(line, testPolicy())
	if err != nil {
		t.Fatalf("Parse = %v", err)
	}
	if len(key.StrippedOptions) == 0 {
		t.Fatal("the submitted options were not recorded")
	}
	// The rendered line must carry none of them.
	rendered := key.Line()
	for _, bad := range []string{"command=", "permitopen=", "no-pty"} {
		if strings.Contains(rendered, bad) {
			t.Errorf("the rendered key still carries %q: %s", bad, rendered)
		}
	}
	// And the operator has to be told, or they would assume their restrictions
	// were applied.
	found := false
	for _, w := range warnings {
		if strings.Contains(string(w), "discarded") {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning about the discarded options: %v", warnings)
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"":                                    "empty",
		"not a key at all":                    "unparseable",
		"ssh-ed25519":                         "no blob",
		"ssh-ed25519 !!!!":                    "invalid base64",
		ed25519Key + "\nssh-ed25519 AAAA x":   "two keys on one line",
		"-----BEGIN OPENSSH PRIVATE KEY-----": "a private key",
		ed25519Key + "\x00":                   "NUL byte",
		strings.Repeat("A", 9000):             "over the line limit",
	}
	for input, why := range cases {
		if _, _, err := Parse(input, testPolicy()); err == nil {
			t.Errorf("Parse accepted %s", why)
		}
	}
}

func TestParseRefusesDSA(t *testing.T) {
	// The algorithm name is enough: ssh-dss is fixed at 1024 bits.
	_, _, err := Parse("ssh-dss AAAAB3NzaC1kc3MAAACBAJ x", testPolicy())
	if err == nil {
		t.Fatal("Parse accepted a DSA key")
	}
}

func TestParseManyReportsPerLineProblems(t *testing.T) {
	input := ed25519Key + "\n# a comment\n\ngarbage line here\n"
	keys, warnings, err := ParseMany([]byte(input), testPolicy())
	if err != nil {
		t.Fatalf("ParseMany = %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("parsed %d keys, want 1", len(keys))
	}
	// One bad entry must not hide the good ones, but it must be reported.
	found := false
	for _, w := range warnings {
		if strings.Contains(string(w), "line 4") {
			found = true
		}
	}
	if !found {
		t.Errorf("the bad line was not reported: %v", warnings)
	}
}

func TestOptionsStartFromRestrict(t *testing.T) {
	cases := map[string]struct {
		grant Grant
		want  []string
		deny  []string
	}{
		"global gets a pty and agent forwarding": {
			grant: Grant{Scope: state.ScopeGlobal},
			want:  []string{"restrict", "pty", "agent-forwarding"},
			deny:  []string{"command="},
		},
		"user gets a pty but no agent forwarding": {
			grant: Grant{Scope: state.ScopeUser, User: "alice"},
			want:  []string{"restrict", "pty"},
			deny:  []string{"agent-forwarding", "command="},
		},
		"site gets a forced command and no pty": {
			grant: Grant{Scope: state.ScopeSite, Site: "example.com", User: "alice",
				ShellWrapper: "/usr/local/lib/ratline/ratline-shell"},
			want: []string{"restrict", `command="/usr/local/lib/ratline/ratline-shell --site example.com"`},
			deny: []string{"pty", "agent-forwarding"},
		},
		"site with allow-shell gets a pty": {
			grant: Grant{Scope: state.ScopeSite, Site: "example.com", User: "alice", AllowShell: true,
				ShellWrapper: "/usr/local/lib/ratline/ratline-shell"},
			want: []string{"restrict", "pty", "--allow-shell"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := Options(&tc.grant)
			// restrict must come first: it is the deny-all that the rest opts out of.
			if !strings.HasPrefix(got, "restrict") {
				t.Errorf("options do not start with restrict: %s", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("options %q are missing %q", got, w)
				}
			}
			for _, d := range tc.deny {
				if strings.Contains(got, d) {
					t.Errorf("options %q should not contain %q", got, d)
				}
			}
		})
	}
}

func TestOptionsSourceAndExpiry(t *testing.T) {
	expires := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
	g := Grant{
		Scope: state.ScopeUser, User: "alice",
		FromCIDR:  []string{"203.0.113.0/24", "198.51.100.7/32"},
		ExpiresAt: expires, ExpirySupported: true,
	}
	got := Options(&g)
	if !strings.Contains(got, `from="203.0.113.0/24,198.51.100.7/32"`) {
		t.Errorf("the source restriction is missing: %s", got)
	}
	if !strings.Contains(got, `expiry-time="20270101120000"`) {
		t.Errorf("the expiry option is missing or malformed: %s", got)
	}

	// On an sshd that does not understand expiry-time, emitting it would be a
	// parse error that breaks the whole file.
	g.ExpirySupported = false
	if strings.Contains(Options(&g), "expiry-time") {
		t.Error("expiry-time was emitted for an sshd that does not support it")
	}
}

func TestResolveScopeValidation(t *testing.T) {
	site := &state.Site{Domain: "example.com", Owner: "alice"}
	cases := map[string]struct {
		grant   Grant
		site    *state.Site
		wantErr bool
	}{
		"global with no target":     {Grant{Scope: state.ScopeGlobal}, nil, false},
		"global with a user":        {Grant{Scope: state.ScopeGlobal, User: "alice"}, nil, true},
		"user without a user":       {Grant{Scope: state.ScopeUser}, nil, true},
		"user with a site":          {Grant{Scope: state.ScopeUser, User: "alice", Site: "x.com"}, nil, true},
		"site without a site":       {Grant{Scope: state.ScopeSite}, nil, true},
		"site resolves its owner":   {Grant{Scope: state.ScopeSite, Site: "example.com"}, site, false},
		"site with the wrong owner": {Grant{Scope: state.ScopeSite, Site: "example.com", User: "bob"}, site, true},
		"allow-shell outside site":  {Grant{Scope: state.ScopeUser, User: "alice", AllowShell: true}, nil, true},
		"unknown scope":             {Grant{Scope: "root"}, nil, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g := tc.grant
			err := ResolveScope(&g, tc.site)
			if tc.wantErr && err == nil {
				t.Error("expected an error")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tc.wantErr && tc.grant.Scope == state.ScopeSite {
				// The owner is taken from the site rather than trusted from a flag.
				if g.User != "alice" {
					t.Errorf("the site's owner was not resolved: %q", g.User)
				}
			}
		})
	}
}

// The managed block is the contract with the operator: everything inside is
// ratline's, everything outside is theirs and survives untouched.
func TestManagedBlockPreservesHandWrittenKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "authorized_keys")
	original := "# my own key, added by hand\n" + ed25519Key + "\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := ReadFile(path, 1<<20)
	if err != nil {
		t.Fatalf("ReadFile = %v", err)
	}
	if len(f.Unmanaged) != 1 {
		t.Fatalf("found %d unmanaged keys, want 1", len(f.Unmanaged))
	}
	if f.Unmanaged[0].Fingerprint == "" {
		t.Error("the unmanaged key's fingerprint was not computed, so audit cannot identify it")
	}

	managed := []*state.Key{{
		ID: "k_7f3a", Label: "Deploy CI", Fingerprint: "SHA256:x", Algorithm: "ssh-ed25519",
		Blob: "AAAAC3Nz", Comment: "ci@runner", Scope: state.ScopeSite, Site: "example.com",
		Options: `restrict,command="/usr/local/lib/ratline/ratline-shell --site example.com"`,
		AddedAt: time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC), AddedBy: "ali",
	}}
	rendered := string(f.Render(managed))

	// The operator's content is byte-for-byte intact.
	if !strings.Contains(rendered, "# my own key, added by hand") {
		t.Error("the hand-written comment was lost")
	}
	if !strings.Contains(rendered, ed25519Key) {
		t.Error("the hand-written key was lost")
	}
	// And the managed block is complete and traceable.
	for _, want := range []string{
		BlockBegin, BlockEnd,
		`# ratline id=k_7f3a label="Deploy CI" scope=site site=example.com added=2026-08-04 by=ali`,
		`restrict,command="/usr/local/lib/ratline/ratline-shell --site example.com" ssh-ed25519 AAAAC3Nz ci@runner`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendered file is missing %q:\n%s", want, rendered)
		}
	}

	// Re-reading and re-rendering must be stable, or every sync would churn.
	if err := os.WriteFile(path, []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}
	f2, err := ReadFile(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(f2.Render(managed)); got != rendered {
		t.Errorf("rendering is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", rendered, got)
	}
	if len(f2.Unmanaged) != 1 {
		t.Errorf("after a round trip there are %d unmanaged keys, want 1", len(f2.Unmanaged))
	}
}

func TestReadFileOnAMissingFile(t *testing.T) {
	f, err := ReadFile(filepath.Join(t.TempDir(), "absent"), 1<<20)
	if err != nil {
		t.Fatalf("ReadFile on a missing file = %v", err)
	}
	if f.HadBlock || len(f.Unmanaged) != 0 {
		t.Errorf("unexpected content: %+v", f)
	}
}

func TestCheckPermissionsCatchesTheModesSSHDRefuses(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home", "alice")
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o777); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(sshDir, "authorized_keys")
	if err := os.WriteFile(keyFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sshDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyFile, 0o666); err != nil {
		t.Fatal(err)
	}

	problems := CheckPermissions(home, keyFile)
	// sshd silently ignores an over-permissive key file, which is a genuinely
	// baffling failure if nothing points it out.
	if len(problems) < 2 {
		t.Errorf("found %d problems, want the .ssh directory and the key file: %v", len(problems), problems)
	}
}

func TestRenderDropInNeverTouchesTheDangerousDirectives(t *testing.T) {
	users := []*state.User{
		{Name: "alice"},
		{Name: "contractor", SFTPOnly: true},
	}
	out := string(RenderDropIn(users, "/etc/ratline/ssh/revoked_keys"))

	if !strings.Contains(out, "RevokedKeys /etc/ratline/ssh/revoked_keys") {
		t.Error("the revoked key list is not wired in")
	}
	if !strings.Contains(out, "Match User contractor") {
		t.Errorf("no Match block for the SFTP-only user:\n%s", out)
	}
	if strings.Contains(out, "Match User alice") {
		t.Error("a shell user was given a Match block")
	}
	if !strings.Contains(out, "ForceCommand internal-sftp") || !strings.Contains(out, "ChrootDirectory %h") {
		t.Error("the SFTP confinement is incomplete")
	}
	// Changing any of these on a remote server can end the operator's session,
	// so ratline never sets them.
	for _, forbidden := range []string{"PermitRootLogin", "PasswordAuthentication", "AllowUsers", "\nPort "} {
		for _, line := range strings.Split(out, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.HasPrefix(trimmed, strings.TrimSpace(forbidden)) {
				t.Errorf("the drop-in sets %s, which ratline must never touch: %q", forbidden, line)
			}
		}
	}
}

func TestParseEffectiveConfig(t *testing.T) {
	out := "port 22\npubkeyauthentication yes\nauthorizedkeysfile .ssh/authorized_keys\npermitrootlogin no\n"
	m := parseEffectiveConfig(out)
	if m["port"] != "22" || m["pubkeyauthentication"] != "yes" {
		t.Errorf("parsed = %v", m)
	}
	if !mentionsAuthorizedKeys(m["authorizedkeysfile"]) {
		t.Error("the authorized_keys path was not recognised")
	}
}

func TestParseOpenSSHVersion(t *testing.T) {
	// expiry-time= arrived in 8.2, so the boundary matters.
	cases := map[string]bool{
		"OpenSSH_8.9p1 Ubuntu-3ubuntu0.4": true,
		"OpenSSH_9.6p1":                   true,
		"OpenSSH_8.2p1":                   true,
		"OpenSSH_8.1p1":                   false,
		"OpenSSH_7.9p1":                   false,
	}
	for banner, wantSupported := range cases {
		major, minor, ok := parseOpenSSHVersion(banner)
		if !ok {
			t.Errorf("could not parse %q", banner)
			continue
		}
		supported := major > 8 || (major == 8 && minor >= 2)
		if supported != wantSupported {
			t.Errorf("%q: supported = %v, want %v", banner, supported, wantSupported)
		}
	}
	if _, _, ok := parseOpenSSHVersion("no version here"); ok {
		t.Error("parsed a version out of nothing")
	}
}

func TestDescribeIsHonestAboutSiteScope(t *testing.T) {
	now := time.Now()
	confined := &state.Key{
		Label: "Deploy CI", Fingerprint: "SHA256:x9K", Algorithm: "ssh-ed25519",
		Scope: state.ScopeSite, Site: "example.com", Owner: "alice",
		FromCIDR: []string{"203.0.113.0/24"}, ExpiresAt: now.Add(149 * 24 * time.Hour),
		LastUsedAt: now.Add(-2 * 24 * time.Hour), LastUsedIP: "203.0.113.19",
	}
	c := Describe(confined, "/home/alice/example.com", now)

	if !strings.Contains(c.Login, "no interactive shell") {
		t.Errorf("login = %q", c.Login)
	}
	if !strings.Contains(c.ConfinedTo, "/home/alice/example.com") {
		t.Errorf("confined to = %q", c.ConfinedTo)
	}
	// The honesty requirement: this is not a kernel boundary, and the tool says so
	// every time it is asked.
	if !strings.Contains(c.Note, "Not a kernel boundary") {
		t.Errorf("the note does not state the limit of the confinement: %q", c.Note)
	}
	if !strings.Contains(c.Expires, "149 days") {
		t.Errorf("expires = %q", c.Expires)
	}
	if !strings.Contains(c.LastUse, "203.0.113.19") {
		t.Errorf("last use = %q", c.LastUse)
	}

	// --allow-shell must be described as removing most of the confinement.
	confined.AllowShell = true
	c = Describe(confined, "/home/alice/example.com", now)
	if !strings.Contains(c.Note, "allow-shell") || !strings.Contains(c.Note, "removes most") {
		t.Errorf("the note does not warn about --allow-shell: %q", c.Note)
	}
}

func TestAcceptedPublicKeyLineParsing(t *testing.T) {
	line := "2026-08-02T14:11:03+0000 host sshd[123]: Accepted publickey for alice from 203.0.113.19 port 54321 ssh2: ED25519 SHA256:x9KabcDEF"
	m := acceptedRe.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("the accepted-publickey line did not match:\n%s", line)
	}
	if m[2] != "alice" || m[3] != "203.0.113.19" || m[4] != "SHA256:x9KabcDEF" {
		t.Errorf("captured %q", m[1:])
	}
	at := parseLogTime(line, time.Now())
	if at.Year() != 2026 || at.Month() != 8 || at.Day() != 2 {
		t.Errorf("parsed time = %v", at)
	}
}

func TestRenderRevokedIncludesOnlyRevokedKeys(t *testing.T) {
	keys := []*state.Key{
		{Label: "live", Algorithm: "ssh-ed25519", Blob: "AAAA"},
		{Label: "gone", Algorithm: "ssh-ed25519", Blob: "BBBB", RevokedAt: time.Now()},
	}
	out := string(RenderRevoked(keys))
	if strings.Contains(out, "AAAA") {
		t.Error("a live key was written to the revoked list")
	}
	if !strings.Contains(out, "BBBB") {
		t.Error("the revoked key is missing from the revoked list")
	}
}

func TestRenderDropInStrictIsolation(t *testing.T) {
	users := []*state.User{{Name: "alice"}}
	strict := []StrictSite{{Owner: "alice", Domain: "example.com", Dir: "/var/lib/ratline/chroot/alice-example_com"}}
	out := string(RenderDropInStrict(users, "/etc/ratline/ssh/revoked_keys", strict))

	if !strings.Contains(out, "ChrootDirectory /var/lib/ratline/chroot/alice-example_com") {
		t.Errorf("no chroot block:\n%s", out)
	}
	// The chroot target must not be the tenant's home: sshd requires every
	// component to be root-owned and not group-writable, which a home is not.
	if strings.Contains(out, "ChrootDirectory /home/") {
		t.Error("the chroot points at a home directory, which sshd will refuse")
	}
	if !strings.Contains(out, "ForceCommand internal-sftp -d /site") {
		t.Error("the forced command is missing from the chroot block")
	}
	// Off by default: the plain renderer must produce no chroot at all.
	plain := string(RenderDropIn(users, "/etc/ratline/ssh/revoked_keys"))
	if strings.Contains(plain, "ChrootDirectory") {
		t.Error("strict isolation leaked into the default drop-in")
	}
}
