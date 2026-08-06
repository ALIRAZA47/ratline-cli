package sshkey

import (
	"context"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
)

// Reported from a real server: a public key pasted at the interactive prompt was read as
// a filename, and the error named "no such file: /root/ssh-ed25519 AAAAC3Nz… ark@ark".
// Asked for a key and handed a key, taking it is the only sensible reading.
func TestAPastedKeyIsTakenAsTheKey(t *testing.T) {
	m := &Manager{Cfg: config.Default(), Log: log.Discard()}
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOwLeZQuGgvewm+UF2V+CKV+7zpl3eKOkJZdxXD8liJK ark@ark"

	got, err := m.readKeyRef(context.Background(), key, nil)
	if err != nil {
		t.Fatalf("a pasted key was refused: %v", err)
	}
	if string(got) != key {
		t.Errorf("got %q, want the key itself", got)
	}
	// And it still goes through the real validator, rather than being trusted.
	if _, _, err := Parse(string(got), m.Policy()); err != nil {
		t.Errorf("the pasted key does not parse: %v", err)
	}
}

func TestEveryKeyTypeIsRecognisedAsMaterial(t *testing.T) {
	for _, ref := range []string{
		"ssh-ed25519 AAAAC3Nz ark@ark",
		"ssh-rsa AAAAB3Nz ark@ark",
		"ecdsa-sha2-nistp256 AAAAE2 ark@ark",
		"sk-ssh-ed25519@openssh.com AAAAG ark@ark",
		"  ssh-ed25519 AAAAC3Nz ark@ark  ", // pasted with whitespace
	} {
		if !looksLikeAKey(ref) {
			t.Errorf("looksLikeAKey(%q) = false, want true", ref)
		}
	}
}

// A path must still be a path, or this change would break every existing invocation.
func TestPathsAreStillPaths(t *testing.T) {
	for _, ref := range []string{
		"~/.ssh/id_ed25519.pub",
		"/root/keys/ci.pub",
		"key.pub",
		"-",
		"https://github.com/ark.keys",
	} {
		if looksLikeAKey(ref) {
			t.Errorf("looksLikeAKey(%q) = true, want false: it is a reference, not material", ref)
		}
	}
}

// A private key pasted by mistake must reach the parser, which says so plainly, rather
// than being reported as a missing file.
func TestAPastedPrivateKeyIsNamedAsSuch(t *testing.T) {
	m := &Manager{Cfg: config.Default(), Log: log.Discard()}
	const priv = "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1r\n-----END OPENSSH PRIVATE KEY-----"

	body, err := m.readKeyRef(context.Background(), priv, nil)
	if err != nil {
		t.Fatalf("reading it should defer to the parser, not fail here: %v", err)
	}
	_, _, perr := Parse(string(body), m.Policy())
	if perr == nil {
		t.Fatal("a private key was accepted")
	}
	if !strings.Contains(perr.Error(), "private key") {
		t.Errorf("error = %q, want it to say this is a private key", perr)
	}
}
