package mongod

import (
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/templates"
)

// The embedded keys become a root of trust for packages apt installs as root, so the
// decode is tested against the real keys, not synthetic fixtures: a key that fails to
// dearmor here would fail on the server, and this is the cheaper place to find out.
func TestDearmorEmbeddedKeys(t *testing.T) {
	for _, version := range []string{"7.0", "8.0"} {
		asc, err := templates.FS.ReadFile("mongo/server-" + version + ".asc")
		if err != nil {
			t.Fatalf("no embedded key for %s: %v", version, err)
		}
		raw, err := Dearmor(asc)
		if err != nil {
			t.Fatalf("dearmoring the %s key: %v", version, err)
		}
		// An OpenPGP public-key packet in the old format starts with 0x99 (tag 6,
		// two-octet length) — what gpg --dearmor would have produced.
		if len(raw) == 0 || raw[0] != 0x99 {
			t.Errorf("the %s key did not decode to a public-key packet (first byte %#x)", version, raw[0])
		}
	}
}

func TestDearmorRefusesACorruptedKey(t *testing.T) {
	asc, err := templates.FS.ReadFile("mongo/server-8.0.asc")
	if err != nil {
		t.Fatal(err)
	}
	// Flip one character of the base64 body. The checksum must catch it — this is the
	// assertion that the CRC check is real, not decoration.
	body := string(asc)
	i := strings.Index(body, "\n\n")
	if i < 0 {
		// The 8.0 key has no armor headers; the body starts right after BEGIN.
		i = strings.Index(body, "-----\n")
	}
	target := i + 10
	flipped := body[:target]
	if body[target] == 'A' {
		flipped += "B"
	} else {
		flipped += "A"
	}
	flipped += body[target+1:]
	if flipped == body {
		t.Fatal("the corruption did not change the input, so the test would prove nothing")
	}
	if _, err := Dearmor([]byte(flipped)); err == nil {
		t.Error("a corrupted key was dearmored without complaint")
	}
}

func TestDearmorRefusesNonKeys(t *testing.T) {
	for name, input := range map[string]string{
		"empty":     "",
		"plaintext": "deb https://example.invalid stable main\n",
		"bare":      "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\n=ABCD\n-----END PGP PUBLIC KEY BLOCK-----\n",
	} {
		if _, err := Dearmor([]byte(input)); err == nil {
			t.Errorf("%s input was dearmored without complaint", name)
		}
	}
}
