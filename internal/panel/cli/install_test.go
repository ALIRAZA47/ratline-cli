package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/log"
	"github.com/ALIRAZA47/ratline-cli/internal/panel"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/auth"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

func testApp(t *testing.T) (*App, *store.Store) {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Real files, not a terminal: canPrompt() is false, which is the installer's
	// situation when it runs from a package or from curl piped into sh.
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = devnull.Close() })

	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		Cfg:    panel.Default(),
		Log:    log.Discard(),
		Stdin:  devnull,
		Stdout: out,
		Stderr: out,
	}
	return app, st
}

// The change this makes to the product: after an install there is an account, so
// there is never a moment in which the panel is answering and unclaimed.
func TestInstallCreatesTheFirstSuperAdmin(t *testing.T) {
	app, st := testApp(t)
	ctx := context.Background()

	created, err := app.ensureFirstAdmin(ctx, st, installOptions{adminEmail: "Dana@Example.COM "})
	if err != nil {
		t.Fatalf("ensureFirstAdmin: %v", err)
	}
	if created == nil || created.Email != "dana@example.com" {
		t.Fatalf("created = %+v, want the normalised address", created)
	}
	if created.Password == "" {
		t.Fatal("no password was generated, so nobody can sign in")
	}

	account, err := st.FindAccountByEmail(ctx, "dana@example.com")
	if err != nil {
		t.Fatalf("the account was not stored: %v", err)
	}
	if account.Role != store.RoleSuperAdmin {
		t.Errorf("role = %q, want superadmin", account.Role)
	}
	// The generated password must actually work, and must not be recoverable from
	// what was stored.
	ok, err := auth.VerifyPassword(account.PasswordHash, created.Password)
	if err != nil || !ok {
		t.Fatalf("the generated password does not verify against the stored hash: %v", err)
	}
	if strings.Contains(account.PasswordHash, created.Password) {
		t.Fatal("the password is recoverable from the stored hash")
	}
}

// Running the installer twice is the normal way to fix a mistake in it, and the
// natural way to add the panel to a server that already has ratline. It must not
// create a second account or reset the first.
func TestInstallIsSafeToRunTwice(t *testing.T) {
	app, st := testApp(t)
	ctx := context.Background()
	opts := installOptions{adminEmail: "dana@example.com"}

	first, err := app.ensureFirstAdmin(ctx, st, opts)
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.FindAccountByEmail(ctx, "dana@example.com")
	if err != nil {
		t.Fatal(err)
	}

	second, err := app.ensureFirstAdmin(ctx, st, opts)
	if err != nil {
		t.Fatalf("a second install failed: %v", err)
	}
	if second.Existing != 1 {
		t.Errorf("Existing = %d, want 1", second.Existing)
	}
	if second.Password != "" {
		t.Error("a second install printed a password, which would look like a reset")
	}
	after, err := st.FindAccountByEmail(ctx, "dana@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if after.PasswordHash != before.PasswordHash {
		t.Error("a second install changed the existing password")
	}
	accounts, err := st.ListAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Errorf("%d accounts exist after two installs, want 1", len(accounts))
	}
	_ = first
}

// Without a terminal and without an address there is nothing to do but refuse. The
// alternative is inventing an address, or leaving the panel unclaimed while telling
// nobody — and the exit code says which of the two problems this is.
func TestInstallRefusesToGuessAnAddress(t *testing.T) {
	app, st := testApp(t)
	_, err := app.ensureFirstAdmin(context.Background(), st, installOptions{})
	if err == nil {
		t.Fatal("the installer invented an account")
	}
	if rlerr.CodeOf(err) != rlerr.CodeInputRequired {
		t.Errorf("code = %s, want input_required", rlerr.CodeOf(err))
	}
	if !strings.Contains(rlerr.Hint(err), "--admin-email") {
		t.Errorf("the hint does not name the flag to use: %q", rlerr.Hint(err))
	}
}

// --no-admin is the deliberate opt-out, and it must leave the database empty rather
// than quietly creating something.
func TestNoAdminLeavesThePanelUnclaimed(t *testing.T) {
	app, st := testApp(t)
	ctx := context.Background()
	created, err := app.ensureFirstAdmin(ctx, st, installOptions{noAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	if created != nil {
		t.Errorf("created = %+v, want nothing", created)
	}
	n, err := st.CountAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d accounts exist despite --no-admin", n)
	}
}

func TestInstallRefusesAnAddressThatIsNotOne(t *testing.T) {
	app, st := testApp(t)
	for _, bad := range []string{"not-an-address", "a@b@c", "", "dana@"} {
		if _, err := app.ensureFirstAdmin(context.Background(), st,
			installOptions{adminEmail: bad}); err == nil {
			t.Errorf("%q was accepted as an address", bad)
		}
	}
}

// The installer's output is parsed by the integration suite, and by anybody who pipes
// it into a provisioning log. The two lines that matter are the address and the
// password, and this pins their shape so a change to the wording cannot silently
// break a script that reads them.
func TestTheInstallerPrintsTheCredentialsInAStableShape(t *testing.T) {
	app, st := testApp(t)
	created, err := app.ensureFirstAdmin(context.Background(), st,
		installOptions{adminEmail: "dana@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	app.reportAdmin(created)

	out := app.Stdout
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	printed := string(body)

	var email, password string
	for _, line := range strings.Split(printed, "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 4 && fields[0] == "Sign" && fields[1] == "in" && fields[2] == "as":
			email = fields[3]
		case len(fields) == 2 && fields[0] == "Password":
			password = fields[1]
		}
	}
	if email != "dana@example.com" {
		t.Errorf("could not read the address back out of:\n%s", printed)
	}
	if password != created.Password {
		t.Errorf("could not read the password back out of:\n%s", printed)
	}
	// And it says the thing that stops somebody going looking for it later.
	if !strings.Contains(printed, "shown once") {
		t.Error("the output does not say the password is shown once")
	}
	// The password must not also appear in the log, which goes to the journal and is
	// kept for weeks.
	if strings.Contains(printed, "level=") && strings.Contains(printed, created.Password) {
		t.Error("the password reached the logger")
	}
}

// The generated password has to be usable: readable off one screen, typeable into
// another, and strong enough that the account is not the weak point.
func TestGeneratedPasswordsAreUsableAndDistinct(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		p, err := auth.GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if err := auth.CheckPasswordStrength(p); err != nil {
			t.Fatalf("a generated password fails the panel's own check: %v", err)
		}
		if seen[p] {
			t.Fatalf("a generated password repeated within 200: %q", p)
		}
		seen[p] = true
		for _, r := range p {
			// Ambiguous characters are the reason somebody types it wrong three
			// times and assumes the install is broken.
			if strings.ContainsRune("0O1lIuv", r) {
				t.Errorf("the alphabet includes %q, which is misread: %s", r, p)
			}
		}
	}
}
