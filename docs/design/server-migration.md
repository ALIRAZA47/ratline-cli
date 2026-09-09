# `ratline migrate` — moving a server to another server

Status: **plan**. Nothing below is built.

## The problem

An operator on a VPS that is too small, in the wrong region, or on a provider they are
leaving, wants everything on it to exist on a different box: every tenant, their keys,
their sites, the code, the `.env` files, the databases, the scheduled jobs. Today the
honest answer is a runbook — `export`, then `import`, then a `backup` per site, then a
`restore` per site, then `db dump` and `db restore` per database, then re-issue every
certificate, then fix the connection strings by hand because the passwords changed.

Every one of those steps exists and works. What does not exist is the thing that runs them
in the right order against two machines, remembers where it got to, and tells you at the
end what is *not* the same — which is the part an operator cannot reconstruct from a clean
exit code.

## What already exists (and what this must not reinvent)

| Piece | Where | What it gives the migration |
|---|---|---|
| `ratline export` | `cmd_doctor.go:711`, `state.Export` (`repo_ops.go:239`) | users, sites, ssh_keys, certificates, ports, site_units as JSON. No private key material |
| `ratline import <file>` | `cmd_import.go` | rebuilds tenants, keys, site shape and aliases from an export. Already documents its own gaps |
| `ratline backup` / `ratline restore` | `cmd_site_extra.go:429`, `internal/site/restore.go` | a site or home as a gzipped tar including `.env` and the manifest; a restore that rebuilds the state row from the travelling manifest, re-renders vhost and unit, sets ownership **from the local account** and swaps atomically |
| `.ratline/site.yaml` | `internal/site/manifest.go` | the only record of a site that travels with the site's own directory |
| `ratline db dump` / `db restore` | `cmd_db_dump.go` | per-database archives, `--into` to land somewhere else |
| `compose.go` | `internal/cli/compose.go` | plan-then-execute, an undo stack, and the rule that composites compose *commands* |
| `system.Runner` binary registry | `internal/system/bin.go` | `ssh`, `ssh-keyscan`, `rsync`, `tar` are all already registered |
| `diag`'s Host-header probe | `internal/diag/site.go:383` (`timedGet(ctx, env, client, url, host)`) | already takes a `Host` separately from the URL — which is exactly how you verify a site on a new IP **before** DNS moves |
| `state.Health` | `repo_health.go` | one row per site with `consecutive_failures` and `failing_since` — the before/after comparison |

The migration is mostly an *orchestrator*. The one genuinely new primitive is talking to a
second machine.

---

## Direction: this is a **pull**, run on the new server

`ratline migrate pull root@old-server` runs on the target — the empty box the operator has
just provisioned and is already sitting on.

Three reasons, in order of how much they matter:

1. **The source stays untouched and serving until cutover.** A pull touches the source with
   `ratline export --json` and `tar --create` to stdout. Both are reads. A push necessarily
   runs mutating code on the machine that is currently earning money, and the failure mode
   of a push is a half-modified live server. This is the argument that decides it.
2. **It is `restore` at server scale.** `restore` already runs on the machine being built,
   takes files that came from somewhere else, and rebuilds ownership from the accounts *as
   they exist here* rather than from the uids in the archive. Migration is the same shape,
   so it inherits the same reasoning and, in phase M3, literally the same code.
3. **Rollback is "stop and walk away."** If a pull fails at any point, the source is exactly
   as it was. There is nothing to unwind on the machine that matters.

A `migrate push` is deliberately **not** offered. It looks symmetrical and is not: it puts
mutation on the wrong box and turns the source into the thing that can be left broken.

### The host key is not optional

The stream carries every tenant's `.env`. `StrictHostKeyChecking=no` on this command means
shipping your customers' database passwords to whoever answers on that address.

- `--known-hosts <path>` uses an existing file, and that is the quiet path for automation.
- Otherwise `migrate plan` runs `ssh-keyscan` (already in the registry), prints the
  fingerprint, and requires a typed confirmation — the same treatment `sshd`'s
  `PermitRootLogin` gets. It writes a pinned known-hosts file for the run.
- There is no flag that skips this. `--yes` does not skip it either; `--yes` means "do not
  ask me to confirm the destructive thing you already described", not "accept an unknown
  server".

### The source-side credential

Phase M2 takes an operator-supplied identity (`--identity /root/.ssh/migrate_ed25519`) with
root on the source. Fine, and honest about what it is.

Phase M7+ can do better, and the machinery is already here. `cmd/ratline-shell` exists as a
forced command for scoped keys, and `key add --scope` exists. `ratline migrate key` on the
**source** would mint a key whose forced command permits `ratline export --json` and
`tar --create -C <site dir>` and nothing else — a credential that can read the server it is
leaving and cannot change it. That is a much better thing to hand a migration than root,
and it is the natural end state.

---

## The command surface

Group/verb, per `docs/reference/command-surface.md`.

```
ratline migrate plan   <root@host[:port]>   # preflight + resolved plan; mutates nothing
ratline migrate run    <root@host[:port]>   # execute, journalled, resumable
ratline migrate status [<id>]               # the journal; --json is what the panel polls
ratline migrate verify <root@host[:port]>   # health both sides, DNS-independent; re-runnable
ratline migrate abort  <id>                 # unwind what this run created on THIS server
ratline migrate cutover                     # run on the SOURCE: the checklist, and 503 the sites
ratline migrate key                         # run on the SOURCE: mint a read-only migration key (M7)
```

Selection and mapping flags on `plan` and `run`:

| Flag | Effect |
|---|---|
| `--only site=a.com,b.com` / `--only user=acme` | migrate a subset; repeatable |
| `--skip files` / `--skip databases` / `--skip keys` | when one part is being handled another way |
| `--owner-map old=new` | land a tenant under a different account name on the target |
| `--with-history` | bring deployments and events too (off by default: it is history, and it is large) |
| `--identity`, `--known-hosts`, `--port` | how to reach the source |
| `--resume <id>` | continue a journalled run |
| `--concurrency N` | sites in parallel; default 1, because a parallel migration that fails is much harder to read |

`--dry-run` is not the preview here. `CLAUDE.md` already records why: *"A command that
composes other commands cannot rehearse itself by running them with `--dry-run`"* — the
second step is told "no such user" and the preview reports a failure for something
perfectly buildable. `migrate plan` **is** the preview; it resolves everything and executes
nothing, exactly as `new`, `import` and `clone` do.

---

## What moves, in dependency order

Each stage is a compose-transaction. Its steps unwind together; the run as a whole does
not.

### 0. Preflight (`plan` only, and again at the top of `run`)

The point of preflight is that a migration should fail in the first thirty seconds or not
at all. Everything here is cheap and every one of them is a real way migrations die:

- The source answers, the host key is pinned, `ratline version` on the far end is
  understood. **A source running a newer state schema than this binary is a refusal** —
  the same rule the state database already applies to itself.
- **Free space.** `system.DirSize` over each source home versus free space on the target,
  plus headroom for the staging copy. This is the single most common way a migration dies
  four hours in.
- Domains: none of the incoming domains or aliases already exist on the target.
- Owners: every source tenant either does not exist on the target, or exists and
  `--owner-map` says so deliberately. A silent merge into an existing account is a refusal.
- Runtimes: every `node_version` / `python_version` the sites name is installed on the
  target, or `--install-runtimes` says to fetch them.
- Database engines: for every engine a site uses, that engine is present on the target and
  `features.db_provisioning` is on.
- Ports: sites that listen on TCP get fresh allocations from the target's own range; a
  collision is not an error, it is a remap, and the plan says so.
- `mongodump`/`mysqldump` present on **both** ends — they ship separately from the shell,
  which `bin.go` already notes.

### 1. Configuration

`/etc/ratline/config.yaml` is part policy and part host fact. Copying it wholesale is
wrong: `server.hostname`, paths, and anything the target's `init` decided are local. So the
plan **diffs** the two configs and presents the source-only policy settings —
`users.allow_sudo`, `features.*`, ACME settings, `databases.*.env_key`, port range — as an
explicit list to apply with `ratline config set`. Nothing is copied silently. Secrets in
config files (`acme.alerts.webhook_url` is already flagged as secret-bearing in
`config/edit.go:329`) are listed as "set this yourself", never transferred.

### 2. Tenants

`ratline user add` per source user, carrying shell, comment, quota, `memory_max`,
`sftp_only`, `password_login`, `disabled`. **UIDs are not carried** — that is deliberate and
already the `restore` behaviour: ownership comes from the account as it exists on this
server. A migration that insists on preserving uids is a migration that fails on a target
where uid 1001 is taken.

Passwords do not travel (there is nothing to travel — the hash is in `/etc/shadow` and
ratline never held it). `password_login` accounts are listed in the closing report as
needing a new password set.

### 3. SSH keys

Straight from the export. Public blobs, fingerprints, scope, options, expiry. This is the
one part that is genuinely complete, because a public key is the whole secret.

Revoked keys come across as revoked — dropping them would quietly un-revoke a contractor.

### 4. Site shape

The existing `ratline import` path, per site, from the export. Everything render-bound goes
through `site.validateSiteRow`, which `parseManifest` already calls for exactly this reason:
*a restore reads an untrusted manifest and renders straight from it*. A manifest that
arrived over a network is more untrusted, not less.

### 5. Site files

The mechanically interesting stage.

```
rsync --archive --hard-links --acls --xattrs --numeric-ids=false --delete \
      --rsh <ssh with the pinned known-hosts> \
      root@source:/home/<user>/<slug>/  /var/lib/ratline/migrate/<run>/<slug>/
```

Then the swap is **not** rsync's job. The staging directory is root-owned and outside
`/home`; handing it to the existing `site.Restore` swap path is what gets us
`system.CheckNoSymlinks` from the `/home` boundary down and an atomic rename. Doing it in
one rsync straight into `/home` would write as root into a tenant-owned tree, which is
precisely the invariant `CLAUDE.md` spells out.

Why rsync rather than a tar stream: resume after a dropped connection, delta transfer on a
re-run, and it is one argv — no `sh -c`, no Go-side pipe between two `exec.Cmd`s, which
`system.Runner` has no shape for today and would need to grow.

`.env` travels inside the encrypted stream. The staging directory is 0700 root-owned. It is
removed when the site's transaction commits, and `migrate abort` removes it too — a
forgotten staging tree full of tenants' `.env` files is its own incident.

### 6. Databases

This is the subtle one, and the subtlety is a *good* property being inconvenient.

**ratline has never stored a database password** — `repo_databases.go:12` says so, and it is
right: the engine keeps a hash and will not give it back, so a lost password is rotated,
not recovered. Which means the migrated user cannot have the old password, and the
connection string that just arrived inside the site's `.env` is now wrong.

The sequence, per database:

1. `db create <name> --engine <e> --owner <user>` on the target → **a new password**.
2. `db dump <name>` on the source → archive over the same SSH transport.
3. `db restore <archive> --into <name> --drop` on the target.
4. **Rewrite the site's `.env`**: `site env set <domain> <ENV_KEY>=<new uri> --stdin`. The
   key name comes from the attachment row (`EnvKey`), which the export carries, so it is
   whatever the operator actually used — `DATABASE_URL`, `MONGODB_URI`, `REDIS_URL`, or
   something bespoke.
5. Record in the report that the credential changed, because anything *outside* the site
   that used that password — a colleague's laptop, a CI secret, a BI tool — is now broken
   and nothing on either server can know about it.

There is a tempting alternative — read the old password out of the travelling `.env` and
recreate the user with it — and it should be **refused by default**. The old password has
been sitting in backups, in the old provider's snapshots, and possibly in a support ticket.
A migration is the one moment rotation is free. `--preserve-db-passwords` can exist for the
operator who has twelve external consumers and cannot rotate today, but it is a flag with a
warning, not the default.

Redis has no dump path (`redisNoDump`) — the plan says so, loudly, per keyspace, rather
than silently migrating an empty cache.

`db access` rules (`repo_mongo_access.go`) travel as a **listed proposal**, not an
application: an allow-list of addresses is a firewall decision, and `CLAUDE.md` is explicit
that ratline never enables a firewall for you. The report prints the `ratline db access
allow` commands.

### 7. Scheduled jobs and workers

`state.Export.SiteUnits` already carries them, and `restore` already prints the warning that
they do not travel with an archive. Here they do: replay `site cron add` / `site worker add`
per unit. The command and timeout go through the same `unit.RenderSiteUnit` gate as typed
input.

**A migrated timer that fires on both servers is a duplicate charge, a duplicate email, a
duplicate export.** So migrated units land **disabled** by default, and enabling them is
part of cutover — after the source's are stopped. `--start-jobs` overrides for the operator
who knows their jobs are idempotent.

### 8. Certificates

They do not move, and that is correct rather than a limitation. Private keys are never
exported (`state_test.go:551` asserts it), and a certificate issued for a host that is about
to stop answering is not the certificate you want anyway.

What the migration does instead:

- Records which domains had certificates, their issuer, and their expiry.
- Refuses to attempt HTTP-01 before cutover — the challenge would be served by the *old*
  box, and a failed validation costs against `failed_validations_per_hour`, which ratline
  already tracks and refuses against.
- If a site's certificate used DNS-01 with a provider ratline has credentials for, it
  **can** issue before cutover, and the plan says which sites qualify.
- Everything else is a post-cutover step in the checklist, with the rate-limit budget
  printed alongside it. Twenty sites is twenty issuances; the operator should know that
  before DNS moves, not after.

### 9. History (opt-in)

Deployments and events. Off by default because it is large and because "what happened on
the old server" is a question about the old server. `--with-history` brings it.

---

## The journal, and why the run is not one big transaction

`compose.go`'s undo stack is right for a command that takes ninety seconds. It is wrong for
a migration of twenty sites and 200 GB: unwinding everything because site nineteen had a
bad manifest throws away four hours of correct work, and the operator's actual need is
"fix that one and carry on."

So:

- **Per stage, and per site within a stage: a compose-transaction.** Site seven failing
  unwinds site seven — its staging tree, its state row, its vhost, its unit — and nothing
  else. `rb.UnwindOn(ctx, &err)` as everywhere else.
- **Per run: an append-only journal** in ratline's own state database. Two new tables in
  `internal/state/schema.go`, added as a **new migration entry, never an edit to an
  existing one**:

  ```sql
  CREATE TABLE migrations (
      id, source, direction, mode, started_at, finished_at,
      state,            -- planning | running | paused | failed | complete | aborted
      plan_json, error, created_by
  );
  CREATE TABLE migration_steps (
      id, migration_id, seq, stage, subject,   -- e.g. stage='files', subject='shop.example.com'
      state,            -- pending | running | done | failed | skipped
      started_at, finished_at, bytes, detail, error
  );
  ```

- `migrate run --resume <id>` replays the journal, skips `done`, and restarts at the first
  incomplete step. This is the same mechanism the panel polls, so resume and progress are
  not two implementations of "where are we".
- `migrate abort <id>` unwinds every `done` step in reverse **on the target**. It never
  touches the source, because the source was never touched.

---

## Verification, before DNS moves

This is the part that turns "the commands exited 0" into "it works", and it is possible
only because `diag`'s probe already takes a `Host` separately from the URL.

For each migrated site, on the target:

1. `site health` through its own socket or port — DNS-independent by construction, and the
   same probe a deploy already runs before declaring itself successful, so "healthy" means
   the same thing here as it does there.
2. An HTTP request to the target's own IP carrying `Host: <domain>` — proves nginx routes
   the vhost, the TLS name is right (or absent, expectedly), and the app answers.
3. Compare against the source's `state.Health` row for the same domain. A site that was
   already unhealthy before the migration is reported as *unchanged*, not as a migration
   failure. Blaming the migration for a pre-existing outage is how a report gets ignored.

Also compared and reported: file count and byte total per site, database document/row
counts before and after, key count per scope, unit count per site.

`migrate verify` is a separate re-runnable command precisely so it can be run again after
DNS moves and after certificates are issued.

---

## Cutover, and what ratline refuses to do

ratline does not do DNS, and it should not pretend to. So cutover is a **checklist plus two
safe actions**, and the checklist is the deliverable:

- The A/AAAA records to change, per domain and alias, with the current and target
  addresses, and the advice to drop TTL well beforehand.
- The certificate issuances to run once records have propagated, with the rate-limit budget.
- The scheduled jobs to enable on the target, after they are stopped on the source.
- The `db access` rules to re-add, if any.
- The external consumers whose database credentials changed.
- The `password_login` accounts needing a new password.

The two actions, run **on the source** as `ratline migrate cutover`:

- `site disable` across the migrated sites, which serves 503 — a clear, honest signal to
  anything still resolving to the old address, and instantly reversible.
- Optionally emit a 301 to the new host instead of the 503, for a site where that is
  meaningful.

**It never deletes anything on the source.** Not the sites, not the tenants, not the data.
A source you deleted is not a rollback you have. Retirement is the operator typing
`user delete` when they are ready, and the report says so.

---

## Honest gaps

In the shape `cmd_import.go` already uses, because a clean exit implying a finished
migration is the specific failure this is guarding against. The report prints this at the
end of every run, populated with what actually applied:

- **DNS.** Not ours. Checklist only.
- **Certificates.** Re-issued on the target, not copied. Private keys never leave a server.
- **Database credentials.** Rotated by default. External consumers of them will break.
- **Anything outside `/home` and `/etc/ratline`.** System packages, hand-installed daemons,
  root's crontab, `/etc` changes the operator made, anything a site writes outside its own
  directory.
- **Firewall.** Only the `db access` rules ratline manages are even *listed*. Every other
  ufw rule is the operator's.
- **Account passwords.** Never held, so never moved.
- **Running state.** In-memory sessions, queues, sockets, anything a process held and did
  not write down.
- **The audit log and event history**, unless `--with-history`.
- **Mail, DKIM, reverse DNS, IP reputation** — a new IP is a new IP.

---

## Tests

**Unit.** Plan resolution against a fixture export: the plan for a given source is a pure
function of the export plus the target's state, so it is table-testable without a network.
Preflight refusals each get a case. Journal resume: a run truncated at every step index
resumes to the same end state.

**Fuzz.** `make fuzz` already fuzzes the validators. A remote manifest and a remote export
are new untrusted inputs and belong in it — a control character or a newline in a field
that reaches a rendered vhost is the attack, and `validateSiteRow` is the gate.

**Integration.** This needs a **second host**, which is a real change to
`test/integration/docker-compose.yml`: a `harness-target` service built from the same image
at `10.30.50.6`, with sshd running and — per the gotcha already in `CLAUDE.md` —
**`ratline-integration.service` masked**, or the target boots, runs the suite, and calls
`systemctl exit` out from under the migration.

What the suite must prove, and one thing it must be careful about:

- A full pull moves two tenants, three sites across three runtimes, one database and one
  cron unit, and every site answers on the target by Host header.
- The source is byte-identical afterwards. This is the headline assertion.
- An interrupted run resumes and converges on the same result.
- An aborted run leaves nothing behind on the target — no staging tree, no state row, no
  vhost, no unit, and no "not-found failed" entry in `systemctl --failed` (the
  `reset-failed` gotcha applies to units a migration creates and unwinds).
- **Assert the property, not the exit code.** `CLAUDE.md` records this exactly: a composite
  whose steps can each fail exits 2 or 3 depending on whether a runtime tarball downloaded.
  Pinning the code makes the unwind test pass or fail on the network while saying nothing
  about unwinding. What must hold in every case is *nothing left behind, source untouched*.
- **Prove the negative case.** A vacuous assertion is worse than none: break the manifest,
  watch the refusal fire, put it back. Several tests in this repo passed because the command
  refused before ever reaching the code under test.

---

## Documentation

- `docs/topics/migration.md` — embedded, so it has to read well in a terminal over SSH with
  no browser. That is the constraint, and it is also the situation someone is in when they
  need it.
- `docs/web/src/data/subjects.ts` — a new `migration` subject, or additions to `sites`. The
  navigation is generated from typed data; a page not in `pages.ts` does not exist.
- `docs/reference/commands.md` is **generated**: change the help text in Go, run
  `make docs-commands`, do not hand-edit. CI fails on a diff.
- `docs/reference/command-surface.md` is the authoritative spec and is hand-maintained —
  the new group, its flags and its refusals go there.
- `scripts/check-doc-links.py` must still pass.

## Panel

Covered in [README.md](README.md#the-panel-contract). The wizard: **Preflight → Plan →
Run → Verify → Cutover**, with the Plan step rendering `plan.steps` (including `kept`,
which already explains why a step will not be undone) as a reviewable, deselectable table,
and the Run step polling `migrate status --json`.
