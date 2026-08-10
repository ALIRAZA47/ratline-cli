package mongod

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
)

// accessManager is a host where `db install` has already finished: managed local-only
// config, attached URI, a server whose credentials work.
func accessManager(t *testing.T) (*Manager, *systest.FakeRunner, *mongoServerFake) {
	t.Helper()
	m, fake, mf := testManager(t, true)
	writeManagedConf(t, m, false)
	writeAdminURIFile(t, m)
	mf.authEnforced, mf.userExists, mf.credentialsWork = true, true, true
	return m, fake, mf
}

func TestCanonicalAddress(t *testing.T) {
	for input, want := range map[string]string{
		"203.0.113.19":    "203.0.113.19/32",
		"203.0.113.0/24":  "203.0.113.0/24",
		"2001:db8::7/128": "2001:db8::7/128",
	} {
		got, err := CanonicalAddress(input)
		if err != nil {
			t.Errorf("CanonicalAddress(%q): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("CanonicalAddress(%q) = %q, want %q", input, got, want)
		}
	}
	for _, bad := range []string{"", "banana", "1.2.3.4,5.6.7.8", "300.0.0.1"} {
		if _, err := CanonicalAddress(bad); err == nil {
			t.Errorf("CanonicalAddress(%q) was accepted", bad)
		}
	}
}

func TestAccessAllowFirstAddressOpensTheBind(t *testing.T) {
	m, fake, mf := accessManager(t)
	ctx := context.Background()

	res, err := m.AccessAllow(ctx, "203.0.113.19", "office", "test")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OpenedNetwork || res.Address != "203.0.113.19/32" {
		t.Errorf("result = %+v", res)
	}
	if !fake.Called("ufw allow proto tcp from 203.0.113.19/32 to any port 27017") {
		t.Errorf("no ufw rule was added; calls: %v", fake.Keys())
	}
	if s := m.confState(); !s.Remote {
		t.Error("mongod was not reconfigured to listen beyond localhost")
	}
	if !fake.Called("systemctl restart mongod") {
		t.Error("the wider bind was written but never applied")
	}
	if !mf.authEnforced {
		t.Error("the restarted server does not enforce authorization")
	}
	rows, err := m.State.ListMongoAccess(ctx)
	if err != nil || len(rows) != 1 || rows[0].Address != "203.0.113.19/32" {
		t.Errorf("state rows = %v (err %v)", rows, err)
	}

	// A second address is a firewall rule and a row — the bind is already open, and
	// restarting a serving database for it would be pure disruption.
	restarts := fake.CountCalls("systemctl restart")
	if res, err = m.AccessAllow(ctx, "198.51.100.0/24", "", "test"); err != nil {
		t.Fatal(err)
	}
	if res.OpenedNetwork {
		t.Error("the second address claims to have opened the network")
	}
	if fake.CountCalls("systemctl restart") != restarts {
		t.Error("the second address restarted mongod")
	}

	// The same address again changes nothing and says so.
	calls := len(fake.Calls())
	res, err = m.AccessAllow(ctx, "203.0.113.19/32", "", "test")
	if err != nil || !res.AlreadyThere {
		t.Errorf("re-allow = %+v, %v", res, err)
	}
	if fake.CountCalls("ufw allow") > calls {
		t.Error("re-allowing ran ufw again")
	}
}

func TestAccessAllowRefusalsComeBeforeChanges(t *testing.T) {
	ctx := context.Background()

	t.Run("inactive ufw", func(t *testing.T) {
		m, fake, _ := accessManager(t)
		fake.ExpectOutput("ufw status verbose", "Status: inactive\n")
		_, err := m.AccessAllow(ctx, "203.0.113.19", "", "test")
		if err == nil || !strings.Contains(err.Error(), "not active") {
			t.Fatalf("err = %v", err)
		}
		if fake.Called("ufw allow") || m.confState().Remote {
			t.Error("a refusal still changed the host")
		}
	})

	t.Run("default allow", func(t *testing.T) {
		m, fake, _ := accessManager(t)
		fake.ExpectOutput("ufw status verbose",
			"Status: active\nDefault: allow (incoming), allow (outgoing), disabled (routed)\n")
		_, err := m.AccessAllow(ctx, "203.0.113.19", "", "test")
		if err == nil || !strings.Contains(err.Error(), "default incoming policy") {
			t.Fatalf("err = %v", err)
		}
		if fake.Called("ufw allow") {
			t.Error("a refusal still added a rule")
		}
	})

	t.Run("unmanaged conf", func(t *testing.T) {
		m, fake, _ := testManager(t, true)
		writeAdminURIFile(t, m)
		if err := os.WriteFile(m.confPath(), []byte("net:\n  bindIp: 0.0.0.0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := m.AccessAllow(ctx, "203.0.113.19", "", "test")
		if err == nil || !strings.Contains(err.Error(), "not managed by ratline") {
			t.Fatalf("err = %v", err)
		}
		if fake.Called("ufw") {
			t.Error("a refusal still touched the firewall")
		}
	})

	t.Run("nothing attached", func(t *testing.T) {
		// Opening the bind ends in a verification that needs the admin credentials;
		// their absence must refuse before the firewall changes, not after.
		m, fake, _ := accessManager(t)
		if err := os.Remove(m.Cfg.Paths.MongoURIFile); err != nil {
			t.Fatal(err)
		}
		_, err := m.AccessAllow(ctx, "203.0.113.19", "", "test")
		if err == nil {
			t.Fatal("an unattached host opened its database port")
		}
		if fake.Called("ufw allow") {
			t.Error("the firewall changed before the refusal")
		}
	})
}

func TestAccessAllowUnwindsWhenVerificationFails(t *testing.T) {
	m, fake, mf := accessManager(t)
	mf.brokenConfApply = true // the restart never applies the wider bind

	_, err := m.AccessAllow(context.Background(), "203.0.113.19", "", "test")
	if err == nil {
		t.Fatal("an allow whose restart failed verification reported success")
	}
	if !fake.Called("ufw delete allow proto tcp from 203.0.113.19/32") {
		t.Errorf("the ufw rule was not removed on unwind; calls: %v", fake.Keys())
	}
	rows, _ := m.State.ListMongoAccess(context.Background())
	if len(rows) != 0 {
		t.Errorf("state still lists the address: %v", rows)
	}
	if m.confState().Remote {
		t.Error("the config still binds beyond localhost after the unwind")
	}
}

func TestAccessRevoke(t *testing.T) {
	m, fake, _ := accessManager(t)
	ctx := context.Background()
	if _, err := m.AccessAllow(ctx, "203.0.113.19", "", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AccessAllow(ctx, "198.51.100.7", "", "test"); err != nil {
		t.Fatal(err)
	}

	// Revoking one of two: rule and row go, the bind stays open for the other.
	res, err := m.AccessRevoke(ctx, "198.51.100.7")
	if err != nil {
		t.Fatal(err)
	}
	if res.ClosedNetwork || res.WasAbsent {
		t.Errorf("result = %+v", res)
	}
	if !fake.Called("ufw delete allow proto tcp from 198.51.100.7/32") {
		t.Errorf("the rule was not deleted; calls: %v", fake.Keys())
	}
	if !m.confState().Remote {
		t.Error("revoking one of two addresses closed the bind")
	}

	// Revoking the last one puts mongod back on localhost only.
	res, err = m.AccessRevoke(ctx, "203.0.113.19")
	if err != nil {
		t.Fatal(err)
	}
	if !res.ClosedNetwork {
		t.Error("revoking the last address did not close the network")
	}
	if m.confState().Remote {
		t.Error("the config still binds beyond localhost")
	}
	rows, _ := m.State.ListMongoAccess(ctx)
	if len(rows) != 0 {
		t.Errorf("state rows remain: %v", rows)
	}

	// Revoking an address that was never allowed is the desired state, not an error.
	res, err = m.AccessRevoke(ctx, "192.0.2.1")
	if err != nil || !res.WasAbsent {
		t.Errorf("revoke of absent = %+v, %v", res, err)
	}
}

func TestCheckExposure(t *testing.T) {
	ctx := context.Background()

	t.Run("no server", func(t *testing.T) {
		m, _, _ := testManager(t, false)
		exp, err := m.CheckExposure(ctx)
		if err != nil || exp.Present {
			t.Errorf("exposure = %+v, %v", exp, err)
		}
	})

	t.Run("localhost only", func(t *testing.T) {
		m, _, _ := accessManager(t)
		exp, err := m.CheckExposure(ctx)
		if err != nil || !exp.Present || exp.Remote {
			t.Errorf("exposure = %+v, %v", exp, err)
		}
	})

	t.Run("remote and guarded", func(t *testing.T) {
		m, _, _ := accessManager(t)
		if _, err := m.AccessAllow(ctx, "203.0.113.19", "", "test"); err != nil {
			t.Fatal(err)
		}
		exp, err := m.CheckExposure(ctx)
		if err != nil || !exp.Remote || !exp.Guarded || exp.Allowed != 1 {
			t.Errorf("exposure = %+v, %v", exp, err)
		}
	})

	t.Run("remote and unguarded is the finding", func(t *testing.T) {
		// The operator allowed an address, then later disabled ufw by hand. Nothing
		// refuses that — it happens outside ratline — so doctor has to see it.
		m, fake, _ := accessManager(t)
		if _, err := m.AccessAllow(ctx, "203.0.113.19", "", "test"); err != nil {
			t.Fatal(err)
		}
		fake.ExpectOutput("ufw status verbose", "Status: inactive\n")
		exp, err := m.CheckExposure(ctx)
		if err != nil || !exp.Remote || exp.Guarded {
			t.Errorf("exposure = %+v, %v", exp, err)
		}
	})
}

func TestAccessList(t *testing.T) {
	m, _, _ := accessManager(t)
	ctx := context.Background()
	if _, err := m.AccessAllow(ctx, "203.0.113.19", "office", "test"); err != nil {
		t.Fatal(err)
	}
	s, err := m.AccessList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !s.ConfManaged || !s.BindRemote || !s.UfwPresent || !s.UfwActive || !s.DefaultDeny {
		t.Errorf("status = %+v", s)
	}
	if len(s.Addresses) != 1 || s.Addresses[0].Address != "203.0.113.19/32" || s.Addresses[0].Note != "office" {
		t.Errorf("addresses = %v", s.Addresses)
	}
}
