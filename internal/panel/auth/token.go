package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// tokenBytes is 32 bytes of entropy. A session cookie and an invitation link are
// both bearer credentials for administering a server; there is no reason to be
// frugal here.
const tokenBytes = 32

// NewToken returns a fresh bearer token, URL-safe, and the hash to store beside it.
//
// The hash is what goes in the database. Session tokens and invitation links are
// bearer credentials, so a database that leaks must not hand over working ones on top
// of the password hashes it already cost.
func NewToken() (token, hash string, err error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", rlerr.Wrap(err, rlerr.CodeGeneric, "generating a token")
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken is the one-way mapping from a bearer token to what is stored.
//
// SHA-256, not argon2, and deliberately: a token is 256 bits from crypto/rand, so
// there is no guessing to slow down, and this runs on every authenticated request.
// Password hashing is slow on purpose; doing that per request would be a denial of
// service the panel inflicts on itself.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ConstantTimeEqualString compares two strings without leaking where they differ.
func ConstantTimeEqualString(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// NewID returns an opaque identifier for a row. Not a token: it appears in URLs and
// in the audit log, and nothing is authorised by holding one.
func NewID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "generating an identifier")
	}
	return hex.EncodeToString(b), nil
}
