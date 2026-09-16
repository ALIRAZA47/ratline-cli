package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

const oldBinary = "I AM THE OLD BINARY\n"

// fakeRelease serves one asset, with whatever SHA256SUMS line the caller wants —
// including a wrong one, which is the case worth proving.
type fakeRelease struct {
	asset   string
	body    []byte
	sumLine string // "" omits SHA256SUMS entirely (404)
}

func (f *fakeRelease) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v9.9.9"})
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			if f.sumLine == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			fmt.Fprintln(w, f.sumLine)
		case strings.HasSuffix(r.URL.Path, f.asset):
			_, _ = w.Write(f.body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func updaterFor(t *testing.T, f *fakeRelease, url, target string, allowUnverified bool) *Updater {
	t.Helper()
	lg := log.New(log.Options{Out: os.Stderr, Level: log.LevelError})
	return &Updater{
		Product: "ratline-panel", Repo: "example/repo",
		BaseURL: url, LatestAPI: url + "/releases/latest",
		Current: "0.15.0", Log: lg,
		Runner:          system.NewRunner(system.NewBinaries(), lg, false),
		AllowUnverified: allowUnverified,
		Artefacts: func() ([]Artefact, error) {
			return []Artefact{{Asset: f.asset, Target: target, Mode: 0o755, Required: true}}, nil
		},
		// Nil would skip verification; a function that always passes proves the
		// checksum gate stops things on its own, before anything else gets a say.
		Verify: func(context.Context, string, string) error { return nil },
	}
}

func targetFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ratline-panel")
	if err := os.WriteFile(p, []byte(oldBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustBeUntouched(t *testing.T, target string) {
	t.Helper()
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the target is gone: %v", err)
	}
	if string(got) != oldBinary {
		t.Fatal("the working binary was replaced by a download that was not verified")
	}
}

// The property this package exists for: a download whose bytes do not match the
// release's own SHA256SUMS is never installed, and the binary already on disk is
// left exactly as it was.
func TestAMismatchedChecksumIsNeverInstalled(t *testing.T) {
	wrong := sha256.Sum256([]byte("not what the server will send"))
	f := &fakeRelease{
		asset: "ratline-panel-linux-amd64",
		body:  []byte("the bytes actually served"),
	}
	f.sumLine = hex.EncodeToString(wrong[:]) + "  " + f.asset

	srv := f.server()
	defer srv.Close()
	target := targetFile(t)

	_, err := updaterFor(t, f, srv.URL, target, false).Run(context.Background(), "")
	if err == nil {
		t.Fatal("a tampered download was installed")
	}
	if !strings.Contains(err.Error(), "does not match the published checksum") {
		t.Errorf("error = %q, want it to name the checksum mismatch", err)
	}
	mustBeUntouched(t, target)
}

// A release that publishes no checksums at all is refused too — the failure mode is
// different (nothing to compare against rather than a bad comparison) and the answer
// is the same: do not install it.
func TestARelWithoutChecksumsIsRefusedUnlessOverridden(t *testing.T) {
	f := &fakeRelease{asset: "ratline-panel-linux-amd64", body: []byte("anything")}
	srv := f.server()
	defer srv.Close()
	target := targetFile(t)

	_, err := updaterFor(t, f, srv.URL, target, false).Run(context.Background(), "")
	if err == nil {
		t.Fatal("a release with no SHA256SUMS was installed anyway")
	}
	mustBeUntouched(t, target)

	// And the override is a real override, or the flag is a lie. This one is allowed
	// to fail on the install itself — the point is that it gets past the gate.
	_, err = updaterFor(t, f, srv.URL, target, true).Run(context.Background(), "")
	if err != nil && strings.Contains(err.Error(), "checksum") {
		t.Errorf("--allow-unverified was still stopped by the checksum gate: %v", err)
	}
}

// An asset the release does not list is the supply-chain case: the file downloads
// fine, and there is nothing to check it against.
func TestAnUnlistedAssetIsRefused(t *testing.T) {
	body := []byte("a perfectly readable file")
	sum := sha256.Sum256(body)
	f := &fakeRelease{
		asset: "ratline-panel-linux-amd64",
		body:  body,
		// A valid line, for a different file.
		sumLine: hex.EncodeToString(sum[:]) + "  some-other-artefact",
	}
	srv := f.server()
	defer srv.Close()
	target := targetFile(t)

	_, err := updaterFor(t, f, srv.URL, target, false).Run(context.Background(), "")
	if err == nil {
		t.Fatal("an asset the release does not list was installed")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("error = %q, want it to be about the missing checksum", err)
	}
	mustBeUntouched(t, target)
}

// A SHA256SUMS line whose first field is not a digest is not a checksum for anything.
// It used to be stored as one and then sliced for the error message, which panicked
// rather than refusing.
func TestAMalformedChecksumLineIsNotAChecksum(t *testing.T) {
	f := &fakeRelease{
		asset:   "ratline-panel-linux-amd64",
		body:    []byte("a perfectly readable file"),
		sumLine: "abc  ratline-panel-linux-amd64",
	}
	srv := f.server()
	defer srv.Close()
	target := targetFile(t)

	_, err := updaterFor(t, f, srv.URL, target, false).Run(context.Background(), "")
	if err == nil {
		t.Fatal("an asset with a malformed checksum line was installed")
	}
	if !strings.Contains(err.Error(), "checksum") && !strings.Contains(err.Error(), "unparseable") {
		t.Errorf("error = %q, want it to be about the checksum file", err)
	}
	mustBeUntouched(t, target)
}
