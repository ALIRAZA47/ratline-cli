package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	osuser "os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
)

func onePM2Daemon(app string) []*pm2Daemon {
	return []*pm2Daemon{{Unit: "ratline-pm2@acme.node22.service", Owner: "acme", App: app}}
}

func TestPM2ArgvRefusesWhatWouldFightRatlinesUnits(t *testing.T) {
	for _, verb := range []string{
		"kill", "delete", "stop", "start", "scale", "save", "resurrect", "startup",
		"update", "install", "link", "deploy", "attach",
	} {
		_, err := pm2Argv([]string{verb, "all"}, onePM2Daemon("acme-app_example_com"))
		if err == nil {
			t.Errorf("pm2 %s was passed through", verb)
		}
	}
	// Not on the list at all is refused too: the list is what is allowed.
	if _, err := pm2Argv([]string{"something-new"}, onePM2Daemon("")); err == nil {
		t.Error("an unknown verb was passed through")
	}
	// A leading flag is refused rather than guessed at.
	if _, err := pm2Argv([]string{"--silent", "kill"}, onePM2Daemon("")); err == nil {
		t.Error("a flag ahead of the verb hid a refused verb")
	}
}

func TestPM2ArgvFillsInTheSitesOwnApplication(t *testing.T) {
	daemons := onePM2Daemon("acme-app_example_com")
	for _, tc := range []struct{ in, want string }{
		{"logs", "logs acme-app_example_com"},
		{"logs --lines 200", "logs acme-app_example_com --lines 200"},
		{"describe", "describe acme-app_example_com"},
		{"restart", "restart acme-app_example_com"},
		// A name given is kept: it is another site of the same tenant.
		{"logs acme-other_example_com", "logs acme-other_example_com"},
		// Verbs that take no application are left alone.
		{"list", "list"},
		{"monit", "monit"},
	} {
		got, err := pm2Argv(strings.Fields(tc.in), daemons)
		if err != nil {
			t.Errorf("pm2Argv(%q) = %v", tc.in, err)
			continue
		}
		if strings.Join(got, " ") != tc.want {
			t.Errorf("pm2Argv(%q) = %q, want %q", tc.in, strings.Join(got, " "), tc.want)
		}
	}
	// A user target names no application, so nothing is filled in.
	got, err := pm2Argv([]string{"logs"}, onePM2Daemon(""))
	if err != nil || strings.Join(got, " ") != "logs" {
		t.Errorf("pm2Argv(logs) for a user = %q, %v; want logs alone", got, err)
	}
}

// Across every daemon at once only a listing is allowed: `restart` against every
// tenant on the server is not a thing anybody means to type.
func TestPM2ArgvAgainstEveryDaemonOnlyLists(t *testing.T) {
	all := append(onePM2Daemon(""), &pm2Daemon{Unit: "ratline-pm2@bob.node22.service", Owner: "bob"})
	if _, err := pm2Argv([]string{"list"}, all); err != nil {
		t.Errorf("list across daemons = %v", err)
	}
	for _, verb := range []string{"restart", "reload", "logs", "flush"} {
		if _, err := pm2Argv([]string{verb}, all); err == nil {
			t.Errorf("pm2 %s ran against every daemon on the server", verb)
		}
	}
}

// The passthrough reads the daemon from its unit, so the two cannot drift: render a unit
// the way the site lifecycle does and parse it back.
func TestPM2PassthroughReadsTheUnitTheLifecycleWrites(t *testing.T) {
	m := &unit.Manager{Cfg: config.Default(), Log: log.Discard()}
	body, err := m.RenderPM2Daemon(&unit.PM2Daemon{
		Owner: "acme", Key: "node22",
		Home:    "/home/acme/.ratline/pm2/node22",
		NodeBin: "/opt/ratline/runtimes/node/22/bin/node",
		PM2:     "/opt/ratline/runtimes/node/22/bin/pm2",
		Sites: []*state.Site{{Domain: "app.example.com", Owner: "acme", Runtime: "node",
			Slug: "acme-app_example_com", Enabled: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	d := parsePM2Unit("ratline-pm2@acme.node22.service", string(body))
	if d.Owner != "acme" || d.Home != "/home/acme/.ratline/pm2/node22" ||
		d.PM2 != "/opt/ratline/runtimes/node/22/bin/pm2" ||
		!strings.HasPrefix(d.Path, "/opt/ratline/runtimes/node/22/bin:") {
		t.Errorf("parsed %+v from the rendered unit", d)
	}
}

// pm2 runs as the daemon's tenant with the daemon's PM2_HOME, and a daemon that is down
// is never asked — asking would start one outside its unit.
func TestPM2RunsAsTheTenantAndLeavesADaemonThatIsDownAlone(t *testing.T) {
	me, err := osuser.Current()
	if err != nil {
		t.Skip("no current user")
	}
	cfg := config.Default()
	cfg.Paths.SystemdDir = t.TempDir()
	// A hook rather than the scripted fake, because what matters here is the whole
	// command — its identity and environment — which the fake's record leaves out.
	var cmds []system.Cmd
	daemonUp := true
	runner := systest.NewFakeRunner()
	runner.Hook = func(c system.Cmd) (*system.Result, error) {
		cmds = append(cmds, c)
		if c.Name == "systemctl" && !daemonUp {
			return &system.Result{ExitCode: 3}, nil
		}
		return &system.Result{Stdout: "[]"}, nil
	}
	out := &bytes.Buffer{}
	g := &Globals{Cfg: cfg, Log: log.Discard(), Runner: runner, JSON: true, Stdout: out, Stderr: &bytes.Buffer{}}
	d := &pm2Daemon{
		Unit: "ratline-pm2@" + me.Username + ".node22.service", Owner: me.Username,
		Home: "/home/x/.ratline/pm2/node22", PM2: "/opt/ratline/runtimes/node/22/bin/pm2",
		Path: "/opt/ratline/runtimes/node/22/bin:/usr/bin",
	}
	if err := g.runPM2(context.Background(), []*pm2Daemon{d}, []string{"jlist"}); err != nil {
		t.Fatalf("runPM2 = %v", err)
	}
	var ran bool
	for _, c := range cmds {
		if c.Path != d.PM2 {
			continue
		}
		ran = true
		if c.As == nil || c.As.Name != me.Username {
			t.Errorf("pm2 ran as %+v, want the daemon's tenant", c.As)
		}
		env := strings.Join(c.Env, "\n")
		if !strings.Contains(env, "PM2_HOME="+d.Home) || !strings.Contains(env, "PATH="+d.Path) {
			t.Errorf("pm2's environment lacks the daemon's PM2_HOME or PATH:\n%s", env)
		}
	}
	if !ran {
		t.Fatalf("pm2 never ran: %+v", cmds)
	}
	var env map[string]any
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Errorf("--json printed something that is not one object: %v\n%s", err, out)
	}

	// The negative: a daemon that is down.
	cmds, daemonUp = nil, false
	if err := g.runPM2(context.Background(), []*pm2Daemon{d}, []string{"jlist"}); err == nil {
		t.Error("runPM2 against a daemon that is down succeeded")
	}
	for _, c := range cmds {
		if c.Path == d.PM2 {
			t.Errorf("pm2 was run against a daemon that is down: %+v", cmds)
		}
	}
}

func TestPM2DaemonUnitsListsOnlyDaemons(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"ratline-pm2@acme.node22.service", "ratline-pm2@acme.node18.service",
		"ratline-pm2@bob.node22.service", "ratline-acme-app_example_com.service", "nginx.service",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := pm2DaemonUnits(dir, "")
	if len(all) != 3 {
		t.Errorf("all daemons = %v, want three", all)
	}
	acme, _ := pm2DaemonUnits(dir, "acme")
	if len(acme) != 2 || strings.Contains(strings.Join(acme, " "), "bob") {
		t.Errorf("acme's daemons = %v", acme)
	}
}
