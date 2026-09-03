// Package auth is the panel's credential handling: password hashing, one-time
// tokens and the second factor.
//
// Nothing here is novel, which is the point. A panel that can provision root-owned
// services on someone's server is the wrong place to invent a scheme, so this is
// argon2id with the parameters RFC 9106 recommends for a server, tokens from
// crypto/rand, and RFC 6238 TOTP — all from the standard library and
// golang.org/x/crypto, which ratline already depends on. No new dependency, and the
// binary stays static.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/crypto/argon2"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// The argon2id parameters. RFC 9106's second recommended option: 64 MiB, three
// passes, four lanes. It costs roughly 50 ms on a modest VPS core, which is the
// balance worth striking — slow enough that an offline attack on a stolen database is
// expensive, fast enough that a sign-in does not feel broken.
//
// They are stored in the encoded hash rather than assumed, so raising them later
// verifies old hashes with their own parameters instead of rejecting everyone.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16
)

// MinPasswordLength is the floor.
//
// Length rather than a character-class rule: "at least one number and one symbol"
// reliably produces Password1!, and NIST stopped recommending composition rules for
// exactly that reason. Twelve characters, and a check against the handful of
// passwords every scanner tries first.
const MinPasswordLength = 12

// HashPassword returns the PHC-format encoding of the password.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "generating a salt")
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether the password produces the stored hash.
//
// A malformed hash is false with an error, never a panic and never true. The
// comparison is constant-time.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, rlerr.Genericf("the stored password hash is not in the expected format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, rlerr.Genericf("the stored password hash uses an unsupported argon2 version")
	}
	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, rlerr.Genericf("the stored password hash has unreadable parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, rlerr.Genericf("the stored password hash has an unreadable salt")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, rlerr.Genericf("the stored password hash is unreadable")
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// commonPasswords is not a dictionary and is not pretending to be one. It is the
// short list a scanner tries against a newly discovered panel in its first second,
// which is the attack this actually stops.
var commonPasswords = map[string]bool{
	"password":      true,
	"password1":     true,
	"password123":   true,
	"passw0rd":      true,
	"qwertyuiop":    true,
	"123456789012":  true,
	"1234567890":    true,
	"letmein12345":  true,
	"administrator": true,
	"changeme123":   true,
}

// CheckPasswordStrength refuses a password that would embarrass the server.
func CheckPasswordStrength(password string) error {
	if len([]rune(password)) < MinPasswordLength {
		return rlerr.Usagef("a password must be at least %d characters", MinPasswordLength).
			WithHint("length beats punctuation; four unrelated words are stronger than P@ssw0rd!")
	}
	if len(password) > 1024 {
		// Argon2 will happily hash a megabyte, and a request that asks it to is a
		// denial of service rather than a sign-in.
		return rlerr.Usagef("a password may be at most 1024 bytes")
	}
	if commonPasswords[strings.ToLower(password)] {
		return rlerr.Usagef("that password appears in every list an attacker tries first")
	}
	if uniqueRunes(password) < 5 {
		return rlerr.Usagef("that password repeats too few distinct characters")
	}
	return nil
}

func uniqueRunes(s string) int {
	seen := map[rune]bool{}
	for _, r := range s {
		if !unicode.IsSpace(r) {
			seen[r] = true
		}
	}
	return len(seen)
}

// passwordAlphabet omits the characters people mistake for each other when reading a
// password off a terminal and typing it into a browser: 0/O, 1/l/I, and u/v in some
// faces. What is left is 28 symbols, so each character carries just under five bits.
const passwordAlphabet = "abcdefghjkmnpqrstwxyz23456789"

// GeneratePassword returns a password for the account the installer creates.
//
// Twenty characters in groups of four, which is around 97 bits — far past anything a
// person would pick, and still short enough to read off one screen and type into
// another. It is shown once and never stored in the clear, so it is not meant to be
// remembered; it is meant to get somebody in far enough to set their own.
//
// A default password would be worse in every way, and a blank one worse still: the
// panel would then have a window in which whoever reached it first became its
// administrator.
func GeneratePassword() (string, error) {
	const groups, per = 5, 4
	for attempt := 0; attempt < 8; attempt++ {
		var b strings.Builder
		for g := 0; g < groups; g++ {
			if g > 0 {
				b.WriteByte('-')
			}
			for i := 0; i < per; i++ {
				n, err := rand.Int(rand.Reader, big.NewInt(int64(len(passwordAlphabet))))
				if err != nil {
					return "", rlerr.Wrap(err, rlerr.CodeGeneric, "generating a password")
				}
				b.WriteByte(passwordAlphabet[n.Int64()])
			}
		}
		candidate := b.String()
		// The generator must satisfy the same rule everything else does. It
		// effectively always will; checking means a future change to either cannot
		// quietly produce a password the panel would then refuse.
		if err := CheckPasswordStrength(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", rlerr.Genericf("could not generate a password that passes the strength check")
}

// dummyHash is verified against when no account matches, so that a wrong address and
// a wrong password take the same time. Without it, the response time says which
// addresses have accounts, which is the first thing an attacker wants to know.
var dummyHash string

func init() {
	h, err := HashPassword("ratline-panel-timing-equaliser-" + strconv.Itoa(argonMemory))
	if err == nil {
		dummyHash = h
	}
}

// EqualiseTiming burns the same work a real verification would, for a sign-in
// attempt that has already failed for a reason the caller must not reveal.
func EqualiseTiming(password string) {
	if dummyHash == "" {
		return
	}
	_, _ = VerifyPassword(dummyHash, password)
}
