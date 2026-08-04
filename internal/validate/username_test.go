package validate

import (
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

func TestUsername(t *testing.T) {
	valid := []string{"alice", "bob", "a", "_svc", "acme-web", "web_1", "x0", strings.Repeat("a", 32)}
	for _, name := range valid {
		if err := Username(name); err != nil {
			t.Errorf("Username(%q) = %v, want nil", name, err)
		}
	}

	// Each of these has bitten a real provisioning tool at some point.
	invalid := map[string]string{
		"":                       "empty",
		"Alice":                  "uppercase",
		"1alice":                 "leading digit",
		"alice-":                 "trailing hyphen",
		"-alice":                 "leading hyphen",
		"al ice":                 "space",
		"al.ice":                 "dot",
		"al/ice":                 "slash",
		"../etc/passwd":          "path traversal",
		"alice;reboot":           "command separator",
		"$(id)":                  "command substitution",
		"`id`":                   "backticks",
		"alice\nbob":             "newline",
		"alice\x00":              "NUL byte",
		"аlice":                  "cyrillic homoglyph",
		"root:x:0:0":             "passwd line",
		strings.Repeat("a", 33):  "too long",
		strings.Repeat("a", 500): "far too long",
		"_":                      "no alphanumeric character",
		"__":                     "only underscores",
	}
	for name, why := range invalid {
		err := Username(name)
		if err == nil {
			t.Errorf("Username(%q) = nil, want an error (%s)", name, why)
			continue
		}
		if !rlerr.Is(err, rlerr.CodeUsage) {
			t.Errorf("Username(%q) returned code %v, want usage", name, rlerr.CodeOf(err))
		}
	}
}

func TestUsernameAvailable(t *testing.T) {
	policy := UserPolicy{
		Reserved:    []string{"tenant0"},
		UserExists:  func(n string) bool { return n == "existing" },
		GroupExists: func(n string) bool { return n == "somegroup" },
	}

	if err := UsernameAvailable("fresh", policy); err != nil {
		t.Fatalf("UsernameAvailable(fresh) = %v, want nil", err)
	}
	for _, tc := range []struct{ name, why string }{
		{"root", "built-in reserved"},
		{"www-data", "built-in reserved"},
		{"ratline", "our own name"},
		{"tenant0", "reserved by config"},
		{"existing", "already in /etc/passwd"},
		{"somegroup", "group name collision"},
	} {
		err := UsernameAvailable(tc.name, policy)
		if err == nil {
			t.Errorf("UsernameAvailable(%q) = nil, want an error (%s)", tc.name, tc.why)
			continue
		}
		if !rlerr.Is(err, rlerr.CodePrecondition) {
			t.Errorf("UsernameAvailable(%q) returned code %v, want precondition", tc.name, rlerr.CodeOf(err))
		}
	}
}

// FuzzUsername asserts the validator never panics and never accepts a name that
// would be unsafe in /etc/passwd, a path, or a systemd unit.
func FuzzUsername(f *testing.F) {
	for _, seed := range []string{"alice", "", "_x", "A", "a-", "../x", "a;b", "ünïcode", strings.Repeat("z", 40)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		err := Username(name)
		if err != nil {
			return
		}
		if len(name) == 0 || len(name) > MaxUsernameLen {
			t.Fatalf("accepted a name of length %d: %q", len(name), name)
		}
		for _, r := range name {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' {
				t.Fatalf("accepted %q, which contains %q", name, r)
			}
		}
		if strings.HasSuffix(name, "-") {
			t.Fatalf("accepted a name ending in a hyphen: %q", name)
		}
		if c := name[0]; c >= '0' && c <= '9' {
			t.Fatalf("accepted a name starting with a digit: %q", name)
		}
	})
}
