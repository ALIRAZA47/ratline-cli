package site

import (
	"context"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/runtime"
)

// buildSite is the part of `site add` that turns flags into a row, before
// anything touches the filesystem — so it is where flag combinations can be
// checked without a server.
func testManager() *Manager {
	return &Manager{Cfg: config.Default(), Log: log.Discard(), DryRun: true}
}

func nodeOptions() AddOptions {
	return AddOptions{
		Domain: "app.example.com", Owner: "alice", Runtime: "node",
		Entry: "server.js", Listen: "socket",
	}
}

func TestDaemonFlagAcceptsBothSupervisorsAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		value  string
		wantOK bool
	}{
		{"", true},
		{runtime.ProcessManagerPM2, true},
		{runtime.ProcessManagerDirect, true},
		{"systemd", false},
		{"PM2", false}, // no case folding: the stored value is compared literally
		{"supervisord", false},
	} {
		opts := nodeOptions()
		opts.ProcessManager = tc.value
		site, err := testManager().buildSite(context.Background(), &opts)
		switch {
		case tc.wantOK && err != nil:
			t.Errorf("--daemon %q was refused: %v", tc.value, err)
		case !tc.wantOK && err == nil:
			t.Errorf("--daemon %q was accepted, which would be silently ignored later", tc.value)
		case !tc.wantOK:
			if rlerr.CodeOf(err) != rlerr.CodeUsage {
				t.Errorf("--daemon %q should be a usage error, got code %v", tc.value, rlerr.CodeOf(err))
			}
		case tc.wantOK && site.ProcessManager != tc.value:
			// A value that is accepted has to be stored, or the flag does nothing.
			t.Errorf("stored process manager = %q, want %q", site.ProcessManager, tc.value)
		}
	}
}

func TestDaemonRefusalNamesTheTwoChoices(t *testing.T) {
	opts := nodeOptions()
	opts.ProcessManager = "supervisord"
	_, err := testManager().buildSite(context.Background(), &opts)
	if err == nil {
		t.Fatal("an unknown process manager must be refused")
	}
	if !strings.Contains(err.Error(), "pm2 or direct") {
		t.Errorf("the refusal should name what is allowed, got: %v", err)
	}
}

func TestAStaticSiteIgnoresTheDaemonFlagRatherThanFailing(t *testing.T) {
	// --daemon is meaningless for a static site, but a stray value in a script
	// should not be what stops a deploy: the runtime branch never reads it.
	opts := AddOptions{Domain: "s.example.com", Owner: "alice", Runtime: "static", ProcessManager: "pm2"}
	if _, err := testManager().buildSite(context.Background(), &opts); err != nil {
		t.Errorf("buildSite for a static site = %v", err)
	}
}
