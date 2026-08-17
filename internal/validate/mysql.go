package validate

import (
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// MySQL / MariaDB naming rules. Database and user names become SQL identifiers, and an
// identifier cannot be parameterized — the server takes it as a literal object name — so
// this validation is the security boundary, not a convenience. Anything outside a
// conservative identifier charset is refused rather than quoted, the same discipline the
// nginx and systemd renderers use: validate where the value enters, not where it is
// written.

var mysqlReservedDatabases = []string{"mysql", "information_schema", "performance_schema", "sys"}

// IsMySQLSystemDatabase reports whether a name is one of the server's own schemas.
func IsMySQLSystemDatabase(name string) bool {
	for _, r := range mysqlReservedDatabases {
		if strings.EqualFold(name, r) {
			return true
		}
	}
	return false
}

// MySQLDatabaseName checks a MySQL/MariaDB database (schema) name.
func MySQLDatabaseName(name string) error {
	if name == "" {
		return rlerr.Usagef("the database name is empty")
	}
	if len(name) > 64 {
		return rlerr.Usagef("the database name is %d characters; MySQL limits identifiers to 64", len(name))
	}
	if err := mysqlIdent(name, "database name"); err != nil {
		return err
	}
	if IsMySQLSystemDatabase(name) {
		return rlerr.Usagef("%q is one of MySQL's own schemas", name).
			WithHint("mysql, information_schema, performance_schema and sys belong to the server; " +
				"provisioning inside them can destroy its accounts or its metadata")
	}
	return nil
}

// MySQLUsername checks a MySQL/MariaDB account name (the user part of user@host).
func MySQLUsername(name string) error {
	if name == "" {
		return rlerr.Usagef("the database username is empty")
	}
	// MySQL caps the user part of an account name at 32 characters.
	if len(name) > 32 {
		return rlerr.Usagef("the database username is %d characters; MySQL limits account names to 32", len(name))
	}
	return mysqlIdent(name, "database username")
}

// mysqlIdent enforces the conservative identifier charset: start with a letter or
// underscore, then letters, digits and underscores. This is a strict subset of what MySQL
// permits for a backtick-quoted identifier, chosen so the name is safe as a bare
// identifier in a CREATE/GRANT, safe as an account name, and safe in a connection URI
// without percent-encoding.
func mysqlIdent(name, field string) error {
	if strings.ContainsRune(name, 0) {
		return rlerr.Usagef("the %s contains a NUL byte", field)
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return rlerr.Usagef("the %s starts with a digit: %q", field, name).
					WithHint("start with a letter or underscore")
			}
		default:
			return rlerr.Usagef("the %s contains %q; use letters, digits and underscore only", field, r).
				WithHint("the name becomes a SQL identifier, which cannot be safely quoted away")
		}
	}
	return nil
}

// mysqlRoles are the roles ratline grants, mapped to a human description. Each is scoped
// to a single database; the account-management and server-wide privileges are absent on
// purpose, the same reasoning as the MongoDB role list — a tenant's application must not be
// able to reach another tenant's schema.
var mysqlRoles = map[string]string{
	"read":      "SELECT on every table in the database",
	"readWrite": "SELECT, INSERT, UPDATE, DELETE on every table in the database",
	"dbOwner":   "ALL PRIVILEGES on the database, for this database only",
}

// MySQLRole checks a role name against the roles ratline will grant.
func MySQLRole(role string) error {
	if role == "" {
		return rlerr.Usagef("the role is empty")
	}
	if _, ok := mysqlRoles[role]; ok {
		return nil
	}
	var names []string
	for r := range mysqlRoles {
		names = append(names, r)
	}
	sortStrings(names)
	e := rlerr.Usagef("%q is not a role ratline grants", role).
		WithHint("one of: %s", strings.Join(names, ", "))
	switch strings.ToUpper(role) {
	case "ALL", "ALL PRIVILEGES", "SUPER", "GRANT OPTION":
		return e.WithField("why_not", "a server-wide privilege would give this user every other "+
			"database on the server, including the ones belonging to other tenants")
	}
	return e
}

// MySQLRoles describes the grantable roles, for help text and `db roles`.
func MySQLRoles() [][2]string {
	var names []string
	for r := range mysqlRoles {
		names = append(names, r)
	}
	sortStrings(names)
	out := make([][2]string, 0, len(names))
	for _, n := range names {
		out = append(out, [2]string{n, mysqlRoles[n]})
	}
	return out
}
