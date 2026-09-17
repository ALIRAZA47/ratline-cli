package cli

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
)

// A tenant's `site logs` resolves a site from their own home: the directory has to exist
// and be theirs, because only root makes directories in a tenant's home. Everything else
// is read from the unit file, which is what systemd reads too.
func TestATenantResolvesOnlyTheSitesInTheirOwnHome(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skip("no current user")
	}
	cfg := config.Default()
	cfg.Paths.HomeBase = t.TempDir()
	cfg.Paths.SystemdDir = t.TempDir()
	cfg.Paths.NginxLogDir = t.TempDir()
	g := &Globals{Cfg: cfg, Log: log.Discard(), Invoker: system.Invoker{UID: os.Getuid(), Name: me.Username}}

	if _, err := g.tenantLogTarget("api.example.com"); err == nil || !strings.Contains(err.Error(), "belongs") {
		t.Fatalf("a site with no directory resolved, or was refused for the wrong reason: %v", err)
	}
	if _, err := g.tenantLogTarget("not a domain"); err == nil {
		t.Error("an invalid domain was accepted")
	}

	siteDir := cfg.SiteDir(me.Username, "api.example.com")
	if err := os.MkdirAll(siteDir, 0o750); err != nil {
		t.Fatal(err)
	}
	tgt, err := g.tenantLogTarget("api.example.com")
	if err != nil {
		t.Fatalf("tenantLogTarget = %v", err)
	}
	if tgt.Dynamic || tgt.AppLogIsFile {
		t.Errorf("a site with no unit was read as dynamic=%v file=%v", tgt.Dynamic, tgt.AppLogIsFile)
	}
	if tgt.Owner != me.Username || tgt.UnitName != "ratline-"+strings.ToLower(me.Username)+"-api_example_com.service" {
		t.Errorf("owner=%q unit=%q", tgt.Owner, tgt.UnitName)
	}
	if want := filepath.Join(cfg.Paths.NginxLogDir, strings.ToLower(me.Username)+"-api_example_com", "access.log"); tgt.Paths["access"] != want {
		t.Errorf("access log = %q, want %q", tgt.Paths["access"], want)
	}
	if want := filepath.Join(siteDir, "logs", "app.log"); tgt.Paths["app"] != want {
		t.Errorf("app log = %q, want %q", tgt.Paths["app"], want)
	}

	// The unit on disk says whether there is a service and how it is supervised.
	unitPath := cfg.UnitPath(me.Username, "api.example.com")
	if err := os.WriteFile(unitPath, []byte("[Service]\nType=exec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if tgt, err = g.tenantLogTarget("api.example.com"); err != nil || !tgt.Dynamic || tgt.AppLogIsFile {
		t.Errorf("a direct service: err=%v dynamic=%v file=%v", err, tgt.Dynamic, tgt.AppLogIsFile)
	}
	if err := os.WriteFile(unitPath, []byte("[Service]\nType=forking\nPIDFile=/x/pm2.pid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if tgt, err = g.tenantLogTarget("api.example.com"); err != nil || !tgt.Dynamic || !tgt.AppLogIsFile {
		t.Errorf("a PM2 service: err=%v dynamic=%v file=%v", err, tgt.Dynamic, tgt.AppLogIsFile)
	}
	// gunicorn writes its own log to logs/app.log and captures stdout into it, and says so
	// in the ExecStart; a comment mentioning the file is not the same thing.
	gunicorn := "[Service]\nType=exec\n# the log is logs/app.log\nExecStart=/usr/local/lib/ratline/ratline-shell exec --env-file " +
		siteDir + "/.env -- " + siteDir + "/venv/bin/gunicorn --error-logfile " + siteDir + "/logs/app.log --capture-output app:app\n"
	if err := os.WriteFile(unitPath, []byte(gunicorn), 0o644); err != nil {
		t.Fatal(err)
	}
	if tgt, err = g.tenantLogTarget("api.example.com"); err != nil || !tgt.Dynamic || !tgt.AppLogIsFile {
		t.Errorf("a gunicorn service: err=%v dynamic=%v file=%v", err, tgt.Dynamic, tgt.AppLogIsFile)
	}
	if unitCapturesAppLog("[Service]\n# ExecStart once named /logs/app.log\nExecStart=/x/node server.js\n") {
		t.Error("a comment about the log file was read as capturing to it")
	}

	// A symlink where the site directory should be is not a site of theirs, wherever it
	// points — root never makes one, so a tenant did.
	if err := os.Symlink(siteDir, cfg.SiteDir(me.Username, "other.example.com")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.tenantLogTarget("other.example.com"); err == nil {
		t.Error("a symlinked site directory was accepted")
	}
}

// The command is open to a tenant, and so is its top-level alias; both are the same
// command, so this holds by construction, but it is the property everything above
// depends on.
func TestSiteLogsDoesNotRequireRoot(t *testing.T) {
	for _, cmd := range []string{"site logs", "logs"} {
		c := findCommand(t, cmd)
		if !annotated(c, AnnoAllowNonRoot) {
			t.Errorf("%q requires root; a tenant cannot read their own logs", cmd)
		}
	}
}

// findCommand walks the real command tree to the command at a space-separated path.
func findCommand(t *testing.T, path string) *cobra.Command {
	t.Helper()
	cur := NewRootCommand(&Globals{})
	for _, name := range strings.Fields(path) {
		var next *cobra.Command
		for _, c := range cur.Commands() {
			if c.Name() == name {
				next = c
				break
			}
		}
		if next == nil {
			t.Fatalf("no command %q under %q", name, cur.CommandPath())
		}
		cur = next
	}
	return cur
}
