package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
)

func TestConfigCommandsAreRegistered(t *testing.T) {
	// The file ratline writes has always said "the commented reference is available from
	// 'ratline config reference'" — a command that did not exist, on every server.
	g := NewGlobals()
	root := NewRootCommand(g)
	for _, c := range root.Commands() {
		if c.Name() != "config" {
			continue
		}
		want := map[string]bool{
			"show": false, "get": false, "set": false, "unset": false,
			"path": false, "reference": false, "edit": false, "validate": false,
		}
		for _, sub := range c.Commands() {
			if _, ok := want[sub.Name()]; ok {
				want[sub.Name()] = true
			}
		}
		for name, found := range want {
			if !found {
				t.Errorf("ratline config has no %q subcommand", name)
			}
		}
		// Reading configuration must work without root, and on a server where init has
		// never run — otherwise `config reference` cannot help somebody set it up.
		for _, sub := range c.Commands() {
			switch sub.Name() {
			case "show", "get", "path", "reference", "validate":
				if !annotated(sub, AnnoAllowNonRoot) {
					t.Errorf("config %s only reads but demands root", sub.Name())
				}
				if annotated(sub, AnnoMutates) {
					t.Errorf("config %s only reads but takes the lock", sub.Name())
				}
			case "set", "unset", "edit":
				if !annotated(sub, AnnoMutates) {
					t.Errorf("config %s writes the file but is not marked as mutating", sub.Name())
				}
			}
		}
		return
	}
	t.Fatal("the config command is not registered")
}

func TestTheWrittenFilePointsAtACommandThatExists(t *testing.T) {
	// Save writes a header naming `ratline config reference`. If that command is ever
	// renamed, every server's config.yaml would point at nothing.
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	cfg := config.Default()
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	named := ""
	for _, line := range strings.Split(string(body), "\n") {
		if i := strings.Index(line, "ratline config "); i >= 0 {
			rest := line[i+len("ratline config "):]
			named = strings.Fields(strings.Trim(rest, "'\"."))[0]
			break
		}
	}
	if named == "" {
		return // the header no longer names a command, which is also fine
	}
	g := NewGlobals()
	root := NewRootCommand(g)
	for _, c := range root.Commands() {
		if c.Name() != "config" {
			continue
		}
		for _, sub := range c.Commands() {
			if sub.Name() == named {
				return
			}
		}
		t.Errorf("the configuration file it writes names 'ratline config %s', which does not exist", named)
	}
}

func TestASettingTypoIsRefusedWithASuggestion(t *testing.T) {
	// An unknown key written into the file would sit there being ignored, and the
	// misconfiguration surfaces as ratline writing units somewhere nobody looks. The
	// reference uses exactly this typo as its example.
	for _, tc := range []struct{ typo, want string }{
		{"paths.systemdir", "paths.systemd_dir"},
		{"features.dbprovisioning", "features.db_provisioning"},
		{"acme.emial", "acme.email"},
		{"email", "acme.email"},
		{"memory_max", "defaults.memory_max"},
	} {
		got := nearestSetting(tc.typo)
		if got != tc.want {
			t.Errorf("nearestSetting(%q) = %q, want %q", tc.typo, got, tc.want)
		}
	}
	// And nonsense gets no suggestion rather than a confidently wrong one.
	for _, nonsense := range []string{"zzzzzzzzzz", "completely-unrelated-thing"} {
		if got := nearestSetting(nonsense); got != "" {
			t.Errorf("nearestSetting(%q) = %q; it should admit it does not know", nonsense, got)
		}
	}
}

func TestBooleanSettingsAreRecognised(t *testing.T) {
	// So "yes" can be normalised to the true a YAML parser reads back, rather than
	// written as the string "yes" and silently ignored.
	for _, key := range []string{"features.db_provisioning", "features.strict_isolation", "nginx.gzip"} {
		if !isBoolSetting(key) {
			t.Errorf("isBoolSetting(%q) = false", key)
		}
	}
	for _, key := range []string{"acme.email", "defaults.memory_max", "acme.renew_before_days"} {
		if isBoolSetting(key) {
			t.Errorf("isBoolSetting(%q) = true; it is not a boolean", key)
		}
	}
}

func TestSecretsAreNotPrintedByShow(t *testing.T) {
	// The configuration deliberately holds no passwords — the MongoDB URI and the DNS
	// credentials live in their own 0600 files — but a webhook URL usually carries a
	// token in its path, and `config show` output ends up in support tickets.
	if !config.IsSecret("acme.alerts.webhook_url") {
		t.Error("the alert webhook is not treated as a secret")
	}
	if config.IsSecret("acme.email") {
		t.Error("an email address is not a secret")
	}
}
