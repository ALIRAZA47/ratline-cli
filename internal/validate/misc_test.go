package validate

import (
	"strings"
	"testing"
	"time"
)

func TestLabel(t *testing.T) {
	for _, ok := range []string{"Ali MacBook", "CI runner", "office desktop 2", "clé-usb", "a"} {
		if err := Label(ok); err != nil {
			t.Errorf("Label(%q) = %v, want nil", ok, err)
		}
	}
	// Quotes are refused rather than escaped: the label is rendered inside a
	// label="..." comment in authorized_keys.
	for _, bad := range []string{"", "   ", `say "hi"`, `back\slash`, "line\nbreak", "nul\x00", strings.Repeat("x", 65), "\x07bell"} {
		if err := Label(bad); err == nil {
			t.Errorf("Label(%q) = nil, want an error", bad)
		}
	}
}

func TestEmail(t *testing.T) {
	for _, ok := range []string{"admin@example.com", "a.b+c@sub.example.co.uk", "ops@example.io"} {
		if err := Email(ok); err != nil {
			t.Errorf("Email(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "admin", "admin@", "@example.com", "admin@example", "a b@example.com",
		"admin@example.com\nBcc: x@y.z", "admin@example.com;id", "<admin@example.com>", strings.Repeat("a", 250) + "@example.com"} {
		if err := Email(bad); err == nil {
			t.Errorf("Email(%q) = nil, want an error", bad)
		}
	}
}

func TestSize(t *testing.T) {
	cases := map[string]int64{
		"512M": 512 << 20,
		"1G":   1 << 30,
		"1.5G": 1610612736,
		"20G":  20 << 30,
		"256K": 256 << 10,
		"1024": 1024,
		"2T":   2 << 40,
		"512m": 512 << 20,
		"20GB": 20 << 30,
	}
	for in, want := range cases {
		got, err := Size(in)
		if err != nil {
			t.Errorf("Size(%q) = %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Size(%q) = %d, want %d", in, got, want)
		}
	}
	for _, bad := range []string{"", "M", "-1G", "1X", "1 000M", "512MMM", "$(id)", "1e9"} {
		if got, err := Size(bad); err == nil {
			t.Errorf("Size(%q) = %d, want an error", bad, got)
		}
	}
}

func TestFormatSizeRoundTrips(t *testing.T) {
	for _, in := range []string{"512M", "1G", "20G", "256K", "2T", "448M"} {
		bytes, err := Size(in)
		if err != nil {
			t.Fatalf("Size(%q) = %v", in, err)
		}
		if got := FormatSize(bytes); got != in {
			t.Errorf("FormatSize(Size(%q)) = %q, want %q", in, got, in)
		}
	}
}

func TestCPUQuota(t *testing.T) {
	for _, ok := range []string{"50%", "100%", "200%", "1%"} {
		if err := CPUQuota(ok); err != nil {
			t.Errorf("CPUQuota(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "100", "0%", "-50%", "50 %", "fifty%", "50%%"} {
		if err := CPUQuota(bad); err == nil {
			t.Errorf("CPUQuota(%q) = nil, want an error", bad)
		}
	}
}

func TestDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"30s":   30 * time.Second,
		"15m":   15 * time.Minute,
		"3h":    3 * time.Hour,
		"90d":   90 * 24 * time.Hour,
		"2w":    14 * 24 * time.Hour,
		"1h30m": 90 * time.Minute,
	}
	for in, want := range cases {
		got, err := Duration(in)
		if err != nil {
			t.Errorf("Duration(%q) = %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Duration(%q) = %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"", "0s", "-5m", "forever", "5", "90 d", "999999999d"} {
		if got, err := Duration(bad); err == nil {
			t.Errorf("Duration(%q) = %v, want an error", bad, got)
		}
	}
}

func TestExpiryTime(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	// A bare date means "valid through the end of that day".
	got, err := ExpiryTime("2026-12-31", now)
	if err != nil {
		t.Fatalf("ExpiryTime(2026-12-31) = %v", err)
	}
	want := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("ExpiryTime(2026-12-31) = %v, want %v", got, want)
	}

	got, err = ExpiryTime("90d", now)
	if err != nil {
		t.Fatalf("ExpiryTime(90d) = %v", err)
	}
	if !got.Equal(now.Add(90 * 24 * time.Hour)) {
		t.Errorf("ExpiryTime(90d) = %v, want %v", got, now.Add(90*24*time.Hour))
	}

	for _, bad := range []string{"", "2020-01-01", "yesterday", "2026-13-01", "2026/12/31", "-90d"} {
		if _, err := ExpiryTime(bad, now); err == nil {
			t.Errorf("ExpiryTime(%q) = nil, want an error", bad)
		}
	}
}

func TestCIDRList(t *testing.T) {
	got, err := CIDRList("203.0.113.0/24, 198.51.100.7 ,2001:db8::/32,203.0.113.0/24")
	if err != nil {
		t.Fatalf("CIDRList = %v", err)
	}
	want := []string{"203.0.113.0/24", "198.51.100.7/32", "2001:db8::/32"}
	if len(got) != len(want) {
		t.Fatalf("CIDRList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CIDRList = %v, want %v", got, want)
		}
	}

	// A prefix with host bits set is normalised, not rejected.
	if got, err := CIDRList("203.0.113.19/24"); err != nil || got[0] != "203.0.113.0/24" {
		t.Errorf("CIDRList(203.0.113.19/24) = %v, %v", got, err)
	}
	for _, bad := range []string{"", ",", "not-an-ip", "203.0.113.0/33", "203.0.113.0/24;id", "1.2.3.4.5"} {
		if got, err := CIDRList(bad); err == nil {
			t.Errorf("CIDRList(%q) = %v, want an error", bad, got)
		}
	}
}

func TestEnvKeyAndValue(t *testing.T) {
	for _, ok := range []string{"DATABASE_URL", "_X", "PORT", "a1"} {
		if err := EnvKey(ok); err != nil {
			t.Errorf("EnvKey(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "1PORT", "DB-URL", "DB URL", "DB=1", "DB\n", "DB$"} {
		if err := EnvKey(bad); err == nil {
			t.Errorf("EnvKey(%q) = nil, want an error", bad)
		}
	}
	if err := EnvValue("postgres://user:pw@host/db?sslmode=require"); err != nil {
		t.Errorf("EnvValue on a normal URL = %v", err)
	}
	// systemd's EnvironmentFile cannot represent these.
	for _, bad := range []string{"a\nb", "a\rb", "a\x00b", strings.Repeat("x", 40000)} {
		if err := EnvValue(bad); err == nil {
			t.Errorf("EnvValue(%q…) = nil, want an error", bad[:min(len(bad), 8)])
		}
	}
}

func TestGitURL(t *testing.T) {
	for _, ok := range []string{
		"https://github.com/owner/repo.git",
		"https://gitlab.example.com/group/sub/repo.git",
		"git@github.com:owner/repo.git",
		"ssh://git@github.com/owner/repo.git",
		"https://git.example.com:8443/owner/repo",
	} {
		if err := GitURL(ok); err != nil {
			t.Errorf("GitURL(%q) = %v, want nil", ok, err)
		}
	}
	invalid := map[string]string{
		"":                              "empty",
		"ext::sh -c 'id'":               "the ext transport executes commands",
		"file:///etc":                   "local clone",
		"git://github.com/o/r.git":      "unauthenticated transport",
		"http://github.com/o/r.git":     "unencrypted",
		"--upload-pack=/bin/sh":         "argument injection",
		"https://github.com/../../etc":  "traversal",
		"https://github.com/o/r.git;id": "command separator",
		"https://github.com/o r.git":    "space",
		"/local/path":                   "filesystem path",
	}
	for in, why := range invalid {
		if err := GitURL(in); err == nil {
			t.Errorf("GitURL(%q) = nil, want an error (%s)", in, why)
		}
	}
}

func TestGitRef(t *testing.T) {
	for _, ok := range []string{"main", "release/2.1", "v1.0.0", "feature_x"} {
		if err := GitRef(ok); err != nil {
			t.Errorf("GitRef(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "--upload-pack", "a b", "a..b", "a~1", "a^", "a:b", "a?", "a*", "x.lock", "a/", "/a"} {
		if err := GitRef(bad); err == nil {
			t.Errorf("GitRef(%q) = nil, want an error", bad)
		}
	}
}

func TestFingerprint(t *testing.T) {
	valid := "SHA256:" + strings.Repeat("A", 43)
	if err := Fingerprint(valid); err != nil {
		t.Errorf("Fingerprint(%q) = %v, want nil", valid, err)
	}
	// The prefix is optional on input and added by NormalizeFingerprint.
	if err := Fingerprint(strings.Repeat("A", 43)); err != nil {
		t.Errorf("Fingerprint without a prefix = %v, want nil", err)
	}
	for _, bad := range []string{"", "SHA256:", "MD5:aa:bb", "SHA256:short", "SHA256:" + strings.Repeat("A", 44) + "x", "SHA256:!!!"} {
		if err := Fingerprint(bad); err == nil {
			t.Errorf("Fingerprint(%q) = nil, want an error", bad)
		}
	}
}

func TestPortRange(t *testing.T) {
	if err := PortRange(20000, 29999); err != nil {
		t.Errorf("PortRange(20000, 29999) = %v, want nil", err)
	}
	for _, tc := range [][2]int{{0, 100}, {80, 443}, {30000, 20000}, {20000, 20005}, {60000, 70000}} {
		if err := PortRange(tc[0], tc[1]); err == nil {
			t.Errorf("PortRange(%d, %d) = nil, want an error", tc[0], tc[1])
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
