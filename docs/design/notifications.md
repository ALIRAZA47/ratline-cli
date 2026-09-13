# `ratline notify` — email through Resend

Status: **plan**. Nothing below is built.

## The problem, and the one that is already here

A server provisioned by ratline runs three timers and then goes quiet. Certificates renew,
health checks record, keys expire, jobs fail — and the only way to learn any of it is to
log in and ask. The operator finds out that the nightly renewal has been failing for three
weeks when a browser shows an expired certificate.

There is a smaller and sharper version of this problem already in the tree.
`internal/config/config.go:233` defines:

```go
// Alerts is where renewal failures are reported.
type Alerts struct {
    WebhookURL string `yaml:"webhook_url"`
    Email      string `yaml:"email"`
    WarnDays   int    `yaml:"warn_days"`
}
```

`WebhookURL` is used, once, by `tls/renew.go:326`. **`Email` is validated and nothing ever
sends to it. `WarnDays` is validated and nothing ever reads it.** An operator who set
`acme.alerts.email` has been told their renewal failures are covered, and they are not.

That is the same shape as the bug `cmd_import.go` opens by describing: *"export has said
'for migration' since it was written, and nothing consumed it. A dump nothing reads is a
promise, not a feature."* Two settings here are promises with no far end, and this feature
is where they get one.

---

## Provider: Resend, behind an interface

Resend is a JSON REST API over HTTPS. That matters more than it sounds:

- No new dependency. `net/http` is already used in-process for runtime downloads
  (`cmd_runtime.go:568`), health probes (`diag/site.go`) and the existing alert webhook.
  The binary stays static and dependency-light, same reason `modernc.org/sqlite` is the
  driver.
- No new binary in the registry, no `curl`, no shell. The invariant holds by construction.
- No SMTP. An SMTP client is a bigger surface, needs credentials that look more like a
  password, and needs a port that a VPS provider has probably blocked outbound anyway.

Structure follows the pattern the repo already uses for engines — *"the runtime package is
an interface, so each is a new file rather than a refactor"*:

```
internal/notify/
    notify.go     Notifier: dedupe, recipients, event gating, delivery log, secret screen
    sender.go     type Sender interface { Send(ctx, Message) error }
    resend.go     the Resend API sender
    webhook.go    the existing POST from tls/renew.go, moved here unchanged in behaviour
    render.go     text-first bodies, an escaped HTML alternative
    events.go     the registered event vocabulary
```

**The existing webhook moves behind this interface.** If it does not, there are two
notification paths and they will drift — one with dedupe and a delivery log, one without.
`acme.alerts.webhook_url` keeps working exactly as it does today; it becomes a configured
`Sender` rather than a special case inside the TLS manager.

---

## The API key

It is a credential, so it gets the treatment every other credential in this codebase gets.

- **Arrives on stdin.** `ratline notify setup --from alerts@example.com --stdin`. Never a
  flag, never argv — `/proc/PID/cmdline` is world-readable, and the same reasoning that put
  `env set --stdin` and `db connect --stdin` there applies unchanged.
- **Lives in a 0600 root-owned file**, `paths.resend_key_file`, defaulting to
  `/etc/ratline/resend.key`. Exactly the shape of `paths.mongo_uri_file`. Not in
  `config.yaml`, which is 0644 and gets pasted into support tickets.
- **Never in `--json`, never logged, never in an email, never in a diagnostic.**
- **No `--reveal`.** `site env get --reveal` exists because an operator legitimately needs
  to read back a value they set. Nobody needs to read a Resend key back out of a server —
  it is in the Resend dashboard. `notify status` prints `re_••••…a91f`, when it was set,
  and by whom. Rotation is `notify setup` again.
- `notify setup` **verifies before it stores**: it calls Resend for the account's domains
  and refuses a `from` address on a domain that is not verified. The alternative is
  discovering a 403 at 3am, on the one email that mattered, from a subsystem designed never
  to raise errors.

---

## Configuration

New top-level block in `internal/config/defaults.yaml`:

```yaml
notifications:
  enabled: false
  provider: resend            # named so a second is a new file, not a refactor
  from: ""                    # must be on a verified Resend domain
  reply_to: ""
  recipients: []              # operator addresses
  timeout: 15s
  api_base: ""                # empty = api.resend.com; overridden only by the test harness

  # Suppression, so a flapping site cannot mail every five minutes.
  dedupe_window: 6h
  max_per_hour: 20            # a hard cap; the 21st is dropped and counted, not queued

  events:
    cert_renewal_failed:   true
    cert_expiring:         true
    site_unhealthy:        true
    site_recovered:        true
    deploy_failed:         true
    deploy_succeeded:      false   # noisy on an active server, off by default
    doctor_problem:        true
    key_added:             true
    key_new_source:        true
    sudo_granted:          true
    sshd_policy_changed:   true
    db_exposed:            true
    disk_threshold:        true
    unit_failed:           true
    backup_stale:          true
    self_updated:          true
    migration_finished:    true
    migration_failed:      true
    panel_login_failures:  true
    panel_account_created: true

  heartbeat:
    enabled: false
    schedule: "daily"         # daily | weekly — rendered into the timer's OnCalendar
    at: "08:00"
    digest: weekly            # the roundup; "" disables

  thresholds:
    cert_warn_days: 7         # defaults to acme.alerts.warn_days, which finally does something
    unhealthy_after: 3        # consecutive failures before it counts as an outage
    disk_percent: 85
    backup_stale_days: 8
```

**Not behind `features.*`.** `db_provisioning` is off by default because the command
*cannot work* without an admin URI, and a command that cannot work is better hidden than
offered. `notify setup` can always work — it is how you configure the thing. `enabled` and
the presence of the key file are the gate.

---

## The kinds of email

Everything below is something ratline **already knows**. Nothing here needs a new source of
truth, which is the test for whether a notification is real or invented.

### Event-driven — fired on a state transition

| Event | Source that already exists | Why it earns an email |
|---|---|---|
| `cert.renewal_failed` | `tls/renew.go`, which already builds an alert body | The existing webhook's email half, finally connected |
| `cert.expiring` | `cert.DaysRemaining()`, `acme.alerts.warn_days` | A ladder at 30/14/7/1 days. Renewal failing silently is the classic outage |
| `site.unhealthy` | `state.Health.ConsecutiveFailures` crossing `unhealthy_after` | The health timer already records it and nothing tells anyone |
| `site.recovered` | the same row returning to OK | Without the close of the loop, people stop trusting the open |
| `deploy.failed` / `deploy.succeeded` | `StartDeployment` / `FinishDeployment` | A deploy that failed at 2am and rolled back is exactly what the morning needs to know |
| `doctor.problem` | the `doctor` sweep, which already exits non-zero on problems | Mail the *delta*, not the sweep. A daily identical list is a filter rule waiting to happen |
| `key.added` | `ssh_keys` insert | Somebody now has access. Second pair of eyes on a root-level change |
| `key.new_source` | `key_usage`, which already records `remote_ip` | *"A key authorised as `acme` from an address it has never used."* The security-relevant one |
| `sudo.granted` | `users.allow_sudo`, `visudo -c` | The one escape hatch the tool has |
| `sshd.policy_changed` | the typed confirmation that already gates `PermitRootLogin`, `PasswordAuthentication`, `AllowUsers`, `Port` | The change that locks people out |
| `db.exposed` | `diagnoseMongoExposure` / `diagnoseMySQLExposure`, already written | A database reachable off-box with no firewall in front |
| `disk.threshold` | `repquota`, `system.DirSize` | A tenant filling the disk takes every other tenant down |
| `unit.failed` | `systemctl --failed`, plus the `IsOwnUnit` check | Includes the case `CLAUDE.md` calls out: a managed unit that `update` should have installed and did not |
| `backup.stale` | `paths.backup_dir` mtimes | The silent-truncation class: nothing tells you the nightly stopped |
| `self.updated` | `ratline update` | The root binary on the box changed. The operator should hear that from the box, not from a changelog |
| `migration.finished` / `migration.failed` | the migration journal | A four-hour operation should not need a browser tab held open |
| `panel.login_failures` / `panel.account_created` | the panel's `login_attempts` and `accounts` tables | Sent through `notify send` — one sender, one log |

### Scheduled

**`heartbeat`** — daily or weekly, and the point of it is the *absence*.

That framing decides the design. A heartbeat you do not monitor for absence is decoration,
so the mail says **"the next one is due at `<time>`"** and the topic documentation tells the
operator to point a dead-man's-switch at it. Anything less is a feature that makes people
feel covered while covering nothing — the exact failure this whole plan opened on.

Contents, all from `status`, `doctor` and `site health`:

- hostname, ratline version, uptime, when the last update ran
- sites: count, how many healthy, which are not and since when
- certificates: a table of expiries, soonest first
- disk and quota headroom
- failed units, including missing managed ones
- last deploy per site
- the count of anything `doctor` flagged, with the command to see it
- **next heartbeat due at `<time>`**

`only_when_interesting` is deliberately **not** offered for the heartbeat. A heartbeat that
suppresses itself when all is well is indistinguishable from a heartbeat that broke.

**`digest`** — weekly. Deploys, renewals, new keys, doctor findings, health incidents, in
one mail. This is the low-noise alternative for an operator who turns most per-event
notifications off, and most will.

---

## Delivery: the rules that keep this from becoming a liability

**1. A failed send never fails a command.** The existing webhook already has the right
shape — a 10-second timeout, fire and forget, a `Debug` log if it fails. Same here: a send
runs after the mutation has committed, never inside a rollback stack, never in a path where
its failure could unwind a deploy. Root-level tooling that breaks because a third-party API
had a bad minute is worse than tooling that stays quiet.

**2. But a failure is recorded, or this is another promise with no far end.** New state
table, added as a **new append-only migration entry** in `internal/state/schema.go`:

```sql
CREATE TABLE notifications (
    id, at, event, subject_key,   -- subject_key is what dedupe is keyed on
    recipients, provider, provider_id,
    ok, status_code, error, attempts
);
CREATE INDEX notifications_event ON notifications(event, subject_key, at);
```

And a `doctor` check: **"the last N sends all failed"** is a problem, reported the same way
a failing renewal is. A mailer that has been silently broken for a month is the thing this
feature is built to prevent, so it cannot be the thing this feature does.

**3. Dedupe on the transition, not the tick.** `subject_key` is `event + subject` — a
flapping site produces one mail per `dedupe_window`, and the body says *"3rd occurrence
since 14:02"*. Without this, the health timer mails every five minutes and the operator
writes a filter rule, which is the same as having built nothing.

**4. Safe to run twice.** `notify test` twice sends two mails — that is what it is for. The
health notifier running twice over the same transition sends once, because the transition
is in the table.

**5. Egress is a first-class failure.** These boxes often have locked-down outbound. `notify
test` must distinguish *DNS failed*, *connection refused/timed out to api.resend.com:443*,
*401 bad key*, *403 unverified sender domain*, and *422 bad payload* — each with the hint
that matches. A generic "send failed" on a firewalled box costs an afternoon. `doctor`
checks reachability too.

**6. Nothing secret, ever.** Bodies render domains, site names, unit names, counts, dates,
status codes. Never `.env` values, connection strings, admin URIs, key material, or the
Resend key. The screen is a function in `internal/notify` applied at the point a value
enters a `Message` — **validated where it enters, not where it is written**, which is the
same rule `validateSiteRow` exists to enforce for rendered configuration. A body is
assembled from typed fields, never from a raw error string that might carry a URI a
subprocess printed.

**7. Escaping.** Domains and site names are operator-supplied and land in HTML. Text-first
body with an escaped HTML alternative; `validate.NoControlChars` underneath, as everywhere.

---

## The timer

A fourth managed unit: `templates/systemd/ratline-notify-heartbeat.{service,timer}`.

It goes in **`managedTimers`** *and* in **`IsOwnUnit`** in `internal/unit/unit.go` — the
existing test asserts those two lists agree, because a unit in one and not the other gets
reported by `doctor` as an orphan whose suggested fix is to delete it.

And it is picked up by `EnsureTimers`, which `ratline update` calls (`cmd_update.go:339`) as
well as `init`. `CLAUDE.md` records why this is not a detail: *"`EnsureTimers` ran only from
`init`, which happens once in a server's life, so a release that added a timer shipped the
feature without the thing that runs it."* This is that exact situation, again.

The schedule is config-driven, so the unit is rendered from a template rather than copied
verbatim — a small change from how the other three work, and the `# managed-by: ratline`
header rule still applies, so a hand-edited copy is left alone.

**The event notifiers do not get their own timer.** They ride the existing
`ratline-health-check.timer` and the existing renewal timer, hooking the transitions those
already compute. A second timer that recomputes health would be a second definition of
"unhealthy", and two definitions disagree.

---

## Command surface

```
ratline notify setup --from <addr> [--reply-to <addr>] --stdin   # key on stdin; verifies the domain
ratline notify status                                            # configured? last send? masked key
ratline notify test [--to <addr>]                                # one real mail; diagnoses egress
ratline notify recipients add|remove|list <addr>
ratline notify events                                            # every event, on/off, last fired
ratline notify enable <event> | disable <event>
ratline notify log [--limit N] [--failed]                        # the delivery log
ratline notify heartbeat [--now]                                 # what the timer runs
ratline notify send --event <name> --stdin                       # structured payload, for the panel
ratline notify disable-all                                       # a big red switch that is not "delete the key"
```

`notify send` takes a **registered event name and a JSON payload rendered by ratline's own
templates**. Not a free-form subject and body — free-form makes anyone who can run ratline
an open relay, and the point of routing panel events through here is one sender and one log,
not a generic mail command.

Exit codes: no new ones. `CodeUsage` for a bad address or an unknown event, `CodePrecondition`
for "not configured", `CodeExternal` for a provider that refused. `notify test` failing is
the only place a send failure is allowed to be an error, because asking is the whole point
of the command.

---

## Tests

**Unit.** Render every event body from fixtures and assert **no secret appears** — feed a
site whose `.env` holds a connection string and a fixture error string carrying an admin
URI, and assert neither reaches the message. That is the assertion this feature lives or
dies on. Dedupe: same event twice inside the window sends once, outside sends twice. The
event vocabulary in `events.go` matches the config keys in `defaults.yaml` — a table test,
because a typo there silently disables a notification.

**Fuzz.** `make fuzz` already covers the validators; the body renderer takes
operator-supplied domains and site names into HTML, so it belongs there.

**Integration.** A **fake Resend** in `docker-compose.yml` — a small recorder at
`10.30.50.7` that the harness points at via `notifications.api_base`. The reasoning is
already written in that file about Pebble: *"Tests never touch Let's Encrypt: a test suite
that spends real rate-limit budget is a test suite people stop running."* A suite that
spends real Resend quota, or worse sends real mail to a real address, is the same suite.

What it proves: `notify setup --stdin` writes 0600 and the key is absent from `--json`,
from the audit log and from `/proc/<pid>/cmdline` during the call; a failing renewal in the
Pebble section produces exactly one mail; the heartbeat timer is installed by `init` **and
by `update` on a server that predates it**; a broken sender is reported by `doctor`.

**Prove the negative case.** Delete the key file and assert the send is skipped rather than
erroring the command; point `api_base` at a black hole and assert the deploy still succeeds.
*"A vacuous assertion is worse than none"* — break it, watch the test fail, put it back.

---

## Documentation

- `docs/topics/notifications.md` — embedded for `ratline explain notifications`, so it reads
  in a terminal over SSH. It carries the dead-man's-switch framing, because that is the
  thing an operator has to *do* for the heartbeat to be worth anything.
- `docs/web/src/data/` — `pages.ts`, and a subject (probably folded into operations)
  in `subjects.ts`. Navigation is generated from typed data.
- `docs/reference/configuration.md` — the new block.
- `docs/reference/commands.md` is **generated**: `make docs-commands`. CI fails on a diff.
- `docs/reference/command-surface.md` — hand-maintained and authoritative; the new group
  goes there.

## Panel

Covered in [README.md](README.md#the-panel-contract). Screens: **Settings** (paste key →
piped to `notify setup --stdin`, never stored panel-side beyond `configured` and the last
four), **Events** (per-event toggles → `config set`), **Recipients**, **Delivery log** (from
the `notifications` table, with the failure reasons), and a **Send test** button. Panel-side
events reach the same sender through `notify send`.
