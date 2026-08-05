package mongo

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

func testManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Paths.MongoURIFile = filepath.Join(dir, "mongodb.uri")
	cfg.Paths.RunDir = filepath.Join(dir, "run")
	return &Manager{Cfg: cfg, Log: log.Discard()}, cfg.Paths.MongoURIFile
}

func TestAdminURIRefusesAFileOthersCanRead(t *testing.T) {
	// The file is the admin password for every database on the server. A mode that lets
	// another account read it is not a warning: any tenant on the box could then read
	// and write every other tenant's data, which is the one thing this tool exists to
	// prevent.
	m, path := testManager(t)
	if err := os.WriteFile(path, []byte("mongodb://admin:s3cret@127.0.0.1:27017/"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := m.AdminURI()
	if err == nil {
		t.Fatal("a 0644 admin URI file was accepted")
	}
	if !strings.Contains(err.Error(), "0644") {
		t.Errorf("the refusal should name the mode it found, got: %v", err)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AdminURI(); err != nil {
		t.Errorf("a 0600 file should be accepted: %v", err)
	}
}

func TestAdminURIRefusesSomethingThatIsNotAConnectionString(t *testing.T) {
	// A file holding a password rather than a URI is a plausible mistake, and the
	// resulting driver error is unreadable. Caught here instead.
	m, path := testManager(t)
	for _, body := range []string{"", "   ", "s3cret", "postgres://localhost/db", "http://127.0.0.1"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := m.AdminURI(); err == nil {
			t.Errorf("%q was accepted as a MongoDB connection string", body)
		}
	}
	for _, body := range []string{
		"mongodb://admin:p@127.0.0.1:27017/?authSource=admin",
		"mongodb+srv://admin:p@cluster.example.mongodb.net/?retryWrites=true",
		"  mongodb://127.0.0.1:27017/  \n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := m.AdminURI(); err != nil {
			t.Errorf("%q was refused: %v", body, err)
		}
	}
}

func TestAdminURIMissingSaysHowToWriteIt(t *testing.T) {
	// The first thing anybody hits. The error is the documentation.
	m, _ := testManager(t)
	_, err := m.AdminURI()
	if err == nil {
		t.Fatal("a missing file should be an error")
	}
	combined := err.Error() + " " + rlerr.Hint(err)
	for _, want := range []string{"0600", "mongodb://", "chmod"} {
		if !strings.Contains(combined, want) {
			t.Errorf("the error should mention %q so it can be acted on, got:\n%s", want, combined)
		}
	}
}

func TestTheConnectionURICarriesTheDeploymentsOwnOptions(t *testing.T) {
	// The host, replica set and TLS settings are properties of the deployment and have to
	// survive into the application's URI. A driver connecting to a replica set or to
	// Atlas without them fails in a way that reads as bad credentials.
	m, _ := testManager(t)
	admin := "mongodb://admin:s3cret@a.example:27017,b.example:27017/?replicaSet=rs0&tls=true&retryWrites=true"

	got, err := m.ConnectionURI(admin, "shop", "shop_app", "p4ssw0rd")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("the result is not a valid URI: %v", err)
	}
	if u.Host != "a.example:27017,b.example:27017" {
		t.Errorf("host = %q, want both members", u.Host)
	}
	if u.Path != "/shop" {
		t.Errorf("path = %q, want /shop", u.Path)
	}
	q := u.Query()
	if q.Get("replicaSet") != "rs0" {
		t.Error("replicaSet was dropped; a driver would not find the primary")
	}
	if q.Get("tls") != "true" {
		t.Error("tls was dropped; a managed cluster would refuse the connection")
	}
	// authSource is the user's own database, not whatever the admin URI named.
	if q.Get("authSource") != "shop" {
		t.Errorf("authSource = %q, want shop", q.Get("authSource"))
	}
	// And the admin's password must not survive into an application's credential.
	if strings.Contains(got, "s3cret") {
		t.Error("the admin password leaked into the application's connection string")
	}
	if pw, _ := u.User.Password(); pw != "p4ssw0rd" {
		t.Errorf("password = %q, want the user's own", pw)
	}
}

func TestAPasswordNeedingEncodingSurvivesTheURI(t *testing.T) {
	// Generated passwords are URI-safe by construction, but an operator can supply one.
	// A raw @ or / silently truncates the URI a driver parses, and the failure looks like
	// wrong credentials rather than a quoting bug.
	m, _ := testManager(t)
	nasty := "p@ss/w:rd?#&=+ x"
	got, err := m.ConnectionURI("mongodb://admin:a@127.0.0.1:27017/", "shop", "app", nasty)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("the URI does not parse with an awkward password: %v", err)
	}
	if u.Host != "127.0.0.1:27017" {
		t.Errorf("host = %q — the password ran into the host", u.Host)
	}
	pw, _ := u.User.Password()
	if pw != nasty {
		t.Errorf("password round-tripped as %q, want %q", pw, nasty)
	}
}

func TestGeneratedPasswordsNeedNoEncoding(t *testing.T) {
	// Because they end up in a .env line, in a URI, and occasionally in a shell.
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		p, err := GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if seen[p] {
			t.Fatal("GeneratePassword repeated itself, which means it is not random")
		}
		seen[p] = true
		if len(p) < 40 {
			t.Errorf("password is %d characters; too short to be worth generating", len(p))
		}
		if url.QueryEscape(p) != p {
			t.Errorf("password %q would need percent-encoding in a URI", p)
		}
		if strings.ContainsAny(p, `@/:?#&='" $\`+"`") {
			t.Errorf("password %q contains a character that breaks a URI or a shell", p)
		}
	}
}

func TestRedactHidesThePasswordAndKeepsTheRest(t *testing.T) {
	// The redacted form is what goes into state, logs and `db ping` output. It has to
	// stay useful — an operator needs to see which server this is.
	got := Redact("mongodb://admin:s3cret@db.example:27017/?authSource=admin")
	if strings.Contains(got, "s3cret") {
		t.Fatalf("the password survived redaction: %s", got)
	}
	for _, want := range []string{"admin", "db.example:27017", "authSource=admin"} {
		if !strings.Contains(got, want) {
			t.Errorf("redaction removed %q, which is not a secret: %s", want, got)
		}
	}
	// A URI with no credentials at all must not gain a fake one.
	if plain := Redact("mongodb://127.0.0.1:27017/"); strings.Contains(plain, "REDACTED") {
		t.Errorf("a URI with no password was given one: %s", plain)
	}
	// And an unparseable value must not be echoed back, in case it is a secret in the
	// wrong shape.
	if bad := Redact("mongodb://a b c:\x00"); strings.Contains(bad, "\x00") {
		t.Errorf("an unparseable URI was echoed: %q", bad)
	}
}

func TestTheStagedScriptIsNotWritableByAnyoneElse(t *testing.T) {
	// It carries no secrets — every value arrives through the environment — but root
	// executes it, so a script another account can write is a local privilege escalation.
	m, _ := testManager(t)
	path, err := m.ScriptPathForTests()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&0o177 != 0 {
		t.Errorf("the staged script is mode %04o; root executes it, so nobody else may write it", perm)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The whole security argument rests on this: the script reads its parameters rather
	// than having them baked in. If a future change starts interpolating, this fails.
	if !strings.Contains(string(body), "process.env") {
		t.Error("the script does not read its parameters from the environment")
	}
	if !strings.Contains(string(body), "RATLINE_MONGO_URI") {
		t.Error("the script does not take the connection string from the environment, " +
			"which would put the admin password in argv")
	}
}

func TestDefaultNamesAreUsableAsMongoIdentifiers(t *testing.T) {
	// A domain has dots and hyphens; a MongoDB database name may have neither, because a
	// dot is a namespace separator and would make the database unaddressable in a role
	// document.
	for _, domain := range []string{
		"shop.example.com",
		"a-very-long-subdomain.that.keeps.going.example.co.uk",
		"UPPER.Example.COM",
	} {
		db := DefaultDatabaseName(domain)
		if strings.ContainsAny(db, `. /\"$*<>:|?`) {
			t.Errorf("DefaultDatabaseName(%q) = %q, which MongoDB will not accept", domain, db)
		}
		if db == "" {
			t.Errorf("DefaultDatabaseName(%q) is empty", domain)
		}
		if len(db) > 38 {
			t.Errorf("DefaultDatabaseName(%q) is %d characters", domain, len(db))
		}
		if user := DefaultUsername(db); len(user) > 63 {
			t.Errorf("DefaultUsername(%q) is %d characters", db, len(user))
		}
	}
}

func TestOnlyReadOperationsAreNonMutating(t *testing.T) {
	// --dry-run relies on this. An operation misfiled as read-only would run for real
	// during a preview, which is the one thing --dry-run promises not to do.
	for _, op := range []string{"ping", "listDatabases", "listUsers", "stats"} {
		if mutating(op) {
			t.Errorf("%s is read-only but marked as mutating", op)
		}
	}
	for _, op := range []string{
		"createDatabase", "dropDatabase", "createUser", "dropUser",
		"updatePassword", "updateRole",
		// An unknown operation counts as mutating: failing closed is the safe default
		// if somebody adds one and forgets this list.
		"somethingNew",
	} {
		if !mutating(op) {
			t.Errorf("%s changes the server but is marked read-only, so --dry-run would run it", op)
		}
	}
}

func TestTheResultIsFoundAmongMongoshChatter(t *testing.T) {
	// mongosh prints deprecation notices and connection lines even under --quiet on some
	// versions, so the JSON is located rather than assumed to be all of stdout.
	for _, tc := range []struct {
		name, out, want string
	}{
		{"clean", `{"ok":true,"version":"8.0"}`, `{"ok":true,"version":"8.0"}`},
		{
			"with chatter",
			"Warning: deprecated option\nconnecting...\n{\"ok\":true,\"version\":\"8.0\"}\n",
			`{"ok":true,"version":"8.0"}`,
		},
		{
			"trailing chatter after the result",
			"{\"ok\":false,\"error\":\"nope\"}\nBye!\n",
			`{"ok":false,"error":"nope"}`,
		},
		{"no json at all", "mongosh: command not found\n", ""},
		{"json without ok is not the result", "{\"other\":1}\n", ""},
	} {
		if got := lastJSONLine(tc.out); got != tc.want {
			t.Errorf("%s: lastJSONLine = %q, want %q", tc.name, got, tc.want)
		}
	}
}
