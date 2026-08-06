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
	s := &stack{g: &Globals{DryRun: true, Yes: true, NoInput: true}}
	got := strings.Join(s.inherited(), " ")
	for _, want := range []string{"--dry-run", "--yes", "--no-input"} {
		if !strings.Contains(got, want) {
			t.Errorf("inherited() = %q, missing %s — the step would not honour it", got, want)
		}
	}
	if plain := (&stack{g: &Globals{}}).inherited(); len(plain) != 0 {
		t.Errorf("inherited() with nothing set = %v, want nothing", plain)
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
		s := &stack{g: g}

		// `explain` needs no root, no configuration and no state, so this exercises run()
		// itself rather than the command it happens to be given.
		if err := s.run(t.Context(), "a step", []string{"user", "delete", "nobody"},
			"explain", "layout"); err != nil {
			t.Fatalf("dry=%v: %v", dry, err)
		}

		switch {
		case dry && len(s.done) != 0:
			t.Errorf("a dry run recorded %d undo step(s); a later failure would run a "+
				"delete against something it never created", len(s.done))
		case !dry && len(s.done) != 1:
			t.Errorf("a real step recorded %d undo steps, want 1 — a failure would leave "+
				"it behind", len(s.done))
		}
	}
}
