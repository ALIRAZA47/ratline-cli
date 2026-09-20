package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/runtime"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
)

// `site exec` is root-only, and the root check runs in the command's PreRun — so these
// go at g.execInSite, which is everything after the site and the tenant have been
// resolved. A test that stopped at "ratline must run as root" would prove nothing about
// the command, which is how several tests in this repository came to pass vacuously.

// execTestSite builds a site on disk with one runnable script in it, and returns the
// runtime context the command would have built.
func execTestSite(t *testing.T, runner system.Runner, dryRun bool) (*Globals, *runtime.Context, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Paths.HomeBase = root
	cfg.Paths.RuntimesDir = filepath.Join(root, "runtimes")

	site := &state.Site{Domain: "app.example.com", Owner: "acme", Runtime: "static"}
	seed := filepath.Join(cfg.SiteDir("acme", "app.example.com"), "app", "bin", "seed")
	if err := os.MkdirAll(filepath.Dir(seed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seed, []byte("#!/bin/sh\necho seeded\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := &bytes.Buffer{}
	g := &Globals{
		Cfg: cfg, Log: log.Discard(), Runner: runner, DryRun: dryRun,
		Stdout: out, Stderr: &bytes.Buffer{}, Stdin: strings.NewReader(""),
		CmdPath: "ratline site exec",
	}
	id := &system.Identity{Name: "acme", UID: os.Getuid(), GID: os.Getgid(),
		Home: filepath.Join(root, "acme"), Shell: "/bin/sh"}
	return g, runtime.NewContext(cfg, g.Log, runner, site, id, dryRun), out
}

// A quoted command line is one positional argument, and the menu collects it that way
// too. It is split by the parser build commands and hooks already use — never a shell.
func TestExecArgvSplitsAQuotedCommandLine(t *testing.T) {
	argv, err := execArgv([]string{"npm run bootstrap"})
	if err != nil {
		t.Fatalf("execArgv = %v", err)
	}
	if got, want := strings.Join(argv, "|"), "npm|run|bootstrap"; got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}

	// Already split stays split, including an argument that contains a space.
	argv, err = execArgv([]string{"node", "-e", "console.log('a b')"})
	if err != nil {
		t.Fatalf("execArgv = %v", err)
	}
	if len(argv) != 3 || argv[2] != "console.log('a b')" {
		t.Errorf("argv = %q, want the three arguments passed through untouched", argv)
	}
}

func TestExecArgvRefusesAPipeline(t *testing.T) {
	if _, err := execArgv([]string{"npm run build | tee build.log"}); err == nil {
		t.Error("execArgv with a pipe = nil, want a refusal: nothing here is a shell")
	}
}

// --dry-run has to resolve the plan and print it without running anything. The bug this
// guards is the one this repository has found a dozen times: a rehearsal that acts.
func TestExecDryRunRunsNothing(t *testing.T) {
	fake := systest.NewFakeRunner()
	g, rc, out := execTestSite(t, fake, true)

	if err := g.execInSite(context.Background(), rc, []string{"./bin/seed", "--all"}, siteExecOptions{}); err != nil {
		t.Fatalf("execInSite under --dry-run = %v", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("the rehearsal ran %d command(s): %+v", len(calls), calls)
	}
	printed := out.String()
	for _, want := range []string{"would run", "acme", "bin/seed", "--all"} {
		if !strings.Contains(printed, want) {
			t.Errorf("the plan does not mention %q:\n%s", want, printed)
		}
	}
}

// Under --json stdout carries exactly one object. The child's own output has to be
// captured into it rather than written alongside it, or every caller piping this into
// jq gets a parse error the moment the command prints anything.
func TestExecUnderJSONKeepsStdoutParseable(t *testing.T) {
	fake := systest.NewFakeRunner()
	fake.Hook = func(cmd system.Cmd) (*system.Result, error) {
		if cmd.Stdout != nil || cmd.Stderr != nil {
			t.Error("the child's output is wired to a stream under --json")
		}
		return &system.Result{Path: cmd.Path, Args: cmd.Args, Stdout: "seeded\n"}, nil
	}
	g, rc, out := execTestSite(t, fake, false)
	g.JSON = true

	if err := g.execInSite(context.Background(), rc, []string{"./bin/seed"}, siteExecOptions{}); err != nil {
		t.Fatalf("execInSite = %v", err)
	}

	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			ExitCode int    `json:"exit_code"`
			Stdout   string `json:"stdout"`
			Program  string `json:"program"`
		} `json:"data"`
	}
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	if err := dec.Decode(&envelope); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out.String())
	}
	if dec.More() {
		t.Error("stdout holds more than one object")
	}
	if envelope.Data.Stdout != "seeded\n" {
		t.Errorf("the envelope's stdout = %q, want the command's output", envelope.Data.Stdout)
	}
	if !strings.HasSuffix(envelope.Data.Program, "/bin/seed") {
		t.Errorf("program = %q", envelope.Data.Program)
	}
}

// A program exiting 2 does not mean ratline was called wrongly, so its code is reported
// rather than adopted: exit codes 0-10 are a contract automation branches on.
func TestExecReportsTheProgramsExitCodeWithoutAdoptingIt(t *testing.T) {
	fake := systest.NewFakeRunner()
	fake.Hook = func(cmd system.Cmd) (*system.Result, error) {
		return &system.Result{Path: cmd.Path, Args: cmd.Args, ExitCode: 2, Stderr: "nope\n"},
			rlerr.Externalf("seed failed (exit 2): nope").WithField("exit_code", "2")
	}
	g, rc, out := execTestSite(t, fake, false)
	g.JSON = true

	err := g.execInSite(context.Background(), rc, []string{"./bin/seed"}, siteExecOptions{})
	if err == nil {
		t.Fatal("execInSite = nil, want the failure reported")
	}
	if code := rlerr.CodeOf(err); code != rlerr.CodeExternal {
		t.Errorf("exit code = %d (%s), want %d (external)", code, code.Name(), rlerr.CodeExternal)
	}
	if !strings.Contains(out.String(), `"exit_code": 2`) {
		t.Errorf("the envelope does not carry the program's own exit code:\n%s", out.String())
	}
	// One envelope, even though the command both reported and failed.
	if n := strings.Count(out.String(), `"command"`); n != 1 {
		t.Errorf("stdout holds %d envelopes, want 1:\n%s", n, out.String())
	}
}

// `site exec app.example.com -- npm run build --if-present` works and the same line
// without the -- does not, because cobra reads --if-present as ratline's. The error has
// to say where the boundary goes.
func TestExecFlagErrorPointsAtTheDoubleDash(t *testing.T) {
	g := NewGlobals()
	g.Stdout, g.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
	g.Log = log.Discard()
	root := NewRootCommand(g)
	root.SetArgs([]string{"site", "exec", "app.example.com", "npm", "run", "build", "--if-present"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})

	err := root.Execute()
	if err == nil {
		t.Fatal("an unknown flag was accepted")
	}
	if hint := rlerr.Hint(err); !strings.Contains(hint, "--") || !strings.Contains(hint, "after") {
		t.Errorf("hint = %q, want it to point at the -- boundary", hint)
	}
}
