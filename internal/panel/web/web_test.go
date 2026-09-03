package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The embed contract: there is always something to serve.
//
// A fresh clone has no bundle — it is a build artefact and is not committed — so the
// package embeds a placeholder beside it. Without that, `go build ./...` fails on a
// checkout without Node, which would make the whole repository need a JavaScript
// toolchain to compile a Go binary that does not use one.
func TestThereIsAlwaysSomethingToServe(t *testing.T) {
	assets, err := Assets()
	if err != nil {
		t.Fatalf("Assets: %v", err)
	}
	body, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("no index.html to serve: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(body)), "<!doctype html>") {
		t.Errorf("what is embedded is not an HTML page: %.60s", body)
	}
	// Built() must agree with what Assets() actually returned, because `doctor`
	// reports it and a wrong answer sends somebody looking in the wrong place.
	placeholderish := strings.Contains(string(body), "not built into this binary")
	if Built() == placeholderish {
		t.Errorf("Built() = %v but the page %s the placeholder",
			Built(), map[bool]string{true: "is", false: "is not"}[placeholderish])
	}
}

// A deep link has to reach index.html, or a refresh on /sites/example.com is a 404 —
// React owns the path and the server has to hand it the page before it can.
func TestDeepLinksReachTheApplication(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/sites", "/sites/example.com", "/jobs/abc123"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s returned %d, want 200", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s served %q, want HTML", path, ct)
		}
	}
}

// A missing asset must be a real 404. Serving index.html for a missing .js hands the
// browser HTML where it expected a script, for a blank page and a console error that
// does not say why.
func TestAMissingAssetIsNotTheApplication(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/assets/gone.js", "/favicon.ico", "/nothing.css"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s returned %d, want 404", path, rec.Code)
		}
	}
}

// The shell must never be cached: a cached index.html pointing at the previous
// release's bundle is how a panel breaks after an upgrade, and clears itself only on a
// hard refresh nobody thinks to do.
func TestTheShellIsNotCached(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control on the shell = %q, want no-store", got)
	}
}
