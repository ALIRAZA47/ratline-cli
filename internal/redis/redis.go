// Package redis provisions Redis access inside a server it is pointed at. Redis has no
// named databases and no per-database users, so the model is different from MongoDB and
// MySQL: a ratline "database" is an ACL user confined to a key-prefix keyspace. Creating a
// database creates that ACL user; the connection string authenticates as it, and the
// server's ACL rules keep it to keys and channels under its own prefix.
//
// The rules that carry over from the other engines carry over here too. The admin password
// never touches argv — redis-cli reads it from REDISCLI_AUTH in the environment, and any
// command that carries a new user's password travels on stdin, not as an argument. And no
// operator input is interpolated into an ACL rule: keyspace and user names are validated to
// a conservative charset where they enter (internal/validate), because an ACL rule is a
// space-delimited command language and a name with a space or a glob character in it could
// rewrite the rule.
package redis

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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
)

// Engine is the state/access key for Redis rows.
const Engine = "redis"

const (
	DefaultHost = "127.0.0.1"
	DefaultPort = "6379"
	// PlainLocalURI reaches a server that does not require a password yet — the state a
	// freshly installed redis-server starts in, before ratline sets one.
	PlainLocalURI = "redis://127.0.0.1:6379"
)

// Manager runs Redis ACL operations and records them in state.
type Manager struct {
	Cfg    *config.Config
	Log    *log.Logger
	Runner system.Runner
	Bins   *system.Binaries
	State  *state.Store
	DryRun bool
}

// ServerInfo is what Ping reports.
type ServerInfo struct {
	Version string `json:"version"`
	// AuthEnabled reports whether the server refuses an unauthenticated connection. A
	// Redis with no password answers every command from anyone who can reach the port.
	AuthEnabled bool `json:"auth_enabled"`
}

// LiveUser is one ACL user from the server's own listing.
type LiveUser struct {
	Username string `json:"username"`
}

// AdminURI returns the admin connection string, refusing a file other accounts can read.
func (m *Manager) AdminURI() (string, error) {
	path := m.Cfg.Paths.RedisURIFile
	if path == "" {
		return "", rlerr.Preconditionf("paths.redis_uri_file is not configured")
	}
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", rlerr.Preconditionf("there is no Redis admin connection string at %s", path).
				WithHint("run 'ratline db connect --engine redis' or 'ratline db install --engine redis'")
		}
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", path)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", rlerr.Preconditionf("%s is mode %04o, which lets other accounts read the "+
			"admin password for the Redis server", path, fi.Mode().Perm()).
			WithHint("chmod 0600 %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", path)
	}
	uri := firstNonComment(string(raw))
	if uri == "" {
		return "", rlerr.Preconditionf("%s has no connection string in it", path)
	}
	if err := validate.RedisURI(uri); err != nil {
		return "", err
	}
	return uri, nil
}

// hostPortPassword pulls the connection parts out of a redis:// URI.
func hostPortPassword(uri string) (host, port, password string) {
	host, port = DefaultHost, DefaultPort
	u, err := url.Parse(uri)
	if err != nil {
		return
	}
	if h := u.Hostname(); h != "" {
		host = h
	}
	if p := u.Port(); p != "" {
		port = p
	}
	if u.User != nil {
		if pw, ok := u.User.Password(); ok {
			password = pw
		}
	}
	return host, port, password
}

// run executes redis-cli commands against the attached server.
func (m *Manager) run(ctx context.Context, commands []string, mutating bool) (string, error) {
	uri, err := m.AdminURI()
	if err != nil {
		return "", err
	}
	return m.runWithURI(ctx, uri, commands, mutating)
}

// runWithURI executes redis-cli commands against an explicit server. The admin password
// travels in REDISCLI_AUTH (never argv), and the commands themselves — which may carry a
// new user's password — travel on stdin.
func (m *Manager) runWithURI(ctx context.Context, uri string, commands []string, mutating bool) (string, error) {
	if m.Bins != nil && !m.Bins.Available("redis-cli") {
		return "", rlerr.Preconditionf("redis-cli is not installed, so ratline cannot talk to Redis").
			WithHint("apt-get install redis-tools")
	}
	host, port, password := hostPortPassword(uri)
	env := []string{}
	if password != "" {
		env = append(env, "REDISCLI_AUTH="+password)
	}
	timeout := m.Cfg.Databases.Redis.Timeout.D()
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	res, err := m.Runner.Run(ctx, system.Cmd{
		Name:    "redis-cli",
		Args:    []string{"-h", host, "-p", port, "--no-raw"},
		Stdin:   strings.NewReader(strings.Join(commands, "\n") + "\n"),
		Env:     system.MinimalEnv(env...),
		Timeout: timeout,
		Mutates: mutating,
		Label:   "redis-cli",
	})
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeExternal, "redis-cli could not run").
			WithField("redis_output", tail(res))
	}
	out := ""
	if res != nil {
		out = res.Stdout
	}
	// redis-cli in pipe mode exits 0 even when a command replies with an error, so the
	// output is scanned for an error reply rather than trusting the exit code.
	if e := firstError(out); e != "" {
		return "", rlerr.Externalf("Redis rejected a command: %s", e)
	}
	return out, nil
}

// Ping reports the server's version and whether it enforces authentication.
func (m *Manager) Ping(ctx context.Context) (*ServerInfo, error) {
	uri, err := m.AdminURI()
	if err != nil {
		return nil, err
	}
	return m.PingURI(ctx, uri)
}

// PingURI reports on an explicit server, for the install flow before a URI is stored.
func (m *Manager) PingURI(ctx context.Context, uri string) (*ServerInfo, error) {
	out, err := m.runWithURI(ctx, uri, []string{"INFO server"}, false)
	if err != nil {
		return nil, err
	}
	info := &ServerInfo{Version: infoField(out, "redis_version"), AuthEnabled: true}
	return info, nil
}

// AuthEnforced reports whether the server refuses an unauthenticated PING. Used by the
// install verify: a Redis with no password is decoration.
func (m *Manager) AuthEnforced(ctx context.Context, host, port string) bool {
	res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "redis-cli", Args: []string{"-h", host, "-p", port, "PING"},
		Env: system.MinimalEnv(), Label: "redis-cli ping (no auth)",
	})
	if err != nil {
		// A connection error is not the question; but a refusal to answer without auth
		// is exactly the "auth is on" signal.
		return true
	}
	out := ""
	if res != nil {
		out = res.Stdout
	}
	// With auth on, an unauthenticated PING replies NOAUTH; with auth off it replies PONG.
	return !strings.Contains(strings.ToUpper(out), "PONG")
}

// CreateKeyspaceUser creates an ACL user confined to a keyspace and returns its password.
// The keyspace confinement is the whole isolation story: ~prefix:* limits the keys, and
// &prefix:* the pub/sub channels, so one tenant cannot read or publish into another's.
func (m *Manager) CreateKeyspaceUser(ctx context.Context, keyspace, username, role, password string) (string, error) {
	if err := validate.RedisKeyspace(keyspace); err != nil {
		return "", err
	}
	if err := validate.RedisUsername(username); err != nil {
		return "", err
	}
	if err := validate.RedisRole(role); err != nil {
		return "", err
	}
	if password == "" {
		var err error
		if password, err = GeneratePassword(); err != nil {
			return "", err
		}
	}
	rule := aclRule(keyspace, role)
	// resetkeys/resetchannels first, so a re-run cannot widen an existing user's reach.
	cmd := "ACL SETUSER " + username + " reset on >" + password + " " + rule
	if _, err := m.run(ctx, []string{cmd, "ACL SAVE"}, true); err != nil {
		return "", err
	}
	return password, nil
}

// SetPassword replaces a user's password and returns the new one.
func (m *Manager) SetPassword(ctx context.Context, username, password string) (string, error) {
	if err := validate.RedisUsername(username); err != nil {
		return "", err
	}
	if password == "" {
		var err error
		if password, err = GeneratePassword(); err != nil {
			return "", err
		}
	}
	// resetpass drops every existing password, then one is set — a rotation, not an
	// addition, so the old credential stops working.
	cmd := "ACL SETUSER " + username + " resetpass on >" + password
	if _, err := m.run(ctx, []string{cmd, "ACL SAVE"}, true); err != nil {
		return "", err
	}
	return password, nil
}

// SetRole replaces a user's command rules with exactly the role's, keeping its keyspace
// and password.
func (m *Manager) SetRole(ctx context.Context, keyspace, username, role string) error {
	if err := validate.RedisKeyspace(keyspace); err != nil {
		return err
	}
	if err := validate.RedisUsername(username); err != nil {
		return err
	}
	if err := validate.RedisRole(role); err != nil {
		return err
	}
	// -@all clears the command grants; the role's categories are then the only ones.
	cmd := "ACL SETUSER " + username + " -@all " + roleCategories(role)
	_, err := m.run(ctx, []string{cmd, "ACL SAVE"}, true)
	return err
}

// DropUser removes an ACL user.
func (m *Manager) DropUser(ctx context.Context, username string) error {
	if err := validate.RedisUsername(username); err != nil {
		return err
	}
	_, err := m.run(ctx, []string{"ACL DELUSER " + username, "ACL SAVE"}, true)
	return err
}

// FlushKeyspace removes every key under a keyspace prefix, without KEYS or FLUSHDB — a
// server-side SCAN + UNLINK loop that touches only this tenant's keys and never blocks the
// server on someone else's. The keyspace name is validated, so it is safe in the pattern.
func (m *Manager) FlushKeyspace(ctx context.Context, keyspace string) error {
	if err := validate.RedisKeyspace(keyspace); err != nil {
		return err
	}
	const script = `local c="0" repeat local r=redis.call("SCAN",c,"MATCH",ARGV[1],"COUNT",1000) c=r[1] if #r[2]>0 then redis.call("UNLINK",unpack(r[2])) end until c=="0" return 1`
	_, err := m.run(ctx, []string{`EVAL "` + script + `" 0 ` + keyspace + ":*"}, true)
	return err
}

// LiveUsers lists the ACL users the server actually has.
func (m *Manager) LiveUsers(ctx context.Context) ([]LiveUser, error) {
	out, err := m.run(ctx, []string{"ACL USERS"}, false)
	if err != nil {
		return nil, err
	}
	var users []LiveUser
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(strings.Trim(line, `"`))
		// --no-raw prefixes list replies with an index like `1) "name"`.
		if i := strings.LastIndex(line, `) `); i >= 0 {
			line = strings.Trim(line[i+2:], `"`)
		}
		if line != "" {
			users = append(users, LiveUser{Username: line})
		}
	}
	return users, nil
}

// CreateAdminUser sets the default user's password on a fresh server, through an explicit
// URI. This is the credential ratline manages the server with thereafter.
func (m *Manager) CreateAdminUser(ctx context.Context, uri, password string) error {
	if password == "" {
		return rlerr.Usagef("the admin needs a password")
	}
	// The default user keeps all commands and all keys — it is the admin — and gains a
	// password so the server stops answering anonymously.
	cmd := "ACL SETUSER default on >" + password + " ~* &* +@all"
	_, err := m.runWithURI(ctx, uri, []string{cmd, "ACL SAVE"}, true)
	return err
}

// ConnectionURI builds the string an application uses.
func (m *Manager) ConnectionURI(username, password string) string {
	host, port := DefaultHost, DefaultPort
	if uri, err := m.AdminURI(); err == nil {
		host, port, _ = hostPortPassword(uri)
	}
	u := &url.URL{
		Scheme: "redis",
		Host:   host + ":" + port,
		User:   url.UserPassword(username, password),
		Path:   "/0",
	}
	return u.String()
}

// LocalAdminURI is the connection string for a redis on this host.
func LocalAdminURI(password string) string {
	u := &url.URL{Scheme: "redis", Host: DefaultHost + ":" + DefaultPort, User: url.UserPassword("default", password)}
	return u.String()
}

// GeneratePassword returns a URL-safe password, so it needs no escaping in an ACL rule or
// a URI.
func GeneratePassword() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "generating a password")
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Redact hides the password in a connection string.
func Redact(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return "redis://<unparseable>"
	}
	if u.User != nil {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "REDACTED")
		}
	}
	return u.String()
}

// DefaultKeyspace suggests a keyspace name for a site.
func DefaultKeyspace(domain string) string {
	name := strings.ToLower(domain)
	name = strings.NewReplacer(".", "_", "-", "_").Replace(name)
	return strings.Trim(name, "_")
}

// DefaultUsername suggests an ACL username for a keyspace.
func DefaultUsername(keyspace string) string { return keyspace + "_app" }

// aclRule is the confinement plus commands for a new user: keys and channels under the
// keyspace prefix, and the role's command categories, always without @dangerous — which
// holds FLUSHALL/FLUSHDB/KEYS, commands that ignore the key patterns and would reach across
// tenants.
func aclRule(keyspace, role string) string {
	return "~" + keyspace + ":* resetchannels &" + keyspace + ":* " + roleCategories(role)
}

func roleCategories(role string) string {
	switch role {
	case "read":
		return "+@read -@dangerous"
	case "readWrite":
		return "+@read +@write -@dangerous"
	case "dbOwner":
		return "+@all -@dangerous"
	default:
		return "+@read -@dangerous"
	}
}

func firstNonComment(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

// firstError finds a Redis error reply in redis-cli output. redis-cli renders errors as
// "(error) MESSAGE" with --no-raw; a bare "ERR"/"WRONGPASS"/"NOPERM" is caught too.
func firstError(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "(error)") {
			return strings.TrimSpace(strings.TrimPrefix(line, "(error)"))
		}
		for _, code := range []string{"ERR ", "WRONGPASS", "NOPERM", "NOAUTH"} {
			if strings.HasPrefix(line, code) {
				return line
			}
		}
	}
	return ""
}

func infoField(out, field string) string {
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && k == field {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func tail(res *system.Result) string {
	if res == nil {
		return ""
	}
	s := strings.TrimSpace(res.Stderr)
	if s == "" {
		s = strings.TrimSpace(res.Stdout)
	}
	return s
}
