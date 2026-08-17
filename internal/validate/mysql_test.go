package validate

import "testing"

func TestMySQLDatabaseName(t *testing.T) {
	good := []string{"shop", "shop_app", "Analytics2", "_internal"}
	for _, n := range good {
		if err := MySQLDatabaseName(n); err != nil {
			t.Errorf("MySQLDatabaseName(%q) = %v, want nil", n, err)
		}
	}
	// The dangerous ones: anything that could break out of an identifier, since the name
	// is not parameterizable. A backtick, quote, space, semicolon or comment must never
	// reach a GRANT.
	bad := []string{
		"", "1shop", "shop db", "shop`; DROP", "shop'--", "shop\"x", "shop;go",
		"shop-app", "sh.op", "shop\x00", "café",
		"mysql", "information_schema", "PERFORMANCE_SCHEMA", "sys",
	}
	for _, n := range bad {
		if err := MySQLDatabaseName(n); err == nil {
			t.Errorf("MySQLDatabaseName(%q) was accepted", n)
		}
	}
}

func TestMySQLUsername(t *testing.T) {
	if err := MySQLUsername("shop_app"); err != nil {
		t.Errorf("shop_app: %v", err)
	}
	for _, n := range []string{"", "1user", "a'b", "a`b", "a b", "root@localhost"} {
		if err := MySQLUsername(n); err == nil {
			t.Errorf("MySQLUsername(%q) was accepted", n)
		}
	}
	// 33 characters is past MySQL's 32-char account-name limit.
	long := ""
	for i := 0; i < 33; i++ {
		long += "a"
	}
	if err := MySQLUsername(long); err == nil {
		t.Error("a 33-character username was accepted")
	}
}

func TestMySQLRole(t *testing.T) {
	for _, r := range []string{"read", "readWrite", "dbOwner"} {
		if err := MySQLRole(r); err != nil {
			t.Errorf("MySQLRole(%q) = %v, want nil", r, err)
		}
	}
	// Server-wide privileges are refused, and the refusal explains why.
	for _, r := range []string{"", "ALL", "SUPER", "nonsense"} {
		if err := MySQLRole(r); err == nil {
			t.Errorf("MySQLRole(%q) was accepted", r)
		}
	}
	if len(MySQLRoles()) != 3 {
		t.Errorf("MySQLRoles() returned %d roles, want 3", len(MySQLRoles()))
	}
}
