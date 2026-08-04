package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/diag"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
)

// sampleReport is the shape of a real diagnosis: some passes, the cause, and the
// steps the cause blocked.
func sampleReport() *diag.Report {
	r := &diag.Report{
		Kind: "site", Subject: "app.example.com", Summary: "node, owned by acme",
		Steps: []diag.Step{
			{ID: "enabled", Title: "the site is enabled", Verdict: diag.OK},
			{ID: "unit", Title: "the systemd unit is running", Verdict: diag.OK,
				Detail: "active, pid 41822"},
			{ID: "listening", Title: "the application is listening where nginx expects",
				Verdict: diag.Failed,
				Detail:  "the socket is mode 0640; nginx needs 0660 to connect, so every request is a 502",
				Fix:     "ratline site restart app.example.com", Topic: "sockets"},
			{ID: "app-answers", Title: "the application answers a request",
				Verdict: diag.Skipped, Blocked: "listening",
				Detail: "not checked: listening has to pass first"},
			{ID: "certificate", Title: "a current certificate is attached",
				Verdict: diag.Warning, Detail: "6 days left"},
		},
		Cause:  "the socket is mode 0640; nginx needs 0660 to connect, so every request is a 502",
		Fix:    "ratline site restart app.example.com",
		Topic:  "sockets",
		Failed: 1, Warnings: 1, OK: 2,
	}
	return r
}

func printerHarness(t *testing.T) (*Globals, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	g := NewGlobals()
	g.Stdout, g.Stderr = out, &bytes.Buffer{}
	g.Log = log.Discard()
	return g, out
}

func TestDiagnosisLeadsWithTheCauseAndTheFix(t *testing.T) {
	g, out := printerHarness(t)
	if err := g.printDiagnosis(sampleReport(), false); err != nil {
		t.Fatal(err)
	}
	s := out.String()

	for _, want := range []string{
		"app.example.com",
		"node, owned by acme",
		// The failing step, verbatim.
		"mode 0640",
		// And the headline, which is what the operator acts on.
		"Likely cause:",
		"Try:          ratline site restart app.example.com",
		// The explainer, because this particular failure is the one nobody
		// diagnoses unaided.
		"Background:   ratline explain sockets",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q from:\n%s", want, s)
		}
	}
}

func TestSkippedStepsSayWhatBlockedThem(t *testing.T) {
	g, out := printerHarness(t)
	_ = g.printDiagnosis(sampleReport(), false)
	s := out.String()

	// The whole point of the engine: a step that was not run says so, rather than
	// appearing as a second failure competing with the cause.
	if !strings.Contains(s, "not checked: listening has to pass first") {
		t.Errorf("a blocked step should name its blocker:\n%s", s)
	}
	if strings.Count(s, "FAIL") != 1 {
		t.Errorf("exactly one FAIL row expected, got:\n%s", s)
	}
}

func TestPassingStepsAreFoldedAwayUnlessAskedFor(t *testing.T) {
	g, out := printerHarness(t)
	_ = g.printDiagnosis(sampleReport(), false)
	brief := out.String()

	// On a subject with a problem, twelve `ok` rows push the answer off the screen.
	if strings.Contains(brief, "the site is enabled") {
		t.Errorf("passing steps should be folded into a count by default:\n%s", brief)
	}
	if !strings.Contains(brief, "2 checks passed") {
		t.Errorf("the folded count should be stated:\n%s", brief)
	}

	g2, out2 := printerHarness(t)
	_ = g2.printDiagnosis(sampleReport(), true)
	full := out2.String()
	if !strings.Contains(full, "the site is enabled") {
		t.Errorf("--all should show every step:\n%s", full)
	}
}

func TestAHealthySubjectSaysSoPlainly(t *testing.T) {
	g, out := printerHarness(t)
	_ = g.printDiagnosis(&diag.Report{
		Kind: "site", Subject: "www.example.com", OK: 6,
		Steps: []diag.Step{{ID: "a", Title: "a", Verdict: diag.OK}},
	}, false)
	if !strings.Contains(out.String(), "Nothing is wrong with www.example.com") {
		t.Errorf("a healthy subject should say so:\n%s", out.String())
	}
}

func TestWarningsOnlyIsNotReportedAsACause(t *testing.T) {
	// A certificate expiring in six days is worth reading and is not the diagnosis
	// of whatever is being investigated. Conflating them sends the operator after
	// the wrong thing.
	g, out := printerHarness(t)
	_ = g.printDiagnosis(&diag.Report{
		Kind: "site", Subject: "www.example.com", Warnings: 1,
		Steps: []diag.Step{
			{ID: "cert", Title: "a current certificate is attached",
				Verdict: diag.Warning, Detail: "6 days left"},
		},
	}, false)
	s := out.String()
	if strings.Contains(s, "Likely cause") {
		t.Errorf("a warning is not a cause:\n%s", s)
	}
	if !strings.Contains(s, "Nothing has failed") {
		t.Errorf("it should say nothing failed:\n%s", s)
	}
	if !strings.Contains(s, "1 warning above is worth reading") {
		t.Errorf("the warning count should read as English:\n%s", s)
	}
}

func TestVerdictColumnIsFixedWidthAndPlainText(t *testing.T) {
	// This output gets pasted into issues and chat, where a coloured glyph survives
	// neither — and a ragged first column makes the titles unscannable.
	widths := map[diag.Verdict]int{}
	for _, v := range []diag.Verdict{diag.OK, diag.Failed, diag.Warning, diag.Skipped} {
		m := mark(v)
		widths[v] = len(m)
		for _, r := range m {
			if r > 127 {
				t.Errorf("mark(%q) = %q contains a non-ASCII rune", v, m)
			}
		}
	}
	first := widths[diag.OK]
	for v, w := range widths {
		if w != first {
			t.Errorf("mark(%q) is %d wide, but mark(ok) is %d", v, w, first)
		}
	}
}

func TestTroubleshootIsReadOnlyAndNeedsRoot(t *testing.T) {
	g := NewGlobals()
	root := NewRootCommand(g)
	var found bool
	for _, c := range root.Commands() {
		if c.Name() != "troubleshoot" {
			continue
		}
		found = true
		// Marking it mutating would make a read-only diagnosis take the global lock
		// and block behind whatever deploy is currently stuck.
		if annotated(c, AnnoMutates) {
			t.Error("troubleshoot must not be marked as mutating")
		}
		// And it must not be NonRoot: the socket, the unit and the state database
		// are unreadable otherwise, and a check that silently could not look would
		// be worse than one that refuses.
		if annotated(c, AnnoAllowNonRoot) {
			t.Error("troubleshoot needs root to see sockets and units")
		}
	}
	if !found {
		t.Fatal("the troubleshoot command is not registered")
	}

	code, _, errOut := harness(t, "troubleshoot")
	if code != 3 {
		t.Errorf("exit code = %d, want 3 (precondition: needs root)", code)
	}
	if !strings.Contains(errOut.String(), "root") {
		t.Errorf("it should say why it refused:\n%s", errOut.String())
	}
}

func TestSiteTroubleshootStillExists(t *testing.T) {
	// It was the documented spelling before the general command, and it is where
	// someone looks when the broken thing is a site.
	code, _, _ := harness(t, "site", "troubleshoot", "app.example.com")
	if code != 3 {
		t.Errorf("exit code = %d, want 3 (needs root), not a usage error", code)
	}
}

func TestReportJSONCarriesTheStepsAndTheCause(t *testing.T) {
	body, err := json.Marshal(sampleReport())
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Kind    string `json:"kind"`
		Subject string `json:"subject"`
		Cause   string `json:"likely_cause"`
		Fix     string `json:"fix"`
		Failed  int    `json:"failed"`
		Steps   []struct {
			ID      string `json:"id"`
			Verdict string `json:"verdict"`
			Blocked string `json:"blocked_by"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatal(err)
	}
	if back.Kind != "site" || back.Subject != "app.example.com" {
		t.Errorf("subject lost: %+v", back)
	}
	if back.Cause == "" || back.Fix == "" || back.Failed != 1 {
		t.Errorf("the headline did not survive: %+v", back)
	}
	if len(back.Steps) != 5 {
		t.Fatalf("%d steps, want 5", len(back.Steps))
	}
	// The blocked_by edge is what a consumer needs to reconstruct the causal chain.
	var sawBlocked bool
	for _, s := range back.Steps {
		if s.Verdict == "skipped" && s.Blocked == "listening" {
			sawBlocked = true
		}
	}
	if !sawBlocked {
		t.Error("blocked_by is missing from the JSON")
	}
}
