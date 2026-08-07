package cli

import (
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// The derived database name has to satisfy MongoDB's rules, or the operator gets a
// failure from mongod about a name they never chose.
func TestTheDerivedDatabaseNameIsUsable(t *testing.T) {
	for domain, want := range map[string]string{
		"app.example.com":   "app_example_com",
		"WWW.Example.COM":   "www_example_com",
		"a-b.example.co.uk": "a_b_example_co_uk",
	} {
		got := databaseNameFor(domain)
		if got != want {
			t.Errorf("databaseNameFor(%q) = %q, want %q", domain, got, want)
		}
	}
	// A domain long enough to exceed MongoDB's namespace budget is truncated, not refused.
	long := strings.Repeat("verylongname.", 6) + "example.com"
	got := databaseNameFor(long)
	if len(got) > 38 {
		t.Errorf("databaseNameFor(long) = %q, %d characters; the cap is 38", got, len(got))
	}
	if strings.HasSuffix(got, "_") || strings.HasPrefix(got, "_") {
		t.Errorf("databaseNameFor(long) = %q, which has a stray separator at an end", got)
	}
}

// Every derived name must pass the validator the db command will apply to it.
func TestEveryDerivedNameSurvivesTheRealValidator(t *testing.T) {
	for _, domain := range []string{
		"app.example.com", "a.b.c.d.example.com", "x.io",
		strings.Repeat("long-", 12) + "example.com",
		"123.example.com",
	} {
		if err := validate.DatabaseName(databaseNameFor(domain)); err != nil {
			t.Errorf("databaseNameFor(%q) = %q, which the validator rejects: %v",
				domain, databaseNameFor(domain), err)
		}
	}
}

// The global flags have to reach each step. Binding a fresh root writes the flag defaults
// back over the fields, so --dry-run on the composite would become a real run part-way
// through if these were not passed explicitly.
func TestTheGlobalFlagsArePassedToEveryStep(t *testing.T) {
	c := &composer{g: &Globals{DryRun: true, Yes: true, NoInput: true}}
	got := strings.Join(c.inherited(), " ")
	for _, want := range []string{"--dry-run", "--yes", "--no-input"} {
		if !strings.Contains(got, want) {
			t.Errorf("inherited() = %q, missing %s — the step would not honour it", got, want)
		}
	}
	if plain := (&composer{g: &Globals{}}).inherited(); len(plain) != 0 {
		t.Errorf("inherited() with nothing set = %v, want nothing", plain)
	}
}

// A dry run prints the plan instead of running the steps.
//
// Running them was the first behaviour, and it was wrong in the worst way a preview can be:
// the tenant step correctly created nothing, so the site step was told "no such user" and
// the command exited 3 — reporting a failure for a stack that was perfectly buildable. A
// preview that invents errors is worse than no preview, because it stops people building
// things that would have worked.
func TestADryRunPrintsThePlanRatherThanFailingOnIt(t *testing.T) {
	out := &strings.Builder{}
	g := &Globals{DryRun: true, Stdout: out, Stderr: out}
	g.Log = log.Discard()
	s := &stack{g: g, c: &composer{g: g}, Domain: "app.example.com", Owner: "acme"}

	p := plan{steps: []step{
		{what: "tenant", argv: []string{"user", "add", "acme"}, undo: []string{"user", "delete", "acme"}},
		{what: "site", argv: []string{"site", "add", "app.example.com", "--user", "acme"},
			undo: []string{"site", "delete", "app.example.com"}},
		{what: "cert", argv: []string{"cert", "issue", "app.example.com"},
			kept: "A certificate is the exception: it is not revoked."},
	}}
	s.c.rehearse(p, "The domain and the tenant name were checked.")

	got := out.String()
	for _, want := range []string{
		"ratline user add acme",
		"ratline site add app.example.com --user acme",
		"ratline cert issue app.example.com",
		"Nothing was written",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the rehearsal does not mention %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "everything created before it would be removed") {
		t.Errorf("the rehearsal does not say what a failure would take back:\n%s", got)
	}
	// A step that is not taken back has to be named, or a preview that promises everything
	// comes back is read as covering the certificate too.
	if !strings.Contains(got, "A certificate is the exception") {
		t.Errorf("the rehearsal does not name the step it will not undo:\n%s", got)
	}
	// And it must not put a number on it. How many things come back depends on which step
	// fails; the first version said "the 3 things before it" about a three-step plan, where
	// the most that can ever be removed is two.
	for _, wrong := range []string{"1 thing before", "2 things before", "3 things before"} {
		if strings.Contains(got, wrong) {
			t.Errorf("the rehearsal counts what it would undo (%q), which is only ever right "+
				"for one of the steps:\n%s", wrong, got)
		}
	}
	// It must not claim to have checked what it did not check.
	if strings.Contains(got, "would succeed") || strings.Contains(got, "will work") {
		t.Errorf("the rehearsal promises more than it verified:\n%s", got)
	}
}

// A printed command has to be one that can be pasted back.
//
// The summary is the part of this command people learn the tool from. An install command
// with a space in it, printed bare, is a line that looks copyable and silently does
// something else.
func TestAPrintedCommandSurvivesBeingPastedBack(t *testing.T) {
	got := commandLine([]string{"site", "add", "a.example.com", "--install-command", "npm ci --omit=dev"})
	want := "ratline site add a.example.com --install-command 'npm ci --omit=dev'"
	if got != want {
		t.Errorf("commandLine() = %q, want %q", got, want)
	}
	if got := commandLine([]string{"user", "add", "acme"}); got != "ratline user add acme" {
		t.Errorf("commandLine() quoted something that needed no quoting: %q", got)
	}
}

// A dry run must record nothing to undo.
//
// The undo steps are deletes. Recording one for a step that never happened means a later
// failure runs `site delete` against a site the preview did not create — a preview that
// damages the server, which is the one thing --dry-run promises it cannot do.
func TestADryRunRecordsNothingToUndo(t *testing.T) {
	for _, dry := range []bool{true, false} {
		out := &strings.Builder{}
		g := &Globals{DryRun: dry, Stdout: out, Stderr: out, Stdin: strings.NewReader("")}
		g.Log = log.Discard()
		s := &stack{g: g, c: &composer{g: g}}

		// `explain` needs no root, no configuration and no state, so this exercises run()
		// itself rather than the command it happens to be given.
		if err := s.c.run(t.Context(), step{
			what: "a step",
			argv: []string{"explain", "layout"},
			undo: []string{"user", "delete", "nobody"},
		}); err != nil {
			t.Fatalf("dry=%v: %v", dry, err)
		}

		switch {
		case dry && len(s.c.done) != 0:
			t.Errorf("a dry run recorded %d undo step(s); a later failure would run a "+
				"delete against something it never created", len(s.c.done))
		case !dry && len(s.c.done) != 1:
			t.Errorf("a real step recorded %d undo steps, want 1 — a failure would leave "+
				"it behind", len(s.c.done))
		}
	}
}
