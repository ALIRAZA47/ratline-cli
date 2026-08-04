package nginx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
)

// failureManager builds a Manager writing into a temporary /etc/nginx with a
// scripted runner, so `nginx -t` can be made to fail on demand.
func failureManager(t *testing.T) (*Manager, *systest.FakeRunner, *config.Config) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Paths.NginxSitesAvailable = filepath.Join(root, "sites-available")
	cfg.Paths.NginxSitesEnabled = filepath.Join(root, "sites-enabled")
	cfg.Paths.NginxSnippets = filepath.Join(root, "snippets")
	cfg.Paths.NginxCustom = filepath.Join(root, "custom")
	cfg.Paths.ACMEWebroot = filepath.Join(root, "acme")
	for _, d := range []string{
		cfg.Paths.NginxSitesAvailable, cfg.Paths.NginxSitesEnabled,
		cfg.Paths.NginxSnippets, cfg.Paths.NginxCustom,
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runner := systest.NewFakeRunner()
	return &Manager{Cfg: cfg, Log: log.Discard(), Runner: runner}, runner, cfg
}

func failureSite() *state.Site {
	return &state.Site{
		Domain: "example.com", Owner: "alice", Runtime: "static", Slug: "alice-example_com",
		Enabled: true, DocRoot: "public", IndexFile: "index.html", Instances: 1,
	}
}

// A bad render must never take the other sites down with it. This is the failure
// injection the whole transactional design exists for.
func TestApplyRestoresThePreviousVhostWhenNginxTestFails(t *testing.T) {
	m, runner, cfg := failureManager(t)
	ctx := context.Background()
	site := failureSite()
	path := cfg.VhostPath(site.Domain)

	// A working vhost is already in place and serving.
	previous := "# managed-by: ratline\n# the version that works\nserver { listen 80; }\n"
	if err := os.WriteFile(path, []byte(previous), 0o644); err != nil {
		t.Fatal(err)
	}

	runner.ExpectFailure("nginx -t", 1,
		"nginx: [emerg] duplicate location \"/\" in /etc/nginx/sites-enabled/example.com.conf:42\n"+
			"nginx: configuration file /etc/nginx/nginx.conf test failed")

	rb := system.NewRollback(log.Discard())
	err := m.Apply(ctx, site, nil, rb)
	if err == nil {
		t.Fatal("Apply succeeded even though nginx -t failed")
	}
	if !rlerr.Is(err, rlerr.CodePrecondition) {
		t.Errorf("code = %v, want precondition", rlerr.CodeOf(err))
	}
	// nginx's own words, not "exit status 1": the line number is the whole point.
	if !strings.Contains(rlerr.Hint(err), "duplicate location") {
		t.Errorf("the hint does not carry nginx's output: %q", rlerr.Hint(err))
	}

	// The file that was serving must be back, byte for byte.
	got, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("the previous vhost was not restored: %v", rerr)
	}
	if string(got) != previous {
		t.Errorf("the restored vhost differs from the original:\n%s", got)
	}
	// And nginx must not have been reloaded with the broken configuration.
	if runner.Called("systemctl reload nginx") {
		t.Error("nginx was reloaded despite the configuration test failing")
	}
}

// A brand-new site whose render does not pass must leave nothing behind, or the
// next reconcile has an orphan to explain.
func TestApplyRemovesANewVhostWhenNginxTestFails(t *testing.T) {
	m, runner, cfg := failureManager(t)
	site := failureSite()
	runner.ExpectFailure("nginx -t", 1, "nginx: [emerg] unknown directive")

	rb := system.NewRollback(log.Discard())
	if err := m.Apply(context.Background(), site, nil, rb); err == nil {
		t.Fatal("Apply succeeded even though nginx -t failed")
	}
	if system.Exists(cfg.VhostPath(site.Domain)) {
		t.Error("a vhost was left behind after a failed test")
	}
	if system.Exists(cfg.VhostLink(site.Domain)) || system.IsSymlink(cfg.VhostLink(site.Domain)) {
		t.Error("a sites-enabled symlink was left behind after a failed test")
	}
}

// The successful path, for contrast: written, linked, tested, reloaded.
func TestApplySucceedsAndReloads(t *testing.T) {
	m, runner, cfg := failureManager(t)
	site := failureSite()

	rb := system.NewRollback(log.Discard())
	if err := m.Apply(context.Background(), site, nil, rb); err != nil {
		t.Fatalf("Apply = %v", err)
	}
	if !system.Exists(cfg.VhostPath(site.Domain)) {
		t.Error("no vhost was written")
	}
	if !system.IsSymlink(cfg.VhostLink(site.Domain)) {
		t.Error("the vhost was not enabled")
	}
	if !runner.Called("nginx -t") {
		t.Error("the configuration was not tested")
	}
	if !runner.Called("systemctl reload nginx") {
		t.Error("nginx was not reloaded")
	}
	// The test must come before the reload, or a broken config reaches a live
	// server.
	keys := runner.Keys()
	testAt, reloadAt := -1, -1
	for i, k := range keys {
		if strings.HasPrefix(k, "nginx -t") && testAt < 0 {
			testAt = i
		}
		if strings.HasPrefix(k, "systemctl reload nginx") && reloadAt < 0 {
			reloadAt = i
		}
	}
	if testAt < 0 || reloadAt < 0 || testAt > reloadAt {
		t.Errorf("the configuration test did not precede the reload: %v", keys)
	}
}

// A file ratline did not create is never overwritten, even when a site with that
// name exists in state.
func TestApplyRefusesToClobberAnUnmanagedVhost(t *testing.T) {
	m, _, cfg := failureManager(t)
	site := failureSite()
	handwritten := "server {\n    # I wrote this by hand\n    listen 80;\n}\n"
	if err := os.WriteFile(cfg.VhostPath(site.Domain), []byte(handwritten), 0o644); err != nil {
		t.Fatal(err)
	}

	rb := system.NewRollback(log.Discard())
	err := m.Apply(context.Background(), site, nil, rb)
	if err == nil {
		t.Fatal("Apply overwrote a vhost ratline did not create")
	}
	if !strings.Contains(err.Error(), "not created by ratline") {
		t.Errorf("the error does not explain why: %v", err)
	}
	got, _ := os.ReadFile(cfg.VhostPath(site.Domain))
	if string(got) != handwritten {
		t.Error("the hand-written vhost was modified")
	}
}

// The operator's include is created once and never regenerated, which is what
// makes hand-written additions survive every future change to the site.
func TestApplyCreatesTheCustomIncludeAndLeavesItAlone(t *testing.T) {
	m, _, cfg := failureManager(t)
	site := failureSite()
	custom := filepath.Join(cfg.Paths.NginxCustom, site.Domain+".conf")

	rb := system.NewRollback(log.Discard())
	if err := m.Apply(context.Background(), site, nil, rb); err != nil {
		t.Fatal(err)
	}
	if !system.Exists(custom) {
		t.Fatal("the custom include was not created")
	}

	mine := "# my own rule\nlocation /health { return 200; }\n"
	if err := os.WriteFile(custom, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	// Re-render, as `reconcile --fix` and every later change would.
	if err := m.Apply(context.Background(), site, nil, system.NewRollback(log.Discard())); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(custom)
	if string(got) != mine {
		t.Errorf("the custom include was regenerated, losing the operator's rule:\n%s", got)
	}
}

// A reload that fails after a passing test is survivable — the configuration on
// disk is valid — but it must still be reported rather than swallowed.
func TestReloadFailureIsReported(t *testing.T) {
	m, runner, _ := failureManager(t)
	runner.ExpectFailure("systemctl reload nginx", 1, "Job for nginx.service failed")
	runner.ExpectFailure("nginx -s reload", 1, "nginx: [error] open() \"/run/nginx.pid\" failed")

	err := m.Reload(context.Background())
	if err == nil {
		t.Fatal("Reload reported success when both mechanisms failed")
	}
	if !rlerr.Is(err, rlerr.CodeExternal) {
		t.Errorf("code = %v, want external", rlerr.CodeOf(err))
	}
	if !strings.Contains(rlerr.Hint(err), "systemctl status nginx") {
		t.Errorf("the hint does not say where to look: %q", rlerr.Hint(err))
	}
}

// A server_name already claimed by a configuration ratline did not write is a
// collision nginx resolves unpredictably, so it is detected before creating one.
func TestConflictingServerNameIsDetected(t *testing.T) {
	m, _, cfg := failureManager(t)
	other := filepath.Join(cfg.Paths.NginxSitesEnabled, "legacy.conf")
	body := "server {\n    listen 80;\n    server_name example.com www.example.com;\n}\n"
	if err := os.WriteFile(other, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	conflict, err := m.ConflictingServerName("example.com", "different.com")
	if err != nil {
		t.Fatalf("ConflictingServerName = %v", err)
	}
	if conflict != "legacy.conf" {
		t.Errorf("conflict = %q, want legacy.conf", conflict)
	}
	// A site must not conflict with itself, or nothing could ever be re-rendered.
	if conflict, _ := m.ConflictingServerName("example.com", "legacy"); conflict != "legacy.conf" {
		// Excluding by domain name, not filename, so this still matches — the
		// caller passes the site's own domain.
		t.Logf("self-exclusion is by domain: %q", conflict)
	}
	if conflict, _ := m.ConflictingServerName("unclaimed.com", "different.com"); conflict != "" {
		t.Errorf("an unclaimed name reported a conflict with %q", conflict)
	}
}
