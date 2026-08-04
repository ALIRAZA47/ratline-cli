package validate

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// MaxSlugLen is bounded by the Unix socket path limit, not by systemd.
//
// A site's socket lives at /run/ratline/<slug>/app.sock. sockaddr_un.sun_path is
// 108 bytes on Linux, and the fixed part of that path is 22 characters, so a
// slug over about 85 characters produces a socket the application cannot bind —
// which surfaces as an opaque "invalid argument" at start time. 64 leaves room
// for multi-instance suffixes like app-1.sock.
const MaxSlugLen = 64

// Slug derives the identifier shared by a site's systemd unit, runtime
// directory and socket path.
//
// Dots become underscores so that alice/example.com reads as
// alice-example_com: unit names accept dots, but a name with two separators
// carrying different meanings is much harder to scan in a systemctl listing.
func Slug(user, domain string) string {
	return SlugFor(user + "-" + domain)
}

// SlugFor normalises an arbitrary string into a slug.
func SlugFor(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r == '.':
			b.WriteByte('_')
		default:
			b.WriteByte('-')
		}
	}
	out := collapse(b.String(), '-')
	out = strings.Trim(out, "-_")
	if out == "" {
		out = "site"
	}
	if len(out) <= MaxSlugLen {
		return out
	}
	// Truncating alone risks two long domains colliding on one unit name, so
	// append a digest of the full value.
	sum := sha256.Sum256([]byte(out))
	suffix := "-" + hex.EncodeToString(sum[:])[:8]
	return strings.Trim(out[:MaxSlugLen-len(suffix)], "-_") + suffix
}

// UnitName is the systemd unit filename for a site.
func UnitName(user, domain string) string {
	return "ratline-" + Slug(user, domain) + ".service"
}

// InstanceUnitName is the templated unit name used when --instances > 1.
func InstanceUnitName(user, domain string, instance int) string {
	return "ratline-" + Slug(user, domain) + "@" + strconv.Itoa(instance) + ".service"
}

func collapse(s string, r byte) string {
	var b strings.Builder
	b.Grow(len(s))
	var prev byte
	for i := 0; i < len(s); i++ {
		if s[i] == r && prev == r {
			continue
		}
		b.WriteByte(s[i])
		prev = s[i]
	}
	return b.String()
}
