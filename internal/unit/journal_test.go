package unit

import (
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
	"github.com/ALIRAZA47/ratline-cli/internal/state"
	"github.com/ALIRAZA47/ratline-cli/internal/system"
	"github.com/ALIRAZA47/ratline-cli/internal/system/systest"
)

// withNamespaces makes the probe for systemd's per-namespace journald answer the way the
// test needs, whatever the machine running it has.
func withNamespaces(t *testing.T, supported bool) {
	t.Helper()
	prev := journaldTemplatePaths
	t.Cleanup(func() { journaldTemplatePaths = prev })
	if !supported {
		journaldTemplatePaths = []string{filepath.Join(t.TempDir(), "absent")}
		return
	}
	p := filepath.Join(t.TempDir(), "systemd-journald@.service")
	if err := os.WriteFile(p, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	journaldTemplatePaths = []string{p}
}

// fakeMachine points the journal paths at a scratch tree with a machine id in it, and
// returns the directory the test site's namespace would live in.
func fakeMachine(t *testing.T) (root string) {
	t.Helper()
	base := t.TempDir()
	prevRoot, prevConf, prevID := journalRoot, journaldConfDir, machineIDPath
	t.Cleanup(func() { journalRoot, journaldConfDir, machineIDPath = prevRoot, prevConf, prevID })
	journalRoot = filepath.Join(base, "journal")
	journaldConfDir = filepath.Join(base, "systemd")
	if err := os.MkdirAll(journaldConfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	machineIDPath = filepath.Join(base, "machine-id")
	if err := os.WriteFile(machineIDPath, []byte("0123456789abcdef0123456789abcdef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return journalRoot
}

// ownSite is pythonSite() owned by whoever runs the tests, so that the identity lookup
// the grant depends on succeeds without root.
func ownSite(t *testing.T) *state.Site {
	t.Helper()
	me, err := user.Current()
	if err != nil {
		t.Skip("no current user")
	}
	s := pythonSite()
	s.Owner = me.Username
	return s
}

// Every unit ratline renders for a site — the service, a job, a worker — logs into the
// site's own namespace, so the tenant can read all of them and none of anyone else's.
func TestEveryUnitOfASiteLogsIntoItsOwnNamespace(t *testing.T) {
	withNamespaces(t, true)
	site := pythonSite()
	want := "LogNamespace=" + site.Slug + "\n"

	service := directivesOnly(render(t, site, "/home/alice/api.example.com/venv/bin/gunicorn app:app", RenderOptions{}))
	if !strings.Contains(service, want) {
		t.Errorf("the service unit does not log into the site's namespace:\n%s", service)
	}

	m := testManager()
	job := aJob()
	worker := *job
	worker.Kind, worker.Name, worker.Schedule = state.UnitWorker, "queue", ""
	for _, u := range []*state.SiteUnit{job, &worker} {
		body, _, err := m.RenderSiteUnit(site, u)
		if err != nil {
			t.Fatalf("RenderSiteUnit(%s) = %v", u.Kind, err)
		}
		if !strings.Contains(directivesOnly(string(body)), want) {
			t.Errorf("the %s unit does not log into the site's namespace:\n%s", u.Kind, body)
		}
	}
}

// On a systemd that has no per-namespace journald the directive would be unknown and the
// units would depend on socket units that do not exist. Better the shared journal than a
// site that never starts.
func TestNoNamespaceOnASystemdWithoutOne(t *testing.T) {
	withNamespaces(t, false)
	fakeMachine(t)
	site := ownSite(t)
	out := directivesOnly(render(t, site, "/x/gunicorn app:app", RenderOptions{}))
	if strings.Contains(out, "LogNamespace=") {
		t.Errorf("a unit names a namespace this systemd cannot provide:\n%s", out)
	}
	body, _, err := testManager().RenderSiteUnit(site, aJob())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(directivesOnly(string(body)), "LogNamespace=") {
		t.Errorf("a job unit names a namespace this systemd cannot provide:\n%s", body)
	}
	if err := testManager().EnsureJournalNamespace(t.Context(), site); err != nil {
		t.Errorf("EnsureJournalNamespace = %v on a systemd without namespaces; want a no-op", err)
	}
	if system.Exists(journalRoot) {
		t.Error("a journal directory was created for a namespace nothing will log into")
	}
}

// systemd reads directives, not comments — and the template's comment explaining the
// directive contains its name.
func TestLogNamespaceOfReadsTheDirectiveAndNotTheComment(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.service")
	if err := os.WriteFile(p, []byte("# LogNamespace=nope, this unit has none\n[Service]\nUser=alice\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LogNamespaceOf(p); got != "" {
		t.Errorf("LogNamespaceOf read a comment as a directive: %q", got)
	}
	if err := os.WriteFile(p, []byte("[Service]\n  LogNamespace=alice-site  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LogNamespaceOf(p); got != "alice-site" {
		t.Errorf("LogNamespaceOf = %q, want alice-site", got)
	}
	if got := LogNamespaceOf(filepath.Join(dir, "missing.service")); got != "" {
		t.Errorf("LogNamespaceOf(missing) = %q, want empty", got)
	}
}

// Root sees the namespace merged with the shared journal, so the lines from before a site
// moved are still there. The tenant sees the namespace alone, and is refused — with the
// reason — while the unit on disk still logs into the shared journal.
func TestJournalctlArgsMergeForRootAndConfineTheTenant(t *testing.T) {
	m := testManager()
	m.Cfg.Paths.SystemdDir = t.TempDir()
	const unitName = "ratline-alice-api_example_com.service"

	args, err := m.JournalctlArgs(unitName, 20, false, false)
	if err != nil {
		t.Fatalf("root, no namespace: %v", err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--namespace") || !strings.Contains(joined, "-u "+unitName) || !strings.Contains(joined, "-n 20") {
		t.Errorf("root, no namespace: %q", joined)
	}
	if _, err := m.JournalctlArgs(unitName, 20, false, true); !rlerr.Is(err, rlerr.CodePrecondition) {
		t.Errorf("a tenant asking for a unit in the shared journal got %v; want a precondition refusal", err)
	}

	unitPath := filepath.Join(m.Cfg.Paths.SystemdDir, unitName)
	if err := os.WriteFile(unitPath, []byte("[Service]\nLogNamespace=alice-api_example_com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, err = m.JournalctlArgs(unitName, 20, true, false)
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(args, " ")
	if !strings.Contains(joined, "--namespace=+alice-api_example_com") || strings.Contains(joined, "--quiet") || !strings.Contains(joined, "--follow") {
		t.Errorf("root, namespaced: %q", joined)
	}
	args, err = m.JournalctlArgs(unitName, 20, false, true)
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(args, " ")
	if !strings.Contains(joined, "--namespace=alice-api_example_com") || strings.Contains(joined, "+") || !strings.Contains(joined, "--quiet") {
		t.Errorf("tenant, namespaced: %q", joined)
	}
	if hint := m.JournalHint(unitName); !strings.Contains(hint, "--namespace=+alice-api_example_com") {
		t.Errorf("the hint would send an operator to an empty journal: %q", hint)
	}
}

// The namespace is prepared before the first unit starts into it: the journald instance's
// configuration with the cap, and the directory — root's, setgid, and carrying the tenant's
// read grant — so the very first file journald writes is readable.
func TestEnsureJournalNamespacePreparesTheDirectoryAndConfiguration(t *testing.T) {
	withNamespaces(t, true)
	root := fakeMachine(t)
	site := ownSite(t)
	m := testManager()

	if err := m.EnsureJournalNamespace(t.Context(), site); err != nil {
		t.Fatalf("EnsureJournalNamespace = %v", err)
	}
	conf := filepath.Join(journaldConfDir, "journald@"+site.Slug+".conf")
	body, err := os.ReadFile(conf)
	if err != nil {
		t.Fatalf("no journald configuration was written: %v", err)
	}
	for _, want := range []string{"# managed-by: ratline", "Storage=persistent", "SystemMaxUse=256M"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("journald@%s.conf lacks %q:\n%s", site.Slug, want, body)
		}
	}
	dir := filepath.Join(root, "0123456789abcdef0123456789abcdef."+site.Slug)
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("the namespace directory was not created: %v", err)
	}
	if fi.Mode().Perm() != 0o755 || fi.Mode()&os.ModeSetgid == 0 {
		t.Errorf("the namespace directory is %v; want 2755, as systemd's LogsDirectory= would make it", fi.Mode())
	}
	if runtime.GOOS == "linux" {
		d, err := os.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		id, err := system.LookupIdentity(site.Owner)
		if err != nil {
			t.Fatal(err)
		}
		ok, err := system.DefaultACLGrantsGroupRead(d, id.GID)
		if err != nil && rlerr.Is(err, rlerr.CodePrecondition) {
			t.Skipf("no ACL support here: %v", err)
		}
		if err != nil || !ok {
			t.Errorf("the tenant's group was not granted the directory's contents: %v, %v", ok, err)
		}
		if got, _ := m.JournalReadable(site); !got {
			t.Error("JournalReadable says the tenant cannot read a namespace that was just prepared for them")
		}
	}

	// Twice is fine, and a file written in the meantime gets the grant too.
	existing := filepath.Join(dir, "system@0001.journal")
	if err := os.WriteFile(existing, []byte("j"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := m.EnsureJournalNamespace(t.Context(), site); err != nil {
		t.Fatalf("second EnsureJournalNamespace = %v", err)
	}
	if runtime.GOOS == "linux" {
		f, err := os.Open(existing)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		id, _ := system.LookupIdentity(site.Owner)
		if ok, err := system.ACLGrantsGroupRead(f, id.GID); err != nil || !ok {
			t.Errorf("a journal file that predates the grant was not granted: %v, %v", ok, err)
		}
	}
}

// A journald@<ns>.conf somebody else wrote is theirs, and nothing is prepared over it.
func TestEnsureJournalNamespaceRefusesSomebodyElsesConfiguration(t *testing.T) {
	withNamespaces(t, true)
	root := fakeMachine(t)
	site := ownSite(t)
	conf := filepath.Join(journaldConfDir, "journald@"+site.Slug+".conf")
	if err := os.WriteFile(conf, []byte("[Journal]\nStorage=volatile\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := testManager().EnsureJournalNamespace(t.Context(), site)
	if !rlerr.Is(err, rlerr.CodePrecondition) {
		t.Fatalf("EnsureJournalNamespace = %v; want a precondition refusal", err)
	}
	if got, _ := os.ReadFile(conf); !strings.Contains(string(got), "Storage=volatile") {
		t.Error("the operator's configuration was overwritten")
	}
	if system.Exists(root) {
		t.Error("the directory was created even though the configuration was refused")
	}
}

// --dry-run prepares nothing: no configuration, no directory.
func TestADryRunPreparesNoNamespace(t *testing.T) {
	withNamespaces(t, true)
	root := fakeMachine(t)
	site := ownSite(t)
	m := testManager()
	m.DryRun = true
	if err := m.EnsureJournalNamespace(t.Context(), site); err != nil {
		t.Fatalf("EnsureJournalNamespace = %v", err)
	}
	if entries, _ := os.ReadDir(journaldConfDir); len(entries) != 0 {
		t.Errorf("a dry run wrote %d file(s) into %s", len(entries), journaldConfDir)
	}
	if system.Exists(root) {
		t.Error("a dry run created the journal directory")
	}
}

// Deleting a site stops its journald instance — sockets first, or the next connection
// starts it again — and removes what it wrote only when asked to purge.
func TestRemoveJournalNamespaceStopsTheInstanceAndPurgesOnRequest(t *testing.T) {
	withNamespaces(t, true)
	root := fakeMachine(t)
	site := ownSite(t)
	m := testManager()
	fake := systest.NewFakeRunner()
	m.Runner = fake

	dir := filepath.Join(root, "0123456789abcdef0123456789abcdef."+site.Slug)
	conf := filepath.Join(journaldConfDir, "journald@"+site.Slug+".conf")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "system.journal"), []byte("j"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conf, []byte("# managed-by: ratline\n[Journal]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.RemoveJournalNamespace(t.Context(), site, false); err != nil {
		t.Fatalf("RemoveJournalNamespace(keep) = %v", err)
	}
	keys := strings.Join(fake.Keys(), "\n")
	socketAt := strings.Index(keys, "systemd-journald@"+site.Slug+".socket")
	serviceAt := strings.Index(keys, "systemd-journald@"+site.Slug+".service")
	if socketAt < 0 || serviceAt < 0 {
		t.Fatalf("the journald instance was not stopped:\n%s", keys)
	}
	if socketAt > serviceAt {
		t.Errorf("the service was stopped before its socket, which would start it again:\n%s", keys)
	}
	if !system.Exists(dir) || !system.Exists(conf) {
		t.Error("a delete without --purge removed the journal or its configuration")
	}

	if err := m.RemoveJournalNamespace(t.Context(), site, true); err != nil {
		t.Fatalf("RemoveJournalNamespace(purge) = %v", err)
	}
	if system.Exists(dir) {
		t.Error("--purge left the journal directory behind")
	}
	if system.Exists(conf) {
		t.Error("--purge left journald's configuration behind")
	}

	// And a dry run touches nothing.
	fake.Reset()
	m.DryRun = true
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveJournalNamespace(t.Context(), site, true); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls()) != 0 || !system.Exists(dir) {
		t.Error("a dry-run delete stopped or removed the journal")
	}
}
