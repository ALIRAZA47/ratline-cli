package cli

import (
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// A unit's program is resolved the way systemd resolves one: a bare name off the PATH the
// unit carries — the site's own, so `npm` is the npm of the Node this site is pinned to —
// and an absolute path taken as given. A relative path with a slash cannot be resolved at
// all, and the place that used to say so was the job's log at 3am.
func TestAJobsProgramMustBeResolvable(t *testing.T) {
	siteDir := "/home/acme/app.example.com"

	for _, ok := range []string{
		"npm run nightly",
		"python manage.py clearsessions",
		"/home/acme/app.example.com/app/bin/nightly --verbose",
	} {
		if err := checkUnitProgram(ok, siteDir); err != nil {
			t.Errorf("checkUnitProgram(%q) = %v, want it accepted", ok, err)
		}
	}

	err := checkUnitProgram("./bin/nightly", siteDir)
	if err == nil {
		t.Fatal("a relative path was accepted; the unit would fail when the timer fires")
	}
	if rlerr.CodeOf(err) != rlerr.CodeUsage {
		t.Errorf("code = %s, want usage", rlerr.CodeOf(err))
	}
	// The hint has to carry the path that would have worked, or the operator is left to
	// work out where "relative to" is.
	if hint := rlerr.Hint(err); !strings.Contains(hint, "/home/acme/app.example.com/app/bin/nightly") {
		t.Errorf("hint = %q, want the absolute form of what was typed", hint)
	}

	// A command that needs a shell is refused earlier, with its own message; this one
	// must not swallow it and report something about paths instead.
	if err := checkUnitProgram("", siteDir); err == nil {
		t.Error("an empty command was accepted")
	}
}
