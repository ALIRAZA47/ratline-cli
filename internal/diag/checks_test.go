package diag

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/nginx"
	"github.com/ALIRAZA47/ratline-cli/internal/site"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/unit"
)

// stubRunner answers nothing, so a check that shells out gets a failure it has to
// handle rather than a panic.
type stubRunner struct{}

func (stubRunner) Run(context.Context, system.Cmd) (*system.Result, error) {
	return &system.Result{}, nil
}

func testEnv(t *testing.T) *Env {
	t.Helper()
	cfg := config.Default()
	lg := log.Discard()
	runner := stubRunner{}
	return &Env{
		Cfg:    cfg,
		Log:    lg,
		Runner: runner,
		Bins:   system.NewBinaries(),
		OS:     system.DetectOS(),
		// The probes are real network calls, so the tests would otherwise spend
		// half a minute waiting for connections to nothing — and would depend on
		// the machine having working DNS to pass. A tiny timeout exercises the
		// same code paths and reaches the same verdicts.
		ProbeTimeout: time.Millisecond,
		Resolver:     offlineResolver(),
		Nginx:        &nginx.Manager{Cfg: cfg, Log: lg, Runner: runner, DryRun: true},
		Unit:         &unit.Manager{Cfg: cfg, Log: lg, Runner: runner, DryRun: true},
		Site:         &site.Manager{Cfg: cfg, Log: lg, Runner: runner, DryRun: true},
	}
}

// offlineResolver answers nothing, so the DNS check reaches its failure branch
// without the test depending on a network or on what a real lookup returns.
func offlineResolver() *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return nil, &net.DNSError{Err: "no resolver in tests", IsNotFound: true}
		},
	}
}

func nodeSite() *state.Site {
	return &state.Site{
		Domain: "app.example.com", Owner: "acme", Runtime: "node",
		Slug: "acme-app_example_com", Enabled: true, Entry: "server.js",
		Listen: "socket", Instances: 1,
	}
}

func staticSite() *state.Site {
	return &state.Site{
		Domain: "www.example.com", Owner: "acme", Runtime: "static",
		Slug: "acme-www_example_com", Enabled: true, DocRoot: "public",
		IndexFile: "index.html",
	}
}

// allCheckLists is every list the tool can run, so a new subject cannot be added
// without the invariants below applying to it.
func allCheckLists(t *testing.T) map[string][]Check {
	t.Helper()
	env := testEnv(t)
	return map[string][]Check{
		"site/node":   SiteChecks(env, nodeSite()),
		"site/static": SiteChecks(env, staticSite()),
		"site/port": SiteChecks(env, func() *state.Site {
			s := nodeSite()
			s.Listen, s.Port = "port", 20001
			return s
		}()),
		"site/python": SiteChecks(env, &state.Site{
			Domain: "api.example.com", Owner: "acme", Runtime: "python",
			Slug: "acme-api_example_com", Enabled: true, AppModule: "app.main:app",
			Listen: "socket", Instances: 1,
		}),
		"user": UserChecks(env, &state.User{
			Name: "acme", Home: "/home/acme", Shell: "/bin/bash",
		}),
		"key": KeyChecks(env, &state.Key{
			ID: "k_1", Fingerprint: "SHA256:abc", Algorithm: "ssh-ed25519",
			Scope: "user", Owner: "acme", Blob: "AAAAC3NzaC1lZDI1NTE5",
		}),
		"cert": CertChecks(env, &state.Certificate{
			Name: "app.example.com", Source: state.CertSourceLetsEncrypt,
			CertPath: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
			KeyPath:  "/etc/letsencrypt/live/app.example.com/privkey.pem",
			Attached: []string{"app.example.com"},
		}),
		"nginx":  NginxChecks(env),
		"ssh":    SSHChecks(env),
		"server": ServerChecks(env),
	}
}

func TestEveryCheckListIsWellFormed(t *testing.T) {
	// A Needs entry naming a check that does not exist, or one that runs later,
	// would silently skip everything downstream — producing a diagnosis that looked
	// complete and was not. That is the one bug a diagnostic tool must not have, and
	// it is a property of the code rather than of any particular server.
	for name, checks := range allCheckLists(t) {
		t.Run(name, func(t *testing.T) {
			if err := Validate(checks); err != nil {
				t.Error(err)
			}
			if len(checks) == 0 {
				t.Error("an empty check list would report a healthy subject unconditionally")
			}
		})
	}
}

func TestEveryCheckHasAHumanTitle(t *testing.T) {
	// The title is the whole of the row an operator reads, so a missing one is an
	// unreadable line rather than a cosmetic issue.
	for name, checks := range allCheckLists(t) {
		for _, c := range checks {
			if strings.TrimSpace(c.Title) == "" {
				t.Errorf("%s: check %q has no title", name, c.ID)
			}
			// Phrased as the thing that should be true, so the row reads correctly
			// beside both "ok" and "FAIL".
			if strings.HasSuffix(c.Title, "?") {
				t.Errorf("%s: %q is phrased as a question; state what should be true",
					name, c.Title)
			}
			// Lower-case, so the row reads as a list item — unless the first word is
			// an acronym, where "SSH access" is correct and "ssh access" is not.
			first, _, _ := strings.Cut(c.Title, " ")
			acronym := first == strings.ToUpper(first) && len(first) > 1
			if !acronym && c.Title != strings.ToLower(c.Title[:1])+c.Title[1:] {
				t.Errorf("%s: %q should start lower-case to read as a list item", name, c.Title)
			}
		}
	}
}

func TestCheckIDsAreStableSlugs(t *testing.T) {
	// JSON consumers match on the ID, so it is part of the interface.
	for name, checks := range allCheckLists(t) {
		for _, c := range checks {
			if c.ID != strings.ToLower(c.ID) || strings.ContainsAny(c.ID, " _.") {
				t.Errorf("%s: %q should be a lower-case, hyphenated slug", name, c.ID)
			}
		}
	}
}

// runList executes a list against whatever this machine happens to be, which is a
// host with none of ratline's paths present — so nearly everything fails or skips.
// That is the useful case: it is the shape of a badly broken server, and the
// invariants below have to hold there above all.
func runList(t *testing.T, kind string, checks []Check) *Report {
	t.Helper()
	return Run(context.Background(), kind, "subject", "", checks)
}

func TestEveryFailureAndWarningOffersAWayForward(t *testing.T) {
	for name, checks := range allCheckLists(t) {
		t.Run(name, func(t *testing.T) {
			r := runList(t, name, checks)
			for _, s := range r.Steps {
				switch s.Verdict {
				case Failed:
					// A diagnosis with no next step leaves the operator exactly where
					// they started, which is the failure mode this whole command exists
					// to avoid.
					if s.Fix == "" {
						t.Errorf("%s failed with no fix: %q", s.ID, s.Detail)
					}
					if s.Detail == "" {
						t.Errorf("%s failed with no detail", s.ID)
					}
				case Warning:
					if s.Detail == "" {
						t.Errorf("%s warned with no detail", s.ID)
					}
				case Skipped:
					// A bare "skipped" tells the reader nothing about whether it
					// mattered.
					if s.Detail == "" {
						t.Errorf("%s was skipped with no reason", s.ID)
					}
				}
			}
		})
	}
}

func TestNoCheckPanicsOnAHostWithNothingInstalled(t *testing.T) {
	// Every check runs against paths that do not exist, binaries that are absent and
	// a nil state store. A diagnostic that crashes on a broken server is worse than
	// no diagnostic, because that is precisely when it is run.
	for name, checks := range allCheckLists(t) {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("panicked: %v", p)
				}
			}()
			r := runList(t, name, checks)
			if len(r.Steps) != len(checks) {
				t.Errorf("%d steps for %d checks — one did not report", len(r.Steps), len(checks))
			}
		})
	}
}

func TestASubjectWithAFailureAlwaysNamesACause(t *testing.T) {
	// The headline is the product. A report that counts failures and cannot say what
	// the first one was would be a list again.
	for name, checks := range allCheckLists(t) {
		r := runList(t, name, checks)
		if r.Failed > 0 && r.Cause == "" {
			t.Errorf("%s: %d failures but no cause named", name, r.Failed)
		}
		if r.Failed == 0 && r.Cause != "" {
			t.Errorf("%s: a cause was named with nothing failing: %q", name, r.Cause)
		}
	}
}

func TestSiteChecksDifferByRuntimeButShareTheEntryAndExit(t *testing.T) {
	env := testEnv(t)
	static := ids(SiteChecks(env, staticSite()))
	dynamic := ids(SiteChecks(env, nodeSite()))

	// A static site has no process, so asking about a unit or a socket would produce
	// a permanently skipped row for something that does not exist.
	for _, absent := range []string{"unit", "workers", "listening", "app-answers"} {
		if static[absent] {
			t.Errorf("a static site should not be checked for %q", absent)
		}
		if !dynamic[absent] {
			t.Errorf("a dynamic site should be checked for %q", absent)
		}
	}
	// A dynamic site has no document root ratline serves directly.
	if dynamic["docroot"] {
		t.Error("a dynamic site should not be checked for a document root")
	}
	if !static["docroot"] {
		t.Error("a static site's document root is the whole of it")
	}
	// Both are reached through nginx, secured by a certificate and named in DNS.
	for _, shared := range []string{"enabled", "vhost", "nginx-config", "served", "certificate", "dns"} {
		if !static[shared] || !dynamic[shared] {
			t.Errorf("%q should apply to every runtime", shared)
		}
	}
}

func TestTheSocketPermissionFailureNamesTheTopicThatExplainsIt(t *testing.T) {
	// The silent 502 is the failure people cannot diagnose unaided, so its row has to
	// carry both the fix and the page that explains why write permission is what
	// connect(2) needs.
	env := testEnv(t)
	var found bool
	for _, c := range SiteChecks(env, nodeSite()) {
		if c.ID != "listening" {
			continue
		}
		found = true
		res := c.Run(context.Background())
		// On this machine the socket does not exist, which is the first branch — still
		// a failure, and it must carry the topic.
		if res.Verdict != Failed {
			t.Fatalf("with no socket present, want a failure, got %q", res.Verdict)
		}
		if res.Topic != "sockets" {
			t.Errorf("topic = %q, want sockets", res.Topic)
		}
		if res.Fix == "" {
			t.Error("no fix offered")
		}
	}
	if !found {
		t.Fatal("there is no 'listening' check for a socket site")
	}
}

func TestTheLockoutGuardIsTheLastSSHCheck(t *testing.T) {
	// It has to run after everything it depends on, and it has to be the answer an
	// operator is left with — because acting on any earlier warning while no key can
	// reach the server is how a remote box becomes console-only.
	checks := SSHChecks(testEnv(t))
	if last := checks[len(checks)-1].ID; last != "lockout-guard" {
		t.Errorf("the last SSH check is %q, want lockout-guard", last)
	}
}

func TestTheServedCheckIsTheLastCertificateCheck(t *testing.T) {
	// A certificate can be present, valid, current and attached and still not be the
	// one nginx is serving. Everything before this check explains why the handshake
	// would fail, so it comes last.
	checks := CertChecks(testEnv(t), &state.Certificate{
		Name: "a.example.com", Attached: []string{"a.example.com"},
	})
	if last := checks[len(checks)-1].ID; last != "served" {
		t.Errorf("the last certificate check is %q, want served", last)
	}
}

func ids(checks []Check) map[string]bool {
	out := make(map[string]bool, len(checks))
	for _, c := range checks {
		out[c.ID] = true
	}
	return out
}
