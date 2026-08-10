package mongod

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
)

func opts() InstallOptions {
	return InstallOptions{AdminUser: "admin", Password: "a-password-long-enough"}
}

func TestInstallFreshHost(t *testing.T) {
	m, fake, mf := testManager(t, false)

	res, err := m.Install(context.Background(), opts())
	if err != nil {
		t.Fatal(err)
	}
	if !res.PackageInstalled || !res.AdminUserCreated {
		t.Errorf("result = %+v, want package installed and admin user created", res)
	}
	if res.ServerVersion == "" {
		t.Error("the server's version was not reported")
	}
	if !strings.Contains(res.AdminURI, "admin:") || !strings.Contains(res.AdminURI, "@127.0.0.1:27017") {
		t.Errorf("AdminURI = %q, want local credentials", res.AdminURI)
	}

	// The repository was added with the pinned key before apt ran.
	sources, err := os.ReadFile(m.abs(SourcesPath("8.0")))
	if err != nil {
		t.Fatalf("no sources file: %v", err)
	}
	if !strings.Contains(string(sources), "signed-by=/usr/share/keyrings/mongodb-server-8.0.gpg") {
		t.Errorf("sources = %q, want the pinned keyring", sources)
	}
	if _, err := os.Stat(m.abs(KeyringPath("8.0"))); err != nil {
		t.Errorf("no keyring was written: %v", err)
	}
	for _, key := range []string{"apt-get update", "apt-get install -y mongodb-org",
		"systemctl enable mongod", "systemctl start mongod", "systemctl restart mongod"} {
		if !fake.Called(key) {
			t.Errorf("%q was never run; calls: %v", key, fake.Keys())
		}
	}

	// The final state: a managed, localhost-only, authorization-enforcing config,
	// and a server that actually enforces it.
	conf := m.confState()
	if !conf.Managed || conf.Remote {
		t.Errorf("conf state after install = %+v", conf)
	}
	if !mf.authEnforced {
		t.Error("the running server does not enforce authorization")
	}
}

func TestInstallRefusesAServerItDidNotSetUp(t *testing.T) {
	m, fake, _ := testManager(t, true)
	// A hand-configured mongod: present, with somebody's own config.
	if err := os.WriteFile(m.confPath(), []byte("storage:\n  dbPath: /srv/mongo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := m.Install(context.Background(), opts())
	if err == nil {
		t.Fatal("a foreign mongod was adopted")
	}
	if !strings.Contains(err.Error()+" "+rlerr.Hint(err), "db connect") {
		t.Errorf("the refusal should point at 'db connect': %v", err)
	}
	if body, _ := os.ReadFile(m.confPath()); !strings.Contains(string(body), "/srv/mongo") {
		t.Error("the foreign config was modified")
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("commands ran before the refusal: %v", fake.Keys())
	}
}

func TestInstallUnwindsWhenVerificationFails(t *testing.T) {
	m, fake, mf := testManager(t, false)
	// The restart does not apply the config — a unit override, say. The install must
	// fail, and must put everything back.
	mf.brokenConfApply = true

	_, err := m.Install(context.Background(), opts())
	if err == nil {
		t.Fatal("an install whose server never enforced authorization reported success")
	}
	if !strings.Contains(err.Error(), "does not enforce authorization") {
		t.Errorf("the failure should say what was not achieved: %v", err)
	}

	// Nothing is left behind except the packages, which are stopped and disabled.
	for _, path := range []string{m.confPath(), m.abs(SourcesPath("8.0")), m.abs(KeyringPath("8.0"))} {
		if _, serr := os.Stat(path); serr == nil {
			t.Errorf("%s survived the unwind", path)
		}
	}
	if mf.userExists {
		t.Error("the admin user survived the unwind")
	}
	// And it was removed by an actual dropUser against the local server — not by the
	// fake happening to reset itself.
	dropped := false
	for _, op := range mf.opList() {
		if op == "dropUser:plain" {
			dropped = true
		}
	}
	if !dropped {
		t.Errorf("no dropUser ran during the unwind; ops: %v", mf.opList())
	}
	if !fake.Called("systemctl disable --now mongod") {
		t.Errorf("mongod was not stopped and disabled; calls: %v", fake.Keys())
	}
}

func TestInstallAgainIsANoOpThatVerifies(t *testing.T) {
	m, fake, mf := testManager(t, true)
	writeManagedConf(t, m, false)
	mf.authEnforced, mf.userExists, mf.credentialsWork = true, true, true
	fake.Expect("systemctl is-active mongod", systest.Response{Stdout: "active"})

	res, err := m.Install(context.Background(), opts())
	if err != nil {
		t.Fatal(err)
	}
	if res.PackageInstalled || res.AdminUserCreated {
		t.Errorf("a re-run reported doing work: %+v", res)
	}
	// A healthy server serving tenants is not bounced by a re-run.
	if fake.Called("systemctl restart") || fake.Called("apt-get") {
		t.Errorf("a re-run touched the server: %v", fake.Keys())
	}
}

func TestInstallRefusesTheWrongPasswordOnARerun(t *testing.T) {
	m, _, mf := testManager(t, true)
	writeManagedConf(t, m, false)
	mf.authEnforced, mf.userExists = true, true
	mf.credentialsWork = false // the operator typed a different password this time

	_, err := m.Install(context.Background(), opts())
	if err == nil {
		t.Fatal("a wrong password was accepted")
	}
	if !strings.Contains(err.Error(), "did not work") {
		t.Errorf("the error should be about the password: %v", err)
	}
}
