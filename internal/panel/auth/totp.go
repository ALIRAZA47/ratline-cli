package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 specifies HMAC-SHA1; authenticator apps implement that
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// TOTP, RFC 6238, as every authenticator app implements it: HMAC-SHA1, six digits,
// a thirty-second step.
//
// SHA-1 is not a choice made here. TOTP with SHA-256 is permitted by the RFC and
// supported by almost nothing, and a second factor nobody can enrol is not a second
// factor. Its use in HMAC is not affected by the collision attacks that retired SHA-1
// for signatures.
const (
	totpDigits = 6
	totpPeriod = 30 * time.Second
	// One step either side. Clock drift on a phone is real, and the alternative to
	// a window is a support burden; two steps of tolerance is 90 seconds of validity,
	// which is where most implementations land.
	totpSkew = 1
	// 20 bytes, the SHA-1 block size, which is what RFC 4226 recommends.
	totpSecretBytes = 20
)

// NewTOTPSecret returns a base32 secret in the form authenticator apps expect.
func NewTOTPSecret() (string, error) {
	b := make([]byte, totpSecretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "generating a second-factor secret")
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

// TOTPCode computes the code for a secret at a moment.
func TOTPCode(secret string, at time.Time) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	return hotp(key, uint64(at.UTC().Unix())/uint64(totpPeriod.Seconds())), nil
}

// VerifyTOTP reports whether a code is valid for the secret at that moment.
//
// The comparison is constant-time and every candidate step is checked, rather than
// returning on the first match: a loop that exits early tells an attacker measuring
// the response *which* step matched, and the whole cost here is three HMACs.
func VerifyTOTP(secret, code string, at time.Time) (bool, error) {
	code = strings.TrimSpace(strings.ReplaceAll(code, " ", ""))
	if len(code) != totpDigits {
		return false, nil
	}
	key, err := decodeSecret(secret)
	if err != nil {
		return false, err
	}
	counter := uint64(at.UTC().Unix()) / uint64(totpPeriod.Seconds())
	match := 0
	for i := -totpSkew; i <= totpSkew; i++ {
		c := counter
		if i < 0 {
			if c < uint64(-i) {
				continue
			}
			c -= uint64(-i)
		} else {
			c += uint64(i)
		}
		match |= subtle.ConstantTimeCompare([]byte(hotp(key, c)), []byte(code))
	}
	return match == 1, nil
}

// TOTPURI is the otpauth:// URI an authenticator app reads from a QR code.
func TOTPURI(secret, account, issuer string) string {
	label := issuer + ":" + account
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(int(totpPeriod.Seconds())))
	return "otpauth://totp/" + url.PathEscape(label) + "?" + q.Encode()
}

func decodeSecret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	if err != nil || len(key) == 0 {
		return nil, rlerr.Genericf("the stored second-factor secret is unreadable").
			WithHint("re-enrol the second factor for this account")
	}
	return key, nil
}

// hotp is RFC 4226's dynamic truncation.
func hotp(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, value%1_000_000)
}
