package validate

import (
	"net/url"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// RedisURI checks a Redis connection string before anything is done with it.
func RedisURI(uri string) error {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return rlerr.Usagef("the connection string is empty")
	}
	if !strings.HasPrefix(uri, "redis://") && !strings.HasPrefix(uri, "rediss://") {
		return rlerr.Usagef("that does not look like a Redis connection string").
			WithHint("it should begin with redis:// or rediss://")
	}
	u, err := url.Parse(uri)
	if err != nil {
		return rlerr.Usagef("that connection string is not a valid URI").
			WithHint("run 'ratline db connect --engine redis' and paste it at the prompt, where nothing interprets it")
	}
	if u.Host == "" {
		return rlerr.Usagef("that connection string has no host").
			WithHint("it should look like redis://:password@127.0.0.1:6379")
	}
	return nil
}

// Redis naming rules. Redis has no named databases; ratline models a "database" as an ACL
// user confined to a key-prefix keyspace. The keyspace name becomes part of an ACL pattern
// (~name:*), an ACL username and a connection URI, and ACL rules are a space-separated
// command language — so a name with a space, a glob metacharacter or a control byte could
// rewrite the rule. Names are held to a conservative charset where they enter, the same
// discipline as the SQL identifiers.

// RedisKeyspace checks a keyspace (the ratline "database") name.
func RedisKeyspace(name string) error {
	if name == "" {
		return rlerr.Usagef("the keyspace name is empty")
	}
	if len(name) > 64 {
		return rlerr.Usagef("the keyspace name is %d characters; keep it to 64 or fewer", len(name))
	}
	return redisIdent(name, "keyspace name")
}

// RedisUsername checks an ACL username.
func RedisUsername(name string) error {
	if name == "" {
		return rlerr.Usagef("the username is empty")
	}
	if len(name) > 64 {
		return rlerr.Usagef("the username is %d characters; keep it to 64 or fewer", len(name))
	}
	// "default" is Redis's own always-present user; provisioning over it would take the
	// server's own credential with it.
	if strings.EqualFold(name, "default") {
		return rlerr.Usagef("%q is Redis's own always-present user", name).
			WithHint("choose another name; ratline manages the default user's password itself")
	}
	return redisIdent(name, "username")
}

// redisIdent enforces the conservative charset: letters, digits, underscore and hyphen.
// This keeps the value safe as a bare token in an ACL rule (which is space-delimited), as
// a keyspace prefix in a ~pattern, and in a connection URI without percent-encoding.
func redisIdent(name, field string) error {
	if strings.ContainsRune(name, 0) {
		return rlerr.Usagef("the %s contains a NUL byte", field)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return rlerr.Usagef("the %s contains %q; use letters, digits, underscore or hyphen only", field, r).
				WithHint("the name becomes part of an ACL rule and a keyspace pattern, which are " +
					"space- and glob-sensitive")
		}
	}
	return nil
}

// redisRoles map a role name to what it grants inside the user's keyspace, described for
// help and `db roles`. Each is confined to the keyspace by the ~pattern the caller adds;
// these decide only which command categories are allowed.
var redisRoles = map[string]string{
	"read":      "read commands (+@read) within the keyspace",
	"readWrite": "read and write commands (+@read +@write) within the keyspace",
	"dbOwner":   "all commands (+@all) within the keyspace",
}

// RedisRole checks a role name.
func RedisRole(role string) error {
	if role == "" {
		return rlerr.Usagef("the role is empty")
	}
	if _, ok := redisRoles[role]; ok {
		return nil
	}
	var names []string
	for r := range redisRoles {
		names = append(names, r)
	}
	sortStrings(names)
	return rlerr.Usagef("%q is not a role ratline grants", role).
		WithHint("one of: %s", strings.Join(names, ", "))
}

// RedisRoles describes the grantable roles, for help text and `db roles`.
func RedisRoles() [][2]string {
	var names []string
	for r := range redisRoles {
		names = append(names, r)
	}
	sortStrings(names)
	out := make([][2]string, 0, len(names))
	for _, n := range names {
		out = append(out, [2]string{n, redisRoles[n]})
	}
	return out
}
