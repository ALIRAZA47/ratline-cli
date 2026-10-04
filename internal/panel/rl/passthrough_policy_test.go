package rl

import (
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
)

// `ratline nginx` and `ratline systemctl` take a free-form argument line and can mutate,
// so they are a super admin's; `ratline journalctl` can follow for ever, so a browser is
// never offered it at all.
func TestSystemToolPassthroughsInThePanel(t *testing.T) {
	cat := realCatalogue(t)
	for _, verb := range []string{"nginx", "systemctl"} {
		if _, _, found := Lookup(cat, verb, store.RoleAdmin); found {
			t.Errorf("%s is offered to an admin", verb)
		}
		if _, _, found := Lookup(cat, verb, store.RoleSuperAdmin); !found {
			t.Errorf("%s is not reachable by a super admin", verb)
		}
	}
	for _, role := range []string{store.RoleAdmin, store.RoleSuperAdmin} {
		if _, _, found := Lookup(cat, "journalctl", role); found {
			t.Errorf("journalctl is offered to %s", role)
		}
	}
}
