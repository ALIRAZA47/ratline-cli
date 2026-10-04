package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
	"github.com/ALIRAZA47/ratline-cli/internal/validate"
)

// `ratline nginx`, `systemctl` and `journalctl` are root-only, and the root check runs in
// PersistentPreRun — so these tests go at the planners and at g.run*, everything after
// cobra. A test that stopped at "ratline must run as root" would prove nothing.

const (
	ptDomain = "app.example.com"
	ptStatic = "static.example.com"
)

var (
	ptSlug = validate.Slug("acme", ptDomain)
	ptUnit = validate.UnitName("acme", ptDomain)
)

// passthroughGlobals builds a Globals with a real state database holding one node site
// and one static site, the node site's unit file on disk naming its journal namespace,
// and the lock under the test's own directory.
func passthroughGlobals(t *testing.T, runner system.Runner, dryRun bool) (*Globals, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Paths.StateDB = filepath.Join(dir, "state.db")
	cfg.Paths.Lock = filepath.Join(dir, "ratline.lock")
	cfg.Paths.SystemdDir = filepath.Join(dir, "systemd")
	if err := os.MkdirAll(cfg.Paths.SystemdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	unitBody := "[Service]\nLogNamespace=" + ptSlug + "\n"
	if err := os.WriteFile(filepath.Join(cfg.Paths.SystemdDir, ptUnit), []byte(unitBody), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := state.Open(cfg.Paths.StateDB)
	if err != nil {
		t.Fatalf("state.Open = %v", err)
	}
	ctx := context.Background()
	if err := st.PutUser(ctx, &state.User{Name: "acme", Home: filepath.Join(dir, "acme"), Shell: "/bin/sh"}); err != nil {
		t.Fatalf("PutUser = %v", err)
	}
	for _, s := range []*state.Site{
		{Domain: ptDomain, Owner: "acme", Runtime: "node", Slug: ptSlug},
		{Domain: ptStatic, Owner: "acme", Runtime: "static", Slug: validate.Slug("acme", ptStatic)},
	} {
		if err := st.PutSite(ctx, s); err != nil {
			t.Fatalf("PutSite(%s) = %v", s.Domain, err)
		}
	}
	st.Close()

	out := &bytes.Buffer{}
	g := &Globals{
		Cfg: cfg, Log: log.Discard(), Runner: runner, DryRun: dryRun,
		Stdout: out, Stderr: &bytes.Buffer{}, Stdin: strings.NewReader(""),
		CmdPath: "ratline passthrough",
	}
	t.Cleanup(func() { g.teardown(nil, 0) })
	return g, out
}

func callKeys(f *systest.FakeRunner) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Key)
	}
	return out
}

func wantUsage(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: accepted, want a refusal", what)
		return
	}
	if code := rlerr.CodeOf(err); code != rlerr.CodeUsage {
		t.Errorf("%s: exit code %d (%v), want usage: %v", what, code, code, err)
	}
	if rlerr.Hint(err) == "" {
		t.Errorf("%s: refused without a hint: %v", what, err)
	}
}

// ── nginx ────────────────────────────────────────────────────────────────────

func TestPlanNginxAcceptsTheReadSwitchesAndReload(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		reload bool
	}{
		{[]string{"-t"}, false},
		{[]string{"-T"}, false},
		{[]string{"-v"}, false},
		{[]string{"-V"}, false},
		{[]string{"-t", "-q"}, false},
		{[]string{"reload"}, true},
		{[]string{"-s", "reload"}, true},
		{[]string{"-sreload"}, true},
	} {
		p, err := planNginx(tc.args)
		if err != nil {
			t.Errorf("planNginx(%q) = %v, want accepted", tc.args, err)
			continue
		}
		if p.Reload != tc.reload {
			t.Errorf("planNginx(%q).Reload = %v, want %v", tc.args, p.Reload, tc.reload)
		}
		if !p.Reload && strings.Join(p.Args, " ") != strings.Join(tc.args, " ") {
			t.Errorf("planNginx(%q).Args = %q, want them untouched", tc.args, p.Args)
		}
	}
}

func TestPlanNginxRefusesEverythingElse(t *testing.T) {
	for _, args := range [][]string{
		{"-s", "stop"},
		{"-s", "quit"},
		{"-squit"},
		{"-s", "reopen"},
		{"-s"},
		{"-s", "hup"},
		{"-c", "/tmp/mine.conf"},
		{"-c/tmp/mine.conf"},
		{"-t", "-c", "/tmp/mine.conf"},
		{"-g", "daemon off;"},
		{"-p", "/tmp"},
		{"-e", "/tmp/err.log"},
		{"-tq"},
		{"-h"},
		{"--help"},
		{"stop"},
		{"reload", "-t"},
		{"-s", "reload", "-t"},
		// -q alone is not a test: nginx with no -t, -T, -v or -V starts a master.
		{"-q"},
		{},
	} {
		_, err := planNginx(args)
		wantUsage(t, err, "planNginx("+strings.Join(args, " ")+")")
	}
}

func TestNginxStopHintSaysWhy(t *testing.T) {
	_, err := planNginx([]string{"-s", "stop"})
	if hint := rlerr.Hint(err); !strings.Contains(hint, "no web server") {
		t.Errorf("hint = %q, want it to say ratline would have no web server", hint)
	}
}

func TestNginxReadRunsTheRegistryBinaryWithoutTheLock(t *testing.T) {
	fake := systest.NewFakeRunner()
	g, _ := passthroughGlobals(t, fake, false)

	if err := g.runNginx(context.Background(), &nginxPlan{Args: []string{"-t"}}); err != nil {
		t.Fatalf("runNginx(-t) = %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Key != "nginx -t" || calls[0].Name != "nginx" || calls[0].Mutates {
		t.Errorf("calls = %+v, want exactly one read of 'nginx -t' by registry name", calls)
	}
	if g.lock != nil {
		t.Error("a read took the server lock")
	}
}

// The reload is the one mutation, and it must never reach nginx as -s reload.
func TestNginxReloadTestsThenReloadsThroughSystemdUnderTheLock(t *testing.T) {
	fake := systest.NewFakeRunner()
	g, _ := passthroughGlobals(t, fake, false)

	if err := g.runNginx(context.Background(), &nginxPlan{Reload: true}); err != nil {
		t.Fatalf("runNginx(reload) = %v", err)
	}
	keys := callKeys(fake)
	test, reload := -1, -1
	for i, k := range keys {
		switch k {
		case "nginx -t":
			test = i
		case "systemctl reload nginx":
			reload = i
		case "nginx -s reload":
			t.Errorf("the reload signalled nginx directly: %q", keys)
		}
	}
	if test < 0 || reload < 0 || test > reload {
		t.Errorf("calls = %q, want nginx -t and then systemctl reload nginx", keys)
	}
	if g.lock == nil {
		t.Error("the reload did not take the server lock")
	}
}

func TestNginxReloadDoesNotReloadAConfigurationThatFailsItsTest(t *testing.T) {
	fake := systest.NewFakeRunner()
	fake.ExpectFailure("nginx -t", 1, "nginx: [emerg] unexpected \"}\" in /etc/nginx/sites-enabled/x:12")
	g, _ := passthroughGlobals(t, fake, false)

	err := g.runNginx(context.Background(), &nginxPlan{Reload: true})
	if err == nil {
		t.Fatal("a reload went ahead on a configuration that failed nginx -t")
	}
	if fake.Called("systemctl") {
		t.Errorf("systemctl ran after a failed test: %q", callKeys(fake))
	}
}

func TestNginxReloadDryRunReloadsNothing(t *testing.T) {
	fake := systest.NewFakeRunner()
	g, out := passthroughGlobals(t, fake, true)

	if err := g.runNginx(context.Background(), &nginxPlan{Reload: true}); err != nil {
		t.Fatalf("runNginx(reload) under --dry-run = %v", err)
	}
	for _, k := range callKeys(fake) {
		if k != "nginx -t" {
			t.Errorf("the rehearsal ran %q; only the read-only test may run", k)
		}
	}
	if !strings.Contains(out.String(), "would reload") {
		t.Errorf("the rehearsal did not say what it would do:\n%s", out.String())
	}
	if g.lock != nil {
		t.Error("a rehearsal took the server lock")
	}
}

// ── systemctl ────────────────────────────────────────────────────────────────

func TestPlanSystemctlTranslatesSitesAndBuildsTheArgv(t *testing.T) {
	fake := systest.NewFakeRunner()
	g, _ := passthroughGlobals(t, fake, false)
	journald := "systemd-journald@" + ptSlug + ".service"

	for _, tc := range []struct {
		args      []string
		argv      string
		control   bool
		testNginx bool
	}{
		{[]string{"status", ptDomain}, "--no-pager status -- " + ptUnit, false, false},
		{[]string{"restart", ptDomain}, "--no-pager restart -- " + ptUnit, true, false},
		{[]string{"list-units", "ratline-*", "--all"}, "--no-pager list-units --all -- ratline-*", false, false},
		{[]string{"list-units", "--failed"}, "--no-pager list-units --failed", false, false},
		{[]string{"show", "nginx", "--property=ActiveState"}, "--no-pager show --property=ActiveState -- nginx", false, false},
		{[]string{"status", "ssh"}, "--no-pager status -- ssh", false, false},
		{[]string{"status", "php8.2-fpm"}, "--no-pager status -- php8.2-fpm", false, false},
		{[]string{"restart", "nginx"}, "--no-pager restart -- nginx.service", true, true},
		{[]string{"stop", "nginx.service"}, "--no-pager stop -- nginx.service", true, false},
		{[]string{"restart", "ratline-pm2@acme.service"}, "--no-pager restart -- ratline-pm2@acme.service", true, false},
		{[]string{"start", "ratline-renew.timer"}, "--no-pager start -- ratline-renew.timer", true, false},
		{[]string{"reset-failed", "ratline.target"}, "--no-pager reset-failed -- ratline.target", true, false},
		{[]string{"restart", journald}, "--no-pager restart -- " + journald, true, false},
	} {
		p, err := g.planSystemctl(context.Background(), tc.args)
		if err != nil {
			t.Errorf("planSystemctl(%q) = %v, want accepted", tc.args, err)
			continue
		}
		if got := strings.Join(p.Argv, " "); got != tc.argv {
			t.Errorf("planSystemctl(%q).Argv = %q, want %q", tc.args, got, tc.argv)
		}
		if p.Control != tc.control || p.TestNginx != tc.testNginx {
			t.Errorf("planSystemctl(%q): control=%v testNginx=%v, want %v/%v",
				tc.args, p.Control, p.TestNginx, tc.control, tc.testNginx)
		}
	}
}

func TestPlanSystemctlRefuses(t *testing.T) {
	fake := systest.NewFakeRunner()
	g, _ := passthroughGlobals(t, fake, false)

	for _, args := range [][]string{
		// Verbs that fight ratline, each with a hint.
		{"enable", ptDomain},
		{"disable", ptDomain},
		{"mask", ptUnit},
		{"unmask", ptUnit},
		{"edit", ptUnit},
		{"set-property", ptUnit, "MemoryMax=1G"},
		{"daemon-reload"},
		{"kill", ptDomain},
		{"isolate", "rescue.target"},
		{"poweroff"},
		{"reboot"},
		{"frobnicate"},
		{"--all"},
		// Control on something ratline does not manage.
		{"restart", "ssh"},
		{"stop", "sshd.service"},
		{"restart", "ratline-panel.socket"},
		{"restart", "systemd-journald@nobody.service"},
		{"restart", "systemd-journald.service"},
		// Control with a pattern, a switch, or nothing to act on.
		{"restart", "ratline-*"},
		{"restart", "ratline-[ab].service"},
		{"restart", "--now", ptDomain},
		{"restart", "-f", ptDomain},
		{"restart"},
		// Reads with switches ratline does not pass, or a value split from its switch.
		{"status", "--host=elsewhere", "nginx"},
		{"status", "-H", "elsewhere"},
		{"status", "--root=/mnt"},
		{"status", "--user"},
		{"show", "--property", "ActiveState", "nginx"},
		{"status", "--no-pager-ish"},
		// A static site has no unit.
		{"status", ptStatic},
		{"restart", ptStatic},
		// Names that are not names.
		{"status", "-x"},
		{"status", "a/b.service"},
		{"cat"},
	} {
		_, err := g.planSystemctl(context.Background(), args)
		wantUsage(t, err, "planSystemctl("+strings.Join(args, " ")+")")
	}
}

func TestSystemctlRefusalHintsNameTheRatlineCommand(t *testing.T) {
	g, _ := passthroughGlobals(t, systest.NewFakeRunner(), false)
	for verb, want := range map[string]string{
		"enable":        "ratline site enable",
		"disable":       "ratline site disable",
		"edit":          "ratline site scale",
		"daemon-reload": "ratline reconcile --fix",
	} {
		_, err := g.planSystemctl(context.Background(), []string{verb, ptDomain})
		if hint := rlerr.Hint(err); !strings.Contains(hint, want) {
			t.Errorf("systemctl %s: hint = %q, want it to name %q", verb, hint, want)
		}
	}
}

// The menu and the panel collect "restart app.example.com" as one field.
func TestSystemctlFieldsSplitsAOneFieldCommand(t *testing.T) {
	if got := strings.Join(systemctlFields([]string{"restart " + ptDomain}), "|"); got != "restart|"+ptDomain {
		t.Errorf("systemctlFields = %q", got)
	}
}

// The lock is taken at run time, so the property that matters is checked at run time: a
// read never holds it, a control verb always does.
func TestSystemctlLocksOnlyForControl(t *testing.T) {
	ctx := context.Background()

	fake := systest.NewFakeRunner()
	g, _ := passthroughGlobals(t, fake, false)
	p, err := g.planSystemctl(ctx, []string{"status", ptDomain})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.runSystemctl(ctx, p); err != nil {
		t.Fatalf("runSystemctl(status) = %v", err)
	}
	if g.lock != nil {
		t.Error("a read verb took the server lock")
	}
	if calls := fake.Calls(); len(calls) != 1 || calls[0].Mutates {
		t.Errorf("calls = %+v, want one non-mutating systemctl", calls)
	}

	fake = systest.NewFakeRunner()
	g, _ = passthroughGlobals(t, fake, false)
	p, err = g.planSystemctl(ctx, []string{"restart", ptDomain})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.runSystemctl(ctx, p); err != nil {
		t.Fatalf("runSystemctl(restart) = %v", err)
	}
	if g.lock == nil {
		t.Error("a control verb did not take the server lock")
	}
	calls := fake.Calls()
	if len(calls) != 1 || !calls[0].Mutates || calls[0].Name != "systemctl" ||
		calls[0].Key != "systemctl --no-pager restart -- "+ptUnit {
		t.Errorf("calls = %+v, want exactly one mutating 'systemctl --no-pager restart -- %s'", calls, ptUnit)
	}
}

func TestSystemctlControlDryRunRunsNothing(t *testing.T) {
	fake := systest.NewFakeRunner()
	g, out := passthroughGlobals(t, fake, true)
	ctx := context.Background()

	for _, args := range [][]string{{"restart", ptDomain}, {"restart", "nginx"}} {
		p, err := g.planSystemctl(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.runSystemctl(ctx, p); err != nil {
			t.Fatalf("runSystemctl(%q) under --dry-run = %v", args, err)
		}
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("the rehearsal ran %q", callKeys(fake))
	}
	if !strings.Contains(out.String(), "would run: systemctl --no-pager restart -- "+ptUnit) {
		t.Errorf("the rehearsal did not print the plan:\n%s", out.String())
	}
	if g.lock != nil {
		t.Error("a rehearsal took the server lock")
	}
}

func TestSystemctlTestsNginxBeforeRestartingIt(t *testing.T) {
	ctx := context.Background()

	fake := systest.NewFakeRunner()
	g, _ := passthroughGlobals(t, fake, false)
	p, _ := g.planSystemctl(ctx, []string{"restart", "nginx"})
	if err := g.runSystemctl(ctx, p); err != nil {
		t.Fatalf("runSystemctl = %v", err)
	}
	if got := strings.Join(callKeys(fake), " | "); got != "nginx -t | systemctl --no-pager restart -- nginx.service" {
		t.Errorf("calls = %q", got)
	}

	fake = systest.NewFakeRunner()
	fake.ExpectFailure("nginx -t", 1, "nginx: [emerg] bad")
	g, _ = passthroughGlobals(t, fake, false)
	p, _ = g.planSystemctl(ctx, []string{"restart", "nginx"})
	if err := g.runSystemctl(ctx, p); err == nil {
		t.Fatal("nginx was restarted on a configuration that failed its test")
	}
	if fake.Called("systemctl") {
		t.Errorf("systemctl ran after a failed nginx -t: %q", callKeys(fake))
	}
}

// systemctl status of a stopped unit exits 3. That is reported — exit 4, naming the 3 —
// and not adopted, and under --json the one envelope carries it.
func TestSystemctlExitCodeIsReportedNotAdopted(t *testing.T) {
	fake := systest.NewFakeRunner()
	fake.ExpectFailure("systemctl --no-pager status -- "+ptUnit, 3, "")
	g, out := passthroughGlobals(t, fake, false)
	g.JSON = true
	ctx := context.Background()

	p, err := g.planSystemctl(ctx, []string{"status", ptDomain})
	if err != nil {
		t.Fatal(err)
	}
	err = g.runSystemctl(ctx, p)
	if code := rlerr.CodeOf(err); code != rlerr.CodeExternal {
		t.Fatalf("exit code = %v (%v), want external", code, err)
	}
	if !strings.Contains(err.Error(), "exited 3") || rlerr.Fields(err)["exit_code"] != "3" {
		t.Errorf("the error does not name the program's code: %v %v", err, rlerr.Fields(err))
	}
	var env struct {
		Data struct {
			ExitCode int               `json:"exit_code"`
			Sites    map[string]string `json:"sites"`
		} `json:"data"`
	}
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out.String())
	}
	if dec.More() {
		t.Error("stdout holds more than one object")
	}
	if env.Data.ExitCode != 3 || env.Data.Sites[ptDomain] != ptUnit {
		t.Errorf("envelope data = %+v", env.Data)
	}
}

// ── journalctl ───────────────────────────────────────────────────────────────

func TestCheckJournalctlArgs(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		follow bool
	}{
		{nil, false},
		{[]string{"-n", "50"}, false},
		{[]string{"-n50"}, false},
		{[]string{"-f"}, true},
		{[]string{"--follow"}, true},
		{[]string{"--fol"}, true},
		{[]string{"-fn50"}, true},
		{[]string{"-n", "20", "-f"}, true},
		{[]string{"--since=-1h", "-p", "err"}, false},
		{[]string{"-S", "-1h"}, false},
		{[]string{"--grep=-u"}, false},
		{[]string{"-gfoo-u"}, false},
		{[]string{"-o", "json"}, false},
		{[]string{"--cursor=s=abc"}, false},
		{[]string{"--user"}, false},
		{[]string{"--no-pager", "-r", "-x", "-e"}, false},
	} {
		follow, err := checkJournalctlArgs(tc.args)
		if err != nil {
			t.Errorf("checkJournalctlArgs(%q) = %v, want accepted", tc.args, err)
			continue
		}
		if follow != tc.follow {
			t.Errorf("checkJournalctlArgs(%q) follow = %v, want %v", tc.args, follow, tc.follow)
		}
	}
}

func TestCheckJournalctlArgsRefusesWritesRedirectsAndScope(t *testing.T) {
	for _, args := range [][]string{
		{"--vacuum-size=1G"},
		{"--vacuum-size", "1G"},
		{"--vacuum-time=1d"},
		{"--vacuum-files=2"},
		{"--rotate"},
		{"--rot"},
		{"--flush"},
		{"--sync"},
		{"--relinquish-var"},
		{"--smart-relinquish-var"},
		{"--setup-keys"},
		{"--update-catalog"},
		{"--cursor-file=/tmp/c"},
		{"--directory=/tmp"},
		{"--directory", "/tmp"},
		{"--dir=/tmp"},
		{"-D", "/tmp"},
		{"-D/tmp"},
		{"--file=/tmp/x.journal"},
		{"-i", "/tmp/x.journal"},
		{"--root=/mnt"},
		{"--image=/tmp/x.img"},
		{"--namespace=other"},
		{"--namespace", "other"},
		{"--names=other"},
		{"--machine=box"},
		{"-M", "box"},
		{"-m"},
		{"--merge"},
		{"-u", "sshd.service"},
		{"--unit=sshd.service"},
		{"--unit", "sshd.service"},
		{"--user-unit=x"},
		{"-fu", "x"},
		{"-n", "5", "-m"},
	} {
		_, err := checkJournalctlArgs(args)
		wantUsage(t, err, "checkJournalctlArgs("+strings.Join(args, " ")+")")
	}
}

func TestJournalctlReadsASiteFromItsNamespace(t *testing.T) {
	fake := systest.NewFakeRunner()
	g, _ := passthroughGlobals(t, fake, false)

	if err := g.runJournalctl(context.Background(), ptDomain, []string{"-n", "50"}); err != nil {
		t.Fatalf("runJournalctl = %v", err)
	}
	want := "journalctl -u " + ptUnit + " --namespace=+" + ptSlug + " --no-pager -n 50"
	if got := callKeys(fake); len(got) != 1 || got[0] != want {
		t.Errorf("calls = %q, want %q", got, want)
	}
	if calls := fake.Calls(); len(calls) == 1 && calls[0].Mutates {
		t.Error("journalctl ran as a mutation")
	}
	if g.lock != nil {
		t.Error("a journal read took the server lock")
	}
}

func TestJournalctlReadsAUnitAsNamed(t *testing.T) {
	fake := systest.NewFakeRunner()
	g, _ := passthroughGlobals(t, fake, false)
	ctx := context.Background()

	if err := g.runJournalctl(ctx, "nginx.service", nil); err != nil {
		t.Fatalf("runJournalctl(nginx.service) = %v", err)
	}
	// A site's unit named directly still gets its namespace, from the unit file.
	if err := g.runJournalctl(ctx, ptUnit, nil); err != nil {
		t.Fatalf("runJournalctl(%s) = %v", ptUnit, err)
	}
	got := callKeys(fake)
	want := []string{
		"journalctl -u nginx.service --no-pager",
		"journalctl -u " + ptUnit + " --namespace=+" + ptSlug + " --no-pager",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestJournalctlRefusesTargetsItCannotRead(t *testing.T) {
	fake := systest.NewFakeRunner()
	g, _ := passthroughGlobals(t, fake, false)
	ctx := context.Background()

	for _, target := range []string{
		ptStatic,          // static: no unit, no journal
		"app.exmaple.com", // a typo of a site, which would read an empty journal
		"ratline-*",       // a pattern
		"-x",
		"a/b.service",
	} {
		wantUsage(t, g.runJournalctl(ctx, target, nil), "runJournalctl("+target+")")
	}
	// A refused argument stops it before anything runs.
	wantUsage(t, g.runJournalctl(ctx, ptDomain, []string{"--vacuum-size=1G"}), "runJournalctl --vacuum-size")
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("a refused journalctl ran %q", callKeys(fake))
	}
}

// --follow has to run until interrupted: no two-minute default timeout, the output
// streamed, and Ctrl-C an ending rather than a failure.
func TestJournalctlFollowStreamsWithoutATimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := systest.NewFakeRunner()
	fake.Hook = func(c system.Cmd) (*system.Result, error) {
		if c.Timeout < 24*time.Hour*365 {
			t.Errorf("a follow ran with a %s timeout", c.Timeout)
		}
		if c.Stdout == nil {
			t.Error("a follow is not streamed")
		} else {
			_, _ = c.Stdout.Write([]byte("a line\n"))
		}
		cancel()
		return &system.Result{Path: "/usr/bin/journalctl", Args: c.Args, ExitCode: 143},
			rlerr.Externalf("journalctl was interrupted")
	}
	g, out := passthroughGlobals(t, fake, false)

	if err := g.runJournalctl(ctx, ptDomain, []string{"-f"}); err != nil {
		t.Errorf("an interrupted follow = %v, want nil", err)
	}
	if !strings.Contains(out.String(), "a line") {
		t.Errorf("the followed output did not reach stdout: %q", out.String())
	}
}

func TestJournalctlFollowIsRefusedUnderJSON(t *testing.T) {
	fake := systest.NewFakeRunner()
	g, _ := passthroughGlobals(t, fake, false)
	g.JSON = true
	wantUsage(t, g.runJournalctl(context.Background(), ptDomain, []string{"-f"}), "journalctl -f --json")
	if len(fake.Calls()) != 0 {
		t.Error("journalctl ran")
	}
}

// ── the shared edges ─────────────────────────────────────────────────────────

// `ratline nginx -t` without the -- is the commonest mistake, and cobra reads -t as
// ratline's. The hint has to show this command's spelling, not site exec's.
func TestPassthroughFlagErrorPointsAtItsOwnDoubleDash(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"nginx", "-t"}, "ratline nginx -- -t"},
		{[]string{"journalctl", ptDomain, "-n", "5"}, "ratline journalctl app.example.com --"},
		{[]string{"systemctl", "list-units", "--failed"}, "ratline systemctl -- list-units --failed"},
	} {
		g := NewGlobals()
		g.Stdout, g.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
		g.Log = log.Discard()
		root := NewRootCommand(g)
		root.SetArgs(tc.args)
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		err := root.Execute()
		if err == nil {
			t.Errorf("%q: an unknown flag was accepted", tc.args)
			continue
		}
		if hint := rlerr.Hint(err); !strings.Contains(hint, tc.want) {
			t.Errorf("%q: hint = %q, want it to show %q", tc.args, hint, tc.want)
		}
	}
}

func TestPassthroughCommandsAreDeclaredAsTheyBehave(t *testing.T) {
	root := NewRootCommand(NewGlobals())
	for name, want := range map[string][2]bool{
		// {Mutating, SkipLock}: the two that can change something declare it and take
		// the lock themselves; the reader declares neither.
		"nginx":      {true, true},
		"systemctl":  {true, true},
		"journalctl": {false, false},
	} {
		cmd, _, err := root.Find([]string{name})
		if err != nil || cmd.Name() != name {
			t.Fatalf("no %s command: %v", name, err)
		}
		if got := [2]bool{annotated(cmd, AnnoMutates), annotated(cmd, AnnoSkipLock)}; got != want {
			t.Errorf("%s: mutating/skiplock = %v, want %v", name, got, want)
		}
		if annotated(cmd, AnnoAllowNonRoot) {
			t.Errorf("%s runs without root", name)
		}
	}
}
