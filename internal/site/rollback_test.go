package site

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every rollback stack in this package is unwound on failure.
//
// `Scale` built one, pushed the unit's undo into it through applyUnit, and then
// returned `nil, err` from three later steps without ever running it — so a scale
// that re-rendered the unit and then failed to reload nginx left the site with a
// changed unit and the old vhost, which is precisely the half-applied state the
// stack exists to prevent. `Enable`, `Disable`, `AddAlias` and `RemoveAlias` had the
// same omission. nginx.Apply returns without restoring anything when the *reload*
// fails (it self-heals only the `nginx -t` path), so every one of them was reachable.
//
// Asserted structurally rather than by driving each command, because the failure is
// an omission: a new mutating method with the same gap is the thing to catch, and no
// behavioural test of the five that exist today would notice the sixth. The undo
// steps themselves are exercised in internal/unit and internal/system.
func TestEveryRollbackStackInTheSitePackageIsUnwound(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	create := regexp.MustCompile(`(\w+)\s*:=\s*system\.NewRollback\(`)

	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			m := create.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			found++
			name := m[1]
			// The unwind belongs on the next line. Allowing a window would let a
			// stack be created, pushed to, and only then guarded — which is the
			// window the bug lived in.
			next := ""
			if i+1 < len(lines) {
				next = strings.TrimSpace(lines[i+1])
			}
			want := "defer " + name + ".UnwindOn(ctx, &err)"
			if next != want {
				t.Errorf("%s:%d creates a rollback stack but the next line is not its unwind.\n"+
					"  want: %s\n"+
					"  got:  %s\n"+
					"  a stack that is never unwound leaves a failed command half applied",
					f, i+1, want, next)
			}
		}
	}
	// The scan itself has to be working, or this passes on a regex that matches
	// nothing — which is how the omission survived review in the first place.
	if found < 8 {
		t.Fatalf("only found %d rollback stacks in the package, so the scan is broken, not the code", found)
	}
}
