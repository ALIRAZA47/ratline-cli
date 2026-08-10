package diag

import (
	"context"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
)

// The exposure check answers from the socket and the firewall — the two facts an
// operator can break by hand, outside ratline, with no command refusing them. A mongod
// listening on every interface behind an inactive ufw is the misconfiguration this
// server-walk check exists to catch; the bare sweep shares the implementation.
func TestServerWalkCatchesAnExposedMongod(t *testing.T) {
	run := func(t *testing.T, ss, ufw string) Result {
		t.Helper()
		bins := system.NewBinaries()
		for _, b := range []string{"mongod", "ufw", "ss"} {
			bins.Set(b, "/usr/bin/"+b)
		}
		fake := systest.NewFakeRunner()
		fake.ExpectOutput("ss -H -l -t -n", ss)
		fake.ExpectOutput("ufw status verbose", ufw)
		env := &Env{Cfg: config.Default(), Log: log.Discard(), Runner: fake, Bins: bins}

		for _, c := range ServerChecks(env) {
			if c.ID == "mongodb-exposure" {
				return c.Run(context.Background())
			}
		}
		t.Fatal("the server walk has no mongodb-exposure check")
		return Result{}
	}

	const (
		remote = "LISTEN 0 4096 0.0.0.0:27017 0.0.0.0:*\n"
		local  = "LISTEN 0 4096 127.0.0.1:27017 0.0.0.0:*\n"
		active = "Status: active\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n"
	)

	if r := run(t, remote, "Status: inactive\n"); r.Verdict != Failed {
		t.Errorf("exposed mongod behind an inactive ufw = %+v, want a failure", r)
	} else if !strings.Contains(r.Detail, "password") {
		t.Errorf("the failure should say what stands between the port and the world: %s", r.Detail)
	}
	if r := run(t, remote, active); r.Verdict != OK {
		t.Errorf("guarded remote mongod = %+v, want a pass", r)
	}
	if r := run(t, local, "Status: inactive\n"); r.Verdict != OK {
		t.Errorf("localhost-only mongod = %+v, want a pass regardless of ufw", r)
	}
}
