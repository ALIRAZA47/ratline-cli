// Package sshkey manages authorized SSH keys across ratline's three scopes.
//
// A key is a managed resource with a required human label, because an operator
// looking at a server two years from now needs to tell "Ali MacBook" from "CI
// runner" without guessing.
package sshkey

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// PublicKey is a validated, normalised public key.
//
// Options that arrived on the submitted line are recorded in StrippedOptions but
// never carried forward. A pasted key bringing its own command= or permitopen=
// is an escalation vector: only the options ratline derives from its own flags
// are ever written.
type PublicKey struct {
	Algorithm       string
	Blob            string // base64, exactly as it will be written
	Comment         string
	Fingerprint     string // SHA256:…
	Bits            int
	StrippedOptions []string
}

// Line renders the key without options, which is how it is stored.
func (k *PublicKey) Line() string {
	if k.Comment == "" {
		return k.Algorithm + " " + k.Blob
	}
	return k.Algorithm + " " + k.Blob + " " + k.Comment
}

// Policy is the algorithm and size policy from config.
type Policy struct {
	MinRSABits         int
	WarnRSABits        int
	AllowedAlgorithms  []string
	RejectedAlgorithms []string
	MaxLineBytes       int
}

// Warning is a non-fatal finding about a key.
type Warning string

// Parse validates one authorized_keys line and returns the normalised key.
//
// Validation happens entirely in process. Writing an attacker-influenced blob to
// a temporary file so that ssh-keygen can be run over it would add a filesystem
// round trip and a subprocess for no gain: x/crypto/ssh performs the same
// structural parse OpenSSH does, and refuses the same malformed input.
func Parse(line string, p Policy) (*PublicKey, []Warning, error) {
	if p.MaxLineBytes <= 0 {
		p.MaxLineBytes = 8192
	}
	if len(line) > p.MaxLineBytes {
		return nil, nil, rlerr.Usagef("the key is %d bytes long, over the %d-byte limit", len(line), p.MaxLineBytes)
	}
	if strings.ContainsRune(line, 0) {
		return nil, nil, rlerr.Usagef("the key contains a NUL byte")
	}
	trimmed := strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(trimmed) == "" {
		return nil, nil, rlerr.Usagef("the key is empty")
	}
	// Before the multi-line check, deliberately. A pasted private key is several lines,
	// so the line check matched first and answered "the key spans more than one line" —
	// true, unhelpful, and burying the only thing that matters, which is that somebody
	// has just handed their private key to a command that installs things.
	if strings.HasPrefix(strings.TrimSpace(trimmed), "-----BEGIN") {
		return nil, nil, rlerr.Usagef("that is a private key, not a public key").
			WithHint("pass the .pub file instead, and never share the private half — " +
				"if this one left your machine, treat it as compromised and rotate it")
	}
	// A key spanning lines would let an attacker append a second, unreviewed
	// entry to authorized_keys.
	if strings.ContainsAny(trimmed, "\n\r") {
		return nil, nil, rlerr.Usagef("the key spans more than one line").
			WithHint("pass one key at a time, or a file containing one key per line")
	}

	parsed, comment, options, _, err := ssh.ParseAuthorizedKey([]byte(trimmed))
	if err != nil {
		return nil, nil, rlerr.Wrap(err, rlerr.CodeUsage, "the key could not be parsed").
			WithHint("an authorized_keys line looks like: ssh-ed25519 AAAAC3Nz… you@laptop")
	}

	key := &PublicKey{
		Algorithm:       parsed.Type(),
		Blob:            base64Of(parsed),
		Comment:         sanitiseComment(comment),
		Fingerprint:     ssh.FingerprintSHA256(parsed),
		Bits:            bitsOf(parsed),
		StrippedOptions: options,
	}

	var warnings []Warning
	if len(options) > 0 {
		// Reported rather than silently dropped: an operator who pasted a
		// restricted key needs to know ratline replaced those restrictions with
		// its own, not weaker ones.
		warnings = append(warnings, Warning("the submitted key carried its own options ("+
			strings.Join(options, ",")+"); they were discarded and replaced by the options ratline derives from your flags"))
	}

	if err := checkAlgorithm(key, p); err != nil {
		return nil, nil, err
	}
	if key.Algorithm == ssh.KeyAlgoRSA || strings.HasPrefix(key.Algorithm, "rsa-sha2") {
		if key.Bits < p.MinRSABits {
			return nil, nil, rlerr.Usagef("this RSA key is %d bits; the minimum is %d", key.Bits, p.MinRSABits).
				WithHint("generate a modern key instead: ssh-keygen -t ed25519")
		}
		if p.WarnRSABits > 0 && key.Bits < p.WarnRSABits {
			warnings = append(warnings, Warning("this RSA key is "+itoa(key.Bits)+" bits; ed25519 is preferred"))
		}
	}
	if key.Algorithm == ssh.KeyAlgoRSA {
		// ssh-rsa names the SHA-1 signature algorithm. The key itself is fine,
		// and modern sshd negotiates rsa-sha2-*, so this is a nudge not a block.
		warnings = append(warnings, Warning("this key is stored as ssh-rsa; ed25519 keys are shorter, faster and have no SHA-1 baggage"))
	}
	return key, warnings, nil
}

func checkAlgorithm(k *PublicKey, p Policy) error {
	for _, r := range p.RejectedAlgorithms {
		if k.Algorithm == r {
			return rlerr.Usagef("%s keys are refused", k.Algorithm).
				WithHint("DSA keys are fixed at 1024 bits and have no place on a new server; use ssh-keygen -t ed25519")
		}
	}
	if len(p.AllowedAlgorithms) == 0 {
		return nil
	}
	for _, a := range p.AllowedAlgorithms {
		if k.Algorithm == a {
			return nil
		}
	}
	return rlerr.Usagef("%s is not in the allowed algorithm list", k.Algorithm).
		WithHint("the list lives under ssh.allowed_algorithms in /etc/ratline/config.yaml")
}

// ParseMany validates a file or fetched list containing several keys, reporting
// each line's own error so one bad entry does not hide the good ones.
func ParseMany(data []byte, p Policy) ([]*PublicKey, []Warning, error) {
	var (
		keys     []*PublicKey
		warnings []Warning
		problems []string
	)
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, w, err := Parse(line, p)
		if err != nil {
			problems = append(problems, "line "+itoa(i+1)+": "+err.Error())
			continue
		}
		keys = append(keys, key)
		warnings = append(warnings, w...)
	}
	if len(keys) == 0 {
		if len(problems) > 0 {
			return nil, nil, rlerr.Usagef("no valid key was found:\n  - %s", strings.Join(problems, "\n  - "))
		}
		return nil, nil, rlerr.Usagef("no key was found in the input")
	}
	for _, p := range problems {
		warnings = append(warnings, Warning("skipped "+p))
	}
	return keys, warnings, nil
}

// base64Of re-encodes the parsed key, so what gets written is the canonical form
// rather than whatever whitespace or padding the operator pasted.
func base64Of(k ssh.PublicKey) string {
	line := string(ssh.MarshalAuthorizedKey(k))
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

// bitsOf reports a key's size, which only means something for RSA.
func bitsOf(k ssh.PublicKey) int {
	cpk, ok := k.(ssh.CryptoPublicKey)
	if !ok {
		return 0
	}
	switch pub := cpk.CryptoPublicKey().(type) {
	case *rsa.PublicKey:
		return pub.N.BitLen()
	case *ecdsa.PublicKey:
		return pub.Curve.Params().BitSize
	case ed25519.PublicKey:
		return 256
	default:
		return 0
	}
}

// sanitiseComment keeps the operator's comment from breaking the file it lands
// in. The comment is the last field of an authorized_keys line, so a newline
// there would create a second entry.
func sanitiseComment(c string) string {
	c = strings.TrimSpace(c)
	c = strings.Map(func(r rune) rune {
		switch {
		case r == '\n', r == '\r', r == 0:
			return -1
		case r < 0x20 || r == 0x7f:
			return -1
		default:
			return r
		}
	}, c)
	if len(c) > 128 {
		c = c[:128]
	}
	return c
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
