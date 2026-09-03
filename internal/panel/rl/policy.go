package rl

import (
	"sort"
	"strings"

	"github.com/ALIRAZA47/ratline-cli/internal/panel/store"
)

// Policy is what the panel decides about a command, as opposed to what the binary
// declares about it.
//
// The split matters. Whether `site add` mutates, and which flags it takes, are facts
// about the installed binary and are read from `ratline schema`. Whether an admin may
// run it, whether it needs a name typed back first, and whether it belongs in a job
// queue are judgements — so they are written here, once, where they can be read and
// argued with.
type Policy struct {
	// Denied keeps a command out of the panel altogether. Absent from the
	// catalogue, not present-and-refusing: a button that always fails is a bug
	// report waiting to happen, and a refusal somebody can retry is an invitation.
	Denied bool
	// DeniedWhy is shown in the catalogue listing so the absence is explained
	// rather than mysterious.
	DeniedWhy string
	// MinRole is the lowest role that may run it.
	MinRole string
	// Destructive requires the operator to type the target's name before it runs,
	// the same discipline `ConfirmTyped` enforces at a terminal. A y/N is too easy
	// to hit by reflex when the thing being deleted is somebody's site.
	Destructive bool
	// Long runs it as a job with a transcript rather than inside the request. A
	// deploy outlives the browser tab that started it.
	Long bool
	// Stdin describes the value this command reads from standard input, if it
	// reads one. It is not a flag and never becomes one: /proc/PID/cmdline is
	// world-readable, so a credential in argv is a credential every account on
	// the server can read for as long as the command runs.
	Stdin *StdinSpec
	// Args overrides the positional arguments read out of the command's usage
	// line. Needed where that line is prose rather than a list — `site env set
	// <domain> [KEY=VALUE | KEY ...]` parses into nonsense, and a form built from
	// it would invite somebody to type a secret into a positional argument, which
	// is the one thing this whole arrangement exists to prevent.
	Args []string
	// Group places the action in the panel's navigation.
	Group string
}

// StdinSpec describes what a command expects on standard input.
//
// The shape matters because it is not always the bare secret. `user password set`
// reads the password verbatim; `site env set` reads KEY=VALUE lines, so the panel
// asks for a name as well and composes the line itself — which keeps the value out of
// argv, where typing it as a positional assignment would put it.
type StdinSpec struct {
	// Label names the field in the form.
	Label string `json:"label"`
	// Help says what the value is and why it travels this way.
	Help string `json:"help"`
	// KeyLabel, when set, means the form also asks for a name and the panel writes
	// "NAME=value" to stdin rather than the value alone.
	KeyLabel string `json:"key_label,omitempty"`
}

// Groups, in the order the panel shows them.
const (
	GroupOverview  = "overview"
	GroupSites     = "sites"
	GroupUsers     = "users"
	GroupKeys      = "keys"
	GroupCerts     = "certs"
	GroupDatabases = "databases"
	GroupRuntimes  = "runtimes"
	GroupServer    = "server"
)

// policies is the classification, keyed by verb.
//
// It does not have to be complete, and that is the safety property: anything absent
// falls to defaultPolicy, which puts read-only commands in an admin's hands and every
// unclassified *mutation* behind a super admin. A ratline release that adds a command
// therefore appears in the panel locked down rather than wide open, and a test lists
// the unclassified mutations so the gap is visible rather than silent.
var policies = map[string]Policy{
	// ── Sites ──────────────────────────────────────────────────────────────────
	// The day-to-day surface. An admin exists to run this.
	"site add":           {MinRole: store.RoleAdmin, Long: true, Group: GroupSites},
	"site list":          {MinRole: store.RoleAdmin, Group: GroupSites},
	"site show":          {MinRole: store.RoleAdmin, Group: GroupSites},
	"site status":        {MinRole: store.RoleAdmin, Group: GroupSites},
	"site health":        {MinRole: store.RoleAdmin, Group: GroupSites},
	"site logs":          {MinRole: store.RoleAdmin, Group: GroupSites},
	"site enable":        {MinRole: store.RoleAdmin, Group: GroupSites},
	"site disable":       {MinRole: store.RoleAdmin, Group: GroupSites},
	"site start":         {MinRole: store.RoleAdmin, Group: GroupSites},
	"site stop":          {MinRole: store.RoleAdmin, Group: GroupSites},
	"site restart":       {MinRole: store.RoleAdmin, Group: GroupSites},
	"site reload":        {MinRole: store.RoleAdmin, Group: GroupSites},
	"site scale":         {MinRole: store.RoleAdmin, Group: GroupSites},
	"site deploy":        {MinRole: store.RoleAdmin, Long: true, Group: GroupSites},
	"site clone":         {MinRole: store.RoleAdmin, Long: true, Group: GroupSites},
	"site runtime":       {MinRole: store.RoleAdmin, Long: true, Group: GroupSites},
	"site troubleshoot":  {MinRole: store.RoleAdmin, Group: GroupSites},
	"site alias add":     {MinRole: store.RoleAdmin, Group: GroupSites},
	"site alias remove":  {MinRole: store.RoleAdmin, Destructive: true, Group: GroupSites},
	"site hook set":      {MinRole: store.RoleAdmin, Group: GroupSites},
	"site hook clear":    {MinRole: store.RoleAdmin, Group: GroupSites},
	"site cron add":      {MinRole: store.RoleAdmin, Group: GroupSites},
	"site cron list":     {MinRole: store.RoleAdmin, Group: GroupSites},
	"site cron remove":   {MinRole: store.RoleAdmin, Destructive: true, Group: GroupSites},
	"site cron run":      {MinRole: store.RoleAdmin, Long: true, Group: GroupSites},
	"site cron logs":     {MinRole: store.RoleAdmin, Group: GroupSites},
	"site worker add":    {MinRole: store.RoleAdmin, Group: GroupSites},
	"site worker list":   {MinRole: store.RoleAdmin, Group: GroupSites},
	"site worker remove": {MinRole: store.RoleAdmin, Destructive: true, Group: GroupSites},
	"site worker logs":   {MinRole: store.RoleAdmin, Group: GroupSites},
	// The name and the value arrive on stdin as NAME=value, which is what --stdin
	// reads. Typing it as a positional assignment works too, and also puts a
	// database URL in the process table for every tenant on the box to read.
	"site env set": {
		MinRole: store.RoleAdmin, Group: GroupSites,
		Args: []string{"<domain>"},
		Stdin: &StdinSpec{
			Label:    "value",
			KeyLabel: "variable",
			Help: "The name and the value are written to ratline on standard input as " +
				"NAME=value, so the value never appears in the process table.",
		},
	},
	"site env get":           {MinRole: store.RoleAdmin, Group: GroupSites},
	"site env list":          {MinRole: store.RoleAdmin, Group: GroupSites},
	"site env unset":         {MinRole: store.RoleAdmin, Destructive: true, Group: GroupSites},
	"site env import":        {MinRole: store.RoleSuperAdmin, Group: GroupSites},
	"site deploy-key create": {MinRole: store.RoleAdmin, Group: GroupSites},
	"site deploy-key show":   {MinRole: store.RoleAdmin, Group: GroupSites},
	"site deploy-key rotate": {MinRole: store.RoleAdmin, Group: GroupSites},
	"site deploy-key remove": {MinRole: store.RoleAdmin, Destructive: true, Group: GroupSites},
	// Deleting a site takes a tenant's application off the internet, and --purge
	// takes their files with it.
	"site delete": {MinRole: store.RoleSuperAdmin, Destructive: true, Long: true, Group: GroupSites},

	// The composite provisioners, which are the fastest way to a working site and
	// also the slowest to run: they clone, install, build and issue.
	"new static": {MinRole: store.RoleAdmin, Long: true, Group: GroupSites},
	"new node":   {MinRole: store.RoleAdmin, Long: true, Group: GroupSites},
	"new bun":    {MinRole: store.RoleAdmin, Long: true, Group: GroupSites},
	"new python": {MinRole: store.RoleAdmin, Long: true, Group: GroupSites},

	// ── Tenants ────────────────────────────────────────────────────────────────
	"user add":     {MinRole: store.RoleAdmin, Group: GroupUsers},
	"user list":    {MinRole: store.RoleAdmin, Group: GroupUsers},
	"user show":    {MinRole: store.RoleAdmin, Group: GroupUsers},
	"user enable":  {MinRole: store.RoleAdmin, Group: GroupUsers},
	"user disable": {MinRole: store.RoleAdmin, Group: GroupUsers},
	"user password set": {
		MinRole: store.RoleSuperAdmin, Group: GroupUsers,
		Args:  []string{"<username>"},
		Stdin: &StdinSpec{Label: "password", Help: "Read by ratline from standard input."},
	},
	// Deleting a tenant cascades to their sites; --purge takes the home directory.
	"user delete": {MinRole: store.RoleSuperAdmin, Destructive: true, Long: true, Group: GroupUsers},
	// sudo is the one escape hatch out of the tenant sandbox. It is a super admin's
	// decision every time, and ratline still validates the grant with visudo -c.
	"user sudo grant":  {MinRole: store.RoleSuperAdmin, Destructive: true, Group: GroupUsers},
	"user sudo revoke": {MinRole: store.RoleSuperAdmin, Group: GroupUsers},
	"user sudo list":   {MinRole: store.RoleAdmin, Group: GroupUsers},

	// ── SSH keys ───────────────────────────────────────────────────────────────
	"key add":    {MinRole: store.RoleAdmin, Group: GroupKeys},
	"key list":   {MinRole: store.RoleAdmin, Group: GroupKeys},
	"key show":   {MinRole: store.RoleAdmin, Group: GroupKeys},
	"key test":   {MinRole: store.RoleAdmin, Group: GroupKeys},
	"key audit":  {MinRole: store.RoleAdmin, Group: GroupKeys},
	"key move":   {MinRole: store.RoleAdmin, Group: GroupKeys},
	"key remove": {MinRole: store.RoleAdmin, Destructive: true, Group: GroupKeys},
	"key revoke": {MinRole: store.RoleAdmin, Destructive: true, Group: GroupKeys},
	// A sweep, not a single revocation: it can remove many keys in one run, and a
	// mistake here is somebody's access to a server they are holding.
	"key prune": {MinRole: store.RoleSuperAdmin, Destructive: true, Group: GroupKeys},
	"key sync":  {MinRole: store.RoleSuperAdmin, Group: GroupKeys},

	// ── Certificates ───────────────────────────────────────────────────────────
	// Issuance spends a rate-limit budget, so it is a job with a transcript: the
	// preflight detail is the useful part and it should not vanish with the tab.
	"cert issue":              {MinRole: store.RoleAdmin, Long: true, Group: GroupCerts},
	"cert renew":              {MinRole: store.RoleAdmin, Long: true, Group: GroupCerts},
	"cert test-renewal":       {MinRole: store.RoleAdmin, Long: true, Group: GroupCerts},
	"cert list":               {MinRole: store.RoleAdmin, Group: GroupCerts},
	"cert show":               {MinRole: store.RoleAdmin, Group: GroupCerts},
	"cert attach":             {MinRole: store.RoleAdmin, Group: GroupCerts},
	"cert detach":             {MinRole: store.RoleAdmin, Destructive: true, Group: GroupCerts},
	"cert import":             {MinRole: store.RoleSuperAdmin, Group: GroupCerts},
	"cert selfsign":           {MinRole: store.RoleAdmin, Group: GroupCerts},
	"cert auto-renew status":  {MinRole: store.RoleAdmin, Group: GroupCerts},
	"cert auto-renew enable":  {MinRole: store.RoleAdmin, Group: GroupCerts},
	"cert auto-renew disable": {MinRole: store.RoleAdmin, Destructive: true, Group: GroupCerts},
	"cert account show":       {MinRole: store.RoleAdmin, Group: GroupCerts},
	"cert account register":   {MinRole: store.RoleSuperAdmin, Group: GroupCerts},
	// Revocation is announced to the CA and cannot be taken back.
	"cert revoke": {MinRole: store.RoleSuperAdmin, Destructive: true, Group: GroupCerts},
	"cert delete": {MinRole: store.RoleSuperAdmin, Destructive: true, Group: GroupCerts},
	// certbot invokes this itself, while an issuance holds the lock. It is
	// machinery, not an operation.
	"cert deploy-hook": {Denied: true, DeniedWhy: "certbot calls this itself during a renewal"},

	// ── Databases ──────────────────────────────────────────────────────────────
	"db list":          {MinRole: store.RoleAdmin, Group: GroupDatabases},
	"db show":          {MinRole: store.RoleAdmin, Group: GroupDatabases},
	"db ping":          {MinRole: store.RoleAdmin, Group: GroupDatabases},
	"db create":        {MinRole: store.RoleAdmin, Group: GroupDatabases},
	"db roles":         {MinRole: store.RoleAdmin, Group: GroupDatabases},
	"db dump":          {MinRole: store.RoleAdmin, Long: true, Group: GroupDatabases},
	"db enable":        {MinRole: store.RoleAdmin, Group: GroupDatabases},
	"db disable":       {MinRole: store.RoleAdmin, Destructive: true, Group: GroupDatabases},
	"db user add":      {MinRole: store.RoleAdmin, Group: GroupDatabases},
	"db user list":     {MinRole: store.RoleAdmin, Group: GroupDatabases},
	"db user grant":    {MinRole: store.RoleAdmin, Group: GroupDatabases},
	"db user password": {MinRole: store.RoleAdmin, Group: GroupDatabases},
	"db user delete":   {MinRole: store.RoleSuperAdmin, Destructive: true, Group: GroupDatabases},
	"db drop":          {MinRole: store.RoleSuperAdmin, Destructive: true, Group: GroupDatabases},
	// Restoring writes over whatever is in the database now.
	"db restore": {MinRole: store.RoleSuperAdmin, Destructive: true, Long: true, Group: GroupDatabases},
	// Installing a database server is the one thing ratline installs rather than
	// configures, and it takes an admin password that must not reach argv.
	"db install": {
		MinRole: store.RoleSuperAdmin, Long: true, Group: GroupDatabases,
		Stdin: &StdinSpec{Label: "admin password", Help: "Read by ratline from standard input."},
	},
	"db connect": {
		MinRole: store.RoleSuperAdmin, Group: GroupDatabases,
		Stdin: &StdinSpec{
			Label: "connection string",
			Help:  "The admin URI. Read from standard input; it never appears in argv or in the audit record.",
		},
	},
	// The firewall. ratline never enables or disables ufw, and widening a database
	// port to an address is not a thing to do from a form without a super admin.
	"db access list":   {MinRole: store.RoleAdmin, Group: GroupDatabases},
	"db access allow":  {MinRole: store.RoleSuperAdmin, Destructive: true, Group: GroupDatabases},
	"db access revoke": {MinRole: store.RoleSuperAdmin, Group: GroupDatabases},

	// ── Runtimes ───────────────────────────────────────────────────────────────
	"runtime list":    {MinRole: store.RoleAdmin, Group: GroupRuntimes},
	"runtime install": {MinRole: store.RoleAdmin, Long: true, Group: GroupRuntimes},
	"runtime default": {MinRole: store.RoleSuperAdmin, Group: GroupRuntimes},

	// ── The server itself ──────────────────────────────────────────────────────
	"status":           {MinRole: store.RoleAdmin, Group: GroupOverview},
	"doctor":           {MinRole: store.RoleAdmin, Group: GroupOverview},
	"troubleshoot":     {MinRole: store.RoleAdmin, Group: GroupOverview},
	"explain":          {MinRole: store.RoleAdmin, Group: GroupOverview},
	"version":          {MinRole: store.RoleAdmin, Group: GroupOverview},
	"export":           {MinRole: store.RoleAdmin, Group: GroupServer},
	"backup":           {MinRole: store.RoleAdmin, Long: true, Group: GroupServer},
	"config show":      {MinRole: store.RoleAdmin, Group: GroupServer},
	"config get":       {MinRole: store.RoleAdmin, Group: GroupServer},
	"config path":      {MinRole: store.RoleAdmin, Group: GroupServer},
	"config reference": {MinRole: store.RoleAdmin, Group: GroupServer},
	"config validate":  {MinRole: store.RoleAdmin, Group: GroupServer},
	"config set":       {MinRole: store.RoleSuperAdmin, Group: GroupServer},
	"config unset":     {MinRole: store.RoleSuperAdmin, Destructive: true, Group: GroupServer},
	"reconcile":        {MinRole: store.RoleSuperAdmin, Long: true, Group: GroupServer},
	"init":             {MinRole: store.RoleSuperAdmin, Group: GroupServer},
	"update":           {MinRole: store.RoleSuperAdmin, Long: true, Group: GroupServer},
	// A restore rewrites the server from an archive; an import rewrites the state
	// database from a manifest. Both are recovery operations with a blast radius
	// the size of the machine.
	"restore": {MinRole: store.RoleSuperAdmin, Destructive: true, Long: true, Group: GroupServer},
	"import":  {MinRole: store.RoleSuperAdmin, Destructive: true, Long: true, Group: GroupServer},

	// ── Not for a browser ──────────────────────────────────────────────────────
	// config edit spawns $EDITOR and waits for it. Over HTTP that is a request
	// that never returns and a lock nobody can release.
	"config edit": {Denied: true, DeniedWhy: "it opens an editor and waits; use config set, or edit the file over SSH"},
	// A stdio JSON-RPC server. Starting one from a web request would hand the panel
	// a child that never exits.
	"mcp": {Denied: true, DeniedWhy: "it is a stdio server for agents, not an operation"},
	// Both write files for a shell or a man reader, neither of which is here.
	"man":    {Denied: true, DeniedWhy: "it generates man pages for a terminal"},
	"schema": {Denied: true, DeniedWhy: "the panel reads it already; it is how these forms are built"},
}

// defaultPolicy is what an unclassified command gets.
//
// Read-only commands fall to an admin, because the worst case is that somebody sees
// something. Mutations fall to a super admin, because the worst case is not. The
// direction of the default is the whole point: a ratline upgrade that adds a command
// makes it appear locked down, never wide open.
func defaultPolicy(cmd *SchemaCommand) Policy {
	p := Policy{MinRole: store.RoleAdmin, Group: GroupServer}
	if cmd.Mutates {
		p.MinRole = store.RoleSuperAdmin
	}
	return p
}

// PolicyFor returns the policy for a verb, and whether it was classified by hand.
func PolicyFor(verb string, cmd *SchemaCommand) (Policy, bool) {
	if p, ok := policies[verb]; ok {
		if p.MinRole == "" && !p.Denied {
			p.MinRole = defaultPolicy(cmd).MinRole
		}
		if p.Group == "" {
			p.Group = defaultPolicy(cmd).Group
		}
		return p, true
	}
	// A subcommand of something denied is denied: `completion bash` should not
	// arrive on its own merits because nobody wrote a line for it.
	for prefix, p := range policies {
		if p.Denied && strings.HasPrefix(verb, prefix+" ") {
			return p, true
		}
	}
	return defaultPolicy(cmd), false
}

// UnclassifiedMutations lists mutating commands with no hand-written policy.
//
// Not an error — the default already holds the line — but a list the test suite
// prints, so that a ratline release adding a command is noticed by whoever next runs
// the tests rather than by whoever next wonders why an admin cannot press a button.
func UnclassifiedMutations(cat *Catalogue) []string {
	var out []string
	for verb, cmd := range cat.Leaves {
		if !cmd.Mutates {
			continue
		}
		if _, classified := PolicyFor(verb, cmd); !classified {
			out = append(out, verb)
		}
	}
	sort.Strings(out)
	return out
}
