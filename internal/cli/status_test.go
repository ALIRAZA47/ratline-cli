package cli

import (
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

func TestPluralReadsAsEnglish(t *testing.T) {
	// "1 sites" reads as a bug in the tool rather than as a count of one.
	for _, tc := range []struct {
		n          int
		noun, want string
	}{
		{0, "site", "0 sites"},
		{1, "site", "1 site"},
		{2, "site", "2 sites"},
		{1, "SSH key", "1 SSH key"},
		{3, "day", "3 days"},
	} {
		if got := plural(tc.n, tc.noun); got != tc.want {
			t.Errorf("plural(%d, %q) = %q, want %q", tc.n, tc.noun, got, tc.want)
		}
	}
}

func TestResolvesHereIsGenerousWhenItCannotKnow(t *testing.T) {
	// With no recorded public address there is nothing to compare against, so
	// claiming a mismatch would be a guess presented as a finding.
	if !resolvesHere([]string{"203.0.113.9"}, nil, nil) {
		t.Error("with no known addresses this must not report a mismatch")
	}
	if !resolvesHere([]string{"203.0.113.9"}, []string{"203.0.113.9"}, nil) {
		t.Error("a matching IPv4 address should resolve here")
	}
	if !resolvesHere([]string{"2001:db8::1"}, nil, []string{"2001:db8::1"}) {
		t.Error("a matching IPv6 address should resolve here")
	}
	if resolvesHere([]string{"198.51.100.1"}, []string{"203.0.113.9"}, nil) {
		t.Error("an address that is not ours should report a mismatch")
	}
}

func TestUptimeHumanIsEmptyRatherThanWrong(t *testing.T) {
	// /proc/uptime does not exist on darwin, and a summary screen should simply
	// omit the field rather than report an error for it.
	got := uptimeHuman()
	if got != "" && !strings.ContainsAny(got, "mhd") {
		t.Errorf("uptimeHuman = %q, want a duration or nothing", got)
	}
}

func TestFillSiteStateFlagsADisabledSite(t *testing.T) {
	row := SiteStatusRow{}
	fillSiteState(nil, nil, &state.Site{Domain: "x.example.com", Runtime: "static"}, &row)
	// Disabled needs attention: nginx is not serving it, which is almost never
	// what someone looking at this screen expects.
	if row.State != "disabled" || !row.NeedsAttention {
		t.Errorf("got %+v, want a disabled row needing attention", row)
	}
}

func TestFillSiteStateAsksNothingOfAStaticSite(t *testing.T) {
	// A nil manager proves no unit or process lookup happens: a static site has no
	// process, so there is nothing to query and nothing that can fail.
	row := SiteStatusRow{}
	fillSiteState(nil, nil, &state.Site{Domain: "x.example.com", Runtime: "static", Enabled: true}, &row)
	if row.State != "serving" || row.NeedsAttention {
		t.Errorf("got %+v, want a healthy serving row", row)
	}
}

func TestStatusAndTroubleshootRequireRoot(t *testing.T) {
	// Both read the state database at 0600 and probe a socket only root can reach.
	// Refusing is correct; silently reporting less would not be.
	for _, args := range [][]string{
		{"status"},
		{"site", "troubleshoot", "app.example.com"},
	} {
		code, _, errOut := harness(t, args...)
		if code != 3 {
			t.Errorf("%v exited %d, want 3 (needs root)", args, code)
		}
		if !strings.Contains(errOut.String(), "root") {
			t.Errorf("%v should say why it refused:\n%s", args, errOut.String())
		}
	}
}

func TestStatusIsNotAMutation(t *testing.T) {
	// It must not be annotated as mutating, or a read-only summary would take the
	// global lock and block behind whatever deploy is in flight.
	g := NewGlobals()
	root := NewRootCommand(g)
	for _, c := range root.Commands() {
		if c.Name() == "status" {
			if annotated(c, AnnoMutates) {
				t.Error("status must not be marked as mutating")
			}
			return
		}
	}
	t.Fatal("the status command is not registered")
}
