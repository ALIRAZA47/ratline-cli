// Package mysql provisions MySQL/MariaDB databases and users inside a server it is
// pointed at. Like internal/mongo, it does not install the server — internal/mysqld does
// that — and it works the same against a local mysqld and a managed one, the only
// difference being the admin credentials.
//
// Two rules carry over from the MongoDB manager and matter just as much here:
//
//   - The admin password never touches argv. mysql reads it from a 0600 defaults-file
//     handed to it as --defaults-extra-file; /proc/PID/cmdline is world-readable, and a
//     password on the command line would leak the root of every database on the box.
//   - No operator input is interpolated into a query as data. SQL cannot bind an
//     identifier, so database and user names are validated to a conservative charset
//     where they enter (internal/validate) and then backtick/quote-wrapped; generated
//     passwords use a URL-safe alphabet with no quote or backslash, so a string literal
//     needs no escaping. Every statement is sent on stdin, never as -e in argv.
package mysql

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// Engine is the state/access key for MySQL rows.
const Engine = "mysql"

// DefaultHost and DefaultPort are what a connection assumes when the defaults-file does
// not say otherwise. The port is fixed the way MongoDB's is: every generated URI, ufw
// rule and doctor check agrees on it by construction.
const (
	DefaultHost = "127.0.0.1"
	DefaultPort = "3306"
)

// Manager runs MySQL operations and records them in state.
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
	// AuthEnabled is always true for a reachable MySQL: the server has no
	// "anonymous everything" mode short of --skip-grant-tables, and reaching it at all
	// meant authenticating. Kept for symmetry with the MongoDB manager and doctor.
	AuthEnabled bool `json:"auth_enabled"`
}

// LiveDatabase is one schema from the server's own listing.
type LiveDatabase struct {
	Name string `json:"name"`
}

// LiveUser is one account from the server's own listing.
type LiveUser struct {
	Username string `json:"username"`
	Host     string `json:"host"`
}

// Creds is a set of admin credentials, rendered into a defaults-file.
type Creds struct {
	User     string
	Password string
	Host     string
	Port     string
}

// defaultsFileBody renders the [client] section mysql reads. It carries the password, so
// the file it is written to must be 0600.
func (c Creds) defaultsFileBody() string {
	host := c.Host
	if host == "" {
		host = DefaultHost
	}
	port := c.Port
	if port == "" {
		port = DefaultPort
	}
	var b strings.Builder
	b.WriteString("# " + system.ManagedHeader + "\n")
	b.WriteString("# MySQL admin credentials for ratline. This grants full control of every\n")
	b.WriteString("# database on the server, which is why this file is 0600 and root-owned.\n")
	b.WriteString("[client]\n")
	b.WriteString("user=" + c.User + "\n")
	b.WriteString("password=" + c.Password + "\n")
	b.WriteString("host=" + host + "\n")
	b.WriteString("port=" + port + "\n")
	return b.String()
}

// RenderDefaultsFile is the file body `db connect`/`db install` store at
// paths.mysql_defaults_file: the [client] section plus a managed header explaining what it
// is. It carries the password, so the file it lands in must be 0600.
func RenderDefaultsFile(c Creds) string { return c.defaultsFileBody() }

// AdminDefaultsFile returns the path to the stored admin defaults-file, refusing one any
// other account can read — it is the root credential for every database on the server.
func (m *Manager) AdminDefaultsFile() (string, error) {
	path := m.Cfg.Paths.MySQLDefaultsFile
	if path == "" {
		return "", rlerr.Preconditionf("paths.mysql_defaults_file is not configured")
	}
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", rlerr.Preconditionf("there is no MySQL admin credentials file at %s", path).
				WithHint("run 'ratline db connect --engine mysql', or 'ratline db install --engine mysql'")
		}
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "reading %s", path)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", rlerr.Preconditionf("%s is mode %04o, which lets other accounts read the "+
			"admin password for every database on this server", path, fi.Mode().Perm()).
			WithHint("chmod 0600 %s", path)
	}
	return path, nil
}

// adminHostPort reads the host and port out of the stored defaults-file, for building
// per-user connection URIs whose deployment properties match the admin's.
func (m *Manager) adminHostPort() (host, port string) {
	host, port = DefaultHost, DefaultPort
	path := m.Cfg.Paths.MySQLDefaultsFile
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if k, v, ok := strings.Cut(line, "="); ok {
			switch strings.TrimSpace(k) {
			case "host":
				if v = strings.TrimSpace(v); v != "" {
					host = v
				}
			case "port":
				if v = strings.TrimSpace(v); v != "" {
					port = v
				}
			}
		}
	}
	return host, port
}

// runSQL executes SQL against the server addressed by defaultsFile and returns the
// batch (tab-separated, header-less) output. The SQL travels on stdin so nothing —
// least of all a password inside CREATE USER — reaches argv.
func (m *Manager) runSQL(ctx context.Context, defaultsFile, sql string, mutating bool) (string, error) {
	if m.Bins != nil && !m.Bins.Available("mysql") {
		return "", rlerr.Preconditionf("the mysql client is not installed, so ratline cannot talk to MySQL").
			WithHint("apt-get install mysql-client (or mariadb-client)")
	}
	timeout := m.Cfg.Databases.MySQL.Timeout.D()
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	res, err := m.Runner.Run(ctx, system.Cmd{
		Name: "mysql",
		// --defaults-extra-file must be first. --batch --skip-column-names give
		// tab-separated, header-less output that parses without guessing.
		Args:    []string{"--defaults-extra-file=" + defaultsFile, "--batch", "--skip-column-names"},
		Stdin:   strings.NewReader(sql),
		Env:     system.MinimalEnv(),
		Timeout: timeout,
		Mutates: mutating,
		Label:   "mysql",
	})
	if err != nil {
		return "", translate(err, res)
	}
	if res != nil {
		return res.Stdout, nil
	}
	return "", nil
}

// run executes against the stored admin defaults-file.
func (m *Manager) run(ctx context.Context, sql string, mutating bool) (string, error) {
	path, err := m.AdminDefaultsFile()
	if err != nil {
		return "", err
	}
	return m.runSQL(ctx, path, sql, mutating)
}

// Ping reports the server's version, via the stored admin credentials.
func (m *Manager) Ping(ctx context.Context) (*ServerInfo, error) {
	path, err := m.AdminDefaultsFile()
	if err != nil {
		return nil, err
	}
	return m.PingWith(ctx, path)
}

// PingWith reports the server's version via an explicit defaults-file, for the moments
// during install when the stored file does not exist yet.
func (m *Manager) PingWith(ctx context.Context, defaultsFile string) (*ServerInfo, error) {
	out, err := m.runSQL(ctx, defaultsFile, "SELECT VERSION();", false)
	if err != nil {
		return nil, err
	}
	return &ServerInfo{Version: strings.TrimSpace(out), AuthEnabled: true}, nil
}

// LiveDatabases asks the server what schemas exist.
func (m *Manager) LiveDatabases(ctx context.Context) ([]LiveDatabase, error) {
	out, err := m.run(ctx, "SHOW DATABASES;", false)
	if err != nil {
		return nil, err
	}
	var dbs []LiveDatabase
	for _, line := range splitLines(out) {
		dbs = append(dbs, LiveDatabase{Name: line})
	}
	return dbs, nil
}

// LiveUsers lists the accounts that hold any privilege on one database.
func (m *Manager) LiveUsers(ctx context.Context, database string) ([]LiveUser, error) {
	if err := validate.MySQLDatabaseName(database); err != nil {
		return nil, err
	}
	// information_schema.SCHEMA_PRIVILEGES names the grantee as 'user'@'host'.
	q := fmt.Sprintf(
		"SELECT DISTINCT GRANTEE FROM information_schema.SCHEMA_PRIVILEGES WHERE TABLE_SCHEMA = %s;",
		sqlString(database))
	out, err := m.run(ctx, q, false)
	if err != nil {
		return nil, err
	}
	var users []LiveUser
	for _, line := range splitLines(out) {
		// GRANTEE looks like 'shop_app'@'%'.
		u, h := parseGrantee(line)
		if u != "" {
			users = append(users, LiveUser{Username: u, Host: h})
		}
	}
	return users, nil
}

// CreateDatabase creates a schema if it does not exist.
func (m *Manager) CreateDatabase(ctx context.Context, name string) error {
	if err := validate.MySQLDatabaseName(name); err != nil {
		return err
	}
	sql := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;",
		sqlIdent(name))
	_, err := m.run(ctx, sql, true)
	return err
}

// DropDatabase removes a schema and everything in it.
func (m *Manager) DropDatabase(ctx context.Context, name string) error {
	if err := validate.MySQLDatabaseName(name); err != nil {
		return err
	}
	_, err := m.run(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s;", sqlIdent(name)), true)
	return err
}

// CreateUser creates an account scoped to one database and returns its password. The
// account is `username`@`%`: reachability is controlled by the firewall (db access), not
// by a host pattern, which matches the MongoDB model where users are not host-scoped.
func (m *Manager) CreateUser(ctx context.Context, database, username, role, password string) (string, error) {
	if err := validate.MySQLDatabaseName(database); err != nil {
		return "", err
	}
	if err := validate.MySQLUsername(username); err != nil {
		return "", err
	}
	if err := validate.MySQLRole(role); err != nil {
		return "", err
	}
	if password == "" {
		var err error
		if password, err = GeneratePassword(); err != nil {
			return "", err
		}
	}
	account := sqlAccount(username, "%")
	sql := strings.Join([]string{
		fmt.Sprintf("CREATE USER IF NOT EXISTS %s IDENTIFIED BY %s;", account, sqlString(password)),
		fmt.Sprintf("GRANT %s ON %s.* TO %s;", privilegesFor(role), sqlIdent(database), account),
		"FLUSH PRIVILEGES;",
	}, "\n")
	if _, err := m.run(ctx, sql, true); err != nil {
		return "", err
	}
	return password, nil
}

// SetPassword replaces a user's password and returns the new one.
func (m *Manager) SetPassword(ctx context.Context, username, host, password string) (string, error) {
	if err := validate.MySQLUsername(username); err != nil {
		return "", err
	}
	if host == "" {
		host = "%"
	}
	if password == "" {
		var err error
		if password, err = GeneratePassword(); err != nil {
			return "", err
		}
	}
	sql := fmt.Sprintf("ALTER USER %s IDENTIFIED BY %s;", sqlAccount(username, host), sqlString(password))
	if _, err := m.run(ctx, sql, true); err != nil {
		return "", err
	}
	return password, nil
}

// SetRole replaces a user's privileges on one database with exactly those of the role.
func (m *Manager) SetRole(ctx context.Context, database, username, host, role string) error {
	if err := validate.MySQLDatabaseName(database); err != nil {
		return err
	}
	if err := validate.MySQLUsername(username); err != nil {
		return err
	}
	if err := validate.MySQLRole(role); err != nil {
		return err
	}
	if host == "" {
		host = "%"
	}
	account := sqlAccount(username, host)
	sql := strings.Join([]string{
		fmt.Sprintf("REVOKE ALL PRIVILEGES ON %s.* FROM %s;", sqlIdent(database), account),
		fmt.Sprintf("GRANT %s ON %s.* TO %s;", privilegesFor(role), sqlIdent(database), account),
		"FLUSH PRIVILEGES;",
	}, "\n")
	_, err := m.run(ctx, sql, true)
	return err
}

// DropUser removes an account.
func (m *Manager) DropUser(ctx context.Context, username, host string) error {
	if err := validate.MySQLUsername(username); err != nil {
		return err
	}
	if host == "" {
		host = "%"
	}
	_, err := m.run(ctx, fmt.Sprintf("DROP USER IF EXISTS %s;", sqlAccount(username, host)), true)
	return err
}

// CreateAdminUser creates the managed root-equivalent account on a freshly installed
// server, through an explicit defaults-file (the socket-authenticated root). It is the
// credential ratline manages the server with thereafter.
func (m *Manager) CreateAdminUser(ctx context.Context, viaDefaults, username, password string) error {
	if err := validate.MySQLUsername(username); err != nil {
		return err
	}
	if password == "" {
		return rlerr.Usagef("the admin user needs a password")
	}
	account := sqlAccount(username, "%")
	sql := strings.Join([]string{
		fmt.Sprintf("CREATE USER IF NOT EXISTS %s IDENTIFIED BY %s;", account, sqlString(password)),
		fmt.Sprintf("GRANT ALL PRIVILEGES ON *.* TO %s WITH GRANT OPTION;", account),
		"FLUSH PRIVILEGES;",
	}, "\n")
	_, err := m.runSQL(ctx, viaDefaults, sql, true)
	return err
}

// ConnectionURI builds the string an application uses, from the admin's host/port and the
// user's own credentials.
func (m *Manager) ConnectionURI(database, username, password string) string {
	host, port := m.adminHostPort()
	u := &url.URL{
		Scheme:   "mysql",
		Host:     host + ":" + port,
		User:     url.UserPassword(username, password),
		Path:     "/" + database,
		RawQuery: "charset=utf8mb4",
	}
	return u.String()
}

// GeneratePassword returns a URL-safe password: 32 bytes of crypto/rand, base64url with
// no padding. Its alphabet is A–Z a–z 0–9 - _, so it needs no escaping in a SQL string
// literal and no percent-encoding in a URI.
func GeneratePassword() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "generating a password")
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Redact returns a connection string safe to print or log.
func Redact(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return "mysql://<unparseable>"
	}
	if u.User != nil {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "REDACTED")
		}
	}
	return u.String()
}

// DefaultDatabaseName suggests a database name for a site: the domain with dots and
// hyphens flattened to underscores, since neither is a legal bare identifier.
func DefaultDatabaseName(domain string) string {
	name := strings.ToLower(domain)
	name = strings.NewReplacer(".", "_", "-", "_").Replace(name)
	if len(name) > 60 {
		name = name[:60]
	}
	name = strings.Trim(name, "_")
	// An identifier must not start with a digit.
	if name != "" && name[0] >= '0' && name[0] <= '9' {
		name = "db_" + name
	}
	return name
}

// DefaultUsername suggests a username for a database.
func DefaultUsername(database string) string {
	name := database + "_app"
	if len(name) > 32 {
		name = name[:32]
	}
	return name
}

// privilegesFor maps a role name to its SQL privilege list. The role name is validated by
// validate.MySQLRole before this is reached, so the default is unreachable in practice; it
// falls back to the least privilege rather than the most.
func privilegesFor(role string) string {
	switch role {
	case "read":
		return "SELECT"
	case "readWrite":
		return "SELECT, INSERT, UPDATE, DELETE"
	case "dbOwner":
		return "ALL PRIVILEGES"
	default:
		return "SELECT"
	}
}

// sqlIdent wraps a validated identifier in backticks. The value has already been checked
// to contain only [A-Za-z0-9_], so there is no backtick to double.
func sqlIdent(name string) string { return "`" + name + "`" }

// sqlAccount renders a validated user and a controlled host as 'user'@'host'.
func sqlAccount(user, host string) string {
	return "'" + user + "'@'" + host + "'"
}

// sqlString renders a value as a single-quoted string literal. Callers pass only
// generated passwords (URL-safe alphabet) and validated identifiers, so there is no quote
// or backslash to escape — but the doubling is done anyway, cheaply, as defense in depth.
func sqlString(s string) string {
	return "'" + strings.NewReplacer("'", "''", "\\", "\\\\").Replace(s) + "'"
}

func splitLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// parseGrantee splits information_schema's 'user'@'host' grantee form.
func parseGrantee(g string) (user, host string) {
	g = strings.TrimSpace(g)
	at := strings.LastIndex(g, "@")
	if at < 0 {
		return strings.Trim(g, "'"), ""
	}
	user = strings.Trim(g[:at], "'")
	host = strings.Trim(g[at+1:], "'")
	return user, host
}

// translate turns a mysql-client failure into something an operator can act on, without
// echoing the SQL (which for CREATE USER carries the password).
func translate(err error, res *system.Result) error {
	body := ""
	if res != nil {
		body = strings.ToLower(res.Stderr)
	}
	switch {
	case strings.Contains(body, "access denied"):
		return rlerr.Preconditionf("the MySQL server rejected ratline's admin credentials").
			WithHint("check %s, or re-run 'ratline db connect --engine mysql'", "paths.mysql_defaults_file")
	case strings.Contains(body, "can't connect"), strings.Contains(body, "connection refused"):
		return rlerr.Preconditionf("nothing is listening for MySQL at the configured host and port").
			WithHint("check that mysqld is running and the host/port in the defaults-file are right")
	case strings.Contains(body, "already exists"):
		return rlerr.Preconditionf("that database or user already exists").WithField("mysql", firstLine(res.Stderr))
	}
	detail := ""
	if res != nil {
		detail = firstLine(res.Stderr)
	}
	e := rlerr.Wrap(err, rlerr.CodeExternal, "the mysql command failed")
	if detail != "" {
		e = e.WithField("mysql", detail)
	}
	return e
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// StageDefaultsFile writes a Creds set to a 0600 file in the run directory's staging area
// and returns its path. Used at install time, before the admin file is stored, to connect
// as a freshly created account. The caller removes it.
func (m *Manager) StageDefaultsFile(c Creds) (string, error) {
	return m.StageDefaultsRaw(c.defaultsFileBody())
}

// StageDefaultsRaw writes a defaults-file body verbatim to a 0600 staging file — for the
// install-time root-over-socket bootstrap, whose defaults-file names no password.
func (m *Manager) StageDefaultsRaw(body string) (string, error) {
	dir := m.Cfg.Paths.RunDir
	if dir == "" {
		dir = os.TempDir()
	} else {
		if _, err := system.EnsureDir(dir, 0o755, system.KeepUnchanged, system.KeepUnchanged); err != nil {
			return "", err
		}
		dir = filepath.Join(dir, "staging")
		if _, err := system.EnsureDir(dir, 0o700, system.KeepUnchanged, system.KeepUnchanged); err != nil {
			return "", err
		}
	}
	f, err := os.CreateTemp(dir, "mysql-*.cnf")
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodeGeneric, "staging a MySQL defaults-file")
	}
	path := f.Name()
	if _, err := f.WriteString(body); err != nil {
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
