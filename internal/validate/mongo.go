package validate

import (
	"net/url"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// MongoDB's own naming rules, enforced here rather than discovered later.
//
// The server rejects most of these itself, but its errors arrive as a wall of
// driver output well after the command was accepted — and some it does not reject at
// all, it just behaves oddly. A name with a dot in it, for instance, is accepted by
// createUser and then cannot be addressed unambiguously in a role document.
//
// These are also the values that end up in a connection URI written to a site's .env,
// so anything needing percent-encoding to survive a URI is refused rather than escaped.
// An operator who cannot type the name into a shell will not enjoy owning it.

// mongoReservedDatabases are the server's own databases. Provisioning inside them is
// how an operator destroys their cluster's credentials or its oplog.
var mongoReservedDatabases = []string{"admin", "local", "config"}

// IsMongoSystemDatabase reports whether a name is one of MongoDB's own.
//
// Separate from DatabaseName because the two questions differ: "may ratline create this"
// is a policy, and "does this belong to the server" is a fact. Conflating them made
// `db list --live` hide every database whose name ratline would not have chosen, which is
// the opposite of what that flag is for.
func IsMongoSystemDatabase(name string) bool {
	for _, reserved := range mongoReservedDatabases {
		if strings.EqualFold(name, reserved) {
			return true
		}
	}
	return false
}

// DatabaseName checks a MongoDB database name.
func DatabaseName(name string) error {
	if name == "" {
		return rlerr.Usagef("the database name is empty")
	}
	// MongoDB's own limit is 64 bytes for the namespace; a name near that leaves no
	// room for a collection, and the server's refusal names bytes rather than the name.
	if len(name) > 38 {
		return rlerr.Usagef("the database name is %d characters; keep it to 38 or fewer", len(name)).
			WithHint("MongoDB limits the whole namespace — database, dot, collection — to 64 bytes")
	}
	// The server forbids these outright on Windows and in most drivers; refusing them
	// everywhere keeps a database portable between hosts.
	if bad := strings.IndexAny(name, ` /\."$*<>:|?`); bad >= 0 {
		return rlerr.Usagef("the database name contains %q, which MongoDB does not allow", name[bad]).
			WithHint("letters, digits, hyphen and underscore are safe everywhere")
	}
	if strings.ContainsAny(name, "\x00") {
		return rlerr.Usagef("the database name contains a NUL byte")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return rlerr.Usagef("the database name contains %q; use letters, digits, hyphen or underscore", r)
		}
	}
	if IsMongoSystemDatabase(name) {
		return rlerr.Usagef("%q is one of MongoDB's own databases", name).
			WithHint("admin, local and config belong to the server; provisioning inside " +
				"them can destroy its credentials or its replication log")
	}
	return nil
}

// DatabaseUsername checks a MongoDB username.
//
// Deliberately stricter than the server, which accepts almost anything: the name goes
// into a connection URI, and a URI that needs percent-encoding to be valid is a
// support ticket rather than a feature.
func DatabaseUsername(name string) error {
	if name == "" {
		return rlerr.Usagef("the database username is empty")
	}
	if len(name) > 63 {
		return rlerr.Usagef("the database username is %d characters; keep it to 63 or fewer", len(name))
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return rlerr.Usagef("the database username contains %q", r).
				WithHint("letters, digits, hyphen, underscore and dot only — the name goes into " +
					"a connection URI, and anything else would have to be percent-encoded")
		}
	}
	// A leading dot or a run of them reads as a namespace separator to anything parsing
	// the name later.
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") {
		return rlerr.Usagef("the database username has a misplaced dot: %q", name)
	}
	return nil
}

// mongoRoles are the built-in roles ratline will grant. Deliberately a short list:
// every one of them is scoped to a single database.
//
// The cluster-wide roles — root, userAdminAnyDatabase, dbAdminAnyDatabase — are
// absent on purpose. Granting one to a tenant's application gives it every other
// tenant's data, which is the whole thing ratline exists to prevent, and it would be a
// one-word flag away if the list were open.
var mongoRoles = map[string]string{
	"read":      "read every collection in the database",
	"readWrite": "read and write every collection in the database",
	"dbAdmin":   "manage indexes and collection statistics, but not read the data",
	"dbOwner":   "readWrite plus dbAdmin plus userAdmin, for this database only",
}

// DatabaseRole checks a role name against the roles ratline will grant.
func DatabaseRole(role string) error {
	if role == "" {
		return rlerr.Usagef("the role is empty")
	}
	if _, ok := mongoRoles[role]; ok {
		return nil
	}
	// Named explicitly, because "invalid role" sends an operator to MongoDB's manual to
	// find a role this will still refuse.
	var names []string
	for r := range mongoRoles {
		names = append(names, r)
	}
	sortStrings(names)
	e := rlerr.Usagef("%q is not a role ratline grants", role).
		WithHint("one of: %s", strings.Join(names, ", "))
	switch role {
	case "root", "userAdminAnyDatabase", "dbAdminAnyDatabase", "readWriteAnyDatabase", "readAnyDatabase":
		return e.WithField("why_not", "a cluster-wide role would give this user every other "+
			"database on the server, including the ones belonging to other tenants")
	}
	return e
}

// DatabaseRoles describes the grantable roles, for help text and `db roles`.
func DatabaseRoles() [][2]string {
	var names []string
	for r := range mongoRoles {
		names = append(names, r)
	}
	sortStrings(names)
	out := make([][2]string, 0, len(names))
	for _, n := range names {
		out = append(out, [2]string{n, mongoRoles[n]})
	}
	return out
}

// sortStrings is a small insertion sort, so this file does not pull in sort for one use.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// MongoURI checks a MongoDB connection string before anything is done with it.
//
// This exists because the check that was here — "does it start with mongodb://" — let a
// mangled string through, and the real parse happened much later, inside the code that
// reads the file back. So an operator whose shell had eaten their connection string got
// told the file was invalid, naming a path they had never written to, from a command that
// said in the same breath that nothing had been stored.
//
// The specific case that produced it: a password containing a percent sign, passed through
// `printf`, which read it as a format verb and truncated the string mid-URI. What arrived
// was `mongodb://admin:PASSWORD` with no host at all — which `url.Parse` reads as a port,
// giving "invalid port after host". Hence the explicit host check and its own message: the
// operator's problem is a missing host, not a malformed port.
func MongoURI(uri string) error {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return rlerr.Usagef("the connection string is empty")
	}
	if !strings.HasPrefix(uri, "mongodb://") && !strings.HasPrefix(uri, "mongodb+srv://") {
		return rlerr.Usagef("that does not look like a MongoDB connection string").
			WithHint("it should begin with mongodb:// or mongodb+srv://")
	}

	// The authority is everything between the scheme and the first / or ?.
	rest := uri[strings.Index(uri, "//")+2:]
	authority := rest
	if i := strings.IndexAny(authority, "/?"); i >= 0 {
		authority = authority[:i]
	}
	// Userinfo cannot contain a bare @ — it must be percent-encoded — so the last one
	// separates the credentials from the host.
	hasCredentials := strings.Contains(authority, "@")

	u, err := url.Parse(uri)
	if err != nil {
		// url.Parse describes the parser's confusion, not the operator's mistake. The
		// commonest mistake by far produces "invalid port", because a truncated string
		// leaves `user:password` where `host:port` was expected — so it is worth saying
		// what that actually means before repeating the parser.
		if !hasCredentials {
			return rlerr.Usagef("that connection string has no @, so there is no host in it").
				WithHint("it should look like mongodb://user:password@host:27017/?authSource=admin.\n"+
					"        What is there reads as host %q with an unusable port, which is what a\n"+
					"        shell leaves behind when it truncates the password — printf treats a %% "+
					"as a format verb.\n        Run 'ratline db connect' with no flags and paste it "+
					"at the prompt; nothing interprets it there.",
					hostOf(authority))
		}
		return rlerr.Usagef("that connection string is not a valid URI").
			WithHint("if the password contains %% or !, a shell may have mangled it before " +
				"ratline saw it. Run 'ratline db connect' with no flags and paste it at " +
				"the prompt instead, where nothing interprets it")
	}
	if u.Host == "" {
		return rlerr.Usagef("that connection string has no host").
			WithHint("it should look like mongodb://user:password@host:27017/?authSource=admin")
	}
	// SRV records carry the port, and mongosh refuses a +srv URI that also names one.
	if strings.HasPrefix(uri, "mongodb+srv://") && u.Port() != "" {
		return rlerr.Usagef("a mongodb+srv:// connection string must not name a port").
			WithHint("SRV records supply the ports; drop the :%s", u.Port())
	}
	return nil
}

// hostOf returns the part of an authority before the first colon, for a message that
// names what the parser actually saw.
func hostOf(authority string) string {
	if i := strings.Index(authority, ":"); i >= 0 {
		return authority[:i]
	}
	return authority
}
