package unit

import (
	"strings"
	"testing"
)

// The --relax flag on `site add` must actually reach the rendered unit. It was
// accepted and ignored once; this is the assertion that keeps it wired.
func TestRelaxFromSiteReachesTheUnit(t *testing.T) {
	site := pythonSite()
	site.Relaxed = []string{"ProtectSystem"}
	out := render(t, site, "/venv/bin/gunicorn app:app", RenderOptions{})
	if strings.Contains(out, "\nProtectSystem=strict") {
		t.Error("--relax ProtectSystem was ignored: the directive is still active")
	}
	if !strings.Contains(out, "# ProtectSystem=strict — relaxed for this site") {
		t.Errorf("the relaxed directive is not recorded in the unit:\n%s", out)
	}
	if !strings.Contains(out, "NoNewPrivileges=true") {
		t.Error("relaxing one directive dropped the rest of the sandbox")
	}
}
