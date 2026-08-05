package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The editor rewrites the file that configures everything else, so the tests are about
// what must survive an edit rather than about the edit itself.

const sample = `# ratline configuration
version: 1

paths:
  state_db: /var/lib/ratline/state.db
  # The MongoDB admin connection string, 0600 and root-owned.
  mongo_uri_file: /etc/ratline/db/mongodb.uri

acme:
  email: ""
  renew_before_days: 30      # the window that decides
  rate_limits:
    duplicate_certs_per_week: 5

features:
  # Turns on ratline db.
  db_provisioning: false
  strict_isolation: false
`

func TestSettingAValueKeepsEveryComment(t *testing.T) {
	// The whole reason this exists. Re-encoding the struct destroyed every comment in the
	// file, including on the first `ratline init` — so an operator who opened it found a
	// bare list of values and no explanation of any of them.
	out, err := SetValue([]byte(sample), "features.db_provisioning", "true")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, comment := range []string{
		"# ratline configuration",
		"# The MongoDB admin connection string, 0600 and root-owned.",
		"# Turns on ratline db.",
		"# the window that decides",
	} {
		if !strings.Contains(got, comment) {
			t.Errorf("the edit lost this comment: %s", comment)
		}
	}
	if !strings.Contains(got, "db_provisioning: true") {
		t.Errorf("the value was not set:\n%s", got)
	}
	if strings.Contains(got, "db_provisioning: false") {
		t.Error("the old value is still there")
	}
	// Untouched settings must be untouched, byte for byte.
	if !strings.Contains(got, "strict_isolation: false") {
		t.Error("an unrelated setting changed")
	}
	if !strings.Contains(got, "  state_db: /var/lib/ratline/state.db") {
		t.Error("indentation was not preserved")
	}
}

func TestATrailingCommentSurvivesTheValueChanging(t *testing.T) {
	// The comment explains the setting, so it has to outlive the value it explains.
	out, err := SetValue([]byte(sample), "acme.renew_before_days", "45")
	if err != nil {
		t.Fatal(err)
	}
	line := lineContaining(string(out), "renew_before_days")
	if !strings.Contains(line, "45") {
		t.Errorf("value not set: %q", line)
	}
	if !strings.Contains(line, "# the window that decides") {
		t.Errorf("the trailing comment was dropped: %q", line)
	}
}

func TestTheResultStillParsesAsTheSameShape(t *testing.T) {
	// A textual edit that produces a file the loader cannot read would be discovered by
	// the next command rather than by the one that made it.
	out, err := SetValue([]byte(sample), "acme.email", `"ops@example.com"`)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(out, &m); err != nil {
		t.Fatalf("the edited file no longer parses: %v", err)
	}
	acme, ok := m["acme"].(map[string]any)
	if !ok {
		t.Fatal("the acme section is gone")
	}
	if acme["email"] != "ops@example.com" {
		t.Errorf("email = %v", acme["email"])
	}
	// And the deeper section is still nested where it was, not flattened.
	if _, ok := acme["rate_limits"].(map[string]any); !ok {
		t.Error("acme.rate_limits stopped being a section")
	}
}

func TestThreeLevelsDeepIsReachable(t *testing.T) {
	// databases.mongodb.default_role and acme.rate_limits.* are real settings.
	out, err := SetValue([]byte(sample), "acme.rate_limits.duplicate_certs_per_week", "7")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "duplicate_certs_per_week: 7") {
		t.Errorf("the nested value was not set:\n%s", out)
	}
	got, found, err := GetValue(out, "acme.rate_limits.duplicate_certs_per_week")
	if err != nil || !found || got != "7" {
		t.Errorf("GetValue = (%q, %v, %v)", got, found, err)
	}
}

func TestAKeyIsNotConfusedWithTheSameNameElsewhere(t *testing.T) {
	// Two sections both have a key called strict_isolation-shaped name in real configs;
	// here `email` exists under acme, and a naive line search for "email:" would also
	// match one under alerts. Matching is by depth and by parent, so it does not.
	body := `acme:
  email: first@example.com
  alerts:
    email: second@example.com
`
	out, err := SetValue([]byte(body), "acme.alerts.email", "changed@example.com")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "email: first@example.com") {
		t.Errorf("the wrong email was changed:\n%s", got)
	}
	if !strings.Contains(got, "email: changed@example.com") {
		t.Errorf("the nested email was not changed:\n%s", got)
	}
}

func TestAnAbsentKeyIsAddedInsideItsSection(t *testing.T) {
	// A setting the shipped file omits — because the default applies — still has to be
	// settable, and it has to land inside the right section rather than after it.
	body := "acme:\n  email: a@b.c\n\nfeatures:\n  strict_isolation: false\n"
	out, err := SetValue([]byte(body), "acme.key_type", "rsa")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(out, &m); err != nil {
		t.Fatalf("the result does not parse: %v\n%s", err, out)
	}
	acme := m["acme"].(map[string]any)
	if acme["key_type"] != "rsa" {
		t.Errorf("the key landed in the wrong place:\n%s", out)
	}
	// And it must not have been swallowed into the following section.
	feat := m["features"].(map[string]any)
	if _, wrong := feat["key_type"]; wrong {
		t.Errorf("the key landed in features:\n%s", out)
	}
}

func TestAMissingSectionIsRefused(t *testing.T) {
	// Inventing a section means guessing where it belongs and what else in it is
	// missing. Refusing names the command that shows the shipped file.
	_, err := SetValue([]byte("version: 1\n"), "databases.mongodb.default_role", "read")
	if err == nil {
		t.Fatal("a missing section was invented")
	}
	if !strings.Contains(err.Error(), "databases.mongodb") {
		t.Errorf("the refusal should name the missing section: %v", err)
	}
}

func TestUnsettingRestoresTheDefaultAndTakesItsCommentWith(t *testing.T) {
	out, removed, err := UnsetValue([]byte(sample), "features.db_provisioning")
	if err != nil || !removed {
		t.Fatalf("UnsetValue = (%v, %v)", removed, err)
	}
	got := string(out)
	if strings.Contains(got, "db_provisioning") {
		t.Errorf("the setting is still there:\n%s", got)
	}
	// Its own comment goes with it; leaving it orphaned above an unrelated setting would
	// describe the wrong thing.
	if strings.Contains(got, "# Turns on ratline db.") {
		t.Errorf("the setting's comment was left behind describing something else:\n%s", got)
	}
	if !strings.Contains(got, "strict_isolation: false") {
		t.Error("the sibling setting was removed too")
	}
	if _, found, _ := GetValue(out, "features.db_provisioning"); found {
		t.Error("GetValue still finds it")
	}
}

func TestScalarsThatWouldChangeTypeAreQuoted(t *testing.T) {
	// An unquoted `no` is a boolean to YAML, so a label or a role that reads that way
	// would come back as the wrong type — and the failure would be a confusing type
	// error on a later command rather than here.
	for _, tc := range []struct{ in, want string }{
		{"true", "true"},
		{"false", "false"},
		{"yes", `"yes"`},
		{"no", `"no"`},
		{"on", `"on"`},
		{"off", `"off"`},
		{"null", `"null"`},
		{"", `""`},
		{"30", "30"},
		{"0", "0"},
		{"0755", `"0755"`}, // otherwise octal
		{"+1", `"+1"`},     // not valid YAML bare
		{"1.5", "1.5"},
		{"readWrite", "readWrite"},
		{"ops@example.com", "ops@example.com"},
		{"mongodb://a:b@c/d", `"mongodb://a:b@c/d"`}, // colons
		{" leading", `" leading"`},
	} {
		if got := FormatScalar(tc.in); got != tc.want {
			t.Errorf("FormatScalar(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
	// And every one of those round-trips to the string it was typed as.
	for _, in := range []string{"yes", "no", "off", "null", "0755", "+1", " leading", "mongodb://a:b@c/d"} {
		body, err := SetValue([]byte("acme:\n  email: x\n"), "acme.email", FormatScalar(in))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := yaml.Unmarshal(body, &m); err != nil {
			t.Fatalf("%q produced a file that does not parse: %v", in, err)
		}
		if got := m["acme"].(map[string]any)["email"]; got != in {
			t.Errorf("%q round-tripped as %#v", in, got)
		}
	}
}

func TestAnUnparseableIndentIsRefused(t *testing.T) {
	// A hand-edited file with three-space indents is not something this can edit safely,
	// and a wrong edit to a configuration file is worse than no edit.
	_, err := SetValue([]byte("acme:\n   email: a@b.c\n"), "acme.email", "c@d.e")
	if err == nil {
		t.Fatal("an oddly indented file was edited anyway")
	}
	if !strings.Contains(err.Error(), "indented") {
		t.Errorf("the refusal should say why: %v", err)
	}
}

func TestEveryShippedKeyIsFoundAndKnown(t *testing.T) {
	// KnownKeys drives completion and `config show`, and KeyExists is what turns a typo
	// into a refusal. Both are checked against the shipped file, so a new setting added
	// to defaults.yaml is picked up without a second list to maintain.
	keys := KnownKeys()
	if len(keys) < 40 {
		t.Fatalf("only %d keys found in the shipped defaults, which cannot be right", len(keys))
	}
	for _, k := range keys {
		if !KeyExists(k) {
			t.Errorf("KnownKeys lists %q but KeyExists says it does not exist", k)
		}
		if _, found, err := GetValue(DefaultYAML(), k); err != nil || !found {
			t.Errorf("%q is listed but not readable from the shipped file", k)
		}
	}
	// Spot-check the ones this session added, and a typo.
	for _, k := range []string{
		"features.db_provisioning", "paths.mongo_uri_file",
		"databases.mongodb.default_role", "acme.email",
	} {
		if !KeyExists(k) {
			t.Errorf("KeyExists(%q) = false", k)
		}
	}
	for _, k := range []string{"paths.systemdir", "features.nope", "acme.emial"} {
		if KeyExists(k) {
			t.Errorf("KeyExists(%q) = true for a key that does not exist", k)
		}
	}
}

func TestTheShippedDefaultsCanBeEditedInPlace(t *testing.T) {
	// The real file, not a fixture: it is the one operators actually edit, and it has
	// section comments, trailing comments, blank lines and three levels of nesting.
	body := DefaultYAML()
	for _, tc := range []struct{ key, value string }{
		{"features.db_provisioning", "true"},
		{"acme.email", `"ops@example.com"`},
		{"databases.mongodb.default_role", "read"},
		{"defaults.memory_max", "1G"},
	} {
		out, err := SetValue(body, tc.key, tc.value)
		if err != nil {
			t.Fatalf("SetValue(%s) = %v", tc.key, err)
		}
		body = out
	}
	// It must still load through the real loader, strictly — an unknown key is an error
	// there, so a misplaced line is caught.
	var c Config
	if err := decodeStrict(body, &c); err != nil {
		t.Fatalf("the edited shipped file no longer loads: %v", err)
	}
	if !c.Features.DBProvisioning {
		t.Error("features.db_provisioning did not take")
	}
	if c.ACME.Email != "ops@example.com" {
		t.Errorf("acme.email = %q", c.ACME.Email)
	}
	if c.Databases.MongoDB.DefaultRole != "read" {
		t.Errorf("default_role = %q", c.Databases.MongoDB.DefaultRole)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("the edited file does not validate: %v", err)
	}
	// And the comments are all still there.
	if n := strings.Count(string(body), "#"); n < 50 {
		t.Errorf("only %d comment markers left in the shipped file; they were being destroyed", n)
	}
}

func lineContaining(body, needle string) string {
	for _, l := range strings.Split(body, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	return ""
}

func TestSaveKeepsTheCommentsItUsedToDestroy(t *testing.T) {
	// The bug this fixes: `ratline init` records the ACME email, calls Save, and the
	// commented reference the documentation promises became a bare list of values. The
	// operator's next step is usually to open that file and read it.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if _, err := Seed(path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	commentsBefore := strings.Count(string(before), "#")
	if commentsBefore < 50 {
		t.Fatalf("the seeded file has only %d comments, so this test proves nothing", commentsBefore)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ACME.Email = "ops@example.com"
	cfg.Features.DBProvisioning = true
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(after), "#"); n < commentsBefore {
		t.Errorf("comments went from %d to %d — Save is still flattening the file",
			commentsBefore, n)
	}
	// And the values it was called to change actually changed.
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("the saved file no longer loads: %v", err)
	}
	if reloaded.ACME.Email != "ops@example.com" {
		t.Errorf("acme.email = %q", reloaded.ACME.Email)
	}
	if !reloaded.Features.DBProvisioning {
		t.Error("features.db_provisioning did not persist")
	}
	// Saving again with nothing changed must not churn the file.
	if err := reloaded.Save(path); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(after) {
		t.Error("a no-op Save rewrote the file, which would churn it on every command")
	}
}

func TestSaveStillWorksWithNoExistingFile(t *testing.T) {
	// The fallback path: there is nothing to merge into, so a full encode is correct.
	dir := t.TempDir()
	path := filepath.Join(dir, "new.yaml")
	cfg := Default()
	cfg.ACME.Email = "ops@example.com"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.ACME.Email != "ops@example.com" {
		t.Errorf("email = %q", back.ACME.Email)
	}
}
