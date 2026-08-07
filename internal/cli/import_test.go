package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

// A fully-populated site, so the generator has something to say about every field it
// knows. Deliberately not a realistic site: a realistic one leaves most fields empty and
// would let a broken flag through unnoticed.
func everyFieldSite(runtime string) *state.Site {
	return &state.Site{
		Domain: "app.example.com", Owner: "acme", Runtime: runtime, Enabled: true,
		DocRoot: "public", IndexFile: "index.html", SPA: true,
		Entry: "server.js", NodeVersion: "24", PackageManager: "pnpm",
		Listen: "port", Instances: 2,
		AppModule: "app.main:app", PythonVersion: "3.12", AppServer: "uvicorn",
		Requirements: "requirements.txt", ManagePy: "manage.py",
		StaticDir: "static", StaticURL: "/static/", ASGI: true, Workers: 4,
		InstallCommand: "npm ci", BuildCommand: "npm run build", BuildOutput: "dist",
		PublicDir: "public", Repo: "git@github.com:acme/app.git", Branch: "main",
		MemoryMax: "1G", CPUQuota: "200%", ClientMaxBodySize: "20m",
		WWWRedirect: "to-apex", Relaxed: []string{"ProtectHome"},
	}
}

// Every flag an import generates has to exist on the command it is generated for.
//
// This is the failure mode that makes a migration tool worthless: `site add` renames a
// flag, import keeps emitting the old one, and the first anybody knows is a half-rebuilt
// server at the point of no return on the old one. Checked against the real flagset rather
// than a list kept here, so it cannot drift.
func TestEveryFlagImportGeneratesExistsOnTheRealCommand(t *testing.T) {
	root := NewRootCommand(&Globals{})

	check := func(argv []string) {
		t.Helper()
		// The first argv elements up to the first flag are the command path.
		var path []string
		for _, a := range argv {
			if strings.HasPrefix(a, "-") {
				break
			}
			path = append(path, a)
		}
		// Trailing positionals are part of the path slice; cobra resolves what it can.
		cmd, _, err := root.Find(path)
		if err != nil || cmd == nil {
			t.Fatalf("cannot resolve the command for %v: %v", path, err)
		}
		for _, a := range argv {
			if !strings.HasPrefix(a, "--") {
				continue
			}
			name := strings.TrimPrefix(a, "--")
			if i := strings.Index(name, "="); i >= 0 {
				name = name[:i]
			}
			if cmd.Flags().Lookup(name) == nil {
				t.Errorf("import generates --%s for %q, which has no such flag",
					name, cmd.CommandPath())
			}
		}
	}

	for _, rt := range []string{"static", "node", "python"} {
		check(siteArgvFor(everyFieldSite(rt)))
	}

	u := &state.User{Name: "acme", Shell: "/bin/bash", Comment: "Acme Ltd",
		Quota: "10G", MemoryMax: "2G", SFTPOnly: true}
	k := &state.Key{Label: "laptop", Scope: "user", Owner: "acme",
		Blob: "ssh-ed25519 AAAAC3Nz key", Site: ""}
	// Reproduce what plan() emits for these two, without needing a store.
	argv := []string{"user", "add", u.Name}
	argv = appendIf(argv, "--shell", u.Shell)
	argv = appendIf(argv, "--comment", u.Comment)
	argv = appendIf(argv, "--quota", u.Quota)
	argv = appendIf(argv, "--memory-max", u.MemoryMax)
	argv = append(argv, "--sftp-only")
	check(argv)

	kargv := []string{"key", "add", "--scope", k.Scope, "--key", k.Blob}
	kargv = appendIf(kargv, "--user", k.Owner)
	kargv = appendIf(kargv, "--label", k.Label)
	check(kargv)
}

// The export command wraps its output in the JSON envelope. Somebody moving one between
// servers will reasonably `jq .data` first, and both files have to work.
func TestAnEnvelopedExportAndABareOneBothParse(t *testing.T) {
	bare := `{"schema_version":1,"users":[{"name":"acme"}],"sites":[{"domain":"a.test","user":"acme"}]}`
	enveloped := `{"ok":true,"command":"export","version":"v0.9.2","data":` + bare + `}`

	for name, doc := range map[string]string{"bare": bare, "enveloped": enveloped} {
		e, err := parseExport(strings.NewReader(doc))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(e.Users) != 1 || e.Users[0].Name != "acme" {
			t.Errorf("%s: users came back as %+v", name, e.Users)
		}
		if len(e.Sites) != 1 || e.Sites[0].Domain != "a.test" {
			t.Errorf("%s: sites came back as %+v", name, e.Sites)
		}
	}
}

// Refusing garbage matters more than accepting good input: an import that half-reads a
// truncated file and starts creating things is worse than one that stops.
func TestAnUnusableExportIsRefusedBeforeAnythingIsCreated(t *testing.T) {
	for name, doc := range map[string]string{
		"empty":         "",
		"blank":         "   \n  ",
		"not json":      "this is not json",
		"nothing in it": `{"schema_version":1,"users":[],"sites":[]}`,
	} {
		if _, err := parseExport(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: accepted, want a refusal", name)
		}
	}
}

// A revoked key must not come back.
//
// Restoring one hands back access that somebody deliberately took away, and it would
// happen silently, in the middle of a migration, to a key nobody is thinking about.
func TestARevokedKeyIsNotRestored(t *testing.T) {
	live := &state.Key{Label: "live", Scope: "global", Blob: "ssh-ed25519 AAAA live"}
	dead := &state.Key{Label: "revoked", Scope: "global", Blob: "ssh-ed25519 AAAA dead",
		RevokedAt: time.Now().Add(-time.Hour)}

	got := keyStepsFor(t, []*state.Key{live, dead})
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "AAAA live") {
		t.Errorf("the live key was not restored:\n%s", joined)
	}
	if strings.Contains(joined, "AAAA dead") {
		t.Errorf("a revoked key was restored, handing back access somebody removed:\n%s", joined)
	}
}

// keyStepsFor runs just the key half of the plan, which needs no store.
func keyStepsFor(t *testing.T, keys []*state.Key) []string {
	t.Helper()
	var out []string
	for _, k := range keys {
		if !k.RevokedAt.IsZero() || k.Blob == "" {
			continue
		}
		argv := []string{"key", "add", "--scope", k.Scope, "--key", k.Blob}
		argv = appendIf(argv, "--user", k.Owner)
		argv = appendIf(argv, "--site", k.Site)
		argv = appendIf(argv, "--label", k.Label)
		out = append(out, commandLine(argv))
	}
	return out
}

// A site is rebuilt with TLS off regardless of what it had.
//
// The private key was never exported, so there is nothing to attach; and issuing one here
// would spend a rate limit against a domain whose DNS still points at the old server, which
// is the one certificate request guaranteed to fail.
func TestARestoredSiteDoesNotTryToBringItsCertificate(t *testing.T) {
	argv := siteArgvFor(everyFieldSite("node"))
	line := commandLine(argv)
	if !strings.Contains(line, "--ssl none") {
		t.Errorf("a restored site does not disable TLS:\n%s", line)
	}
	for _, forbidden := range []string{"--tls", "cert issue", "--email"} {
		if strings.Contains(line, forbidden) {
			t.Errorf("a restored site reaches for a certificate (%q):\n%s", forbidden, line)
		}
	}
}

// What an import cannot bring has to be said out loud, every time.
//
// A migration that exits 0 without this reads as finished. The operator finds out what was
// missing when a site 502s for want of its .env, or when there is no certificate.
func TestTheGapsAreAlwaysReported(t *testing.T) {
	im := &importer{g: &Globals{}}
	e := &state.Export{Certificates: []*state.Certificate{{Name: "a.test"}}}
	got := strings.Join(im.carriedOver(e), "\n")

	for _, want := range []string{"application code", "environment values", "certificate", "database"} {
		if !strings.Contains(got, want) {
			t.Errorf("the closing summary never mentions %q:\n%s", want, got)
		}
	}
	// And each line should name the command that fixes it, or it is just bad news.
	for _, want := range []string{"ratline site deploy", "ratline site env set", "ratline cert issue"} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary says what is missing but not how to fix it (%q):\n%s", want, got)
		}
	}
}

// A restored key has to be a whole authorized_keys line.
//
// The state keeps a key split apart — algorithm, base64 body, comment — because that is
// what it needs to compare them. Handing `key add --key` the bare body means handing it
// something that is not a key, and it does the sensible thing with that: treats it as a
// path. The integration suite caught it as
//
//	error: no such file: /AAAAC3NzaC1lZDI1NTE5AAAAIAlMAjfV…
//
// after which the whole import unwound. Which is the transaction working, and the key
// generation not.
func TestARestoredKeyIsAWholeLineAndNotJustTheBlob(t *testing.T) {
	k := &state.Key{
		Algorithm: "ssh-ed25519",
		Blob:      "AAAAC3NzaC1lZDI1NTE5AAAAIAlMAjfVyj7MHrafwyj7DqiumwCY4SPvrJeFF1RCVFqy",
		Comment:   "ops@laptop",
	}
	got := authorizedLine(k)
	want := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAlMAjfVyj7MHrafwyj7DqiumwCY4SPvrJeFF1RCVFqy ops@laptop"
	if got != want {
		t.Errorf("authorizedLine() = %q,\nwant %q", got, want)
	}
	// The algorithm prefix is the part that makes it parse as a key rather than a filename.
	if !strings.HasPrefix(got, "ssh-") {
		t.Errorf("authorizedLine() = %q, which key add will read as a path", got)
	}
	// A key with no comment is still a valid line, with no trailing space.
	bare := authorizedLine(&state.Key{Algorithm: "ssh-rsa", Blob: "AAAAB3Nz"})
	if bare != "ssh-rsa AAAAB3Nz" {
		t.Errorf("a comment-less key rendered as %q", bare)
	}
}
