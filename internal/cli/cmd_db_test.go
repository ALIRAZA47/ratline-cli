package cli

import (
	"strings"
	"testing"
)

func TestAttachAndNoUserAreRefusedTogether(t *testing.T) {
	// --attach writes a user's connection string; --no-user says not to create one. Both
	// together silently dropped the attach, which is how an operator comes to believe a
	// site was given a credential it never received — and then debugs the application
	// rather than the provisioning. The project's rule is to refuse rather than pick a
	// winner, and this is the same shape as the --instances refusal.
	code, _, errOut := harness(t, "db", "create", "shop",
		"--owner", "acme", "--no-user", "--attach", "shop.example.com")
	// Without root, or with the feature off, this refuses earlier — so the assertion is
	// on the message when it is the flags that were caught, and on it never being a
	// success either way.
	if code == 0 {
		t.Error("--attach with --no-user was accepted; the attach would be silently dropped")
	}
	if strings.Contains(errOut.String(), "contradict") {
		if !strings.Contains(errOut.String(), "--no-user") {
			t.Errorf("the refusal should name both flags, got:\n%s", errOut.String())
		}
	}
}

func TestTheDatabaseCommandIsRegisteredWithItsVerbs(t *testing.T) {
	// The stub declared verbs that did nothing. These are the real ones, and a missing
	// registration would only show up as "unknown command" on a server.
	g := NewGlobals()
	root := NewRootCommand(g)
	for _, c := range root.Commands() {
		if c.Name() != "db" {
			continue
		}
		want := map[string]bool{
			"ping": false, "create": false, "list": false, "show": false,
			"drop": false, "user": false, "roles": false,
		}
		for _, sub := range c.Commands() {
			if _, ok := want[sub.Name()]; ok {
				want[sub.Name()] = true
			}
		}
		for name, found := range want {
			if !found {
				t.Errorf("ratline db has no %q subcommand", name)
			}
		}
		// And the user group, which is where the credentials are managed.
		for _, sub := range c.Commands() {
			if sub.Name() != "user" {
				continue
			}
			verbs := map[string]bool{
				"add": false, "list": false, "password": false, "grant": false, "delete": false,
			}
			for _, v := range sub.Commands() {
				if _, ok := verbs[v.Name()]; ok {
					verbs[v.Name()] = true
				}
			}
			for name, found := range verbs {
				if !found {
					t.Errorf("ratline db user has no %q subcommand", name)
				}
			}
		}
		return
	}
	t.Fatal("the db command is not registered")
}

func TestMutatingDatabaseCommandsTakeTheLock(t *testing.T) {
	// They write to MongoDB and to a site's .env, so they must not interleave with a
	// deploy that is halfway through rendering a unit. The read-only ones must not take
	// the lock, or `db list` during a long deploy would block.
	g := NewGlobals()
	root := NewRootCommand(g)
	for _, c := range root.Commands() {
		if c.Name() != "db" {
			continue
		}
		for _, sub := range c.Commands() {
			switch sub.Name() {
			case "create", "drop":
				if !annotated(sub, AnnoMutates) {
					t.Errorf("db %s changes the server but is not marked as mutating", sub.Name())
				}
			case "ping", "list", "show", "roles":
				if annotated(sub, AnnoMutates) {
					t.Errorf("db %s only reads but takes the lock", sub.Name())
				}
			}
		}
	}
}
