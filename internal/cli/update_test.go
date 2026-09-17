package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/selfupdate"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// `update` replaces the binary on a server that is serving, so the tests are about
// the refusals. A successful copy is not the interesting case.

func TestSameVersionIgnoresTheTagPrefix(t *testing.T) {
	// A release tag carries a v and buildinfo.Version does not, so a naive comparison
	// would offer an "update" from 1.2.0 to v1.2.0 for ever.
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"1.2.0", "1.2.0", true},
		{"v1.2.0", "1.2.0", true},
		{"1.2.0", "v1.2.0", true},
		{"1.2.0", "1.2.1", false},
		{"dev", "1.2.0", false},
	} {
		if got := selfupdate.SameVersion(tc.a, tc.b); got != tc.same {
			t.Errorf("selfupdate.SameVersion(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
	}
}

func TestBackupNamingRoundTrips(t *testing.T) {
	// The rollback finds the kept binary by name, so the two have to agree.
	dir := t.TempDir()
	target := filepath.Join(dir, "ratline")

	if path, version := selfupdate.NewestBackup(target); path != "" || version != "" {
		t.Errorf("with nothing kept, newestBackup = (%q, %q), want empty", path, version)
	}

	kept := selfupdate.BackupPath(target, "1.2.0")
	if err := os.WriteFile(kept, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, version := selfupdate.NewestBackup(target)
	if path != kept {
		t.Errorf("newestBackup path = %q, want %q", path, kept)
	}
	if version != "1.2.0" {
		t.Errorf("newestBackup version = %q, want 1.2.0", version)
	}
}

func TestNewestBackupPrefersTheMostRecent(t *testing.T) {
	// Several updates leave several copies; a rollback means "undo the last one".
	dir := t.TempDir()
	target := filepath.Join(dir, "ratline")
	older := selfupdate.BackupPath(target, "1.0.0")
	newer := selfupdate.BackupPath(target, "1.1.0")
	for _, p := range []string{older, newer} {
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Make the ordering unambiguous rather than relying on write order.
	old := mustStat(t, older).ModTime()
	if err := os.Chtimes(newer, old.Add(time.Hour), old.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if path, version := selfupdate.NewestBackup(target); path != newer || version != "1.1.0" {
		t.Errorf("newestBackup = (%q, %q), want the 1.1.0 copy", path, version)
	}
}

func TestBackupNamingIgnoresUnrelatedNeighbours(t *testing.T) {
	// /usr/local/bin holds other things, including ratline-shell, whose own kept
	// copies must not be mistaken for the main binary's.
	dir := t.TempDir()
	target := filepath.Join(dir, "ratline")
	for _, name := range []string{"ratline-shell.1.0.0.previous", "ratline.txt", "other"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if path, _ := selfupdate.NewestBackup(target); path != "" {
		t.Errorf("newestBackup matched an unrelated file: %q", path)
	}
}

func TestChecksumsAreParsedFromTheStandardFormat(t *testing.T) {
	// sha256sum's own output, including the '*' binary marker, because that is what a
	// release publishes.
	body := "abc123  ratline-linux-amd64\n" +
		"def456 *ratline-shell-linux-amd64\n" +
		"\n" +
		"not a checksum line at all\n"
	sums := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		sums[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	if sums["ratline-linux-amd64"] != "abc123" {
		t.Errorf("plain entry not parsed: %v", sums)
	}
	if sums["ratline-shell-linux-amd64"] != "def456" {
		t.Errorf("the '*' binary marker was not stripped: %v", sums)
	}
	if len(sums) != 2 {
		t.Errorf("%d entries parsed, want 2: %v", len(sums), sums)
	}
}

func TestUpdateRefusesWithoutChecksums(t *testing.T) {
	// An unverified binary installed as root on a server holding every tenant's keys
	// is the supply-chain hole the runtime installer already refuses to leave open.
	// The default must be a refusal, not a warning.
	g := NewGlobals()
	root := NewRootCommand(g)
	var found bool
	for _, c := range root.Commands() {
		if c.Name() != "update" {
			continue
		}
		found = true
		f := c.Flags().Lookup("allow-unverified")
		if f == nil {
			t.Fatal("there is no --allow-unverified flag, so the default cannot be a refusal")
		}
		if f.DefValue != "false" {
			t.Errorf("--allow-unverified defaults to %q; verification must be the default", f.DefValue)
		}
	}
	if !found {
		t.Fatal("the update command is not registered")
	}
}

func TestUpdateIsMutatingAndNeedsRoot(t *testing.T) {
	// It writes under /usr/local and takes the lock for the swap, so it must not
	// interleave with a deploy that is halfway through rendering a unit.
	g := NewGlobals()
	root := NewRootCommand(g)
	for _, c := range root.Commands() {
		if c.Name() != "update" {
			continue
		}
		if !annotated(c, AnnoMutates) {
			t.Error("update must be marked as mutating, so it takes the lock")
		}
		if annotated(c, AnnoAllowNonRoot) {
			t.Error("update replaces files under /usr/local and needs root")
		}
	}
	code, _, errOut := harness(t, "update", "--check")
	if code != 3 {
		t.Errorf("exit code = %d, want 3 (needs root)", code)
	}
	if !strings.Contains(errOut.String(), "root") {
		t.Errorf("it should say why it refused:\n%s", errOut.String())
	}
}

func TestChecksumFileMatchesSha256(t *testing.T) {
	// Compared against crypto/sha256 rather than a constant, so the test states the
	// property — this is a sha256 of the file — instead of restating a digest that
	// would have to be recomputed by hand to be trusted.
	dir := t.TempDir()
	path := filepath.Join(dir, "artefact")
	body := []byte("ratline")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := selfupdate.ChecksumFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Errorf("checksumFile = %q, want %q", got, want)
	}

	if _, err := selfupdate.ChecksumFile(filepath.Join(dir, "absent")); err == nil {
		t.Error("a missing artefact should be an error, not an empty checksum")
	}
}

func TestUpdateStagesEachArtefactBesideItsOwnTarget(t *testing.T) {
	// The shell wrapper's path is configurable, so the two artefacts need not share a
	// directory — and rename(2) is only atomic within a filesystem. Staging both next
	// to the main binary installs one of them across a device boundary, which fails
	// with EXDEV *after* the main binary has already been swapped.
	//
	// Asserted on the paths rather than by provoking EXDEV, which needs two mounts.
	g := NewGlobals()
	g.Cfg = config.Default()
	g.Cfg.Paths.ShellWrapper = "/opt/ratline/bin/ratline-shell"
	u := &updater{g: g}

	items, err := u.artefacts()
	if err != nil {
		t.Skipf("artefacts needs a resolvable self path: %v", err)
	}
	if len(items) < 2 {
		t.Fatalf("expected the binary and the wrapper, got %d artefacts", len(items))
	}
	if a, b := filepath.Dir(items[0].Target), filepath.Dir(items[1].Target); a == b {
		t.Skipf("this test needs the two targets to differ; both are in %s", a)
	}
	// Every target must be absolute, or the staging directory lands somewhere
	// relative to the working directory.
	for _, a := range items {
		if !filepath.IsAbs(a.Target) {
			t.Errorf("%s installs to a relative path: %q", a.Asset, a.Target)
		}
	}
}

func TestUpdateRefusesAnUnconfiguredInstallPath(t *testing.T) {
	// An empty path fails at the rename, which is after the main binary has been
	// replaced — so the failure has to come before anything is downloaded.
	g := NewGlobals()
	g.Cfg = config.Default()
	g.Cfg.Paths.ShellWrapper = ""
	u := &updater{g: g}

	items, err := u.artefacts()
	if err != nil {
		t.Skipf("artefacts needs a resolvable self path: %v", err)
	}
	var sawEmpty bool
	for _, a := range items {
		if strings.TrimSpace(a.Target) == "" {
			sawEmpty = true
		}
	}
	if !sawEmpty {
		t.Skip("the config supplies a default wrapper path, so there is nothing to refuse")
	}
	err = u.run(t.Context(), "1.0.0")
	if err == nil {
		t.Fatal("an unconfigured install path must be refused")
	}
	if !strings.Contains(err.Error(), "install path") {
		t.Errorf("the refusal should name the missing path, got: %v", err)
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func TestTheReleaseLookupSaysWhichFailureItWas(t *testing.T) {
	// Three HTTP statuses mean three different things to whoever ran the command, and
	// only one of them is helped by --version.
	//
	// 404 on the latest-release endpoint means the project has published no releases
	// at all — which is the state of this repository today. Telling that operator to
	// "pass --version" sends them to a second 404 on the asset download.
	for _, tc := range []struct {
		status  int
		wantHas string
	}{
		{404, "no release has been published"},
		{403, "rate limited"},
		{429, "rate limited"},
		{500, "HTTP 500"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
		}))
		u := &updater{g: NewGlobals(), latestAPI: srv.URL}
		_, err := u.engine().Latest(t.Context(), "")
		srv.Close()

		if err == nil {
			t.Errorf("HTTP %d was not reported as an error", tc.status)
			continue
		}
		combined := err.Error() + " " + rlerr.Hint(err)
		if !strings.Contains(combined, tc.wantHas) {
			t.Errorf("HTTP %d should mention %q, got: %s", tc.status, tc.wantHas, combined)
		}
	}
}

func TestAnExplicitVersionSkipsTheLookupEntirely(t *testing.T) {
	// The escape hatch for a mirrored release, so it must not touch the network — a
	// server with no route to github is the case it exists for.
	u := &updater{g: NewGlobals(), latestAPI: "http://127.0.0.1:1/unreachable"}
	got, err := u.engine().Latest(t.Context(), "v1.4.0")
	if err != nil {
		t.Fatalf("resolveVersion with an explicit version = %v", err)
	}
	if got != "1.4.0" {
		t.Errorf("resolveVersion = %q, want 1.4.0 with the v stripped", got)
	}
}

// scriptedPanel stands in for the ratline-panel binary: it records how it was
// invoked and answers however the test needs it to.
type scriptedPanel struct {
	calls  []system.Cmd
	stdout string
	stderr string
	err    error
}

func (s *scriptedPanel) Run(_ context.Context, c system.Cmd) (*system.Result, error) {
	s.calls = append(s.calls, c)
	res := &system.Result{Path: c.Path, Args: c.Args, Stdout: s.stdout, Stderr: s.stderr}
	if s.err != nil {
		res.ExitCode = 1
		return res, s.err
	}
	return res, nil
}

// updaterWithPanel puts a fake ratline-panel on disk beside nothing in particular and
// points an updater at it.
func updaterWithPanel(t *testing.T, r system.Runner) (*updater, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ratline-panel")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	g := NewGlobals()
	g.Log = log.Discard()
	g.Runner = r
	u := &updater{g: g, baseURL: updateBaseURL}
	return u, path
}

// The change this makes to the product: one command takes the whole server to a
// release, rather than leaving somebody to remember the second one.
func TestUpdatingRatlineTakesThePanelWithIt(t *testing.T) {
	runner := &scriptedPanel{stdout: "Updated ratline-panel 0.16.0 → 0.17.0"}
	u, path := updaterWithPanel(t, runner)
	u.panelBinaryOverride = path

	got := u.updatePanel(t.Context(), "0.17.0")
	if !got.Updated {
		t.Fatalf("the panel was not updated: %+v", got)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("ran %d commands, want exactly one", len(runner.calls))
	}
	call := runner.calls[0]
	if call.Path != path {
		t.Errorf("ran %q, want the panel binary %q", call.Path, path)
	}
	// Delegated, not reimplemented: ratline asks the panel to update itself, and
	// pins the version so the two cannot straddle a release.
	if len(call.Args) < 2 || call.Args[0] != "update" {
		t.Fatalf("argv = %v, want it to run the panel's own update", call.Args)
	}
	if call.Args[1] != "--version=0.17.0" {
		t.Errorf("argv = %v, want the version pinned to ratline's", call.Args)
	}
}

// A server with no panel is the common case and must not be told about one.
func TestAServerWithoutThePanelIsLeftAlone(t *testing.T) {
	runner := &scriptedPanel{}
	u, _ := updaterWithPanel(t, runner)
	u.panelBinaryOverride = filepath.Join(t.TempDir(), "definitely-not-here")

	got := u.updatePanel(t.Context(), "0.17.0")
	if got.Updated || got.Skipped != "not installed" {
		t.Fatalf("got %+v, want it skipped as not installed", got)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("ran %v on a server with no panel", runner.calls)
	}
}

// A panel from before the update command existed cannot update itself. Saying
// "unknown command" to somebody who just ran `ratline update` is not an answer.
func TestAPanelTooOldToUpdateItselfSaysSo(t *testing.T) {
	runner := &scriptedPanel{
		stderr: `error: unknown command "update" for "ratline-panel"`,
		err:    rlerr.Genericf("exit status 1"),
	}
	u, path := updaterWithPanel(t, runner)
	u.panelBinaryOverride = path

	got := u.updatePanel(t.Context(), "0.17.0")
	if got.Updated {
		t.Fatal("an old panel was reported as updated")
	}
	if got.Skipped != "too old to update itself" {
		t.Errorf("skipped = %q, want it to name the real reason", got.Skipped)
	}
}

// ratline's own update has already succeeded by the time the panel is touched, so a
// panel that fails is a warning, not a reason to unwind a good update.
func TestAPanelThatFailsDoesNotFailRatlinesOwnUpdate(t *testing.T) {
	runner := &scriptedPanel{
		stderr: "error: a job is running: site deploy app.example.com",
		err:    rlerr.Genericf("exit status 3"),
	}
	u, path := updaterWithPanel(t, runner)
	u.panelBinaryOverride = path

	got := u.updatePanel(t.Context(), "0.17.0")
	if got.Updated {
		t.Fatal("a failed panel update was reported as updated")
	}
	if got.Skipped != "failed" {
		t.Errorf("skipped = %q, want %q", got.Skipped, "failed")
	}
}

// --no-panel is the escape hatch, and it has to actually skip.
func TestNoPanelSkipsIt(t *testing.T) {
	runner := &scriptedPanel{}
	u, path := updaterWithPanel(t, runner)
	u.panelBinaryOverride = path
	u.noPanel = true

	got := u.panel(t.Context(), "0.17.0")
	if got.Skipped != "not asked for" || len(runner.calls) != 0 {
		t.Fatalf("got %+v after %d calls, want it skipped", got, len(runner.calls))
	}
}
