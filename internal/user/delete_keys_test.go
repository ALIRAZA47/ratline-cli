package user

import (
	"context"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

// A privilege audit that lists grants against a deleted account is worse than one that
// lists nothing: the reader cannot tell a stale row from a live one. Deleting a tenant
// left its keys behind, so `key list` showed a key for a user that no longer existed and
// `doctor` still reported the server clean.
func TestDeletingATenantTakesItsKeysWithIt(t *testing.T) {
	m, _, _ := sudoFixture(t, false)
	ctx := context.Background()

	if err := m.State.PutKey(ctx, &state.Key{
		ID: "k1", Fingerprint: "SHA256:aaa", Label: "laptop",
		Algorithm: "ssh-ed25519", Scope: "user", Owner: "alice",
	}); err != nil {
		t.Fatalf("seeding a key: %v", err)
	}
	// A key belonging to somebody else must survive.
	if err := m.State.PutKey(ctx, &state.Key{
		ID: "k2", Fingerprint: "SHA256:bbb", Label: "ops",
		Algorithm: "ssh-ed25519", Scope: "global",
	}); err != nil {
		t.Fatalf("seeding a global key: %v", err)
	}

	if err := m.Delete(ctx, DeleteOptions{Name: "alice", Purge: true}); err != nil {
		t.Fatalf("deleting the tenant: %v", err)
	}

	left, err := m.State.ListKeys(ctx, state.KeyFilter{IncludeRevoked: true})
	if err != nil {
		t.Fatalf("listing keys: %v", err)
	}
	for _, k := range left {
		if k.Scope == "user" && k.Owner == "alice" {
			t.Errorf("the deleted tenant still has key %s (%s) in state", k.ID, k.Label)
		}
	}
	var globals int
	for _, k := range left {
		if k.Scope == "global" {
			globals++
		}
	}
	if globals != 1 {
		t.Errorf("global keys = %d, want 1: deleting a tenant must not touch anybody else's", globals)
	}
}
