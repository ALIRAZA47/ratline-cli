package panel

import (
	"net"
	"net/http"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// parseCIDRs turns the allow list into networks, accepting a bare address as the
// single-host network it obviously means.
//
// Writing 203.0.113.9 rather than 203.0.113.9/32 is what everybody does, and
// refusing it would be pedantry that produces a support question rather than a
// stricter configuration.
func parseCIDRs(list []string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, raw := range list {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(s); err == nil {
			out = append(out, n)
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, rlerr.Usagef("security.allow_from: %q is not an address or a CIDR block", raw)
		}
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	return out, nil
}

// ParseAllowFrom turns security.allow_from into networks, for the HTTP layer.
func ParseAllowFrom(list []string) ([]*net.IPNet, error) { return parseCIDRs(list) }

// AllowedFrom reports whether an address may reach the panel at all.
func AllowedFrom(nets []*net.IPNet, addr string) bool {
	if len(nets) == 0 {
		return true
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP is the address to attribute a request to.
//
// X-Forwarded-For is believed only when listen.trust_proxy says so, and then only
// its last hop — the address nginx itself saw. Trusting the whole chain means
// trusting a header the client wrote, which turns a per-address rate limit into a
// per-whatever-they-typed rate limit.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			last := strings.TrimSpace(parts[len(parts)-1])
			if net.ParseIP(last) != nil {
				return last
			}
		}
		if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(real) != nil {
			return real
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RequestIsSecure reports whether the connection reaching the *client* was HTTPS,
// which is not the same question as whether this hop was.
func RequestIsSecure(r *http.Request, trustProxy bool) bool {
	if r.TLS != nil {
		return true
	}
	if trustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return false
}
