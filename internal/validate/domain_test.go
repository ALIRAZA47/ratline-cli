package validate

import (
	"strings"
	"testing"
)

func TestDomainValid(t *testing.T) {
	cases := map[string]string{
		"example.com":         "example.com",
		"EXAMPLE.COM":         "example.com",
		"  example.com  ":     "example.com",
		"example.com.":        "example.com",
		"api.example.com":     "api.example.com",
		"a.b.c.d.example.com": "a.b.c.d.example.com",
		"my--site.com":        "my--site.com", // CheckHyphens off, deliberately
		"x1.example.co.uk":    "x1.example.co.uk",
		"пример.рф":           "xn--e1afmkfd.xn--p1ai",
		"münchen.de":          "xn--mnchen-3ya.de",
		"foo.github.io":       "foo.github.io", // private suffix, still valid
	}
	for in, want := range cases {
		got, err := Domain(in)
		if err != nil {
			t.Errorf("Domain(%q) = error %v, want %q", in, err, want)
			continue
		}
		if got != want {
			t.Errorf("Domain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDomainInvalid(t *testing.T) {
	longLabel := strings.Repeat("a", 64) + ".com"
	longName := strings.Repeat("a.", 130) + "com"

	invalid := map[string]string{
		"":                        "empty",
		"example":                 "single label",
		"com":                     "public suffix alone",
		"co.uk":                   "multi-label public suffix alone",
		".example.com":            "leading dot",
		"example..com":            "empty label",
		"-example.com":            "label starts with a hyphen",
		"example-.com":            "label ends with a hyphen",
		"exa mple.com":            "space",
		"example.com/../etc":      "path traversal",
		"example.com;reboot":      "command separator",
		"$(id).com":               "command substitution",
		"`id`.com":                "backticks",
		"example.com\nfoo":        "newline",
		"example.com\x00":         "NUL byte",
		"example.com:8080":        "port",
		"http://example.com":      "scheme",
		"192.168.0.1":             "IPv4 address",
		"under_score.com":         "underscore",
		"*.example.com":           "wildcard rejected by Domain",
		"exam*ple.com":            "asterisk",
		longLabel:                 "label over 63 characters",
		longName:                  "name over 253 characters",
		"example.com\\..\\etc":    "backslash",
		"'; DROP TABLE sites; --": "sql injection payload",
	}
	for in, why := range invalid {
		if got, err := Domain(in); err == nil {
			t.Errorf("Domain(%q) = %q, want an error (%s)", in, got, why)
		}
	}
}

func TestDomainOrWildcard(t *testing.T) {
	if got, err := DomainOrWildcard("*.example.com"); err != nil || got != "*.example.com" {
		t.Errorf("DomainOrWildcard(*.example.com) = %q, %v", got, err)
	}
	if got, err := DomainOrWildcard("Example.COM"); err != nil || got != "example.com" {
		t.Errorf("DomainOrWildcard(Example.COM) = %q, %v", got, err)
	}
	for _, bad := range []string{"*.*.example.com", "a*.example.com", "*.com", "*.", "*"} {
		if got, err := DomainOrWildcard(bad); err == nil {
			t.Errorf("DomainOrWildcard(%q) = %q, want an error", bad, got)
		}
	}
}

func TestRegisteredDomain(t *testing.T) {
	cases := map[string]string{
		"example.com":           "example.com",
		"api.example.com":       "example.com",
		"a.b.example.co.uk":     "example.co.uk",
		"*.example.com":         "example.com",
		"deep.nest.example.org": "example.org",
	}
	for in, want := range cases {
		got, err := RegisteredDomain(in)
		if err != nil {
			t.Errorf("RegisteredDomain(%q) = error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("RegisteredDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAliasesDeduplicates(t *testing.T) {
	got, err := Aliases("example.com", []string{"www.example.com", "WWW.example.com", "example.com", "cdn.example.com"})
	if err != nil {
		t.Fatalf("Aliases returned %v", err)
	}
	want := []string{"www.example.com", "cdn.example.com"}
	if len(got) != len(want) {
		t.Fatalf("Aliases = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Aliases = %v, want %v", got, want)
		}
	}
}

// FuzzDomain asserts that whatever Domain accepts is safe to write into an nginx
// server_name, a certbot argument and a filesystem path.
func FuzzDomain(f *testing.F) {
	for _, seed := range []string{
		"example.com", "", "a.b", "xn--e1afmkfd.xn--p1ai", "-a.com", "a..b",
		"example.com;reboot", "example.com/../x", strings.Repeat("a", 300),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out, err := Domain(in)
		if err != nil {
			return
		}
		if out == "" {
			t.Fatal("accepted a domain but returned an empty string")
		}
		if len(out) > MaxDomainLen {
			t.Fatalf("returned %d bytes: %q", len(out), out)
		}
		if strings.ToLower(out) != out {
			t.Fatalf("returned a name that is not lowercase: %q", out)
		}
		if strings.ContainsAny(out, domainForbidden) || strings.ContainsAny(out, "\x00\n\r\t") {
			t.Fatalf("returned an unsafe name: %q", out)
		}
		if strings.Contains(out, "..") || strings.HasPrefix(out, ".") || strings.HasSuffix(out, ".") {
			t.Fatalf("returned a malformed name: %q", out)
		}
		if len(strings.Split(out, ".")) < 2 {
			t.Fatalf("returned a single-label name: %q", out)
		}
		// The output must be a fixed point: normalising twice changes nothing.
		again, err := Domain(out)
		if err != nil || again != out {
			t.Fatalf("Domain is not idempotent: Domain(%q) = %q, %v", out, again, err)
		}
	})
}
