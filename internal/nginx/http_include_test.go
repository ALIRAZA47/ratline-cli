package nginx

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/config"
	"github.com/ALIRAZA47/ratline-cli/internal/log"
)

// The hint the old code printed. It told an operator to hand-edit
// /etc/nginx/nginx.conf — a file ratline does not own and whose whole design
// forbids editing by hand — for a condition `site add` resolves by itself.
const handEditHint = "inside the http block of"

// includeFixture builds an nginx.conf that globs conf.d the way the stock
// Debian and Ubuntu package does, plus the snippet the vhosts include.
func includeFixture(t *testing.T) (m *Manager, buf *bytes.Buffer, httpPath, confPath, confD string) {
	t.Helper()
	root := t.TempDir()
	confD = filepath.Join(root, "conf.d")
	if err := os.MkdirAll(confD, 0o755); err != nil {
		t.Fatal(err)
	}
	confPath = filepath.Join(root, "nginx.conf")
	body := "http {\n    include " + confD + "/*.conf;\n}\n"
	if err := os.WriteFile(confPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	httpPath = filepath.Join(root, "ratline", "ratline-http.conf")
	if err := os.MkdirAll(filepath.Dir(httpPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(httpPath, []byte("map $http_upgrade $connection_upgrade {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf = &bytes.Buffer{}
	m = &Manager{Cfg: config.Default(), Log: log.New(log.Options{Out: buf, Level: log.LevelDebug})}
	return m, buf, httpPath, confPath, confD
}

// A rehearsal must not create the symlink, and must not tell the operator to go
// and edit nginx.conf — `site add` places the link itself on the real run.
func TestCheckHTTPIncludeDryRunNeitherLinksNorTellsYouToEditNginxConf(t *testing.T) {
	m, buf, httpPath, confPath, confD := includeFixture(t)
	m.DryRun = true

	if err := m.checkHTTPIncludeIn(httpPath, confPath, confD); err != nil {
		t.Fatalf("checkHTTPIncludeIn() = %v, want nil", err)
	}

	link := filepath.Join(confD, "ratline-http.conf")
	if _, err := os.Lstat(link); err == nil {
		t.Errorf("the dry run created %s; --dry-run must write nothing at any layer", link)
	}
	if out := buf.String(); strings.Contains(out, handEditHint) {
		t.Errorf("the dry run told the operator to hand-edit nginx.conf, for something the real\n"+
			"run fixes by itself. ratline owns no part of nginx.conf and must never ask.\nlog was: %s", out)
	}
}

// The real run links it, which is what makes the dry run's silence correct.
func TestCheckHTTPIncludeLinksOnTheRealRun(t *testing.T) {
	m, buf, httpPath, confPath, confD := includeFixture(t)

	if err := m.checkHTTPIncludeIn(httpPath, confPath, confD); err != nil {
		t.Fatalf("checkHTTPIncludeIn() = %v, want nil", err)
	}

	link := filepath.Join(confD, "ratline-http.conf")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("the real run did not link the snippet into conf.d: %v", err)
	}
	if target != httpPath {
		t.Errorf("link target = %q, want %q", target, httpPath)
	}
	if out := buf.String(); strings.Contains(out, handEditHint) {
		t.Errorf("warned despite having linked the snippet successfully; log was: %s", out)
	}
}

// Once linked, every later call — including a rehearsal on a provisioned box —
// has to stay quiet. The old code grepped nginx.conf for the snippet path, which
// a glob over conf.d never contains, so it warned on every single run.
func TestCheckHTTPIncludeSilentWhenAlreadyLinked(t *testing.T) {
	for _, dry := range []bool{false, true} {
		m, buf, httpPath, confPath, confD := includeFixture(t)
		m.DryRun = dry
		link := filepath.Join(confD, "ratline-http.conf")
		if err := os.Symlink(httpPath, link); err != nil {
			t.Fatal(err)
		}

		if err := m.checkHTTPIncludeIn(httpPath, confPath, confD); err != nil {
			t.Fatalf("dry=%v: checkHTTPIncludeIn() = %v, want nil", dry, err)
		}
		if out := buf.String(); strings.Contains(out, handEditHint) {
			t.Errorf("dry=%v: warned about a snippet that is already linked and loaded; log was: %s", dry, out)
		}
	}
}

// When conf.d exists but nginx.conf does not glob it, a symlink there loads
// nothing — so the warning is the correct answer and must still appear. The old
// code tested only that conf.d was a directory, wrote the link, returned nil,
// and left the snippet unloaded with no warning at all.
func TestCheckHTTPIncludeWarnsWhenConfDIsNotIncluded(t *testing.T) {
	m, buf, httpPath, confPath, confD := includeFixture(t)
	if err := os.WriteFile(confPath, []byte("http {\n    # nothing is included here\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.checkHTTPIncludeIn(httpPath, confPath, confD); err != nil {
		t.Fatalf("checkHTTPIncludeIn() = %v, want nil", err)
	}

	if out := buf.String(); !strings.Contains(out, handEditHint) {
		t.Errorf("did not warn, but a symlink into an un-included conf.d loads nothing;\nlog was: %s", out)
	}
	if _, err := os.Lstat(filepath.Join(confD, "ratline-http.conf")); err == nil {
		t.Error("linked into a conf.d that nginx.conf does not include, which silently achieves nothing")
	}
}
