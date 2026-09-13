package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// scriptedRunner answers one command with a canned result, so verify() can be
// tested without a binary to run.
type scriptedRunner struct {
	stdout string
	err    error
	seen   []system.Cmd
}

func (r *scriptedRunner) Run(_ context.Context, c system.Cmd) (*system.Result, error) {
	r.seen = append(r.seen, c)
	if r.err != nil {
		return &system.Result{ExitCode: 1}, r.err
	}
	return &system.Result{Path: c.Path, Args: c.Args, Stdout: r.stdout}, nil
}

func updaterWith(t *testing.T, r system.Runner) *panelUpdater {
	t.Helper()
	app, _ := testApp(t)
	app.Runner = r
	return &panelUpdater{app: app}
}

// The change this makes to the product: a download that is not the panel, or is
// not the version the release advertised, is never installed. Both are the shape
// a mixed-up asset name takes, and both would otherwise replace a working binary.
func TestAnUnexpectedBinaryIsNotInstalled(t *testing.T) {
	cases := []struct {
		name, stdout, want string
	}{
		{"the ratline binary under the panel's asset name",
			"ratline 0.16.0 (abc, 2026-01-01)", "does not identify itself as ratline-panel"},
		{"a version that is not the one the release claims",
			"ratline-panel 0.14.2 (abc, 2026-01-01)", "reports a different version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := updaterWith(t, &scriptedRunner{stdout: tc.stdout})
			err := u.verify(context.Background(), "/tmp/candidate", "0.16.0")
			if err == nil {
				t.Fatal("verify accepted it; a wrong binary would have been installed")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestTheRightBinaryPassesVerification(t *testing.T) {
	r := &scriptedRunner{stdout: "ratline-panel 0.16.0 (deadbee, 2026-09-13)\n"}
	u := updaterWith(t, r)
	if err := u.verify(context.Background(), "/tmp/candidate", "0.16.0"); err != nil {
		t.Fatalf("verify rejected the correct binary: %v", err)
	}
	if len(r.seen) != 1 || len(r.seen[0].Args) != 1 || r.seen[0].Args[0] != "version" {
		t.Fatalf("ran %+v, want one 'version' invocation", r.seen)
	}
	// A "v" prefix on the tag is the same version, not a different one.
	u2 := updaterWith(t, &scriptedRunner{stdout: "ratline-panel v0.16.0 (deadbee, 2026-09-13)"})
	if err := u2.verify(context.Background(), "/tmp/candidate", "0.16.0"); err != nil {
		t.Fatalf("a v-prefixed version was rejected: %v", err)
	}
}

func TestABinaryThatDoesNotRunIsNotInstalled(t *testing.T) {
	u := updaterWith(t, &scriptedRunner{err: rlerr.Genericf("exec format error")})
	err := u.verify(context.Background(), "/tmp/candidate", "0.16.0")
	if err == nil {
		t.Fatal("verify accepted a binary that will not execute")
	}
	if !strings.Contains(err.Error(), "does not run") {
		t.Errorf("error = %q, want it to say the binary does not run", err)
	}
}

// The panel is a daemon and a job is a child of the process being replaced, so a
// restart takes the job with it. An update that would do that stops first.
func TestAnUpdateRefusesToKillARunningJob(t *testing.T) {
	jobs := []*store.Job{
		{ID: "1", Action: "site deploy", Target: "old.example.com", State: store.JobDone},
		{ID: "2", Action: "site add", Target: "new.example.com", State: store.JobRunning},
		{ID: "3", Action: "cert issue", Target: "later.example.com", State: store.JobQueued},
	}
	j := firstUnfinishedJob(jobs)
	if j == nil || j.ID != "2" {
		t.Fatalf("firstUnfinishedJob = %+v, want the running one", j)
	}
	err := busyError(j)
	for _, want := range []string{"site add", "new.example.com", "--force", "--no-restart"} {
		if !strings.Contains(err.Error()+" "+rlerr.Hint(err), want) {
			t.Errorf("the refusal never mentions %q: %v", want, err)
		}
	}
	if code := rlerr.ExitCode(err); code != int(rlerr.CodePrecondition) {
		t.Errorf("exit code = %d, want the precondition code", code)
	}
}

// A queued job counts too: it has not started, but the queue is not idle and the
// panel will mark it failed on the way back up.
func TestAQueuedJobAlsoStopsTheUpdate(t *testing.T) {
	if j := firstUnfinishedJob([]*store.Job{{ID: "1", State: store.JobQueued}}); j == nil {
		t.Fatal("a queued job did not stop the update")
	}
	done := []*store.Job{
		{ID: "1", State: store.JobDone},
		{ID: "2", State: store.JobFailed},
	}
	if j := firstUnfinishedJob(done); j != nil {
		t.Fatalf("finished jobs blocked an update: %+v", j)
	}
	if j := firstUnfinishedJob(nil); j != nil {
		t.Fatalf("an empty queue blocked an update: %+v", j)
	}
}

// --force is the way past it, and it has to work or the flag is decoration.
func TestForceSkipsTheBusyCheck(t *testing.T) {
	u := updaterWith(t, &scriptedRunner{})
	u.force = true
	if err := u.refuseIfBusy(context.Background()); err != nil {
		t.Fatalf("--force still refused: %v", err)
	}
}

// The panel ships as one file with its interface embedded, so there is exactly
// one artefact — and it targets the binary that is running, not a guessed path.
func TestTheOnlyArtefactIsThisBinary(t *testing.T) {
	u := updaterWith(t, &scriptedRunner{})
	items, err := u.artefacts()
	if err != nil {
		t.Fatalf("artefacts: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d artefacts, want 1", len(items))
	}
	want := "ratline-panel-linux-" + goruntime.GOARCH
	if items[0].Asset != want {
		t.Errorf("asset = %q, want %q", items[0].Asset, want)
	}
	self, err := system.SelfPath()
	if err != nil {
		t.Skipf("cannot resolve this binary here: %v", err)
	}
	if items[0].Target != self {
		t.Errorf("target = %q, want the running binary %q", items[0].Target, self)
	}
	if !items[0].Required || items[0].Mode != 0o755 {
		t.Errorf("artefact = %+v, want a required 0755 file", items[0])
	}
}

// The two update commands must not be confusable: each names the other, so an
// operator who runs the wrong one is told which one they wanted.
func TestTheUpdateCommandSaysItUpdatesThePanelOnly(t *testing.T) {
	app, _ := testApp(t)
	cmd := newUpdateCommand(app)
	if !strings.Contains(cmd.Long, "ratline update") {
		t.Error("the help never points at 'ratline update' for the CLI itself")
	}
	for _, f := range []string{"check", "rollback", "version", "base-url", "allow-unverified", "force", "no-restart"} {
		if cmd.Flags().Lookup(f) == nil {
			t.Errorf("--%s is missing", f)
		}
	}
}

// --check reports and changes nothing. It is the only subcommand an operator runs
// speculatively, so it must not touch the binary, the unit or the queue.
func TestCheckReportsWithoutInstallingAnything(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v9.9.9"})
	}))
	defer srv.Close()

	r := &scriptedRunner{}
	u := updaterWith(t, r)
	u.latestAPI = srv.URL
	u.app.JSON = true

	if err := u.check(context.Background(), ""); err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(r.seen) != 0 {
		t.Fatalf("--check ran %+v; it must run nothing", r.seen)
	}

	out := readBackStdout(t, u.app)
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Current string `json:"current"`
			Latest  string `json:"latest"`
			Avail   bool   `json:"update_available"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("the output is not the envelope: %v\n%s", err, out)
	}
	if env.Data.Latest != "9.9.9" {
		t.Errorf("latest = %q, want 9.9.9 with the v stripped", env.Data.Latest)
	}
	if !env.Data.Avail {
		t.Errorf("update_available = false, but %s is newer than %s",
			env.Data.Latest, env.Data.Current)
	}
}

// An explicit --version is the escape hatch for a mirrored release, so it must not
// reach for the network at all.
func TestAnExplicitVersionDoesNotCallGitHub(t *testing.T) {
	u := updaterWith(t, &scriptedRunner{})
	u.latestAPI = "http://127.0.0.1:1/unreachable"
	got, err := u.engine().Latest(context.Background(), "v0.16.0")
	if err != nil {
		t.Fatalf("an explicit version still went to the network: %v", err)
	}
	if got != "0.16.0" {
		t.Errorf("version = %q, want 0.16.0", got)
	}
}

func readBackStdout(t *testing.T, app *App) string {
	t.Helper()
	if _, err := app.Stdout.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(app.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
