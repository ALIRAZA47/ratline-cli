package rl

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/ALIRAZA47/ratline-cli/internal/cli"
	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// realCatalogue is the installed binary's own command surface, built by walking the
// cobra tree exactly as `ratline schema` does.
//
// Using the real one rather than a fixture is the point: these tests then assert
// things about the commands that actually exist, and a ratline change that breaks an
// assumption fails here rather than on somebody's server.
func realCatalogue(t *testing.T) *Catalogue {
	t.Helper()
	schema := cli.BuildSchema(cli.NewRootCommand(&cli.Globals{}))
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshalling the schema: %v", err)
	}
	cat, err := ParseSchema(raw)
	if err != nil {
		t.Fatalf("ParseSchema: %v", err)
	}
	return cat
}

func policyFor(t *testing.T, cat *Catalogue, verb string) Policy {
	t.Helper()
	cmd, ok := cat.Leaves[verb]
	if !ok {
		t.Fatalf("the installed ratline has no %q command", verb)
	}
	p, _ := PolicyFor(verb, cmd)
	return p
}

// The envelope the panel parses and the envelope the CLI writes must be the same
// object. They are declared separately on purpose — the panel is a consumer of a
// published contract — so this is what stops them drifting apart silently.
func TestEnvelopeMatchesTheCLIs(t *testing.T) {
	produced, err := json.Marshal(cli.Envelope{
		OK: false, Command: "ratline site add", Version: "v1.2.3",
		Error: &cli.ErrorPayload{
			Code: 3, Name: "precondition_failed", Message: "no such user",
			Hint: "create it first", Fields: map[string]string{"user": "acme"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	env, err := ParseEnvelope(string(produced))
	if err != nil {
		t.Fatalf("the panel could not read the CLI's own envelope: %v", err)
	}
	if env.OK || env.Command != "ratline site add" || env.Version != "v1.2.3" {
		t.Fatalf("the envelope did not round-trip: %+v", env)
	}
	perr := env.Err()
	if perr == nil {
		t.Fatal("a failure envelope produced no error")
	}
	if rlerr.CodeOf(perr) != rlerr.CodePrecondition {
		t.Errorf("code = %s, want precondition_failed", rlerr.CodeOf(perr))
	}
	if rlerr.Hint(perr) != "create it first" {
		t.Errorf("the hint was lost: %q", rlerr.Hint(perr))
	}
	if rlerr.Fields(perr)["user"] != "acme" {
		t.Error("the structured fields were lost")
	}
}

func TestParseEnvelopeRefusesWhatIsNotOne(t *testing.T) {
	for name, stdout := range map[string]string{
		"empty":       "",
		"whitespace":  "   \n",
		"not json":    "error: something went wrong\n",
		"two objects": `{"ok":true}{"ok":false}`,
	} {
		if _, err := ParseEnvelope(stdout); err == nil {
			t.Errorf("%s was accepted as an envelope", name)
		}
	}
}

// The invariant this whole package exists to hold. A secret in argv is a secret in
// /proc/PID/cmdline, which every account on the server can read while the command
// runs — so it must reach ratline on stdin and nowhere else.
func TestASecretNeverReachesArgv(t *testing.T) {
	cat := realCatalogue(t)
	const secret = "s3cr3t-database-password"

	cases := []struct {
		verb string
		args []string
		key  string
	}{
		{"site env set", []string{"example.com"}, "DATABASE_URL"},
		{"user password set", []string{"acme"}, ""},
		{"db install", nil, ""},
		{"db connect", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			policy := policyFor(t, cat, tc.verb)
			if policy.Stdin == nil {
				t.Fatalf("%s is not declared as reading a value from standard input", tc.verb)
			}
			req := Request{Verb: tc.verb, Args: tc.args, Secret: secret, SecretKey: tc.key}
			payload, err := StdinPayload(policy, req)
			if err != nil {
				t.Fatalf("StdinPayload: %v", err)
			}
			if !strings.Contains(payload, secret) {
				t.Fatalf("the secret is not in the stdin payload: %q", payload)
			}
			req.Secret = payload

			argv, err := BuildArgv(cat, policy, req)
			if err != nil {
				t.Fatalf("BuildArgv: %v", err)
			}
			for _, a := range argv {
				if strings.Contains(a, secret) {
					t.Fatalf("the secret appeared in argv: %v", argv)
				}
			}
			if !contains(argv, "--stdin") {
				t.Errorf("--stdin was not passed, so ratline would not read the secret: %v", argv)
			}
		})
	}
}

// `site env set --stdin` reads NAME=value lines. The panel composes that line, so
// both halves are validated first — a name with an "=" would make the split
// ambiguous, and a newline in either would silently set a second variable.
func TestTheEnvironmentAssignmentIsComposedAndValidated(t *testing.T) {
	cat := realCatalogue(t)
	policy := policyFor(t, cat, "site env set")

	payload, err := StdinPayload(policy, Request{
		Verb: "site env set", Secret: "postgres://user:pw@host/db", SecretKey: "DATABASE_URL",
	})
	if err != nil {
		t.Fatal(err)
	}
	if payload != "DATABASE_URL=postgres://user:pw@host/db\n" {
		t.Fatalf("payload = %q", payload)
	}

	bad := []struct {
		name, key, value, why string
	}{
		{"empty name", "", "value", "there is nothing to assign to"},
		{"name with equals", "A=B", "value", "the split would be ambiguous"},
		{"name with newline", "A\nB", "value", "it would become two lines"},
		{"name starting with a digit", "1DATABASE", "value", "ratline's own env-key rule"},
		{"name with a dash", "DATABASE-URL", "value", "the same rule; systemd would not read it"},
		{"value with newline", "KEY", "one\nTWO=two", "it would set a second variable"},
		{"value with carriage return", "KEY", "one\rtwo", "the same, on a different line ending"},
	}
	for _, tc := range bad {
		if _, err := StdinPayload(policy, Request{
			Verb: "site env set", Secret: tc.value, SecretKey: tc.key,
		}); err == nil {
			t.Errorf("%s was accepted (%s)", tc.name, tc.why)
		}
	}
}

func TestARawSecretMayNotContainALineBreak(t *testing.T) {
	cat := realCatalogue(t)
	policy := policyFor(t, cat, "user password set")
	if _, err := StdinPayload(policy, Request{Verb: "user password set", Secret: "one\ntwo"}); err == nil {
		t.Fatal("a password containing a line break was accepted; ratline reads one line")
	}
	if _, err := StdinPayload(policy, Request{Verb: "user password set", Secret: "a fine password"}); err != nil {
		t.Fatalf("an ordinary password was refused: %v", err)
	}
}

// The positional arguments of a secret-bearing command must not invite somebody to
// type the value into one. `site env set <domain> [KEY=VALUE | KEY ...]` would
// otherwise offer a field called "[KEY=VALUE".
func TestASecretCommandOffersNoPositionalForTheValue(t *testing.T) {
	cat := realCatalogue(t)
	action, _, found := Lookup(cat, "site.env.set", store.RoleAdmin)
	if !found {
		t.Fatal("site env set is not available to an admin")
	}
	if len(action.Args) != 1 || action.Args[0].Name != "domain" {
		t.Fatalf("args = %+v, want exactly the domain", action.Args)
	}
	if action.Stdin == nil || action.Stdin.KeyLabel == "" {
		t.Fatal("the form is not told to ask for a variable name")
	}
}

// The global flags decide what a command *means*: --config points ratline at another
// configuration, --dry-run turns a real operation into a rehearsal and back again,
// and --yes is the confirmation the browser already collected.
func TestGlobalFlagsCannotBeSuppliedByACaller(t *testing.T) {
	cat := realCatalogue(t)
	policy := policyFor(t, cat, "site list")
	for _, name := range []string{"config", "json", "yes", "dry-run", "no-input", "quiet", "verbose"} {
		_, err := BuildArgv(cat, policy, Request{Verb: "site list",
			Flags: map[string]any{name: "/tmp/mine.yaml"}})
		if err == nil {
			t.Errorf("--%s was accepted from a request", name)
		}
	}
}

func TestUnknownFlagsAreRefused(t *testing.T) {
	cat := realCatalogue(t)
	policy := policyFor(t, cat, "site add")
	_, err := BuildArgv(cat, policy, Request{
		Verb: "site add", Args: []string{"example.com"},
		Flags: map[string]any{"force-yes": true},
	})
	if err == nil {
		t.Fatal("a flag ratline does not have was accepted")
	}
	if rlerr.CodeOf(err) != rlerr.CodeUsage {
		t.Errorf("code = %s, want usage", rlerr.CodeOf(err))
	}
}

// A positional argument that begins with a dash would be parsed as a flag. Both
// defences are checked: the value is refused outright, and a bare -- separates the
// positionals from the flags whatever they contain.
func TestPositionalArgumentsCannotBecomeFlags(t *testing.T) {
	cat := realCatalogue(t)
	policy := policyFor(t, cat, "site show")

	argv, err := BuildArgv(cat, policy, Request{Verb: "site show", Args: []string{"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	sep := indexOf(argv, "--")
	if sep < 0 {
		t.Fatal("no -- separator before the positional arguments")
	}
	if argv[len(argv)-1] != "example.com" {
		t.Fatalf("the domain is not the last argument: %v", argv)
	}
	for _, a := range argv[:sep] {
		if !strings.HasPrefix(a, "-") && !isVerbWord(a) {
			t.Errorf("%q sits before the separator and is not a flag or a verb: %v", a, argv)
		}
	}
}

// Flags are emitted as one --name=value element. Two elements would let a value
// beginning with a dash be read as the next flag.
func TestFlagsAreOneArgumentEach(t *testing.T) {
	cat := realCatalogue(t)
	policy := policyFor(t, cat, "site add")
	argv, err := BuildArgv(cat, policy, Request{
		Verb: "site add", Args: []string{"example.com"},
		Flags: map[string]any{"user": "acme", "runtime": "static"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(argv, "--user=acme") {
		t.Errorf("--user was not joined to its value: %v", argv)
	}
	if contains(argv, "--user") {
		t.Errorf("--user was emitted as a separate element: %v", argv)
	}
}

func TestControlCharactersAreRefusedBeforeExecve(t *testing.T) {
	cat := realCatalogue(t)
	policy := policyFor(t, cat, "site add")
	for _, bad := range []string{"example.com\nlocation / { root /etc; }", "a\x00b", "a\rb"} {
		if _, err := BuildArgv(cat, policy, Request{Verb: "site add", Args: []string{bad}}); err == nil {
			t.Errorf("a value containing a control character was accepted: %q", bad)
		}
		if _, err := BuildArgv(cat, policy, Request{
			Verb: "site add", Args: []string{"example.com"},
			Flags: map[string]any{"user": bad},
		}); err == nil {
			t.Errorf("a flag value containing a control character was accepted: %q", bad)
		}
	}
}

// A destructive command needs a human to have typed the target's name. The check is
// on the server, in argv construction, not only in the form.
func TestADestructiveActionRefusesWithoutConfirmation(t *testing.T) {
	cat := realCatalogue(t)
	policy := policyFor(t, cat, "site delete")
	if !policy.Destructive {
		t.Fatal("site delete is not classified as destructive")
	}
	if _, err := BuildArgv(cat, policy, Request{Verb: "site delete", Args: []string{"example.com"}}); err == nil {
		t.Fatal("a destructive command was built without a confirmation")
	}
	argv, err := BuildArgv(cat, policy, Request{
		Verb: "site delete", Args: []string{"example.com"}, Confirmed: true,
	})
	if err != nil {
		t.Fatalf("a confirmed destructive command was refused: %v", err)
	}
	if !contains(argv, "--yes") {
		t.Errorf("--yes was not passed, so ratline would stop to ask: %v", argv)
	}

	// A dry run needs no confirmation: it writes nothing, and making somebody type
	// a name to *preview* a deletion trains them to type it without reading.
	if _, err := BuildArgv(cat, policy, Request{
		Verb: "site delete", Args: []string{"example.com"}, DryRun: true,
	}); err != nil {
		t.Errorf("a dry run of a destructive command was refused: %v", err)
	}
}

func TestEveryInvocationIsNonInteractiveAndMachineReadable(t *testing.T) {
	cat := realCatalogue(t)
	for verb, args := range map[string][]string{
		"site list": nil, "site restart": {"example.com"}, "cert issue": {"example.com"},
	} {
		policy := policyFor(t, cat, verb)
		argv, err := BuildArgv(cat, policy, Request{Verb: verb, Args: args})
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		if !contains(argv, "--json") || !contains(argv, "--no-input") {
			t.Errorf("%s is missing --json or --no-input: %v", verb, argv)
		}
	}
}

func TestDryRunAndYesAreNeverBothPassed(t *testing.T) {
	cat := realCatalogue(t)
	policy := policyFor(t, cat, "site restart")
	argv, err := BuildArgv(cat, policy, Request{
		Verb: "site restart", Args: []string{"example.com"}, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(argv, "--dry-run") {
		t.Errorf("--dry-run was not passed: %v", argv)
	}
	if contains(argv, "--yes") {
		t.Errorf("--yes was passed alongside --dry-run: %v", argv)
	}
}

func TestArgvIsDeterministic(t *testing.T) {
	cat := realCatalogue(t)
	policy := policyFor(t, cat, "site add")
	req := Request{
		Verb: "site add", Args: []string{"example.com"},
		Flags: map[string]any{"user": "acme", "runtime": "node", "entry": "server.js", "node": "22"},
	}
	first, err := BuildArgv(cat, policy, req)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		next, err := BuildArgv(cat, policy, req)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(next, " ") != strings.Join(first, " ") {
			t.Fatalf("two identical requests produced different argv:\n%v\n%v", first, next)
		}
	}
}

func TestRepeatableFlagsBecomeSeveralArguments(t *testing.T) {
	cat := realCatalogue(t)
	policy := policyFor(t, cat, "site add")
	argv, err := BuildArgv(cat, policy, Request{
		Verb: "site add", Args: []string{"example.com"},
		Flags: map[string]any{"alias": []any{"www.example.com", "old.example.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(argv, "--alias=www.example.com") || !contains(argv, "--alias=old.example.com") {
		t.Errorf("a repeatable flag did not become two arguments: %v", argv)
	}
}

// The fail-safe direction of the default. A ratline release that adds a command must
// make it appear locked down, never wide open.
func TestAnUnclassifiedMutationIsSuperAdminOnly(t *testing.T) {
	mutating := &SchemaCommand{Path: "ratline invented", Name: "invented", Mutates: true}
	p, classified := PolicyFor("invented", mutating)
	if classified {
		t.Fatal("a made-up verb was reported as classified")
	}
	if p.MinRole != store.RoleSuperAdmin {
		t.Errorf("an unclassified mutation is %q, want superadmin", p.MinRole)
	}

	readOnly := &SchemaCommand{Path: "ratline invented", Name: "invented"}
	p, _ = PolicyFor("invented", readOnly)
	if p.MinRole != store.RoleAdmin {
		t.Errorf("an unclassified read is %q, want admin", p.MinRole)
	}
}

// Not a failure — the default holds the line — but it is printed so a ratline release
// that adds commands is noticed by whoever next runs the tests.
func TestEveryMutatingCommandIsClassified(t *testing.T) {
	cat := realCatalogue(t)
	missing := UnclassifiedMutations(cat)
	if len(missing) > 0 {
		t.Errorf("%d mutating ratline commands have no panel policy, so they are super-admin "+
			"only by default. Classify them in policy.go:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// A denied command must be absent from the catalogue rather than present and
// refusing: a button somebody can press and be told no is a bug report.
func TestDeniedCommandsAreAbsentForEveryRole(t *testing.T) {
	cat := realCatalogue(t)
	for _, role := range []string{store.RoleAdmin, store.RoleSuperAdmin} {
		for _, a := range Actions(cat, role) {
			p, _ := PolicyFor(a.Verb, cat.Leaves[a.Verb])
			if p.Denied {
				t.Errorf("%q is denied but offered to %s", a.Verb, role)
			}
		}
		if _, _, found := Lookup(cat, "mcp", role); found {
			t.Errorf("mcp is reachable by %s", role)
		}
		if _, _, found := Lookup(cat, "config.edit", role); found {
			t.Errorf("config edit is reachable by %s, and it would hang holding the lock", role)
		}
	}
}

// An admin's browser must never receive the super-admin operations. Hiding a button
// is not a control; not sending it is.
func TestAnAdminIsNotOfferedSuperAdminActions(t *testing.T) {
	cat := realCatalogue(t)
	for _, a := range Actions(cat, store.RoleAdmin) {
		if a.MinRole == store.RoleSuperAdmin {
			t.Errorf("%q is super-admin only but was offered to an admin", a.Verb)
		}
	}
	// The negative case: a super admin does get strictly more, or the filter above
	// would pass for an implementation that returned nothing.
	admin := len(Actions(cat, store.RoleAdmin))
	super := len(Actions(cat, store.RoleSuperAdmin))
	if super <= admin {
		t.Fatalf("a super admin sees %d actions and an admin sees %d; the filter is not doing anything",
			super, admin)
	}
	if _, _, found := Lookup(cat, "user.delete", store.RoleAdmin); found {
		t.Error("an admin can reach 'user delete'")
	}
	if _, _, found := Lookup(cat, "user.delete", store.RoleSuperAdmin); !found {
		t.Error("a super admin cannot reach 'user delete'")
	}
}

// The forms are generated from the schema, so a secret must never appear among the
// flags a browser is told about.
func TestNoActionOffersStdinAsAFormField(t *testing.T) {
	cat := realCatalogue(t)
	for _, a := range Actions(cat, store.RoleSuperAdmin) {
		for _, f := range a.Flags {
			if f.Name == "stdin" {
				t.Errorf("%q offers --stdin as a form field; the panel sets it itself", a.Verb)
			}
		}
		if a.Stdin == nil {
			continue
		}
		// A command declared as reading standard input must actually have the flag
		// that tells it to, or the panel would send a value nothing reads.
		if _, ok := cat.Flag(a.Verb, "stdin"); !ok {
			t.Errorf("%q is declared as reading standard input but ratline has no --stdin for it", a.Verb)
		}
	}
}

// `--engine` is declared once, as a persistent flag on `db`, and applies to every verb
// under it. The schema reports it on the group and nowhere else, so a panel that only
// read each leaf's own flags offered no engine field — and every database action it
// ran went to MongoDB with no way to choose otherwise.
func TestGroupLevelFlagsReachTheLeaves(t *testing.T) {
	cat := realCatalogue(t)
	for _, verb := range []string{"db create", "db list", "db drop", "db user add", "db install"} {
		action, _, found := Lookup(cat, verb, store.RoleSuperAdmin)
		if !found {
			t.Fatalf("%q is not available to a super admin", verb)
		}
		var engine *ActionFlag
		for i := range action.Flags {
			if action.Flags[i].Name == "engine" {
				engine = &action.Flags[i]
			}
		}
		if engine == nil {
			t.Errorf("%q offers no --engine field, so it can only ever reach MongoDB", verb)
			continue
		}
		// And the help text must name every engine, because the form builds its
		// options by reading it.
		for _, want := range []string{"mongo", "mysql", "redis"} {
			if !strings.Contains(engine.Usage, want) {
				t.Errorf("%q's --engine usage does not mention %s: %q", verb, want, engine.Usage)
			}
		}
	}

	// A leaf that declares a name itself keeps its own, and nothing gains a flag it
	// has no business having.
	site, _, _ := Lookup(cat, "site.add", store.RoleAdmin)
	for _, f := range site.Flags {
		if f.Name == "engine" {
			t.Error("site add gained --engine, which belongs to the db group")
		}
	}
}

// The globals must not arrive this way. They are published once in the schema's own
// global_flags and set by the panel, never by a request.
func TestGlobalFlagsAreNotOfferedAsFields(t *testing.T) {
	cat := realCatalogue(t)
	for _, a := range Actions(cat, store.RoleSuperAdmin) {
		for _, f := range a.Flags {
			if isGlobalFlagName(f.Name) {
				t.Errorf("%q offers the global --%s as a form field", a.Verb, f.Name)
			}
		}
	}
}

func TestRuntimeSpecificFlagsAreRecognised(t *testing.T) {
	cat := realCatalogue(t)
	action, _, found := Lookup(cat, "site.add", store.RoleAdmin)
	if !found {
		t.Fatal("site add is not available to an admin")
	}
	byName := map[string][]string{}
	for _, f := range action.Flags {
		byName[f.Name] = f.Runtime
	}
	// ratline documents these as "python: …" and "node, bun: …", so the form can
	// hide the thirty flags that do not belong to the runtime being provisioned.
	if got := byName["app-module"]; len(got) != 1 || got[0] != "python" {
		t.Errorf("--app-module runtimes = %v, want [python]", got)
	}
	if got := byName["entry"]; len(got) != 2 {
		t.Errorf("--entry runtimes = %v, want node and bun", got)
	}
	if got := byName["user"]; len(got) != 0 {
		t.Errorf("--user is not runtime-specific but was tagged %v", got)
	}
}

func TestTitlesReadAsActions(t *testing.T) {
	for verb, want := range map[string]string{
		"site deploy":            "Deploy site",
		"site deploy-key rotate": "Rotate site deploy key",
		"status":                 "Status",
		"db user add":            "Add db user",
	} {
		if got := titleFor(verb); got != want {
			t.Errorf("titleFor(%q) = %q, want %q", verb, got, want)
		}
	}
}

func indexOf(list []string, want string) int {
	for i, v := range list {
		if v == want {
			return i
		}
	}
	return -1
}

func isVerbWord(s string) bool {
	return !strings.HasPrefix(s, "-") && !strings.Contains(s, "=")
}

// ratline accepts more positionals than the panel's form shows — `site env set` takes a
// KEY=VALUE one perfectly well — and an extra one is how a secret would reach
// /proc/PID/cmdline and the panel's own action log. The count is enforced where the argv
// is built, not only where the form is drawn.
func TestExtraPositionalsAreRefused(t *testing.T) {
	cat := realCatalogue(t)

	policy := policyFor(t, cat, "site env set")
	_, err := BuildArgv(cat, policy, Request{Verb: "site env set", Args: []string{"example.com", "DATABASE_URL=postgres://u:p@h/db"}})
	if err == nil {
		t.Fatal("a KEY=VALUE positional was accepted for site env set; the value would sit in the process table")
	}
	if _, err := BuildArgv(cat, policy, Request{Verb: "site env set", Args: []string{"example.com"}}); err != nil {
		t.Fatalf("the declared positional was refused: %v", err)
	}

	if _, err := BuildArgv(cat, policyFor(t, cat, "site list"), Request{Verb: "site list", Args: []string{"example.com"}}); err == nil {
		t.Error("a positional was accepted for a command that takes none")
	}

	// A usage line spelled with an ellipsis takes any number.
	//
	// Every one of them, in a fixed order. This stopped at the first it found in a map,
	// so which command it actually checked depended on Go's map ordering — and the day
	// a variadic command needing a typed confirmation was added, it began failing on
	// some runs and passing on others for a reason that had nothing to do with counting
	// positionals.
	variadic := variadicVerbs(cat)
	if len(variadic) == 0 {
		t.Fatal("no command declares a variadic positional; this test is checking nothing")
	}
	for _, verb := range variadic {
		policy, _ := PolicyFor(verb, cat.Leaves[verb])
		if policy.Denied {
			continue
		}
		req := Request{Verb: verb, Args: []string{"a.example.com", "b.example.com", "c.example.com"}}
		// Orthogonal to the count, and refused before the argv is built.
		req.Confirmed = policy.Destructive
		if _, err := BuildArgv(cat, policy, req); err != nil {
			t.Errorf("%s declares a variadic positional but refused three: %v", verb, err)
		}
	}
}

// variadicVerbs lists the commands that take any number of positionals, sorted.
//
// The effective list, not the usage line: a policy that names its own positionals wins,
// which is how `site exec` asks for one command string rather than offering a box
// labelled "args..." that arrives as a single argument.
func variadicVerbs(cat *Catalogue) []string {
	var out []string
	for verb, cmd := range cat.Leaves {
		policy, _ := PolicyFor(verb, cmd)
		names := cmd.Args
		if len(policy.Args) > 0 {
			names = policy.Args
		}
		for _, a := range names {
			if strings.Contains(a, "...") {
				out = append(out, verb)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// pflag stops reading flags at `--`, so a --config appended after the positionals is
// handed to ratline as one more positional and the configured file is silently ignored —
// for exactly the commands that name a site.
func TestTheConfigFlagComesBeforeThePositionals(t *testing.T) {
	c := &Client{ConfigPath: "/etc/ratline/other.yaml"}
	got := c.globals("site", "show", "--json", "--", "example.com")
	want := []string{"site", "show", "--json", "--config=/etc/ratline/other.yaml", "--", "example.com"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("globals = %v, want %v", got, want)
	}
	got = c.globals("site", "list", "--json")
	if got[len(got)-1] != "--config=/etc/ratline/other.yaml" {
		t.Errorf("with no positionals the flag should simply be appended: %v", got)
	}
	if got := (&Client{}).globals("site", "list"); len(got) != 2 {
		t.Errorf("no ConfigPath, no flag: %v", got)
	}
}

// `site exec` is the one action whose positional argument is somebody else's command,
// which makes it the one place where a panel request could most plausibly become an
// argv ratline did not intend. Three things have to hold: the command arrives as a
// single positional after a bare --, a fourth positional is refused, and the action is
// not offered to an admin at all.
func TestSiteExecIsOneCommandAfterADoubleDash(t *testing.T) {
	cat := realCatalogue(t)
	policy := policyFor(t, cat, "site exec")

	argv, err := BuildArgv(cat, policy, Request{
		Verb: "site exec", Args: []string{"app.example.com", "npm run bootstrap"},
		Confirmed: true,
	})
	if err != nil {
		t.Fatalf("BuildArgv = %v", err)
	}
	line := strings.Join(argv, " ")
	if !strings.HasSuffix(line, "-- app.example.com npm run bootstrap") {
		t.Errorf("argv = %q, want the positionals last, after a bare --", line)
	}
	if i := indexOf(argv, "--"); i < 0 || indexOf(argv, "app.example.com") != i+1 {
		t.Errorf("argv = %q, want the domain immediately after the --", line)
	}

	// A request cannot smuggle in a third positional and split the command itself:
	// the policy says two, so two is what ratline is handed.
	if _, err := BuildArgv(cat, policy, Request{
		Verb: "site exec", Args: []string{"app.example.com", "npm", "run"},
		Confirmed: true,
	}); err == nil {
		t.Error("a third positional was accepted for site exec")
	}

	// Arbitrary code as the tenant is not an admin's button.
	if _, _, found := Lookup(cat, "site exec", store.RoleAdmin); found {
		t.Error("site exec is offered to an admin")
	}
	if _, _, found := Lookup(cat, "site exec", store.RoleSuperAdmin); !found {
		t.Error("site exec is not reachable by a super admin")
	}
	if !policy.Destructive {
		t.Error("site exec runs without the domain being typed back")
	}
	if !policy.Long {
		t.Error("site exec is not a job, so a command outliving the tab loses its output")
	}
}
