package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash is not PHC-encoded argon2id: %q", hash)
	}
	ok, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil || !ok {
		t.Fatalf("the right password did not verify: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(hash, "correct horse battery stapl")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Fatal("a wrong password verified")
	}
}

// Two hashes of the same password must differ, or the salt is not doing anything and
// a stolen database can be attacked once for every account at a time.
func TestPasswordHashesAreSalted(t *testing.T) {
	a, err := HashPassword("the same password twice")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("the same password twice")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two hashes of one password are identical, so the salt is not random")
	}
}

func TestVerifyRefusesMalformedHash(t *testing.T) {
	for _, encoded := range []string{
		"", "not-a-hash", "$argon2id$v=19$m=65536",
		"$bcrypt$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=1$m=65536,t=3,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=x,t=3,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=4$!!!$aGFzaA",
	} {
		ok, err := VerifyPassword(encoded, "anything")
		if ok {
			t.Fatalf("a malformed hash %q verified, which is the worst possible failure", encoded)
		}
		if err == nil {
			t.Errorf("a malformed hash %q was rejected silently; it should say so", encoded)
		}
	}
}

func TestPasswordStrength(t *testing.T) {
	tests := []struct {
		password string
		wantErr  bool
		why      string
	}{
		{"short", true, "under the length floor"},
		{"password1234", false, "long enough and not on the list"},
		{"passw0rd", true, "on the list an attacker tries first"},
		{"aaaaaaaaaaaaaaaa", true, "too few distinct characters"},
		{"correct horse battery staple", false, "four words"},
		{strings.Repeat("x", 2000), true, "long enough to be a denial of service on the hasher"},
	}
	for _, tt := range tests {
		err := CheckPasswordStrength(tt.password)
		if (err != nil) != tt.wantErr {
			t.Errorf("CheckPasswordStrength(%q) error = %v, want error: %v (%s)",
				tt.password, err, tt.wantErr, tt.why)
		}
	}
}

// The RFC 6238 appendix B vectors, with the SHA-1 seed and six digits — the
// configuration every authenticator app implements.
func TestTOTPMatchesRFC6238(t *testing.T) {
	// "12345678901234567890" in base32, which is the RFC's seed.
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	tests := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	}
	for _, tt := range tests {
		got, err := TOTPCode(secret, time.Unix(tt.unix, 0).UTC())
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		if got != tt.want {
			t.Errorf("TOTPCode at %d = %s, want %s", tt.unix, got, tt.want)
		}
	}
}

func TestVerifyTOTPAcceptsTheWindowAndNothingElse(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	code, err := TOTPCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}

	for _, offset := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		ok, err := VerifyTOTP(secret, code, now.Add(offset))
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Errorf("a code %s from the accepted window was refused", offset)
		}
	}
	// Two steps away is outside the window. Without this the test would pass for an
	// implementation that accepted every code ever generated.
	for _, offset := range []time.Duration{-90 * time.Second, 90 * time.Second} {
		ok, err := VerifyTOTP(secret, code, now.Add(offset))
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Errorf("a code %s outside the window was accepted", offset)
		}
	}
}

func TestVerifyTOTPRejectsWrongLengthWithoutTouchingTheSecret(t *testing.T) {
	ok, err := VerifyTOTP("not valid base32 !!!", "123", time.Now())
	if err != nil {
		t.Fatalf("a short code should be refused before the secret is decoded: %v", err)
	}
	if ok {
		t.Fatal("a three-digit code was accepted")
	}
}

func TestTOTPURIIsScannable(t *testing.T) {
	uri := TOTPURI("ABCDEFGHIJKLMNOP", "ops@example.com", "ratline")
	for _, want := range []string{
		"otpauth://totp/", "secret=ABCDEFGHIJKLMNOP", "issuer=ratline",
		"algorithm=SHA1", "digits=6", "period=30",
	} {
		if !strings.Contains(uri, want) {
			t.Errorf("the provisioning URI is missing %q: %s", want, uri)
		}
	}
}

// The token itself must not be recoverable from what is stored, and the same token
// must always hash the same way or no session would ever be found again.
func TestTokenHashing(t *testing.T) {
	token, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || hash == "" {
		t.Fatal("NewToken returned an empty token or hash")
	}
	if strings.Contains(hash, token) {
		t.Fatal("the stored hash contains the token")
	}
	if HashToken(token) != hash {
		t.Fatal("hashing the same token twice gave two answers")
	}
	other, _, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if other == token {
		t.Fatal("two tokens came back identical")
	}
}
