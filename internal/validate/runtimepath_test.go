package validate

import (
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// The entry point is executed by node; nginx never serves it. Applying the document-root
// rule to it rejected .next/standalone/server.js — the path in ratline's own Next.js
// guide, and the one every standalone deployment uses.
func TestTheEntryPointMayLiveInAHiddenDirectory(t *testing.T) {
	for _, entry := range []string{
		".next/standalone/server.js",         // Next.js
		".output/server/index.mjs",           // Nuxt
		".svelte-kit/output/server/index.js", // SvelteKit
		"dist/main.js",
		"server.js",
	} {
		if err := NodeEntry(entry); err != nil {
			t.Errorf("NodeEntry(%q) = %v, want accepted", entry, err)
		}
	}
}

// Everything the old rule refused, it must still refuse.
func TestTheEntryPointStillRefusesTraversalAndTheRest(t *testing.T) {
	for entry, why := range map[string]string{
		"../outside/server.js": "traversal",
		"/etc/passwd.js":       "absolute",
		"a//b/server.js":       "empty segment",
		"-rf/server.js":        "leading hyphen, which git and tar read as a flag",
		"dist/main.py":         "not a JavaScript file",
		"":                     "empty",
	} {
		if err := NodeEntry(entry); err == nil {
			t.Errorf("NodeEntry(%q) = nil, want an error (%s)", entry, why)
		}
	}
}

// A document root is served by nginx, which denies hidden files, so the stricter rule
// stays — and now says why rather than listing a dot as allowed while refusing it.
func TestADocumentRootStillMayNotBeHidden(t *testing.T) {
	err := Subdir(".next")
	if err == nil {
		t.Fatal("Subdir(\".next\") = nil, want an error: nginx denies hidden files")
	}
	hint := hintOf(err)
	if !strings.Contains(hint, "may not begin with a dot") {
		t.Errorf("the hint does not explain where the dot may sit: %q", hint)
	}
	if err := Subdir("public"); err != nil {
		t.Errorf("Subdir(\"public\") = %v, want nil", err)
	}
	if err := Subdir("_next"); err != nil {
		t.Errorf("Subdir(\"_next\") = %v, want nil: a leading underscore is a real build output", err)
	}
}

func hintOf(err error) string { return rlerr.Hint(err) }
