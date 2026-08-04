package validate

import (
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := []struct {
		user, domain, want string
	}{
		// The form the spec's example unit name uses.
		{"alice", "example.com", "alice-example_com"},
		{"bob", "app.example.co.uk", "bob-app_example_co_uk"},
		{"Alice", "EXAMPLE.COM", "alice-example_com"},
		{"acme-web", "api.example.com", "acme-web-api_example_com"},
	}
	for _, tc := range cases {
		if got := Slug(tc.user, tc.domain); got != tc.want {
			t.Errorf("Slug(%q, %q) = %q, want %q", tc.user, tc.domain, got, tc.want)
		}
	}
}

func TestUnitName(t *testing.T) {
	if got, want := UnitName("alice", "example.com"), "ratline-alice-example_com.service"; got != want {
		t.Errorf("UnitName = %q, want %q", got, want)
	}
	if err := SystemdUnitName(UnitName("alice", "example.com")); err != nil {
		t.Errorf("the generated unit name is not valid: %v", err)
	}
	if got, want := InstanceUnitName("alice", "example.com", 2), "ratline-alice-example_com@2.service"; got != want {
		t.Errorf("InstanceUnitName = %q, want %q", got, want)
	}
}

func TestSlugTruncatesWithADigest(t *testing.T) {
	long := strings.Repeat("very-long-subdomain.", 8) + "example.com"
	a := Slug("tenant", long)
	if len(a) > MaxSlugLen {
		t.Fatalf("Slug returned %d characters, over the %d limit: %q", len(a), MaxSlugLen, a)
	}
	// Two long names that share a truncation prefix must not collide, or two
	// sites would fight over one systemd unit.
	b := Slug("tenant", strings.Repeat("very-long-subdomain.", 8)+"example.net")
	if a == b {
		t.Fatalf("two long domains collided on the slug %q", a)
	}
}

func TestSlugSocketPathFitsInSunPath(t *testing.T) {
	// sockaddr_un.sun_path is 108 bytes on Linux; a longer path fails to bind
	// with an error that says nothing useful about the cause.
	const prefix = "/run/ratline/"
	const suffix = "/app.sock"
	worst := prefix + strings.Repeat("x", MaxSlugLen) + suffix
	if len(worst) >= 108 {
		t.Fatalf("worst-case socket path is %d bytes, which does not fit in sun_path", len(worst))
	}
}

func TestSlugIsAlwaysSafe(t *testing.T) {
	for _, in := range []string{"", "  ", "...", "---", "a b/c", "$(id)", "../x", "ünïcode", strings.Repeat("a.", 200)} {
		got := SlugFor(in)
		if got == "" {
			t.Fatalf("SlugFor(%q) returned an empty slug", in)
		}
		for _, r := range got {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' {
				t.Fatalf("SlugFor(%q) = %q, which contains %q", in, got, r)
			}
		}
		if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
			t.Fatalf("SlugFor(%q) = %q, which starts or ends with a hyphen", in, got)
		}
		if len(got) > MaxSlugLen {
			t.Fatalf("SlugFor(%q) returned %d characters", in, len(got))
		}
	}
}

func FuzzSlug(f *testing.F) {
	for _, seed := range []string{"alice", "example.com", "", "..", "$(id)", strings.Repeat("x", 200)} {
		f.Add(seed, seed)
	}
	f.Fuzz(func(t *testing.T, user, domain string) {
		got := Slug(user, domain)
		if got == "" || len(got) > MaxSlugLen {
			t.Fatalf("Slug(%q, %q) = %q (%d characters)", user, domain, got, len(got))
		}
		if err := SystemdUnitName("ratline-" + got + ".service"); err != nil {
			t.Fatalf("Slug(%q, %q) = %q, which is not a valid unit name: %v", user, domain, got, err)
		}
	})
}
