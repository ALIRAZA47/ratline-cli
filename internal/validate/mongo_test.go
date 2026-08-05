package validate

import (
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

func TestDatabaseNamesMongoDBCannotAddress(t *testing.T) {
	// A dot is a namespace separator, so a database with one cannot be named
	// unambiguously in a role document — the server accepts createUser and the grant
	// then applies to something else. The rest the server rejects itself, but its
	// refusal arrives as driver output long after the command was accepted.
	for _, name := range []string{
		"", "shop.example.com", "with space", "back\\slash", `quo"te`,
		"dollar$sign", "star*", "less<", "greater>", "colon:", "pipe|", "question?",
		"slash/es", "emoji🎉", "tab\there",
		strings.Repeat("a", 39),
	} {
		if err := DatabaseName(name); err == nil {
			t.Errorf("DatabaseName(%q) was accepted", name)
		}
	}
	for _, name := range []string{"shop", "shop_prod", "shop-prod", "a", "A1", strings.Repeat("a", 38)} {
		if err := DatabaseName(name); err != nil {
			t.Errorf("DatabaseName(%q) was refused: %v", name, err)
		}
	}
}

func TestMongoDBsOwnDatabasesAreRefused(t *testing.T) {
	// Provisioning inside admin destroys the server's credentials; inside local, its
	// replication log. Both are recoverable only from a backup.
	for _, name := range []string{"admin", "local", "config", "ADMIN", "Local"} {
		err := DatabaseName(name)
		if err == nil {
			t.Fatalf("DatabaseName(%q) was accepted", name)
		}
		if !strings.Contains(err.Error()+rlerr.Hint(err), "server") {
			t.Errorf("the refusal for %q should say why, got: %v", name, err)
		}
	}
}

func TestDatabaseUsernamesThatWouldBreakAURI(t *testing.T) {
	// The name goes into a connection string. Anything needing percent-encoding is
	// refused rather than escaped: an operator who cannot type the name into a shell
	// will not enjoy owning it.
	for _, name := range []string{
		"", "with space", "at@sign", "slash/es", "colon:", "hash#",
		".leading", "trailing.", "double..dot", "per%cent", "quo'te",
		strings.Repeat("u", 64),
	} {
		if err := DatabaseUsername(name); err == nil {
			t.Errorf("DatabaseUsername(%q) was accepted", name)
		}
	}
	for _, name := range []string{"shop_app", "shop-app", "app.reader", "A1", "u"} {
		if err := DatabaseUsername(name); err != nil {
			t.Errorf("DatabaseUsername(%q) was refused: %v", name, err)
		}
	}
}

func TestClusterWideRolesAreRefusedAndSayWhy(t *testing.T) {
	// This is the whole isolation argument. A tenant's application with
	// readWriteAnyDatabase has every other tenant's data, and it would be one flag away
	// if the role list were open.
	for _, role := range []string{
		"root", "readWriteAnyDatabase", "readAnyDatabase",
		"userAdminAnyDatabase", "dbAdminAnyDatabase",
	} {
		err := DatabaseRole(role)
		if err == nil {
			t.Fatalf("DatabaseRole(%q) was accepted — that grants every tenant's data", role)
		}
		// The refusal has to explain itself, or somebody will assume it is an oversight
		// and reach for the MongoDB shell instead.
		if !strings.Contains(err.Error()+rlerr.Hint(err), "not a role ratline grants") {
			t.Errorf("DatabaseRole(%q) should name what is allowed, got: %v", role, err)
		}
	}
	for _, role := range []string{"read", "readWrite", "dbAdmin", "dbOwner"} {
		if err := DatabaseRole(role); err != nil {
			t.Errorf("DatabaseRole(%q) was refused: %v", role, err)
		}
	}
	// Not case-insensitive: MongoDB's role names are case-sensitive, and accepting
	// "readwrite" here would produce a server-side error much later.
	for _, role := range []string{"readwrite", "READ", "ReadWrite"} {
		if err := DatabaseRole(role); err == nil {
			t.Errorf("DatabaseRole(%q) was accepted; MongoDB role names are case-sensitive", role)
		}
	}
}

func TestEveryGrantableRoleIsDescribed(t *testing.T) {
	// `db roles` prints these, and a role with no description reads as a bug.
	roles := DatabaseRoles()
	if len(roles) == 0 {
		t.Fatal("no roles are described")
	}
	for _, r := range roles {
		if r[0] == "" || r[1] == "" {
			t.Errorf("role %q has no description", r[0])
		}
		if err := DatabaseRole(r[0]); err != nil {
			t.Errorf("%q is described but not grantable: %v", r[0], err)
		}
	}
	// Sorted, so the help output does not change order between runs.
	for i := 1; i < len(roles); i++ {
		if roles[i-1][0] > roles[i][0] {
			t.Errorf("DatabaseRoles is not sorted: %q before %q", roles[i-1][0], roles[i][0])
		}
	}
}

func TestSystemDatabasesAreAFactSeparateFromThePolicy(t *testing.T) {
	// Two different questions. "May ratline create this name" is a policy — 38 characters,
	// no dots, no spaces. "Does this database belong to the server" is a fact about
	// MongoDB. Conflating them made `db list --live` hide every database whose name
	// ratline would not have chosen, which is the opposite of what that flag is for: a
	// database created outside ratline is exactly the one worth surfacing, because nothing
	// will revoke its users when the tenant goes.
	for _, name := range []string{"admin", "local", "config", "ADMIN", "Local", "CONFIG"} {
		if !IsMongoSystemDatabase(name) {
			t.Errorf("IsMongoSystemDatabase(%q) = false; it belongs to the server", name)
		}
	}
	// Names ratline would refuse to create, but which are still somebody's data and must
	// therefore be listed rather than hidden.
	for _, name := range []string{
		"legacy.reporting", // a dot: ratline refuses it, mongod accepted it
		"a-name-far-longer-than-ratline-would-ever-choose-to-create",
		"Mixed Case With Spaces",
		"shop",
	} {
		if IsMongoSystemDatabase(name) {
			t.Errorf("IsMongoSystemDatabase(%q) = true; it is not one of MongoDB's own", name)
		}
		if name != "shop" && DatabaseName(name) == nil {
			t.Errorf("DatabaseName(%q) should still refuse to *create* it", name)
		}
	}
}
