package user

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

// GrantSudo is the one function here that can hand a tenant root, and until these tests it
// had no coverage at all — not from the unit tests, and not from the integration suite,
// whose only mentions of sudo are the harness using `sudo -u alice` for its own purposes.
//
// Each test names the property rather than the mechanics, because the mechanics are
// allowed to change and the properties are not.

// sudoFixture builds a manager whose sudoers directory is a temporary one, with a tenant in
// state and a fake runner standing in for visudo.
func sudoFixture(t *testing.T, allowSudo bool) (*Manager, *systest.FakeRunner, string) {
	t.Helper()

	store, err := state.OpenMemory()
	if err != nil {
		t.Fatalf("opening a state store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.PutUser(context.Background(), &state.User{
		Name: "alice", UID: 3001, GID: 3001, Home: "/home/alice", Shell: "/bin/bash",
	}); err != nil {
		t.Fatalf("seeding a tenant: %v", err)
	}

	cfg := config.Default()
	cfg.Users.AllowSudo = allowSudo
	cfg.SourcePath = "/etc/ratline/config.yaml"

	dir := t.TempDir()
	runner := systest.NewFakeRunner()
	return &Manager{
		Cfg: cfg, Log: log.Discard(), Runner: runner, State: store, SudoersDir: dir,
	}, runner, dir
}

// realBinary creates a file that exists at an absolute path, so the "must be a real
// program" check passes without depending on what happens to be installed on the host.
func realBinary(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("creating a stand-in binary: %v", err)
	}
	return path
}

// grants returns the drop-in files present, so a test can assert on what was installed
// without knowing the naming scheme.
func grants(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// The config gate is the outermost guard: without it, `--sudo` on a server whose operator
// never opted in would silently work.
func TestGrantSudoRefusedWhenTheConfigHasNotOptedIn(t *testing.T) {
	m, runner, dir := sudoFixture(t, false)
	bin := realBinary(t, "systemctl")

	err := m.GrantSudo(context.Background(), SudoGrant{
		User: "alice", Commands: []string{bin + " restart app"},
	})
	if err == nil {
		t.Fatal("granting sudo succeeded with users.allow_sudo false; the gate does nothing")
	}
	if got := rlerr.CodeOf(err); got != rlerr.CodePrecondition {
		t.Errorf("exit code = %d, want %d (precondition): automation branches on this",
			got, rlerr.CodePrecondition)
	}
	if files := grants(t, dir); len(files) != 0 {
		t.Errorf("a refused grant left %v behind", files)
	}
	if runner.Called("visudo") {
		t.Error("visudo ran for a grant that should have been refused before anything was staged")
	}
}

// A blanket grant is the whole thing this design exists to prevent, so "no commands" must
// not be read as "all commands".
func TestGrantSudoRefusesAnEmptyCommandList(t *testing.T) {
	m, _, dir := sudoFixture(t, true)

	err := m.GrantSudo(context.Background(), SudoGrant{User: "alice"})
	if err == nil {
		t.Fatal("an empty command list was accepted; an ALL grant is one step from here")
	}
	if got := rlerr.CodeOf(err); got != rlerr.CodeUsage {
		t.Errorf("exit code = %d, want %d (usage)", got, rlerr.CodeUsage)
	}
	if files := grants(t, dir); len(files) != 0 {
		t.Errorf("a refused grant left %v behind", files)
	}
}

// A relative program name resolves through the caller's PATH at sudo time, which would let
// the tenant choose what runs as root — the exact hole the feature is meant not to open.
func TestGrantSudoRefusesARelativeProgram(t *testing.T) {
	m, runner, dir := sudoFixture(t, true)

	err := m.GrantSudo(context.Background(), SudoGrant{
		User: "alice", Commands: []string{"systemctl restart app"},
	})
	if err == nil {
		t.Fatal("a relative program name was accepted: the tenant picks what runs as root")
	}
	if got := rlerr.CodeOf(err); got != rlerr.CodeUsage {
		t.Errorf("exit code = %d, want %d (usage)", got, rlerr.CodeUsage)
	}
	if files := grants(t, dir); len(files) != 0 {
		t.Errorf("a refused grant left %v behind", files)
	}
	if runner.Called("visudo") {
		t.Error("visudo ran for a grant rejected on its own arguments")
	}
}

// A grant naming a program that is not there is not harmless: it is a rule nobody notices
// is dead until somebody drops a binary at that path.
func TestGrantSudoRefusesAProgramThatDoesNotExist(t *testing.T) {
	m, _, dir := sudoFixture(t, true)

	err := m.GrantSudo(context.Background(), SudoGrant{
		User: "alice", Commands: []string{"/opt/nothing/here restart app"},
	})
	if err == nil {
		t.Fatal("a grant for a nonexistent program was accepted")
	}
	if got := rlerr.CodeOf(err); got != rlerr.CodePrecondition {
		t.Errorf("exit code = %d, want %d (precondition)", got, rlerr.CodePrecondition)
	}
	if files := grants(t, dir); len(files) != 0 {
		t.Errorf("a refused grant left %v behind", files)
	}
}

// A tenant that is not in state is not a tenant. Writing a rule for a name that does not
// exist means the rule activates later, when somebody happens to create that account.
func TestGrantSudoRefusesAnUnknownTenant(t *testing.T) {
	m, _, dir := sudoFixture(t, true)
	bin := realBinary(t, "systemctl")

	err := m.GrantSudo(context.Background(), SudoGrant{
		User: "mallory", Commands: []string{bin + " restart app"},
	})
	if err == nil {
		t.Fatal("a grant was installed for a user who does not exist")
	}
	if files := grants(t, dir); len(files) != 0 {
		t.Errorf("a refused grant left %v behind", files)
	}
}

// The two properties that make an installed grant narrow: the full argv is pinned, and the
// file visudo is handed is 0440 — the only mode sudo will read.
func TestGrantSudoPinsTheFullArgvAndStagesAt0440(t *testing.T) {
	m, runner, dir := sudoFixture(t, true)
	bin := realBinary(t, "systemctl")

	// visudo is the moment the staged file exists and has not yet been installed, so it is
	// the only place these properties can be observed.
	var staged, mode string
	var stagedIn string
	runner.Hook = func(c system.Cmd) (*system.Result, error) {
		if c.Name == "visudo" && len(c.Args) == 3 {
			path := c.Args[2]
			stagedIn = filepath.Dir(path)
			body, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("visudo was handed %s, which cannot be read: %v", path, err)
			}
			staged = string(body)
			if fi, err := os.Stat(path); err == nil {
				mode = fi.Mode().Perm().String()
			}
		}
		return &system.Result{}, nil
	}

	err := m.GrantSudo(context.Background(), SudoGrant{
		User: "alice", Commands: []string{bin + " restart ratline-app_example_com.service"},
	})
	if err != nil {
		t.Fatalf("granting sudo: %v", err)
	}

	want := "alice ALL=(root) NOPASSWD: " + bin + " restart ratline-app_example_com.service"
	if !strings.Contains(staged, want) {
		t.Errorf("the rule handed to visudo does not pin the arguments.\n got:\n%s\nwant a line:\n%s",
			staged, want)
	}
	// A grant of the bare program name is the failure this pinning prevents: systemctl with
	// arbitrary arguments is root.
	if strings.Contains(staged, "NOPASSWD: "+bin+"\n") {
		t.Error("the rule grants the bare program, so the tenant may pass any arguments to it")
	}
	if !strings.Contains(staged, system.ManagedHeader) {
		t.Error("the file has no managed-by header, so ratline will not recognise it as its own")
	}
	if mode != "-r--r-----" {
		t.Errorf("staged mode = %s, want -r--r----- (0440); sudo ignores any other mode", mode)
	}
	// Staged inside the same directory so the install is a rename within one filesystem —
	// a cross-device rename fails, and it would fail only on the servers that separate them.
	if stagedIn != dir {
		t.Errorf("staged in %s, want %s: installing must be a rename, not a copy", stagedIn, dir)
	}

	files := grants(t, dir)
	if len(files) != 1 || files[0] != "ratline-alice" {
		t.Errorf("installed files = %v, want exactly [ratline-alice]", files)
	}
}

// This is the property that matters most on this path: if visudo rejects the file, the
// machine must be exactly as it was. A malformed sudoers locks out every sudo user.
func TestGrantSudoInstallsNothingWhenVisudoRejectsIt(t *testing.T) {
	m, runner, dir := sudoFixture(t, true)
	bin := realBinary(t, "systemctl")
	runner.ExpectFailure("visudo -c -f", 1, ">>> /etc/sudoers.d/x: syntax error near line 8 <<<")

	err := m.GrantSudo(context.Background(), SudoGrant{
		User: "alice", Commands: []string{bin + " restart app"},
	})
	if err == nil {
		t.Fatal("a grant visudo rejected was installed anyway")
	}
	if got := rlerr.CodeOf(err); got != rlerr.CodePrecondition {
		t.Errorf("exit code = %d, want %d (precondition)", got, rlerr.CodePrecondition)
	}
	// Not just "the grant is absent" — nothing at all, including the staged temporary file,
	// because a leftover .ratline-sudo-check-* in sudoers.d is itself read by sudo.
	if files := grants(t, dir); len(files) != 0 {
		t.Errorf("visudo rejected the rule but %v was left in the sudoers directory", files)
	}
}

// --dry-run writes nothing. This has been broken elsewhere in this codebase before, which
// is why it is asserted at the manager level rather than trusted.
func TestGrantSudoDryRunWritesNothing(t *testing.T) {
	m, runner, dir := sudoFixture(t, true)
	m.DryRun = true

	// Deliberately a program that does not exist: under --dry-run the existence check is
	// skipped, because the point of a dry run is to work on a server that is not yet set up.
	err := m.GrantSudo(context.Background(), SudoGrant{
		User: "alice", Commands: []string{"/opt/not/installed/yet restart app"},
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if files := grants(t, dir); len(files) != 0 {
		t.Errorf("--dry-run wrote %v", files)
	}
	if runner.Called("visudo") {
		t.Error("--dry-run ran visudo; it must not execute anything")
	}
}

// Revoking removes only a file ratline wrote. An operator's own rule in sudoers.d is not
// ours to delete, and deleting one could remove their last route back into the machine.
func TestRevokeSudoLeavesAFileRatlineDidNotWrite(t *testing.T) {
	m, _, dir := sudoFixture(t, true)
	path := filepath.Join(dir, "ratline-alice")
	// No managed-by header: as far as ratline is concerned somebody else wrote this.
	if err := os.WriteFile(path, []byte("alice ALL=(ALL) NOPASSWD: ALL\n"), 0o440); err != nil {
		t.Fatalf("planting a hand-written rule: %v", err)
	}

	if err := m.RevokeSudo(context.Background(), "alice"); err == nil {
		t.Fatal("revoke deleted a sudoers file ratline did not write")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the hand-written rule is gone: %v", err)
	}
}

func TestRevokeSudoRemovesItsOwnGrantAndRevalidates(t *testing.T) {
	m, runner, dir := sudoFixture(t, true)
	bin := realBinary(t, "systemctl")
	if err := m.GrantSudo(context.Background(), SudoGrant{
		User: "alice", Commands: []string{bin + " restart app"},
	}); err != nil {
		t.Fatalf("granting sudo: %v", err)
	}

	if err := m.RevokeSudo(context.Background(), "alice"); err != nil {
		t.Fatalf("revoking: %v", err)
	}
	if files := grants(t, dir); len(files) != 0 {
		t.Errorf("after revoke the sudoers directory still holds %v", files)
	}
	if runner.CountCalls("visudo -c") < 2 {
		t.Error("sudoers was not re-validated after the removal")
	}
}

// A tenant with no grant is not an error worth guessing about, but it must not report
// success either — an operator reading "revoked" would believe access was removed.
func TestRevokeSudoSaysSoWhenThereIsNoGrant(t *testing.T) {
	m, _, _ := sudoFixture(t, true)

	err := m.RevokeSudo(context.Background(), "alice")
	if err == nil {
		t.Fatal("revoking a grant that does not exist reported success")
	}
	if got := rlerr.CodeOf(err); got != rlerr.CodePrecondition {
		t.Errorf("exit code = %d, want %d (precondition)", got, rlerr.CodePrecondition)
	}
}

// SudoGrants is a privilege audit. An unreadable directory must be an error, because an
// empty map reads as "nobody has sudo" — the opposite of the safe answer.
func TestSudoGrantsDistinguishesEmptyFromUnknown(t *testing.T) {
	m, _, dir := sudoFixture(t, true)

	// Absent: genuinely no grants.
	m.SudoersDir = filepath.Join(dir, "does-not-exist")
	got, err := m.SudoGrants(context.Background())
	if err != nil {
		t.Fatalf("a missing sudoers.d should mean no grants, not an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want no grants", got)
	}

	// Present but unreadable: the answer is unknown.
	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0o000); err != nil {
		t.Fatalf("creating an unreadable directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("running as root, which can read a 0000 directory: the unknown case cannot be reached")
	}
	m.SudoersDir = blocked
	if _, err := m.SudoGrants(context.Background()); err == nil {
		t.Error("an unreadable sudoers.d returned no error, which reads as 'nobody has sudo'")
	}
}

// The audit reports what is installed, parsed back out of the file rather than from state —
// so a rule edited by hand shows up as it actually is.
func TestSudoGrantsReadsBackTheInstalledRule(t *testing.T) {
	m, _, _ := sudoFixture(t, true)
	bin := realBinary(t, "systemctl")
	if err := m.GrantSudo(context.Background(), SudoGrant{
		User: "alice", Commands: []string{bin + " restart app"},
	}); err != nil {
		t.Fatalf("granting sudo: %v", err)
	}

	got, err := m.SudoGrants(context.Background())
	if err != nil {
		t.Fatalf("listing grants: %v", err)
	}
	rules, ok := got["alice"]
	if !ok {
		t.Fatalf("alice is not in the audit: %v", got)
	}
	if len(rules) != 1 || !strings.Contains(rules[0], bin+" restart app") {
		t.Errorf("audit shows %v, want the pinned argv", rules)
	}
}

// A dot in a filename makes sudo ignore the file entirely, so a grant for a user whose name
// contains one would be installed, reported as installed, and do nothing.
func TestSudoersFilenameCannotContainADot(t *testing.T) {
	m, _, _ := sudoFixture(t, true)

	got := filepath.Base(m.sudoersFile("odd.name"))
	if strings.Contains(got, ".") {
		t.Errorf("filename %q contains a dot; sudo skips such files and the grant would silently do nothing", got)
	}
}
