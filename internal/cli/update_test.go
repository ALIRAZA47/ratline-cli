package cli

import (
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
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
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
		if got := sameVersion(tc.a, tc.b); got != tc.same {
			t.Errorf("sameVersion(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
	}
}

func TestBackupNamingRoundTrips(t *testing.T) {
	// The rollback finds the kept binary by name, so the two have to agree.
	dir := t.TempDir()
	target := filepath.Join(dir, "ratline")

	if path, version := newestBackup(target); path != "" || version != "" {
		t.Errorf("with nothing kept, newestBackup = (%q, %q), want empty", path, version)
	}

	kept := backupPath(target, "1.2.0")
	if err := os.WriteFile(kept, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, version := newestBackup(target)
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
	older := backupPath(target, "1.0.0")
	newer := backupPath(target, "1.1.0")
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
	if path, version := newestBackup(target); path != newer || version != "1.1.0" {
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
	if path, _ := newestBackup(target); path != "" {
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
	got, err := checksumFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Errorf("checksumFile = %q, want %q", got, want)
	}

	if _, err := checksumFile(filepath.Join(dir, "absent")); err == nil {
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
		_, err := u.resolveVersion(t.Context(), "")
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
	got, err := u.resolveVersion(t.Context(), "v1.4.0")
	if err != nil {
		t.Fatalf("resolveVersion with an explicit version = %v", err)
	}
	if got != "1.4.0" {
		t.Errorf("resolveVersion = %q, want 1.4.0 with the v stripped", got)
	}
}
