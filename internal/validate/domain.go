package validate

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// MaxDomainLen is the DNS limit on a fully qualified name.
const MaxDomainLen = 253

var labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// idnaProfile converts internationalised names to punycode.
//
// CheckHyphens is off on purpose: it rejects a double hyphen in the third and
// fourth positions, which would refuse perfectly ordinary registered domains
// like my--site.com. Leading and trailing hyphens are still caught by labelRe.
var idnaProfile = idna.New(
	idna.MapForLookup(),
	idna.BidiRule(),
	idna.StrictDomainName(true),
	idna.ValidateLabels(true),
	idna.CheckHyphens(false),
	idna.VerifyDNSLength(false),
)

// domainForbidden lists characters that must never survive into an nginx
// server_name, a certbot argument or a filesystem path.
const domainForbidden = "/\\;$`'\"|&<>*?!(){}[]#%^~,:= \t"

// Domain validates a hostname and returns its canonical lowercase punycode form.
//
// The returned value — not the operator's input — is what gets written into
// configs, so a name is normalised exactly once, here.
func Domain(s string) (string, error) {
	orig := s
	s = strings.TrimSpace(s)
	if s == "" {
		return "", rlerr.Usagef("the domain is empty")
	}
	if len(s) > 4*MaxDomainLen {
		return "", rlerr.Usagef("the domain is far too long (%d bytes)", len(s))
	}
	// Invalid UTF-8 has to be refused before the IDNA mapping, not after: the
	// mapping replaces a bad byte with U+FFFD and then punycode-encodes it, which
	// produces a name that looks valid but fails validation the next time anything
	// checks it. Found by FuzzDomain with the input "\x80.A", which mapped to
	// "xn--zn7c.a" and then would not re-validate.
	if !utf8.ValidString(s) {
		return "", rlerr.Usagef("invalid domain %q: it is not valid UTF-8", orig)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", rlerr.Usagef("invalid domain %q: it contains a control character", orig)
		}
		if r == utf8.RuneError {
			return "", rlerr.Usagef("invalid domain %q: it contains the Unicode replacement character", orig)
		}
	}
	if i := strings.IndexAny(s, domainForbidden); i >= 0 {
		return "", rlerr.Usagef("invalid domain %q: the character %q is not allowed in a hostname", orig, s[i:i+1])
	}
	if strings.Contains(s, "..") {
		return "", rlerr.Usagef("invalid domain %q: it contains an empty label", orig)
	}

	// One trailing dot is the legal absolute form; strip it before validating.
	s = strings.TrimSuffix(s, ".")
	if strings.HasPrefix(s, ".") {
		return "", rlerr.Usagef("invalid domain %q: it starts with a dot", orig)
	}

	ascii, err := idnaProfile.ToASCII(s)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeUsage, "invalid domain %q", orig).
			WithHint("internationalised names are converted to punycode; check for stray or invisible characters")
	}
	ascii = strings.ToLower(ascii)
	if len(ascii) > MaxDomainLen {
		return "", rlerr.Usagef("invalid domain %q: %d bytes in punycode form, over the %d-byte DNS limit", orig, len(ascii), MaxDomainLen)
	}

	labels := strings.Split(ascii, ".")
	if len(labels) < 2 {
		return "", rlerr.Usagef("invalid domain %q: a site needs at least two labels", orig).
			WithHint("use a fully qualified name such as app.example.com")
	}
	for _, l := range labels {
		switch {
		case l == "":
			return "", rlerr.Usagef("invalid domain %q: it contains an empty label", orig)
		case len(l) > 63:
			return "", rlerr.Usagef("invalid domain %q: the label %q is longer than 63 characters", orig, l)
		case !labelRe.MatchString(l):
			return "", rlerr.Usagef("invalid domain %q: the label %q is not a valid hostname label", orig, l)
		}
	}
	if allDigits(labels[len(labels)-1]) {
		return "", rlerr.Usagef("invalid domain %q: the last label is all digits, which looks like an IP address", orig).
			WithHint("certificates cannot be issued for bare IP addresses; use a hostname")
	}

	// A public suffix on its own is not registrable, and issuing for it would
	// fail at the CA after burning a rate-limit attempt.
	if suffix, _ := publicsuffix.PublicSuffix(ascii); suffix == ascii {
		return "", rlerr.Usagef("%q is a public suffix rather than a domain you can own", ascii).
			WithHint("use a name under it, for example app.%s", ascii)
	}
	return ascii, nil
}

// IsWildcard reports whether a name is of the form *.example.com.
func IsWildcard(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), "*.") }

// DomainOrWildcard validates a certificate subject, which may be a wildcard.
//
// Only a leading "*." is legal: neither DNS nor the CAs support *.*.example.com
// or a*.example.com, and accepting them here would fail much later with a worse
// message.
func DomainOrWildcard(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !IsWildcard(s) {
		if strings.Contains(s, "*") {
			return "", rlerr.Usagef("invalid wildcard %q: an asterisk is only allowed as the leading label, as in *.example.com", s)
		}
		return Domain(s)
	}
	base, err := Domain(strings.TrimPrefix(s, "*."))
	if err != nil {
		return "", err
	}
	return "*." + base, nil
}

// RegisteredDomain returns the registrable domain (eTLD+1), which is the unit
// Let's Encrypt applies its per-domain rate limits to. Wildcards collapse onto
// their base.
func RegisteredDomain(s string) (string, error) {
	name, err := DomainOrWildcard(s)
	if err != nil {
		return "", err
	}
	name = strings.TrimPrefix(name, "*.")
	rd, err := publicsuffix.EffectiveTLDPlusOne(name)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeUsage, "cannot determine the registered domain for %q", name)
	}
	return rd, nil
}

// IsICANNSuffix reports whether the name sits under a suffix in the ICANN
// section of the public suffix list. Names under a private suffix (github.io)
// are legitimate but rate-limited differently by the CA, which is worth telling
// the operator about.
func IsICANNSuffix(s string) bool {
	name, err := DomainOrWildcard(s)
	if err != nil {
		return false
	}
	_, icann := publicsuffix.PublicSuffix(strings.TrimPrefix(name, "*."))
	return icann
}

// Aliases validates and de-duplicates a site's alias list against its domain.
func Aliases(domain string, aliases []string) ([]string, error) {
	seen := map[string]bool{domain: true}
	out := make([]string, 0, len(aliases))
	for _, a := range aliases {
		name, err := Domain(a)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
