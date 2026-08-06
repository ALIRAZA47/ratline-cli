// Package mongo provisions MongoDB databases and users.
//
// What it does not do: install or configure a MongoDB server. ratline configures nginx
// and drives certbot without installing either, and the same reasoning applies here —
// a database server is a stateful thing with backups and a replication topology, and a
// provisioning tool that silently apt-gets one has made a decision that belongs to
// whoever owns the data. So this connects to a MongoDB it is given and manages what
// lives inside it, which works identically for a local mongod and for Atlas.
//
// Every operation goes through one static JavaScript file, embedded in the binary, run
// as `mongosh --nodb --quiet --file`. Two things follow from that, both deliberate:
//
//   - No operator input is ever interpolated into JavaScript. Values reach the script
//     through the environment. The alternative — building an --eval string — means a
//     username containing a quote can close it and run whatever follows, as root,
//     against a server holding every tenant's data.
//   - The admin URI never appears in argv. mongosh normally takes the connection string
//     as its first argument and /proc/PID/cmdline is world-readable, so any local user
//     could read the admin password of every database on the box. --nodb lets the script
//     connect from inside, using the environment, which is readable only by the owner.
package mongo

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
	"github.com/ALIRAZA47/ratline-cli/templates"
)

// Manager runs MongoDB operations and records them in state.
type Manager struct {
	Cfg    *config.Config
	Log    *log.Logger
	Runner system.Runner
	Bins   *system.Binaries
	State  *state.Store
	DryRun bool
}

// Result is the JSON one run of the script prints.
type Result struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Detail string `json:"detail,omitempty"`

	// Whatever the operation returned. Decoded per-call rather than modelled here,
	// because each operation answers a different question.
	Raw json.RawMessage `json:"-"`
}

// ServerInfo is what ping reports.
type ServerInfo struct {
	Version     string `json:"version"`
	Topology    string `json:"topology"`
	AuthEnabled bool   `json:"auth_enabled"`
}

// LiveDatabase is one entry from the server's own listing, as opposed to ratline's index.
type LiveDatabase struct {
	Name       string `json:"name"`
	SizeOnDisk int64  `json:"size_on_disk"`
	Empty      bool   `json:"empty"`
}

// LiveUser is one entry from the server's own user listing.
type LiveUser struct {
	Username   string   `json:"username"`
	AuthDB     string   `json:"auth_db"`
	Roles      []string `json:"roles"`
	Mechanisms []string `json:"mechanisms"`
}

// Stats is what a database actually holds.
type Stats struct {
	Database    string   `json:"database"`
	Collections int      `json:"collections"`
	Objects     int64    `json:"objects"`
	DataSize    int64    `json:"data_size"`
	StorageSize int64    `json:"storage_size"`
	Indexes     int      `json:"indexes"`
	IndexSize   int64    `json:"index_size"`
	Names       []string `json:"names"`
}

// AdminURI returns the connection string ratline manages the server with.
//
// Read from a file rather than from config.yaml, and held to the same rules as the DNS
// provider credentials: it is a root password for every database on the server, and
// config.yaml is a file operators paste into tickets.
func (m *Manager) AdminURI() (string, error) {
	path := m.Cfg.Paths.MongoURIFile
	if path == "" {
		return "", rlerr.Preconditionf("paths.mongo_uri_file is not configured")
	}
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", rlerr.Preconditionf("there is no MongoDB admin connection string at %s", path).
				WithHint("run 'ratline db connect' and paste it at the prompt — it creates the\n" +
					"        directory, writes the file 0600 root-owned, and checks the\n" +
					"        credentials before keeping any of it")
		}
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", path)
	}
	// A URI any local user can read is the whole server's credentials leaked. Refused
	// rather than warned about, the same as the DNS credentials file.
	if fi.Mode().Perm()&0o077 != 0 {
		return "", rlerr.Preconditionf("%s is mode %04o, which lets other accounts read the "+
			"admin password for every database on this server", path, fi.Mode().Perm()).
			WithHint("chmod 0600 %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", path)
	}
	uri := strings.TrimSpace(string(raw))
	if uri == "" {
		return "", rlerr.Preconditionf("%s is empty", path)
	}
	// The same rule `db connect` applies to the string before storing it. Two copies of
	// this check drifted apart once already: connect tested only the prefix, so a string
	// that could not be parsed was written to disk and rejected here instead — by a
	// message naming this file, in a command that had said nothing was stored.
	if err := validate.MongoURI(uri); err != nil {
		return "", rlerr.Wrap(err, rlerr.CodePrecondition, "the connection string in %s is unusable", path).
			WithHint("replace it with 'ratline db connect --force'")
	}
	return uri, nil
}

// run executes one operation and decodes its JSON result.
//
// env carries the parameters. Nothing is formatted into a command line, and the caller
// never sees the URI in an error: a failure message that quotes the connection string
// puts the admin password into the audit log.
func (m *Manager) run(ctx context.Context, op string, env map[string]string) (json.RawMessage, error) {
	if m.Bins != nil && !m.Bins.Available("mongosh") {
		return nil, rlerr.Preconditionf("mongosh is not installed, so ratline cannot talk to MongoDB").
			WithHint("apt-get install mongodb-mongosh, or see " +
				"https://www.mongodb.com/docs/mongodb-shell/install/")
	}
	uri, err := m.AdminURI()
	if err != nil {
		return nil, err
	}

	script, err := m.stageScript()
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(script) }()

	full := []string{
		"RATLINE_MONGO_OP=" + op,
		"RATLINE_MONGO_URI=" + uri,
	}
	for k, v := range env {
		full = append(full, k+"="+v)
	}

	timeout := m.Cfg.Databases.MongoDB.Timeout.D()
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "mongosh",
		// --nodb so the URI stays out of argv; --quiet so the only thing on stdout is
		// the script's own JSON.
		Args:    []string{"--nodb", "--quiet", "--file", script},
		Env:     system.MinimalEnv(full...),
		Timeout: timeout,
		Mutates: mutating(op),
		Label:   "mongosh " + op,
	})

	// The script prints JSON for failures too, so the body is worth parsing before the
	// exit code is judged: it carries a real reason where the exit code carries 1.
	var parsed Result
	if res != nil {
		if line := lastJSONLine(res.Stdout); line != "" {
			if jerr := json.Unmarshal([]byte(line), &parsed); jerr == nil {
				parsed.Raw = json.RawMessage(line)
			}
		}
	}
	if parsed.Error != "" {
		return nil, translate(parsed, op, env)
	}
	if err != nil {
		// No JSON at all: mongosh itself failed. Its stderr is safe to show — the URI
		// only ever existed in the environment.
		return nil, rlerr.Wrap(err, rlerr.CodeExternal, "mongosh could not run the %s operation", op).
			WithField("mongosh_output", tail(res, 4))
	}
	if !parsed.OK {
		return nil, rlerr.Externalf("the %s operation did not report success", op).
			WithField("mongosh_output", tail(res, 4))
	}
	return parsed.Raw, nil
}

// mutating reports whether an operation changes the server, so --dry-run stops it.
func mutating(op string) bool {
	switch op {
	case "ping", "listDatabases", "listUsers", "stats":
		return false
	}
	return true
}

// stageScript writes the embedded script somewhere mongosh can read it.
//
// 0600 and root-owned in ratline's own run directory. It carries no secrets — that is
// the whole design — but a world-writable script that root then executes is a local
// privilege escalation, so the mode matters anyway.
func (m *Manager) stageScript() (string, error) {
	body, err := templates.FS.ReadFile("mongo/op.js")
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "reading the embedded MongoDB script")
	}
	dir := m.Cfg.Paths.RunDir
	if dir == "" {
		dir = os.TempDir()
	}
	if _, err := system.EnsureDir(dir, 0o750, system.KeepUnchanged, system.KeepUnchanged); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "mongo-op-*.js")
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "staging the MongoDB script")
	}
	path := f.Name()
	if _, err := f.Write(body); err != nil {
		f.Close()
		_ = os.Remove(path)
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "writing %s", path)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "writing %s", path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = os.Remove(path)
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "securing %s", path)
	}
	return path, nil
}

// Ping reports the server's version and whether it enforces authentication.
func (m *Manager) Ping(ctx context.Context) (*ServerInfo, error) {
	raw, err := m.run(ctx, "ping", nil)
	if err != nil {
		return nil, err
	}
	var info ServerInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeExternal, "reading the server's answer")
	}
	return &info, nil
}

// LiveDatabases asks the server what exists, as opposed to what ratline recorded.
func (m *Manager) LiveDatabases(ctx context.Context) ([]LiveDatabase, error) {
	raw, err := m.run(ctx, "listDatabases", nil)
	if err != nil {
		return nil, err
	}
	var body struct {
		Databases []LiveDatabase `json:"databases"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeExternal, "reading the database list")
	}
	return body.Databases, nil
}

// LiveUsers asks the server for the users of one database.
func (m *Manager) LiveUsers(ctx context.Context, database string) ([]LiveUser, error) {
	if err := validate.DatabaseName(database); err != nil {
		return nil, err
	}
	raw, err := m.run(ctx, "listUsers", map[string]string{"RATLINE_MONGO_DB": database})
	if err != nil {
		return nil, err
	}
	var body struct {
		Users []LiveUser `json:"users"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeExternal, "reading the user list")
	}
	return body.Users, nil
}

// Stats reports what a database holds.
func (m *Manager) Stats(ctx context.Context, database string) (*Stats, error) {
	if err := validate.DatabaseName(database); err != nil {
		return nil, err
	}
	raw, err := m.run(ctx, "stats", map[string]string{"RATLINE_MONGO_DB": database})
	if err != nil {
		return nil, err
	}
	var s Stats
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, rlerr.Wrap(err, rlerr.CodeExternal, "reading the statistics")
	}
	return &s, nil
}

// CreateDatabase makes a database real and records who owns it.
//
// MongoDB has no createDatabase: a database exists once something is written into it.
// An initial collection is created so that the database appears in `db list` and a
// backup has something to find — without it, a freshly created database is invisible
// until the application writes, which reads as the create having silently failed.
func (m *Manager) CreateDatabase(ctx context.Context, name, collection string) ([]string, error) {
	if err := validate.DatabaseName(name); err != nil {
		return nil, err
	}
	env := map[string]string{"RATLINE_MONGO_DB": name}
	if collection != "" {
		if err := validate.Label(collection); err != nil {
			return nil, rlerr.Wrap(err, rlerr.CodeUsage, "the initial collection name is not usable")
		}
		env["RATLINE_MONGO_COLLECTION"] = collection
	}
	raw, err := m.run(ctx, "createDatabase", env)
	if err != nil {
		return nil, err
	}
	var body struct {
		Collections []string `json:"collections"`
	}
	_ = json.Unmarshal(raw, &body)
	return body.Collections, nil
}

// DropDatabase removes a database and everything in it.
func (m *Manager) DropDatabase(ctx context.Context, name string) error {
	if err := validate.DatabaseName(name); err != nil {
		return err
	}
	_, err := m.run(ctx, "dropDatabase", map[string]string{"RATLINE_MONGO_DB": name})
	return err
}

// CreateUser creates a user scoped to one database and returns its password.
//
// The password is generated here and returned once. It is never stored: MongoDB keeps a
// hash and will not give it back, so ratline could not display it later even if it
// wanted to — which is the right shape. A lost password is rotated, not recovered.
func (m *Manager) CreateUser(ctx context.Context, database, username, role, password string) (string, error) {
	if err := validate.DatabaseName(database); err != nil {
		return "", err
	}
	if err := validate.DatabaseUsername(username); err != nil {
		return "", err
	}
	if err := validate.DatabaseRole(role); err != nil {
		return "", err
	}
	if password == "" {
		var err error
		if password, err = GeneratePassword(); err != nil {
			return "", err
		}
	}
	_, err := m.run(ctx, "createUser", map[string]string{
		"RATLINE_MONGO_DB":       database,
		"RATLINE_MONGO_USER":     username,
		"RATLINE_MONGO_PASSWORD": password,
		"RATLINE_MONGO_ROLE":     role,
	})
	if err != nil {
		return "", err
	}
	return password, nil
}

// SetPassword replaces a user's password and returns the new one.
func (m *Manager) SetPassword(ctx context.Context, authDB, username, password string) (string, error) {
	if err := validate.DatabaseName(authDB); err != nil {
		return "", err
	}
	if err := validate.DatabaseUsername(username); err != nil {
		return "", err
	}
	if password == "" {
		var err error
		if password, err = GeneratePassword(); err != nil {
			return "", err
		}
	}
	_, err := m.run(ctx, "updatePassword", map[string]string{
		"RATLINE_MONGO_DB":       authDB,
		"RATLINE_MONGO_USER":     username,
		"RATLINE_MONGO_PASSWORD": password,
	})
	if err != nil {
		return "", err
	}
	return password, nil
}

// SetRole replaces a user's roles with exactly the one named.
func (m *Manager) SetRole(ctx context.Context, authDB, username, role string) error {
	if err := validate.DatabaseName(authDB); err != nil {
		return err
	}
	if err := validate.DatabaseUsername(username); err != nil {
		return err
	}
	if err := validate.DatabaseRole(role); err != nil {
		return err
	}
	_, err := m.run(ctx, "updateRole", map[string]string{
		"RATLINE_MONGO_DB":   authDB,
		"RATLINE_MONGO_USER": username,
		"RATLINE_MONGO_ROLE": role,
	})
	return err
}

// DropUser removes a user from its authentication database.
func (m *Manager) DropUser(ctx context.Context, authDB, username string) error {
	if err := validate.DatabaseName(authDB); err != nil {
		return err
	}
	if err := validate.DatabaseUsername(username); err != nil {
		return err
	}
	_, err := m.run(ctx, "dropUser", map[string]string{
		"RATLINE_MONGO_DB":   authDB,
		"RATLINE_MONGO_USER": username,
	})
	return err
}

// GeneratePassword returns a password long enough that nobody is tempted to reuse one.
//
// 32 bytes of crypto/rand, base64url without padding: URI-safe by construction, so it
// needs no percent-encoding in a connection string. That matters — a password containing
// an @ or a / silently truncates the URI a driver parses, and the resulting failure looks
// like wrong credentials rather than a quoting bug.
func GeneratePassword() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "generating a password")
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ConnectionURI builds the string an application uses, from the admin URI's host and
// the user's own credentials.
//
// The host, port, replica set and TLS options come from the admin URI, because they are
// properties of the deployment; the credentials and the database come from the user. The
// admin password never survives into the result.
func (m *Manager) ConnectionURI(adminURI, database, username, password string) (string, error) {
	u, err := url.Parse(adminURI)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodePrecondition, "the admin connection string is not a valid URI")
	}
	out := &url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
		// url.URL percent-encodes userinfo on String(), so a password needing it is
		// handled rather than corrupting the URI.
		User: url.UserPassword(username, password),
		Path: "/" + database,
	}
	q := u.Query()
	// The user authenticates against the database it was created in, which is not
	// necessarily the one the admin URI names.
	q.Set("authSource", database)
	// Anything the deployment requires — tls, replicaSet, retryWrites — is carried over.
	// A driver that connects to Atlas without them fails in a way that reads as a
	// credentials problem.
	out.RawQuery = q.Encode()
	return out.String(), nil
}

// Redact returns a connection string safe to print or log.
func Redact(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return "mongodb://<unparseable>"
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "REDACTED")
		}
	}
	return u.String()
}

// translate turns the server's own refusals into something an operator can act on.
//
// Every branch is a case somebody hits in practice, and each names the next command
// rather than the MongoDB manual page.
func translate(res Result, op string, env map[string]string) error {
	lower := strings.ToLower(res.Error + " " + res.Detail)
	name := env["RATLINE_MONGO_USER"]
	db := env["RATLINE_MONGO_DB"]

	detail := func(e *rlerr.Error) *rlerr.Error {
		e = e.WithField("operation", op)
		if res.Detail != "" {
			e = e.WithField("mongodb_detail", firstLine(res.Detail))
		}
		return e
	}

	switch {
	case strings.Contains(lower, "already exists"):
		return detail(rlerr.Preconditionf("%s already exists in %s", name, db)).
			WithHint("change its password with 'ratline db user password %s', "+
				"or its role with 'ratline db user grant %s --role …'", name, name)

	case strings.Contains(lower, "authentication failed"), strings.Contains(lower, "auth failed"):
		return detail(rlerr.Preconditionf("the MongoDB server rejected ratline's admin credentials")).
			WithHint("check the connection string in %s", "/etc/ratline/db/mongodb.uri")

	case strings.Contains(lower, "not authorized"), strings.Contains(lower, "unauthorized"):
		return detail(rlerr.Preconditionf("ratline's admin user is not allowed to %s", op)).
			WithHint("managing users needs userAdmin on the database, or userAdminAnyDatabase " +
				"for a server-wide admin. The connection reached the server, so this is a " +
				"permission on that account rather than a network problem")

	case strings.Contains(lower, "usernotfound"), strings.Contains(lower, "user not found"):
		return detail(rlerr.Preconditionf("there is no user %s in %s", name, db)).
			WithHint("'ratline db user list --live' shows what the server actually has")

	case strings.Contains(lower, "econnrefused"):
		return detail(rlerr.Preconditionf("nothing is listening at the address in the admin connection string")).
			WithHint("check that mongod is running, and that the host and port in " +
				"/etc/ratline/db/mongodb.uri are the ones it binds")

	case strings.Contains(lower, "timed out"), strings.Contains(lower, "timeout"),
		strings.Contains(lower, "server selection"):
		return detail(rlerr.Preconditionf("the MongoDB server did not answer in time")).
			WithHint("raise databases.mongodb.timeout if the server is simply slow. On Atlas " +
				"this is usually the access list: a cluster that has not allowed this " +
				"server's address does not refuse the connection, it ignores it")

	case strings.Contains(lower, "certificate"), strings.Contains(lower, "tls"), strings.Contains(lower, "ssl"):
		return detail(rlerr.Preconditionf("the TLS handshake with the MongoDB server failed")).
			WithHint("a managed cluster needs tls=true in the connection string, and a " +
				"private CA needs its root in the system trust store")

	case strings.Contains(lower, "not master"), strings.Contains(lower, "notwritableprimary"):
		return detail(rlerr.Preconditionf("the MongoDB server is not the primary of its replica set")).
			WithHint("point the connection string at the replica set rather than one member: " +
				"add replicaSet=<name>, and list the members as the host")
	}
	return detail(rlerr.Externalf("%s", res.Error))
}

// lastJSONLine returns the final line of output that parses as a JSON object.
//
// mongosh prints deprecation notices and connection chatter even under --quiet on some
// versions, so the result is found rather than assumed to be the whole of stdout.
func lastJSONLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(l, "{") || !strings.HasSuffix(l, "}") {
			continue
		}
		var probe map[string]any
		if json.Unmarshal([]byte(l), &probe) == nil {
			if _, ok := probe["ok"]; ok {
				return l
			}
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func tail(res *system.Result, n int) string {
	if res == nil {
		return ""
	}
	body := strings.TrimSpace(res.Stderr)
	if body == "" {
		body = strings.TrimSpace(res.Stdout)
	}
	lines := strings.Split(body, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// DefaultDatabaseName suggests a database name for a site.
//
// The domain with its dots and hyphens flattened, because a dot is a namespace
// separator in MongoDB and would make the database unaddressable in a role document.
func DefaultDatabaseName(domain string) string {
	name := strings.ToLower(domain)
	name = strings.NewReplacer(".", "_", "-", "_").Replace(name)
	if len(name) > 38 {
		name = name[:38]
	}
	return strings.Trim(name, "_")
}

// DefaultUsername suggests a username for a database.
func DefaultUsername(database string) string {
	name := database + "_app"
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// ScriptPathForTests exposes the staging helper to the package's own tests.
func (m *Manager) ScriptPathForTests() (string, error) { return m.stageScript() }
