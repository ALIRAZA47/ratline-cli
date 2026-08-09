package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

// An agent driving a root tool must not have to guess. These check the two properties
// that make that true: the schema describes the binary rather than a copy of it, and the
// MCP server cannot change anything unless it was started to.

func TestTheSchemaDescribesTheRealCommandTree(t *testing.T) {
	s := BuildSchema(NewRootCommand(&Globals{}))

	var leaves, required int
	var walk func(cs []SchemaCommand)
	walk = func(cs []SchemaCommand) {
		for _, c := range cs {
			if len(c.Subcommands) == 0 {
				leaves++
			}
			for _, f := range c.Flags {
				if f.Required {
					required++
				}
			}
			walk(c.Subcommands)
		}
	}
	walk(s.Commands)

	if leaves < 50 {
		t.Errorf("only %d leaf commands in the schema; the walk is not reaching the tree", leaves)
	}
	// The schema's required set must be exactly what the command tree says is required.
	//
	// Pinning a number here meant adding a command with a required flag broke this test
	// for no reason, and tempted whoever hit it to raise the number without checking. The
	// pair is what matters: a schema that reports nothing required would look perfectly
	// healthy while telling an agent it may omit --owner.
	inTree := 0
	var count func(c *cobra.Command)
	count = func(c *cobra.Command) {
		c.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
			if requiredFlag(f) && !f.Hidden {
				inTree++
			}
		})
		for _, child := range runnableChildren(c) {
			count(child)
		}
	}
	for _, c := range runnableChildren(NewRootCommand(&Globals{})) {
		count(c)
	}
	if required != inTree {
		t.Errorf("the schema reports %d required flags, the command tree has %d — "+
			"an agent is being told the wrong thing about what it may omit", required, inTree)
	}
	if required == 0 {
		t.Error("no required flags at all; the annotation is not reaching the schema")
	}
	if len(s.Exits) != 11 {
		t.Errorf("exit codes = %d, want 11", len(s.Exits))
	}
	if len(s.Globals) == 0 {
		t.Error("no global flags in the schema")
	}
}

// The specific thing an agent gets wrong without this: db create takes --owner, not
// --user, and both exist.
func TestTheSchemaDistinguishesOwnerFromUser(t *testing.T) {
	s := BuildSchema(NewRootCommand(&Globals{}))
	var create *SchemaCommand
	var walk func(cs []SchemaCommand)
	walk = func(cs []SchemaCommand) {
		for i := range cs {
			if cs[i].Path == "ratline db create" {
				create = &cs[i]
			}
			walk(cs[i].Subcommands)
		}
	}
	walk(s.Commands)
	if create == nil {
		t.Fatal("db create is missing from the schema")
	}
	got := map[string]bool{}
	for _, f := range create.Flags {
		if f.Required {
			got[f.Name] = true
		}
	}
	if !got["owner"] {
		t.Error("--owner is not marked required, so an agent has no way to know it must pass it")
	}
	if got["user"] {
		t.Error("--user is marked required; it is not, and an agent would supply the wrong flag")
	}
	if !create.Mutates {
		t.Error("db create is not marked as mutating, so an agent cannot tell it from a read")
	}
}

// mcp: the whole safety argument is that a mutating tool is not merely refused but absent.
func TestTheMCPServerIsReadOnlyUntilAsked(t *testing.T) {
	for _, tc := range []struct {
		allow     bool
		wantExtra bool
	}{{false, false}, {true, true}} {
		out := &strings.Builder{}
		g := &Globals{
			Stdin: strings.NewReader(
				`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n" +
					`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":` +
					`{"name":"ratline_site_deploy","arguments":{"domain":"x.test"}}}` + "\n"),
			Stdout: out, Stderr: &strings.Builder{},
		}
		g.Log = log.Discard()
		if err := g.runMCP(context.Background(), tc.allow); err != nil {
			t.Fatalf("allow=%v: %v", tc.allow, err)
		}

		var listed, called map[string]any
		for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) != nil {
				continue
			}
			switch m["id"].(float64) {
			case 1:
				listed = m
			case 2:
				called = m
			}
		}

		tools, _ := listed["result"].(map[string]any)["tools"].([]any)
		var hasDeploy bool
		for _, tr := range tools {
			if tr.(map[string]any)["name"] == "ratline_site_deploy" {
				hasDeploy = true
			}
		}
		if hasDeploy != tc.wantExtra {
			t.Errorf("allow=%v: site_deploy listed = %v, want %v", tc.allow, hasDeploy, tc.wantExtra)
		}
		if !tc.allow {
			// Absent AND refused — and the refusal must say why, or an agent will keep
			// trying rather than telling its operator what to change.
			e, _ := called["error"].(map[string]any)
			msg, _ := e["message"].(string)
			if !strings.Contains(msg, "--allow-mutations") {
				t.Errorf("the refusal does not name the flag that would enable it: %q", msg)
			}
		}
	}
}

// ratline_site_jobs describes itself as listing "scheduled jobs and long-running
// workers", and its first implementation ran `site cron list`, which filters to
// kind=job — so a worker, the part of a site least likely to be noticed when it
// stops, was invisible to exactly the audience the tool was built for. Both kinds
// must come back from one call.
func TestTheSiteJobsToolListsWorkersToo(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	st, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	if err := st.PutUser(t.Context(), &state.User{
		Name: "acme", Home: filepath.Join(dir, "home", "acme"), Shell: "/bin/sh",
	}); err != nil {
		t.Fatalf("PutUser = %v", err)
	}
	if err := st.PutSite(t.Context(), &state.Site{
		Domain: "app.test", Owner: "acme", Runtime: "static", Slug: "acme-app_test",
	}); err != nil {
		t.Fatalf("PutSite = %v", err)
	}
	for _, u := range []*state.SiteUnit{
		{Domain: "app.test", Name: "nightly", Kind: state.UnitJob,
			Command: "/home/acme/app.test/app/bin/nightly", Schedule: "*-*-* 03:00:00", Enabled: true},
		{Domain: "app.test", Name: "queue", Kind: state.UnitWorker,
			Command: "/home/acme/app.test/app/bin/worker", Enabled: true},
	} {
		if err := st.PutSiteUnit(t.Context(), u); err != nil {
			t.Fatalf("PutSiteUnit(%s) = %v", u.Name, err)
		}
	}
	st.Close()

	configPath := filepath.Join(dir, "config.yaml")
	body := "version: 1\npaths:\n  state_db: " + dbPath +
		"\n  lock: " + filepath.Join(dir, "lock") +
		"\n  audit_log: " + filepath.Join(dir, "audit.log") + "\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	out := &strings.Builder{}
	g := &Globals{
		ConfigPath: configPath,
		Stdin: strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` +
				`{"name":"ratline_site_jobs","arguments":{"domain":"app.test"}}}` + "\n"),
		Stdout: out, Stderr: &strings.Builder{},
	}
	g.Log = log.Discard()
	if err := g.runMCP(context.Background(), false); err != nil {
		t.Fatalf("runMCP: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
		t.Fatalf("the reply is not JSON: %v", err)
	}
	res, _ := m["result"].(map[string]any)
	if res == nil {
		t.Fatalf("no result in the reply: %s", out.String())
	}
	if res["isError"] == true {
		t.Fatalf("the tool call failed: %s", out.String())
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	var env Envelope
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("the tool result is not the JSON envelope: %q", text)
	}

	kinds := map[string]string{}
	data, _ := env.Data.(map[string]any)
	units, _ := data["units"].([]any)
	for _, row := range units {
		r, _ := row.(map[string]any)
		name, _ := r["name"].(string)
		kind, _ := r["kind"].(string)
		kinds[name] = kind
	}
	if kinds["nightly"] != state.UnitJob {
		t.Errorf("the job is missing or mislabelled: %v", kinds)
	}
	if kinds["queue"] != state.UnitWorker {
		t.Errorf("the worker is invisible to the tool that promises it: %v", kinds)
	}
}

// Every tool result must be the JSON envelope, including failures: the code and the hint
// are what an agent uses to decide what to do next.
func TestAFailedToolCallStillReturnsTheEnvelope(t *testing.T) {
	out := &strings.Builder{}
	g := &Globals{
		Stdin: strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` +
				`{"name":"ratline_site_show","arguments":{"domain":"nope.invalid"}}}` + "\n"),
		Stdout: out, Stderr: &strings.Builder{},
	}
	g.Log = log.Discard()
	if err := g.runMCP(context.Background(), false); err != nil {
		t.Fatalf("runMCP: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
		t.Fatalf("the reply is not JSON: %v", err)
	}
	res := m["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)

	var env map[string]any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("the tool result is not the JSON envelope: %q", text)
	}
	if env["ok"] != false {
		t.Errorf("a failure reported ok=%v", env["ok"])
	}
	if _, ok := env["error"]; !ok {
		t.Error("the envelope carries no error object, so an agent has no code to branch on")
	}
	if res["isError"] != true {
		t.Error("isError is not set, so the client cannot tell this failed")
	}
}
