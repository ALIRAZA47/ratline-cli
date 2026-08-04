package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDefaultsParse is the guard that keeps defaults.yaml and the Go structs in
// step. Because Default() parses the same embedded file an operator edits, a key
// added to one and not the other fails here rather than in production.
func TestDefaultsParse(t *testing.T) {
	c := Default()
	if c.Version != SchemaVersion {
		t.Errorf("version = %d, want %d", c.Version, SchemaVersion)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("the built-in defaults do not validate: %v", err)
	}
}

func TestDefaultValues(t *testing.T) {
	c := Default()
	checks := map[string]struct{ got, want any }{
		"state db":          {c.Paths.StateDB, "/var/lib/ratline/state.db"},
		"lock":              {c.Paths.Lock, "/run/ratline.lock"},
		"acme webroot":      {c.Paths.ACMEWebroot, "/var/www/ratline-acme"},
		"shell":             {c.Defaults.Shell, "/bin/bash"},
		"umask":             {c.Defaults.Umask, "0027"},
		"home mode":         {c.Users.HomeMode, "0750"},
		"body size":         {c.Defaults.ClientMaxBodySize, "20M"},
		"memory max":        {c.Defaults.MemoryMax, "512M"},
		"worker cap":        {c.Defaults.WorkerCap, 8},
		"port range start":  {c.Ports.RangeStart, 20000},
		"port range end":    {c.Ports.RangeEnd, 29999},
		"min rsa bits":      {c.SSH.MinRSABits, 3072},
		"key type":          {c.ACME.KeyType, "ecdsa"},
		"renew before days": {c.ACME.RenewBeforeDays, 30},
		"nginx user":        {c.Users.NginxUser, "www-data"},
		"log group":         {c.Users.LogGroup, "adm"},
	}
	for name, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", name, c.got, c.want)
		}
	}

	// Defaults that must be off, because turning them on is a decision.
	if c.Defaults.HSTS {
		t.Error("HSTS defaults to on; it must be opt-in")
	}
	if c.Users.AllowSudo {
		t.Error("sudo is allowed by default")
	}
	if c.SSH.AllowRootKeys {
		t.Error("root keys are allowed by default")
	}
	if c.ACME.TOSAgreed {
		t.Error("the CA terms are marked agreed by default")
	}
	if c.Users.QuotaEnabled {
		t.Error("quotas are enabled by default, but a fresh VPS has no quota support")
	}
	if c.Features.DBProvisioning {
		t.Error("the unfinished database feature is enabled by default")
	}
	// Defaults that must be on.
	if !c.SSH.VerifyAfterChange {
		t.Error("sshd changes are not verified by default; that is how people lock themselves out")
	}
	if !c.SSH.SiteScopeSFTPOnly {
		t.Error("site-scoped keys get a shell by default")
	}

	if !contains(c.SSH.RejectedAlgorithms, "ssh-dss") {
		t.Error("ssh-dss is not in the rejected list")
	}
	if !contains(c.SSH.AllowedAlgorithms, "ssh-ed25519") {
		t.Error("ed25519 is not in the allowed list")
	}
}

func TestDurationParsing(t *testing.T) {
	c := Default()
	if got, want := c.Defaults.HealthTimeout.D(), 30*time.Second; got != want {
		t.Errorf("health timeout = %v, want %v", got, want)
	}
	if got, want := c.Runtimes.InstallTimeout.D(), 30*time.Minute; got != want {
		t.Errorf("install timeout = %v, want %v", got, want)
	}
}

func TestLoadMergesOntoDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	// A partial file is a valid file: only the overrides are named.
	if err := os.WriteFile(path, []byte(`
version: 1
acme:
  email: ops@example.com
  renew_before_days: 21
defaults:
  memory_max: 1G
`), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load = %v", err)
	}
	if c.ACME.Email != "ops@example.com" {
		t.Errorf("email = %q", c.ACME.Email)
	}
	if c.ACME.RenewBeforeDays != 21 {
		t.Errorf("renew_before_days = %d, want 21", c.ACME.RenewBeforeDays)
	}
	if c.Defaults.MemoryMax != "1G" {
		t.Errorf("memory_max = %q, want 1G", c.Defaults.MemoryMax)
	}
	// Untouched settings keep their defaults.
	if c.Paths.StateDB != "/var/lib/ratline/state.db" {
		t.Errorf("an unmentioned path lost its default: %q", c.Paths.StateDB)
	}
	if c.ACME.KeyType != "ecdsa" {
		t.Errorf("an unmentioned setting lost its default: %q", c.ACME.KeyType)
	}
	if !c.Loaded {
		t.Error("Loaded is false after a successful load")
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	// A typo in a setting name must be an error. Silently ignoring it is how a
	// server ends up not doing what its config says.
	if err := os.WriteFile(path, []byte("version: 1\nacme:\n  emial: ops@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load accepted an unknown key")
	}
	if !strings.Contains(err.Error(), "emial") {
		t.Errorf("the error does not name the offending key: %v", err)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"a relative path":             "version: 1\npaths:\n  state_db: relative/state.db\n",
		"a bad size":                  "version: 1\ndefaults:\n  memory_max: 512Q\n",
		"a bad cpu quota":             "version: 1\ndefaults:\n  cpu_quota: half\n",
		"a bad port range":            "version: 1\nports:\n  range_start: 500\n",
		"an inverted range":           "version: 1\nports:\n  range_start: 29999\n  range_end: 20000\n",
		"a bad key type":              "version: 1\nacme:\n  key_type: dsa\n",
		"a bad email":                 "version: 1\nacme:\n  email: not-an-email\n",
		"weak rsa policy":             "version: 1\nssh:\n  min_rsa_bits: 1024\n",
		"an empty algo list":          "version: 1\nssh:\n  allowed_algorithms: []\n",
		"a bad log level":             "version: 1\nlogging:\n  level: trace\n",
		"a bad umask":                 "version: 1\ndefaults:\n  umask: \"0999\"\n",
		"a bad duration":              "version: 1\ndefaults:\n  health_timeout: soon\n",
		"a plain http acme":           "version: 1\nacme:\n  directory_url: http://acme.example.com/directory\n",
		"an unknown version":          "version: 99\n",
		"a renew window over 89 days": "version: 1\nacme:\n  renew_before_days: 120\n",
	}
	for why, body := range cases {
		path := filepath.Join(dir, strings.ReplaceAll(why, " ", "_")+".yaml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("Load accepted %s", why)
		}
	}
}

func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	c := Default()
	c.ACME.KeyType = "dsa"
	c.Defaults.MemoryMax = "512Q"
	c.Ports.RangeStart = 10
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate accepted three broken settings")
	}
	// Fixing one problem per run is a poor experience, so all of them are named.
	msg := err.Error()
	for _, want := range []string{"key_type", "memory_max", "ports"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not mention %s:\n%s", want, msg)
		}
	}
}

func TestLoadOrDefaultToleratesAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")
	c, err := LoadOrDefault(path)
	if err != nil {
		t.Fatalf("LoadOrDefault = %v", err)
	}
	if c.Loaded {
		t.Error("Loaded is true for a file that does not exist")
	}
	if c.Paths.StateDB == "" {
		t.Error("the defaults were not applied")
	}
	// A malformed file is still an error, though.
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("version: 1\nnonsense:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrDefault(bad); err == nil {
		t.Error("LoadOrDefault accepted a malformed file")
	}
}

func TestSeedWritesTheCommentedReferenceOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "etc", "config.yaml")
	created, err := Seed(path)
	if err != nil || !created {
		t.Fatalf("Seed = %v, created=%v", err, created)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The seeded file is the reference, comments and all.
	if !strings.Contains(string(data), "# ratline configuration") {
		t.Error("the seeded file lost its comments")
	}
	if !strings.Contains(string(data), "HSTS is opt-in") {
		t.Error("the seeded file lost its explanatory comments")
	}

	// An existing file is never overwritten.
	if err := os.WriteFile(path, []byte("version: 1\n# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err = Seed(path)
	if err != nil || created {
		t.Fatalf("second Seed = %v, created=%v; want nil, false", err, created)
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "# mine") {
		t.Error("Seed overwrote an existing file")
	}
}

func TestSaveRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c := Default()
	c.ACME.Email = "ops@example.com"
	c.Runtimes.NodeDefault = "22"
	if err := c.Save(path); err != nil {
		t.Fatalf("Save = %v", err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Save = %v", err)
	}
	if back.ACME.Email != "ops@example.com" || back.Runtimes.NodeDefault != "22" {
		t.Errorf("values did not survive the round trip: %+v", back.ACME)
	}
}

func TestDerivedPaths(t *testing.T) {
	c := Default()
	cases := map[string]struct{ got, want string }{
		"home":     {c.HomeDir("alice"), "/home/alice"},
		"site dir": {c.SiteDir("alice", "example.com"), "/home/alice/example.com"},
		"run dir":  {c.RuntimeDir("alice", "example.com"), "/run/ratline/alice-example_com"},
		"socket":   {c.SocketPath("alice", "example.com"), "/run/ratline/alice-example_com/app.sock"},
		"vhost":    {c.VhostPath("example.com"), "/etc/nginx/sites-available/example.com.conf"},
		"link":     {c.VhostLink("example.com"), "/etc/nginx/sites-enabled/example.com.conf"},
		"unit":     {c.UnitPath("alice", "example.com"), "/etc/systemd/system/ratline-alice-example_com.service"},
	}
	for name, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", name, tc.got, tc.want)
		}
	}
	if got := c.UmaskValue(); got != 0o027 {
		t.Errorf("umask = %04o, want 0027", got)
	}
	if got := c.HomeFileMode(); got != 0o750 {
		t.Errorf("home mode = %04o, want 0750", got)
	}
}

func TestCloneIsolatesTheCachedDefaults(t *testing.T) {
	a := Default()
	a.Users.Reserved = append(a.Users.Reserved, "mutated")
	a.SSH.CommandPresets["extra"] = "x"
	b := Default()
	if contains(b.Users.Reserved, "mutated") {
		t.Error("mutating one config's slice affected the cached defaults")
	}
	if _, ok := b.SSH.CommandPresets["extra"]; ok {
		t.Error("mutating one config's map affected the cached defaults")
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
