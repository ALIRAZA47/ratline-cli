package validate

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// MaxLabelLen bounds an SSH key label. It is stored in state and rendered into
// an authorized_keys comment, so it needs to be short and printable.
const MaxLabelLen = 64

// Label validates the human-readable name attached to an SSH key.
//
// Quotes are refused rather than escaped: the label is rendered inside a
// label="..." comment, and a validator that cannot produce an ambiguous file is
// worth more than one that accepts every character.
func Label(s string) error {
	if strings.TrimSpace(s) == "" {
		return rlerr.Usagef("the label is empty").
			WithHint(`every key needs a label so it can be recognised later, for example --label "Ali MacBook"`)
	}
	if n := len([]rune(s)); n > MaxLabelLen {
		return rlerr.Usagef("the label is %d characters long; the limit is %d", n, MaxLabelLen)
	}
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			return rlerr.Usagef("the label may not contain %q", r)
		case r == '\n' || r == '\r' || r == 0:
			return rlerr.Usagef("the label may not contain a newline or NUL byte")
		case !unicode.IsPrint(r):
			return rlerr.Usagef("the label contains a non-printable character")
		}
	}
	return nil
}

var emailRe = regexp.MustCompile(`^[^\s@,;<>"']+@[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?\.[A-Za-z]{2,63}$`)

// Email validates an ACME contact address. It is deliberately conservative:
// this address reaches a certificate authority, and a typo means no expiry
// warnings.
func Email(s string) error {
	if s == "" {
		return rlerr.Usagef("the email address is empty")
	}
	if len(s) > 254 {
		return rlerr.Usagef("the email address is longer than 254 characters")
	}
	if !emailRe.MatchString(s) {
		return rlerr.Usagef("invalid email address %q", s).
			WithHint("this is the ACME contact address, for example admin@example.com")
	}
	return nil
}

// Port validates a TCP port an application may listen on.
func Port(n int) error {
	if n < 1024 || n > 65535 {
		return rlerr.Usagef("invalid port %d: use a port between 1024 and 65535", n)
	}
	return nil
}

// PortRange validates the allocation window from config.
func PortRange(start, end int) error {
	if err := Port(start); err != nil {
		return err
	}
	if err := Port(end); err != nil {
		return err
	}
	if start > end {
		return rlerr.Usagef("the port range start (%d) is above its end (%d)", start, end)
	}
	if end-start < 16 {
		return rlerr.Usagef("the port range %d-%d is too small; leave room for at least 16 sites", start, end)
	}
	return nil
}

var sizeRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([KkMmGgTt]?)[Bb]?$`)

// Size parses a systemd-style byte size: 512M, 1.5G, 20G, or a bare byte count.
// Units are powers of 1024, matching systemd and human expectations for RAM.
func Size(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, rlerr.Usagef("the size is empty")
	}
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0, rlerr.Usagef("invalid size %q", s).
			WithHint("use a number with an optional unit, for example 512M, 1.5G or 20G")
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, rlerr.Usagef("invalid size %q", s)
	}
	mult := float64(1)
	switch strings.ToUpper(m[2]) {
	case "K":
		mult = 1 << 10
	case "M":
		mult = 1 << 20
	case "G":
		mult = 1 << 30
	case "T":
		mult = 1 << 40
	}
	bytes := v * mult
	if bytes < 0 || bytes > 1<<62 {
		return 0, rlerr.Usagef("the size %q is out of range", s)
	}
	return int64(bytes), nil
}

// FormatSize renders a byte count the way systemd and humans read it.
func FormatSize(b int64) string {
	switch {
	case b >= 1<<40 && b%(1<<40) == 0:
		return fmt.Sprintf("%dT", b>>40)
	case b >= 1<<30 && b%(1<<30) == 0:
		return fmt.Sprintf("%dG", b>>30)
	case b >= 1<<20 && b%(1<<20) == 0:
		return fmt.Sprintf("%dM", b>>20)
	case b >= 1<<10 && b%(1<<10) == 0:
		return fmt.Sprintf("%dK", b>>10)
	case b >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1fK", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

var cpuQuotaRe = regexp.MustCompile(`^([0-9]{1,5})%$`)

// CPUQuota validates a systemd CPUQuota value. Over 100% is legal and means
// more than one core.
func CPUQuota(s string) error {
	m := cpuQuotaRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return rlerr.Usagef("invalid CPU quota %q", s).
			WithHint("use a percentage, for example 50%% for half a core or 200%% for two cores")
	}
	n, _ := strconv.Atoi(m[1])
	if n == 0 {
		return rlerr.Usagef("invalid CPU quota %q: zero would stop the application entirely", s)
	}
	return nil
}

var durationRe = regexp.MustCompile(`^([0-9]+)([smhdw])$`)

// Duration parses a duration, extending Go's syntax with d (day) and w (week)
// because key expiry and certificate windows are naturally expressed that way.
func Duration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, rlerr.Usagef("the duration is empty")
	}
	if m := durationRe.FindStringSubmatch(s); m != nil {
		n, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return 0, rlerr.Usagef("invalid duration %q", s)
		}
		var unit time.Duration
		switch m[2] {
		case "s":
			unit = time.Second
		case "m":
			unit = time.Minute
		case "h":
			unit = time.Hour
		case "d":
			unit = 24 * time.Hour
		case "w":
			unit = 7 * 24 * time.Hour
		}
		if n == 0 {
			return 0, rlerr.Usagef("the duration %q must be positive", s)
		}
		if n > int64((100*365*24*time.Hour)/unit) {
			return 0, rlerr.Usagef("the duration %q is unreasonably long", s)
		}
		return time.Duration(n) * unit, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, rlerr.Usagef("invalid duration %q", s).
			WithHint("use a number with a unit, for example 30s, 15m, 90d or 2w")
	}
	if d <= 0 {
		return 0, rlerr.Usagef("the duration %q must be positive", s)
	}
	return d, nil
}

var dateRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)

// ExpiryTime resolves --expires, which accepts either an absolute date
// (2026-12-31) or a duration from now (90d).
//
// A bare date means "valid through the end of that day" in UTC, which is what an
// operator writing 2026-12-31 intends. OpenSSH compares against UTC too.
func ExpiryTime(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, rlerr.Usagef("the expiry is empty")
	}
	if m := dateRe.FindStringSubmatch(s); m != nil {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return time.Time{}, rlerr.Usagef("invalid date %q", s).WithHint("use YYYY-MM-DD")
		}
		end := t.UTC().Add(24*time.Hour - time.Second)
		if !end.After(now) {
			return time.Time{}, rlerr.Usagef("%s is in the past", s)
		}
		return end, nil
	}
	d, err := Duration(s)
	if err != nil {
		return time.Time{}, rlerr.Usagef("invalid expiry %q", s).
			WithHint("use a date such as 2026-12-31, or a duration such as 90d")
	}
	return now.UTC().Add(d), nil
}

// CIDRList parses the --from source restriction into canonical prefixes. Bare
// addresses are widened to a single-host prefix.
func CIDRList(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, rlerr.Usagef("the address list is empty")
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, raw := range parts {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var canonical string
		if strings.Contains(raw, "/") {
			p, err := netip.ParsePrefix(raw)
			if err != nil {
				return nil, rlerr.Usagef("invalid network %q", raw).
					WithHint("use CIDR notation, for example 203.0.113.0/24 or 2001:db8::/32")
			}
			canonical = p.Masked().String()
		} else {
			a, err := netip.ParseAddr(raw)
			if err != nil {
				return nil, rlerr.Usagef("invalid address %q", raw).
					WithHint("use an address or CIDR block, for example 203.0.113.19 or 203.0.113.0/24")
			}
			canonical = netip.PrefixFrom(a, a.BitLen()).String()
		}
		if !seen[canonical] {
			seen[canonical] = true
			out = append(out, canonical)
		}
	}
	if len(out) == 0 {
		return nil, rlerr.Usagef("the address list is empty")
	}
	return out, nil
}

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// EnvKey validates an environment variable name for a site's .env file.
func EnvKey(s string) error {
	if s == "" {
		return rlerr.Usagef("the variable name is empty")
	}
	if len(s) > 128 {
		return rlerr.Usagef("the variable name %q is longer than 128 characters", s)
	}
	if !envKeyRe.MatchString(s) {
		return rlerr.Usagef("invalid variable name %q", s).
			WithHint("use letters, digits and underscores, starting with a letter or underscore, for example DATABASE_URL")
	}
	return nil
}

// EnvValue rejects values that cannot survive systemd's EnvironmentFile parser.
func EnvValue(v string) error {
	if strings.ContainsRune(v, 0) {
		return rlerr.Usagef("the value contains a NUL byte")
	}
	if strings.ContainsAny(v, "\n\r") {
		return rlerr.Usagef("the value contains a newline").
			WithHint("systemd's EnvironmentFile cannot represent multi-line values; " +
				"store the payload in a file inside the site directory and point a variable at it")
	}
	if len(v) > 32<<10 {
		return rlerr.Usagef("the value is %d bytes, over the 32768-byte limit", len(v))
	}
	return nil
}

// SensitiveEnvKeys are refused because they change how the runtime itself
// loads code, which is a foot-gun rather than a feature.
var SensitiveEnvKeys = map[string]bool{
	"LD_PRELOAD":            true,
	"LD_LIBRARY_PATH":       true,
	"LD_AUDIT":              true,
	"DYLD_INSERT_LIBRARIES": true,
}

var (
	httpsGitRe = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:\d{1,5})?/[A-Za-z0-9._/~-]+$`)
	sshGitRe   = regexp.MustCompile(`^(ssh://)?[A-Za-z0-9._-]+@[A-Za-z0-9.-]+(:\d{1,5})?[:/][A-Za-z0-9._/~-]+$`)
)

// GitURL validates a clone source.
//
// Refused on purpose: a leading hyphen (which git would read as a flag),
// ext:: and file:: transports (which execute commands), git:// (unauthenticated
// and unencrypted) and plain http.
func GitURL(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return rlerr.Usagef("the repository URL is empty")
	}
	if len(s) > 512 {
		return rlerr.Usagef("the repository URL is longer than 512 characters")
	}
	if strings.HasPrefix(s, "-") {
		return rlerr.Usagef("invalid repository URL %q: it must not start with a hyphen", s)
	}
	for _, r := range s {
		if r < 0x21 || r > 0x7e {
			return rlerr.Usagef("invalid repository URL %q: it contains a space or a non-ASCII character", s)
		}
	}
	lower := strings.ToLower(s)
	for _, bad := range []struct{ prefix, why string }{
		{"ext::", "the ext:: transport runs arbitrary commands"},
		{"file://", "local clones are not supported"},
		{"git://", "the git protocol is neither authenticated nor encrypted"},
		{"http://", "plain HTTP is not encrypted"},
	} {
		if strings.HasPrefix(lower, bad.prefix) {
			return rlerr.Usagef("refusing the repository URL %q: %s", s, bad.why).
				WithHint("use an https:// or ssh:// URL")
		}
	}
	if strings.Contains(s, "..") {
		return rlerr.Usagef("invalid repository URL %q: it contains %q", s, "..")
	}
	if httpsGitRe.MatchString(s) || sshGitRe.MatchString(s) {
		return nil
	}
	return rlerr.Usagef("invalid repository URL %q", s).
		WithHint("use https://github.com/owner/repo.git or git@github.com:owner/repo.git")
}

// GitRef validates a branch or tag name for `git checkout`.
func GitRef(s string) error {
	if s == "" {
		return rlerr.Usagef("the branch name is empty")
	}
	if len(s) > 255 {
		return rlerr.Usagef("the branch name is longer than 255 characters")
	}
	if strings.HasPrefix(s, "-") {
		return rlerr.Usagef("invalid branch name %q: it must not start with a hyphen", s)
	}
	// git check-ref-format's rules, reduced to what matters here.
	if strings.ContainsAny(s, " ~^:?*[\\\t\n") || strings.Contains(s, "..") ||
		strings.HasSuffix(s, ".lock") || strings.HasSuffix(s, "/") || strings.HasPrefix(s, "/") {
		return rlerr.Usagef("invalid branch name %q", s)
	}
	return nil
}

var fingerprintRe = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}=?$`)

// Fingerprint validates an OpenSSH SHA256 fingerprint.
func Fingerprint(s string) error {
	if !fingerprintRe.MatchString(NormalizeFingerprint(s)) {
		return rlerr.Usagef("invalid fingerprint %q", s).
			WithHint("use the SHA256 form printed by ssh-keygen -lf, for example SHA256:x9K…")
	}
	return nil
}

// NormalizeFingerprint adds the SHA256: prefix when an operator omits it.
func NormalizeFingerprint(s string) string {
	s = strings.TrimSpace(s)
	if s != "" && !strings.Contains(s, ":") {
		return "SHA256:" + s
	}
	return s
}

// NoControlChars refuses a value that carries a control character other than a tab.
//
// The one check that stops a newline turning a single generated directive — an nginx
// location, a systemd ExecStart — into two. It is deliberately blunt and applied wherever a
// stored string is written verbatim into a config file: no legitimate value for any such
// field has ever contained a control character, and the tools that check those files accept
// a second valid directive without complaint.
func NoControlChars(field, value string) error {
	for _, r := range value {
		if r != '\t' && unicode.IsControl(r) {
			return rlerr.Usagef("the %s contains a control character", field).
				WithHint("it is written into a generated configuration file, where a newline " +
					"would add a line nobody asked for")
		}
	}
	return nil
}
