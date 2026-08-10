package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/mongod"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

func TestInstallAndAccessAreRegistered(t *testing.T) {
	g := NewGlobals()
	root := NewRootCommand(g)
	for _, c := range root.Commands() {
		if c.Name() != "db" {
			continue
		}
		found := map[string]bool{"install": false, "access": false}
		for _, sub := range c.Commands() {
			if _, ok := found[sub.Name()]; ok {
				found[sub.Name()] = true
			}
			switch sub.Name() {
			case "install":
				// It installs packages, writes /etc and restarts a service: mutating,
				// under the global lock.
				if !annotated(sub, AnnoMutates) {
					t.Error("db install is not marked as mutating")
				}
			case "access":
				verbs := map[string]bool{"allow": false, "revoke": false, "list": false}
				for _, v := range sub.Commands() {
					if _, ok := verbs[v.Name()]; ok {
						verbs[v.Name()] = true
					}
					switch v.Name() {
					case "allow", "revoke":
						if !annotated(v, AnnoMutates) {
							t.Errorf("db access %s changes the firewall but is not marked as mutating", v.Name())
						}
					case "list":
						if annotated(v, AnnoMutates) {
							t.Error("db access list only reads but takes the lock")
						}
					}
				}
				for name, ok := range verbs {
					if !ok {
						t.Errorf("ratline db access has no %q subcommand", name)
					}
				}
			}
		}
		for name, ok := range found {
			if !ok {
				t.Errorf("ratline db has no %q subcommand", name)
			}
		}
		return
	}
	t.Fatal("the db command is not registered")
}

func TestCheckAdminPassword(t *testing.T) {
	// The floor is against accidents — an empty string, a stray "y" from a mistyped
	// pipeline — and the control-character rule is the same one every render-bound
	// field obeys.
	for _, bad := range []string{"", "y", "seven77", "with\nnewline9"} {
		if err := checkAdminPassword(bad); err == nil {
			t.Errorf("password %q was accepted", bad)
		}
	}
	for _, good := range []string{"eight888", "a much longer password with spaces", "p%40ss/w:rd@8"} {
		if err := checkAdminPassword(good); err != nil {
			t.Errorf("password %q was refused: %v", good, err)
		}
	}
}

func TestInstallPlanIsResolvedWithoutExecuting(t *testing.T) {
	// The plan must come from resolving, not rehearsing: each real step preconditions
	// on the previous one having happened, so running them under --dry-run would
	// report a false failure. What can be printed truthfully is what would be done.
	out := &bytes.Buffer{}
	g := NewGlobals()
	g.Stdout, g.Stderr, g.Stdin = out, &bytes.Buffer{}, strings.NewReader("")
	g.Log = log.Discard()
	g.Cfg = config.Default()

	mgr := &mongod.Manager{
		Cfg: g.Cfg, Log: g.Log,
		OS:             system.OSInfo{ID: "ubuntu", Codename: "jammy", Arch: "amd64"},
		InstalledProbe: func() bool { return false },
	}
	if err := g.printInstallPlan(mgr, ""); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{
		"Would install MongoDB 8.0",
		"jammy/mongodb-org/8.0",
		"signed-by=/usr/share/keyrings/mongodb-server-8.0.gpg",
		"authorization enabled, localhost only",
		"verify it enforces authorization",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the plan is missing %q:\n%s", want, s)
		}
	}

	// An unsupported host refuses at plan time too, with the same message the real
	// run would give.
	mgr.OS = system.OSInfo{ID: "fedora", Codename: "rawhide", Arch: "amd64", PrettyName: "Fedora"}
	if err := g.printInstallPlan(mgr, ""); err == nil {
		t.Error("a plan was printed for a host the repository does not publish for")
	}
}
