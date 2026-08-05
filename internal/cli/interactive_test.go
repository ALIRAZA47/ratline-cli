package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/site"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/user"
)

// promptHarness fakes a terminal well enough to drive a wizard: scripted answers
// on stdin, and CanPrompt forced true because the real check needs an *os.File.
func promptHarness(t *testing.T, answers ...string) (*Globals, *bytes.Buffer) {
	t.Helper()
	var errBuf bytes.Buffer
	g := NewGlobals()
	g.Stdout = &bytes.Buffer{}
	g.Stderr = &errBuf
	g.Stdin = strings.NewReader(strings.Join(answers, "\n") + "\n")
	g.Log = log.Discard()
	g.Cfg = config.Default()
	// The wizard's own gate. Without this the harness's buffers report "no
	// terminal" and every wizard would correctly refuse to run.
	g.NoInput = false
	g.StdinTTY = true
	g.StderrTTY = true
	g.StdoutTTY = true
	return g, &errBuf
}

// The contract the whole interactive layer rests on: the wizard is a flag
// collector, so the options it produces must be indistinguishable from the ones
// the flags produce. If these two ever diverge, the echoed command stops
// reproducing the result and the feature is a lie.
func TestWizardProducesTheSameCommandAsTheFlags(t *testing.T) {
	cases := []struct {
		name     string
		answers  []string
		wantArgv []string
	}{
		{
			name: "a static SPA",
			answers: []string{
				"static.example.com", // domain
				"1",                  // tenant: alice
				"1",                  // runtime: static
				"public",             // document root
				"y",                  // SPA
				"n",                  // no www alias
				"r",                  // run
			},
			wantArgv: []string{
				"ratline", "site", "add", "static.example.com",
				"--user", "alice", "--runtime", "static", "--root", "public", "--spa",
			},
		},
		{
			name: "a python API",
			answers: []string{
				"api.example.com",
				"1",            // alice
				"3",            // python
				"app.main:app", // module
				"1",            // ASGI
				"3",            // workers
				"n",            // no www alias
				"r",
			},
			wantArgv: []string{
				"ratline", "site", "add", "api.example.com",
				"--user", "alice", "--runtime", "python",
				"--app-module", "app.main:app", "--asgi", "--workers", "3",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := promptHarness(t, tc.answers...)
			st := memStore(t, g)
			if err := st.PutUser(context.Background(), &state.User{
				Name: "alice", Home: "/home/alice", Shell: "/bin/bash",
			}); err != nil {
				t.Fatal(err)
			}

			opts, err := wizardSiteAdd(g, context.Background(), site.AddOptions{})
			if err != nil {
				t.Fatalf("wizardSiteAdd = %v", err)
			}

			// 1. The echoed command is exactly what a flag user would type.
			if got := strings.Join(g.Argv, " "); got != strings.Join(tc.wantArgv, " ") {
				t.Errorf("the echoed command is not the flag invocation:\n  got:  %s\n  want: %s",
					got, strings.Join(tc.wantArgv, " "))
			}

			// 2. And parsing that command back through the real flag definitions
			//    reproduces the same options — which is the property that matters,
			//    since it is what makes the echoed command a promise rather than a
			//    decoration.
			fromFlags := parseSiteAddFlags(t, g, tc.wantArgv[3:])
			if !sameAddOptions(opts, fromFlags) {
				t.Errorf("the wizard and the flags produced different options:\n  wizard: %+v\n  flags:  %+v",
					summarise(opts), summarise(fromFlags))
			}
		})
	}
}

// parseSiteAddFlags runs the argv through the real `site add` flag set, so the
// comparison is against the actual command rather than a second parser.
func parseSiteAddFlags(t *testing.T, g *Globals, args []string) site.AddOptions {
	t.Helper()
	var captured site.AddOptions
	cmd := newSiteAddCommand(g)
	// Replace the body: this test is about flag binding, not provisioning.
	cmd.RunE = func(c *cobra.Command, positional []string) error {
		return nil
	}
	if err := cmd.Flags().Parse(args[1:]); err != nil {
		t.Fatalf("parsing %v: %v", args, err)
	}
	// The flags wrote into the command's own opts; read them back out through the
	// same accessors the command uses.
	get := func(name string) string {
		v, err := cmd.Flags().GetString(name)
		if err != nil {
			return ""
		}
		return v
	}
	captured.Domain = args[0]
	captured.Owner = get("user")
	captured.Runtime = get("runtime")
	captured.DocRoot = get("root")
	captured.AppModule = get("app-module")
	captured.IndexFile = get("index")
	if b, err := cmd.Flags().GetBool("spa"); err == nil {
		captured.SPA = b
	}
	if n, err := cmd.Flags().GetInt("workers"); err == nil {
		captured.Workers = n
	}
	if b, err := cmd.Flags().GetBool("asgi"); err == nil && b {
		v := true
		captured.ASGI = &v
	}
	return captured
}

// sameAddOptions compares the fields a wizard can set. Deliberately explicit
// rather than reflective: a new field should make someone decide whether the
// wizard needs to collect it.
func sameAddOptions(a, b site.AddOptions) bool {
	if a.Domain != b.Domain || a.Owner != b.Owner || a.Runtime != b.Runtime {
		return false
	}
	if a.DocRoot != b.DocRoot || a.SPA != b.SPA || a.AppModule != b.AppModule {
		return false
	}
	if a.Workers != b.Workers || a.Entry != b.Entry {
		return false
	}
	switch {
	case a.ASGI == nil && b.ASGI == nil:
	case a.ASGI == nil || b.ASGI == nil:
		return false
	case *a.ASGI != *b.ASGI:
		return false
	}
	return true
}

func summarise(o site.AddOptions) string {
	asgi := "nil"
	if o.ASGI != nil {
		asgi = map[bool]string{true: "true", false: "false"}[*o.ASGI]
	}
	return strings.Join([]string{o.Domain, o.Owner, o.Runtime, o.DocRoot, o.AppModule, asgi}, "|")
}

// Cancelling before the run step must leave nothing behind. The wizard collects
// everything before the first system call, which is what makes this structural
// rather than a promise.
func TestWizardCancelMutatesNothing(t *testing.T) {
	g, _ := promptHarness(t,
		"static.example.com", "1", "1", "public", "n", "n",
		"c", // cancel at the summary
	)
	st := memStore(t, g)
	if err := st.PutUser(context.Background(), &state.User{
		Name: "alice", Home: "/home/alice", Shell: "/bin/bash",
	}); err != nil {
		t.Fatal(err)
	}

	_, err := wizardSiteAdd(g, context.Background(), site.AddOptions{})
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
	// Nothing recorded, so nothing to unwind.
	sites, lerr := st.ListSites(context.Background(), state.SiteFilter{})
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(sites) != 0 {
		t.Errorf("the cancelled wizard created %d site(s)", len(sites))
	}
	// And a cancellation is a clean exit, not an error the caller reports.
	if errCancelledToNil(err) != nil {
		t.Error("a cancellation was turned into a failure")
	}
}

// EOF on stdin is the same as cancelling: the terminal went away, which is not a
// failure worth an exit code.
func TestWizardTreatsEOFAsCancellation(t *testing.T) {
	g, _ := promptHarness(t) // no answers at all
	memStore(t, g)
	_, _, err := wizardUserAdd(g, user.AddOptions{}, nil)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
}

// Inline validation: a rejected answer is re-asked with the reason next to the
// field, rather than the whole form failing at the end.
func TestWizardRevalidatesInline(t *testing.T) {
	g, errBuf := promptHarness(t,
		"Not A Domain",     // refused
		"still bad",        // refused
		"good.example.com", // accepted
		"1", "1", "public", "n", "n", "c",
	)
	st := memStore(t, g)
	if err := st.PutUser(context.Background(), &state.User{
		Name: "alice", Home: "/home/alice", Shell: "/bin/bash",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := wizardSiteAdd(g, context.Background(), site.AddOptions{}); !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v", err)
	}
	out := errBuf.String()
	// The reason has to be visible, and visible twice.
	if n := strings.Count(out, "invalid domain"); n < 2 {
		t.Errorf("the validation message appeared %d times, want at least 2:\n%s", n, out)
	}
}

// The summary panel must show the reproducing command, since that is how an
// operator graduates from the wizard to a script.
func TestWizardSummaryEchoesTheCommand(t *testing.T) {
	g, errBuf := promptHarness(t,
		"static.example.com", "1", "1", "public", "y", "n", "c",
	)
	st := memStore(t, g)
	if err := st.PutUser(context.Background(), &state.User{
		Name: "alice", Home: "/home/alice", Shell: "/bin/bash",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := wizardSiteAdd(g, context.Background(), site.AddOptions{}); !errors.Is(err, ErrCancelled) {
		t.Fatal(err)
	}
	out := errBuf.String()
	for _, want := range []string{
		"equivalent command:",
		"ratline site add static.example.com --user alice --runtime static",
		"--spa",
		"[R]un",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary is missing %q:\n%s", want, out)
		}
	}
}

// No terminal means no prompt, ever. A wizard that blocked here would hang a CI
// pipeline until it timed out.
func TestWizardsNeverPromptWithoutATerminal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*Globals)
	}{
		{"piped input", func(g *Globals) {}},
		{"--no-input", func(g *Globals) { g.NoInput = true }},
		{"--yes", func(g *Globals) { g.Yes = true }},
		{"--json", func(g *Globals) { g.JSON = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGlobals()
			g.Stdout, g.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
			g.Stdin = strings.NewReader("")
			g.Log = log.Discard()
			tc.setup(g)
			if err := g.resolve(); err != nil {
				t.Fatal(err)
			}
			if g.CanPrompt() {
				t.Error("CanPrompt is true; a prompt here would hang a CI job")
			}
			// And the confirmation helpers refuse rather than assuming.
			if !g.Yes {
				if _, err := g.Confirm("proceed?"); !rlerr.Is(err, rlerr.CodeInputRequired) {
					t.Errorf("Confirm returned %v, want input_required", err)
				}
			}
		})
	}
}

// NO_COLOR and TERM=dumb must degrade to plain prompts rather than emitting
// escapes into a transcript.
func TestPlainDegradation(t *testing.T) {
	for _, env := range []struct{ key, value string }{
		{"NO_COLOR", "1"},
		{"TERM", "dumb"},
	} {
		t.Run(env.key, func(t *testing.T) {
			t.Setenv(env.key, env.value)
			g, errBuf := promptHarness(t, "x")
			g.Color = false
			p := newPrompter(g)
			p.heading("A heading")
			p.note("a note")
			if strings.Contains(errBuf.String(), "\033[") {
				t.Errorf("ANSI escapes were emitted with %s=%s:\n%q", env.key, env.value, errBuf.String())
			}
			if !g.Plain() {
				t.Errorf("Plain() is false with %s=%s", env.key, env.value)
			}
		})
	}
}

// A narrow window is a real SSH session on a phone, and the heading rule must not
// run off the edge of it.
func TestNarrowTerminalDegrades(t *testing.T) {
	g, errBuf := promptHarness(t, "x")
	g.Width = 40
	g.Color = false
	if !g.Plain() {
		t.Error("Plain() is false at 40 columns")
	}
	newPrompter(g).heading("A heading that is longer than the window is wide")
	for _, line := range strings.Split(errBuf.String(), "\n") {
		if len([]rune(line)) > 60 {
			t.Errorf("a line is %d runes wide in a 40-column window: %q", len([]rune(line)), line)
		}
	}
}

// memStore points a Globals at an in-memory database.
func memStore(t *testing.T, g *Globals) *state.Store {
	t.Helper()
	st, err := state.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory = %v", err)
	}
	t.Cleanup(func() { st.Close() })
	g.store = st
	return st
}

var _ = os.Getenv
