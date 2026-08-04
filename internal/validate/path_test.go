package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realTempDir resolves symlinks in the test directory. On macOS /tmp is itself a
// symlink, so a containment test that skipped this would be testing the wrong
// thing.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return dir
}

func TestSubdir(t *testing.T) {
	for _, ok := range []string{"public", "dist", "static-files", "build/output", "_next", "a.b.c", "v1.2"} {
		if err := Subdir(ok); err != nil {
			t.Errorf("Subdir(%q) = %v, want nil", ok, err)
		}
	}
	invalid := map[string]string{
		"":                       "empty",
		"/public":                "absolute",
		"../public":              "traversal",
		"public/../../etc":       "traversal in the middle",
		"./public":               "dot segment",
		"public/":                "trailing slash produces an empty segment",
		"pub lic":                "space",
		"public;rm":              "command separator",
		"$(id)":                  "command substitution",
		"pub\\lic":               "backslash",
		"public\x00":             "NUL byte",
		"-rf":                    "leading hyphen looks like a flag",
		"..":                     "parent",
		".":                      "current",
		strings.Repeat("a", 256): "too long",
	}
	for in, why := range invalid {
		if err := Subdir(in); err == nil {
			t.Errorf("Subdir(%q) = nil, want an error (%s)", in, why)
		}
	}
}

func TestWithinRoot(t *testing.T) {
	root := "/home/alice"
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"example.com", "/home/alice/example.com"},
		{"example.com/public", "/home/alice/example.com/public"},
		{"/home/alice/x", "/home/alice/x"},
		{"/home/alice", "/home/alice"},
	} {
		got, err := WithinRoot(root, tc.in)
		if err != nil {
			t.Errorf("WithinRoot(%q, %q) = %v", root, tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("WithinRoot(%q, %q) = %q, want %q", root, tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"../bob", "/home/bob", "/etc/passwd", "example.com/../../bob", "/home/alice2"} {
		if got, err := WithinRoot(root, bad); err == nil {
			t.Errorf("WithinRoot(%q, %q) = %q, want an error", root, bad, got)
		}
	}
}

func TestResolveWithinFollowsSymlinks(t *testing.T) {
	base := realTempDir(t)
	home := filepath.Join(base, "home", "alice")
	outside := filepath.Join(base, "secret")
	for _, d := range []string{home, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// A link inside the home pointing out of it is the attack this exists for.
	escape := filepath.Join(home, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got, err := ResolveWithin(home, "escape"); err == nil {
		t.Errorf("ResolveWithin followed a symlink out of the home to %q", got)
	}
	if got, err := ResolveWithin(home, "escape/deeper/file.txt"); err == nil {
		t.Errorf("ResolveWithin followed a symlink out of the home to %q", got)
	}

	// A link that stays inside is fine.
	inner := filepath.Join(home, "site")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "current")
	if err := os.Symlink(inner, link); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveWithin(home, "current")
	if err != nil {
		t.Fatalf("ResolveWithin(current) = %v, want the resolved path", err)
	}
	if got != inner {
		t.Errorf("ResolveWithin(current) = %q, want %q", got, inner)
	}
}

func TestResolveWithinAcceptsPathsThatDoNotExistYet(t *testing.T) {
	home := realTempDir(t)
	want := filepath.Join(home, "example.com", "public")
	got, err := ResolveWithin(home, "example.com/public")
	if err != nil {
		t.Fatalf("ResolveWithin on a missing path = %v, want it to resolve", err)
	}
	if got != want {
		t.Errorf("ResolveWithin = %q, want %q", got, want)
	}
}

func TestResolveWithinRejectsTraversal(t *testing.T) {
	home := realTempDir(t)
	for _, bad := range []string{"..", "../..", "../sibling", "/etc/passwd", "a/../../..", "a/b/../../../etc"} {
		if got, err := ResolveWithin(home, bad); err == nil {
			t.Errorf("ResolveWithin(%q, %q) = %q, want an error", home, bad, got)
		}
	}
}

func TestResolveWithinRequiresAnExistingRoot(t *testing.T) {
	if _, err := ResolveWithin(filepath.Join(realTempDir(t), "nope"), "x"); err == nil {
		t.Error("ResolveWithin accepted a root that does not exist")
	}
	if _, err := ResolveWithin("relative/root", "x"); err == nil {
		t.Error("ResolveWithin accepted a relative root")
	}
}

// FuzzResolveWithin asserts containment holds for arbitrary input: whatever
// comes back is inside the root, or an error.
func FuzzResolveWithin(f *testing.F) {
	for _, seed := range []string{"public", "..", "../..", "/etc/passwd", "a/b/c", "", "./", "a/../..", strings.Repeat("../", 40)} {
		f.Add(seed)
	}
	root, err := filepath.EvalSymlinks(f.TempDir())
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, candidate string) {
		got, err := ResolveWithin(root, candidate)
		if err != nil {
			return
		}
		if got != root && !strings.HasPrefix(got, root+string(filepath.Separator)) {
			t.Fatalf("ResolveWithin(%q, %q) = %q, which escapes the root", root, candidate, got)
		}
		if !filepath.IsAbs(got) {
			t.Fatalf("returned a relative path: %q", got)
		}
		if got != filepath.Clean(got) {
			t.Fatalf("returned an unclean path: %q", got)
		}
	})
}
