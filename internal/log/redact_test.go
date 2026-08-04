package log

import (
	"strings"
	"testing"
)

func TestArgvRedaction(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "environment values are always secrets",
			in:   []string{"ratline", "site", "env", "set", "example.com", "DATABASE_URL=postgres://u:pw@h/db", "PORT=3000"},
			want: []string{"ratline", "site", "env", "set", "example.com", "DATABASE_URL=" + Redacted, "PORT=" + Redacted},
		},
		{
			name: "a secret-looking flag with an inline value",
			in:   []string{"ratline", "user", "password", "set", "alice", "--password=hunter2"},
			want: []string{"ratline", "user", "password", "set", "alice", "--password=" + Redacted},
		},
		{
			name: "a secret-looking flag with a separate value",
			in:   []string{"ratline", "x", "--api-token", "abc123", "--verbose"},
			want: []string{"ratline", "x", "--api-token", Redacted, "--verbose"},
		},
		{
			name: "paths that only look like secrets stay legible",
			in:   []string{"ratline", "cert", "issue", "example.com", "--dns-credentials", "/etc/ratline/dns/cloudflare.ini"},
			want: []string{"ratline", "cert", "issue", "example.com", "--dns-credentials", "/etc/ratline/dns/cloudflare.ini"},
		},
		{
			name: "public key paths stay legible",
			in:   []string{"ratline", "key", "add", "--key", "/home/ali/.ssh/id_ed25519.pub", "--label", "Ali MacBook"},
			want: []string{"ratline", "key", "add", "--key", "/home/ali/.ssh/id_ed25519.pub", "--label", "Ali MacBook"},
		},
		{
			name: "ordinary arguments are untouched",
			in:   []string{"ratline", "site", "add", "example.com", "--user", "alice", "--runtime", "python"},
			want: []string{"ratline", "site", "add", "example.com", "--user", "alice", "--runtime", "python"},
		},
		{
			name: "a domain is not mistaken for an assignment",
			in:   []string{"ratline", "cert", "issue", "example.com"},
			want: []string{"ratline", "cert", "issue", "example.com"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Argv(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("Argv = %q, want %q", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("Argv = %q, want %q", got, tc.want)
				}
			}
		})
	}
}

func TestIsSecretName(t *testing.T) {
	for _, secret := range []string{"password", "--password", "PASSPHRASE", "api-key", "api_key", "token", "client_secret", "AWS_SECRET_ACCESS_KEY", "session", "webhook-url"} {
		if !IsSecretName(secret) {
			t.Errorf("IsSecretName(%q) = false, want true", secret)
		}
	}
	for _, safe := range []string{"user", "runtime", "domain", "email", "config", "dns-credentials", "key", "cert", "chain", "workers", "from-github"} {
		if IsSecretName(safe) {
			t.Errorf("IsSecretName(%q) = true, want false", safe)
		}
	}
}

func TestArgvStringIsRedacted(t *testing.T) {
	got := ArgvString([]string{"ratline", "site", "env", "set", "x", "SECRET_KEY=abcdef"})
	if strings.Contains(got, "abcdef") {
		t.Errorf("ArgvString leaked a value: %q", got)
	}
	if !strings.Contains(got, "SECRET_KEY=") {
		t.Errorf("ArgvString lost the variable name: %q", got)
	}
}

func FuzzArgv(f *testing.F) {
	f.Add("--password", "hunter2")
	f.Add("KEY", "value")
	f.Add("--user", "alice")
	f.Fuzz(func(t *testing.T, a, b string) {
		got := Argv([]string{a, b})
		if len(got) != 2 {
			t.Fatalf("Argv changed the argument count: %q", got)
		}
		// Whatever the input, an assignment's value never survives.
		if i := strings.IndexByte(a, '='); i > 0 && !strings.HasPrefix(a, "-") {
			name := a[:i]
			if envAssignRe.MatchString(a) && got[0] != name+"="+Redacted {
				t.Fatalf("Argv(%q) = %q, want the value redacted", a, got[0])
			}
		}
	})
}
