package mongod

import (
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

func TestRenderConf(t *testing.T) {
	local, err := RenderConf(false)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := RenderConf(true)
	if err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{"local": string(local), "remote": string(remote)} {
		// The header is what lets ratline rewrite the file later, and — more
		// important — what makes it refuse files that are not its own.
		if !strings.HasPrefix(body, system.ManagedHeader) {
			t.Errorf("the %s render does not begin with the managed header", name)
		}
		// There is no render of this template without enforcement. If this ever
		// fails, a code path can produce an open server.
		if !strings.Contains(body, "authorization: enabled") {
			t.Errorf("the %s render does not enable authorization", name)
		}
	}

	if !strings.Contains(string(local), "bindIp: 127.0.0.1") {
		t.Error("the local render does not bind to localhost")
	}
	if strings.Contains(string(local), "bindIpAll") {
		t.Error("the local render mentions bindIpAll")
	}
	if !strings.Contains(string(remote), "bindIpAll: true") {
		t.Error("the remote render does not open the bind")
	}
	if strings.Contains(string(remote), "bindIp: 127.0.0.1") {
		t.Error("the remote render still binds only localhost")
	}
}

func TestReadConfState(t *testing.T) {
	m, _, _ := testManager(t, true)

	if s := m.confState(); s.Exists {
		t.Error("a missing file reads as existing")
	}
	writeManagedConf(t, m, false)
	if s := m.confState(); !s.Exists || !s.Managed || s.Remote {
		t.Errorf("managed local conf read as %+v", s)
	}
	writeManagedConf(t, m, true)
	if s := m.confState(); !s.Managed || !s.Remote {
		t.Errorf("managed remote conf read as %+v", s)
	}
}
