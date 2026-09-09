# Design plans

Not user documentation. These are the plans for work that has not been built yet, kept
in the repository because two of them are being built in parallel and have to agree.

Nothing here is embedded into the binary and nothing here reaches the docs site:
`docs/embed.go` embeds `topics/*.md` only, and the site's `prebuild` copies the same
files. This directory is inert.

| Plan | What it is |
|---|---|
| [server-migration.md](server-migration.md) | `ratline migrate` — clone or move a whole server to another one |
| [notifications.md](notifications.md) | `ratline notify` — email through Resend: heartbeats, alerts, digests |

---

## What both plans inherit

Both features are new *surface* on a tool whose promises are already written down. The
invariants in `CLAUDE.md` are not style preferences, and neither of these features gets
an exemption. The ones each plan has to answer for, and where:

| Invariant | Migration | Notifications |
|---|---|---|
| Never build a shell command | rsync/ssh are argv slices through `system.Runner`; a remote command is an argv too, never a string a remote shell splits | the Resend call is `net/http` in-process — no `curl`, no shell |
| Secrets never touch argv | the target's SSH identity is a path, never a key on the command line; `.env` moves inside an encrypted stream | the API key arrives on **stdin** and lives in a 0600 root-owned file, like `paths.mongo_uri_file` |
| Staged, verified, committed, rollback stack | each site is one compose-transaction with its own undo; the run as a whole is a resumable journal, not one giant unwind | a send is not a mutation and never rolls anything back |
| Refuse rather than guess | an unverified target host key, a domain that already exists on the target, a runtime the target lacks — all refusals | a `from` address on an unverified Resend domain is refused at `notify setup`, not discovered at 3am |
| Safe to run twice | re-running reports what already moved and skips it | a transition notifier keyed on the transition, so the same event never mails twice |
| Validated where it enters | every field that reaches a rendered vhost or unit on the target goes through `site.validateSiteRow` — a `restore` already reads an untrusted manifest, and a migration's manifest arrives over the network | every value that reaches an email body is escaped and secret-screened at the point it enters the notification, not at the template |
| Never write as root through a symlink into a tenant tree | rsync lands in a root-owned staging directory outside `/home`; the swap into place is the existing `site.Restore` path, which already calls `system.CheckNoSymlinks` from the `/home` boundary down | n/a |
| A managed unit must be installed by `update`, not only by `init` | n/a | the heartbeat timer goes in `managedTimers` **and** `IsOwnUnit`, so `EnsureTimers` installs it on upgrade and `doctor` reports it missing |

Two more that these features add:

- **`migrate` never mutates the source.** The whole design is a pull, so the source is
  touched with `export` and `tar --create` and nothing else. A migration you cannot
  abandon halfway is not a migration, it is a gamble. This belongs in `CLAUDE.md` when
  the feature lands.
- **A notification never carries a secret.** No `.env` values, no connection strings, no
  admin URIs, no key material, no API keys — not in the body, not in the subject, not in
  a rendered diagnostic. Email is the one output ratline produces that leaves the server
  and gets stored somewhere neither the operator nor ratline controls.

---

## The panel contract

A separate agent is building a web UI (`internal/panel/`, whose `store/schema.go` already
defines `accounts`, `sessions`, `invites`, `actions`, `jobs`, `login_attempts`, `settings`).
Both features are designed to be driven from it, and the design decisions that exist *for*
the panel are these:

**1. The panel shells out to ratline. It does not reimplement it.**
Same rule as `compose.go`: composites compose the *commands*, so a flag added to
`site add` tomorrow is available everywhere tomorrow. The panel is another composite.

**2. Long work is a `jobs` row, and progress is polled, not streamed.**
A migration outlives the request that started it, the browser tab, and possibly the panel
process. So `migrate run` journals its progress into ratline's own state database and the
panel polls `ratline migrate status --json`. No new streaming protocol, no websocket that
has to survive a restart, and the journal is needed for `--resume` regardless. The panel's
`jobs.output` holds the transcript; the journal holds the truth.

**3. The plan is the UI.**
`compose.go` already models a plan as `[]step`, each with `what`, `argv`, `undo` and
`kept` — and `kept` is literally *"why this step will not be taken back"*. That is a UI
affordance sitting in the code already. `migrate plan --json` emits it; the panel renders
a reviewable table with checkboxes before anything runs.

**4. The panel never stores a credential ratline can hold instead.**
The Resend API key is pasted into a panel form over TLS and piped straight to
`ratline notify setup --stdin`. The panel's `settings` table records `configured: true`
and the last four characters — nothing else. The migration's SSH identity is a path on
the server; the panel references it and never sees it.

**5. Panel-side events go out through ratline's sender.**
A failed-login streak, a new panel account, an accepted invite — those are exactly the
events an operator wants mailed, and they live in the panel's database. The panel calls
`ratline notify send --event panel.<name> --stdin` with a JSON payload. One sender, one
delivery log, one place where "did it actually send" is answerable. `notify send` takes a
**registered event name**, never a free-form subject and body — otherwise anyone who can
run ratline has an open relay.

**6. Role gating is the panel's job, not ratline's.**
ratline runs as root and cannot know who asked; that is exactly why the panel has an
`actions` table. Migration and notification configuration are owner/admin only.

---

## Sequencing

Notifications first. It is smaller, it is independently useful, and migration wants it —
"the migration finished" and "the migration failed at site 7 of 20" are the two emails a
four-hour operation should send.

| Phase | Ships | Depends on |
|---|---|---|
| N1 | `internal/notify` + Resend sender + `notify setup/status/test`, key file, config block | — |
| N2 | The existing ACME webhook re-plumbed through `internal/notify`; `acme.alerts.email` finally has a far end | N1 |
| N3 | Event notifiers on the transitions that already exist in state (health, certs, deploys, keys, sudo) + dedupe + delivery log + `doctor` check | N2 |
| N4 | Heartbeat timer, weekly digest, `notify heartbeat --now` | N3 |
| N5 | Panel: settings page, event toggles, recipients, delivery log, `notify send` for panel events | N1–N4 |
| M1 | `migrate plan` — preflight and a resolved plan, mutating nothing | — |
| M2 | `migrate run` for tenants, keys and site shape (reuses `export`/`import`) + the journal | M1 |
| M3 | Site files over rsync into staging, then the existing `site.Restore` swap | M2 |
| M4 | Databases: engine check, `db create`, dump/restore, `.env` rewrite | M3 |
| M5 | `migrate verify` (Host-header probes against the target IP, DNS-independent) and the cutover checklist | M3 |
| M6 | Two-host integration harness | M2–M5 |
| M7 | Panel: the migration wizard | M1–M5 |
