# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Two products from one repository.

`ratline` is a single static Go binary that runs **as root on a bare Ubuntu/Debian VPS** and
provisions isolated system users and their web apps (static, node, python). It is the
provisioning core of Ploi/RunCloud/Dokku without the web UI and without containers: it
configures nginx, systemd, SSH, certbot and MongoDB rather than installing or replacing
them. The one explicit exception is `db install` (`internal/mongod`), which installs and
owns a MongoDB server on the host when asked to — and refuses one it did not set up.

`ratline-panel` (`cmd/ratline-panel`, `internal/panel/*`, `panel/web`) is a web interface
for it: a separate binary, a separate systemd service and a separate install. It
**reimplements nothing** — every action runs `ratline <verb> --json` and reads the
envelope, so a mutation made in a browser goes through the same lock, the same
staged/verified/committed path and the same rollback stack. It is a caller, and the moment
it stops being one it becomes a second, worse ratline that drifts from the first.

Because it runs as root on someone's server, the invariants below are not style preferences —
they are the properties the tool promises.

## Commands

```bash
make build            # both binaries into bin/ (CGO_ENABLED=0, ldflags from git)
make panel            # ratline-panel, interface built into the binary it embeds
make panel-go         # the binary only, around whatever bundle is already built
make test             # go test -race ./...
make lint             # golangci-lint, pinned to the version CI uses
make check            # fmt + vet + test
make integration      # the real suite: Ubuntu + systemd PID 1 + Pebble ACME + MongoDB
                      # its panel section installs the panel onto the server the suite
                      # has already provisioned — that is the install worth testing
```

One test, one package:

```bash
go test -run TestGrantSudo ./internal/user
```

The panel's interface (`panel/web`, Vite + React, built into `internal/panel/web/dist`):

```bash
npm --prefix panel/web run build   # tsc -b && vite build, straight into the Go package
npm --prefix panel/web run dev     # dev server, proxying /api to a panel on :8420

# a live panel with a fake ratline behind it, for looking at
RATLINE_PANEL_PREVIEW=1 go test ./internal/panel/httpapi -run TestServeForPreview -v -timeout 0
```

Docs site (`docs/web`, Vite + React):

```bash
npm --prefix docs/web run dev      # dev server
npm --prefix docs/web run build    # tsc -b && vite build; prebuild copies topics + install.sh
python3 scripts/check-doc-links.py # every internal link resolves to a real route
sh scripts/check-csp-hash.sh       # vercel.json still pins the inline script's hash
```

`make fuzz` fuzzes the validators. CI budgets fuzzing in **executions**, not seconds — a
`-fuzztime 20s` boundary race failed a job on `context deadline exceeded` after 1.1M
executions with no crasher.

## Architecture

**The layers.** `cmd/ratline` → `internal/cli` (cobra) → managers (`internal/user`, `site`,
`tls`, `sshkey`, `mongo`, `unit`, `nginx`, `runtime`) → `internal/system` + `internal/state`.
Managers hold the logic and are the level tests target; the CLI layer parses flags and prints.
`cmd/ratline-shell` is a separate tiny binary used as the forced command for scoped SSH keys.

**The panel's layers.** `cmd/ratline-panel` → `internal/panel/cli` (cobra) →
`internal/panel/httpapi` (handlers, middleware, RBAC) → `internal/panel/rl` (the only
thing that touches the system: it builds argv and runs the ratline binary) — beside
`internal/panel/store` (its own SQLite), `auth` (argon2id, TOTP, tokens), `jobs` (the
queue and transcripts), `install` (unit, vhost, certificate) and `web` (the embedded
bundle). `internal/panel` itself holds only the configuration, so that `httpapi` can read
it without an import cycle.

**Everything external goes through `system.Runner`.** It resolves binaries to absolute paths
from a registry and does not inherit `PATH`. There is no `sh -c` anywhere and no string ever
becomes a command line. `--dry-run` is implemented here, so it writes nothing at any layer.

**Every mutation is staged, verified, committed, with a rollback stack.** Render to a temp
file, validate it with the real tool (`nginx -t`, `visudo -c`, `sshd -T`, `systemd-analyze
verify`), rename atomically, and `rb.Push` an undo step. `rb.UnwindOn(ctx, &err)` runs the
stack on failure; `rb.Commit()` discards it on success. A command that fails halfway leaves
the server as it was, still serving.

**`internal/state` is SQLite** (`modernc.org/sqlite`, pure Go so the binary stays static).
Migrations are an **append-only** list — never edit an existing entry, add a new one, so a
server upgrading from any version converges. A binary refuses to run against a state database
newer than it understands rather than corrupting it.

**`internal/rlerr` maps typed errors to exit codes 0–10**, each with a hint. Exit codes are a
public contract that automation branches on; `--json` wraps everything in
`{ok, command, version, data}`. When asserting on JSON in tests, walk the envelope
(`.. | objects`) rather than assuming a top-level key — a `jq` filter that reads `.findings`
silently matched nothing and made a broken case pass.

**`templates/` is an embedded FS** (nginx vhosts, systemd units, logrotate, the mongosh
script). Generated files carry a `# managed-by: ratline` header, and ratline refuses to
overwrite a file at one of its paths that lacks both that header and a state row.

**MongoDB** runs `mongosh --nodb --quiet --file <static script>` with every value in the
environment. No script is built from user input and the admin URI never appears in argv.

## Invariants a change must not break

- **Never build a shell command.** argv slices through `system.Runner`, always.
- **Secrets never touch argv** (`/proc/PID/cmdline` is world-readable). They arrive on stdin
  — `env set --stdin`, `db connect --stdin` — and are redacted unless `--reveal`.
- **Never install as root what belongs to a tenant.** `npm install` and `pip install` run as
  the site user; root running a tenant's `postinstall` is a full escalation.
- **No sudo for created users.** Any escape hatch is gated on `users.allow_sudo` and every
  grant is still validated with `visudo -c`.
- **Refuse rather than guess.** Contradicting flags are an error, not a coin toss.
- **Everything is safe to run twice.** Re-running `user add` reports what exists.
- Private keys are never world-readable, never logged, never in `--json`, never anywhere
  nginx can serve. `.env` is 0600 and never inside a document root.
- sshd's `PermitRootLogin`, `PasswordAuthentication`, `AllowUsers` and `Port` are never
  modified without an explicit flag and a typed confirmation.
- **ratline never runs `ufw enable` or `ufw disable`.** `db access` adds and deletes
  per-address rules for MongoDB's port only, and refuses to widen mongod's bind unless
  ufw is already active with a default-deny incoming policy — enabling a firewall is how
  an operator locks themselves out of SSH, and that decision stays with them. Bind
  changes are verified against the listening socket (`ss`), not the config file, and
  `security.authorization: enabled` has no render path that omits it.
- **A value that reaches a generated config is validated where it enters, not where it is
  written.** `nginx -t` and `systemd-analyze verify` reject configuration that does not
  *parse*, not configuration that parses and says something nobody asked for — a newline, a
  space or a `;` in a field is a syntactically valid way to add a directive to a root-owned
  file. Every render-bound field on a site row is gated by `internal/site.validateSiteRow`,
  called from both `buildSite` (typed input) and `parseManifest` (a `restore` reads an
  untrusted manifest and renders straight from it). The job/worker command and timeout are
  gated in `unit.RenderSiteUnit`. `validate.NoControlChars` is the blanket check underneath.
- **Never write as root through a symlink into a tenant-owned tree.** A tenant owns their
  site directory and can swap a subdirectory for a link between operations. `WriteFileAtomic`
  and `EnsureDir` `Lstat` the component they touch and refuse a symlink; site provisioning
  calls `system.CheckNoSymlinks` from the root-owned `/home` boundary down, to catch one
  swapped higher up the path than a single `Lstat` can see.

## The panel's own invariants

- **The panel is a caller, never a co-owner.** It never writes to `state.db`; anything it
  wants to know about sites, users, certificates or databases it asks the CLI for, so
  there is one answer to "what exists on this server". Its own database holds only what
  ratline cannot know: which human asked.
- **The command surface is read, not written down.** `internal/panel/rl` parses
  `ratline schema` at runtime, so the forms describe the installed binary. What *is*
  written down is `policy.go` — who may run what, what needs a name typed back, what runs
  as a job — because that is a judgement rather than a fact about the binary. Its default
  is fail-safe: an unclassified **mutation** is super-admin only, an unclassified read is
  an admin's, so a ratline release that adds a command appears locked down.
- **The installer creates the first super admin.** There is no window in which the panel
  is answering and unclaimed, and no default password: `install --admin-email` creates the
  account and prints a generated password once, or reads one from stdin. `--no-admin` puts
  the claimable state back deliberately and `doctor` calls it a problem. The HTTP setup
  endpoint still exists for the genuinely-empty database (`--purge`, `--no-admin`, the last
  account deleted) and returns 409 otherwise.
- **Secrets are a declared `StdinSpec`, not a flag.** `site env set` is the case that
  matters: its stdin is `NAME=value` lines, so the panel asks for the name separately,
  validates both halves and composes the line — because typing it as a positional
  assignment works perfectly and puts the value in `/proc/PID/cmdline`. The policy also
  overrides that command's positional list, or the form would offer a field literally
  called `[KEY=VALUE`.
- **Three details stop argv injection**, and all three have tests: flags are emitted as one
  `--name=value` element (the two-element form lets a leading dash be read as a flag);
  positionals come after a bare `--` (or a "domain" of `--config=/tmp/mine.yaml` picks a
  different configuration); and a request can never set a global flag.
- **The role gate is a filter, not a hidden button.** An admin's browser is never sent the
  super-admin actions, and asking for one returns the same "no such action" as a command
  that does not exist.
- **The bundle must satisfy the panel's own CSP.** No inline script or style, because the
  policy has no `unsafe-inline` — the bundle is built to comply rather than the policy
  relaxed to fit it. CI checks the built `index.html`.

## Documentation — two places that must agree

- `docs/topics/*.md` is **embedded into the binary** for `ratline explain`, so a topic must
  read well in a terminal over SSH with no browser. The docs site renders these same files
  (copied by the `prebuild` step) — one source of truth, no second copy to drift.
- `docs/web/` is the React site. Navigation is generated from typed data: `data/groups.ts`
  (commands), `data/subjects.ts` (which pages belong to which subject), `data/pages.ts`
  (labels, blurbs, search keywords). Sections are **subjects**, not document kinds — a
  subject owns its commands, concepts, in-depth topics, runbooks and config settings.
- `docs/reference/commands.md` is **generated**. Change the help text in Go and run
  `make docs-commands`; do not hand-edit it. CI regenerates it and fails on a diff.

## Gotchas that have cost real time here

- **A cached integration image runs old code.** The Dockerfile `COPY`s `run.sh` and the
  binary, so if `--build` fails (an unreachable Docker Hub, say) compose silently reuses the
  previous image and you get a confident result about code that is not running.
- **Cross-compiling for the wrong container arch** gives "cannot execute binary file". Check
  with `docker image inspect --format '{{.Architecture}}'`.
- **The integration image auto-runs the suite at boot** and calls `systemctl exit` when it
  finishes, which stops the container. Mask `ratline-integration.service` before using one
  interactively as a test bed.
- **nginx reload is asynchronous.** The master starts new workers *before* telling old ones
  to stop accepting, so "is the config live?" means checking that no pre-reload worker is
  still accepting — a draining worker reports `nginx: worker process is shutting down`.
- **Builds must be reproducible from the tag**: the Makefile bakes in the *commit* date, not
  the wall clock, so a published `SHA256SUMS` can be verified by rebuilding.
- **Docs-site routes are lazy**, so a page's content is not in the DOM on the frame after
  navigation. Anything that reaches for a rendered element — the scroll-to-anchor being the
  one that bit — has to wait for it rather than assume one animation frame is enough.
- **A vacuous assertion is worse than none.** Prove the negative case: break the thing, watch
  the test fail, put it back. Several tests here passed because the command refused before
  ever reaching the code under test — and a mutation test only counts if the mutation
  actually applied, which is worth checking before believing the result.
- **Assert the property, not the exit code, when which step fails depends on the
  environment.** A composite whose steps can each fail exits 2 or 3 depending on whether a
  runtime tarball downloaded, so pinning the code made an unwind test pass or fail on the
  network while saying nothing about unwinding. What must hold in every case — nothing left
  behind — is the thing to check.
- **`RevokedKeys` pointing at a file sshd cannot read refuses *every* public key**, for
  every account — `sshd_config(5)` says so explicitly, and it is the opposite of the
  intuition that a missing revocation list would let revoked keys back in. `sshd -t` accepts
  the directive, and `sshd -T` reports the path without opening it, so neither the syntax
  check nor the effective-config verification catches it. This locked a live server out. The
  list is created before the drop-in names it, and the post-change verification now opens it.
- **A fix to the walk is not a fix to the sweep.** `diag`'s checks run for `doctor <subject>`
  and `troubleshoot`; the bare `doctor` sweep is a separate implementation in
  `cmd_doctor.go`. Two features shipped visible to one and not the other.
- **`ratline update` must install newly-added managed units.** `EnsureTimers` ran only from
  `init`, which happens once in a server's life, so a release that added a timer shipped the
  feature without the thing that runs it. And a self-updater can only fix updates it performs
  itself, so `doctor` also reports a managed unit that is missing.
- **systemd remembers that a unit failed after its file is gone.** Removing a unit without
  `systemctl reset-failed` leaves a "not-found failed" entry in `systemctl --failed` for
  ever, which is what monitoring watches.
- **A command that composes other commands cannot rehearse itself by running them with
  `--dry-run`.** Each step preconditions on the previous one having really happened, so the
  second is told "no such user" and the preview reports a failure for something perfectly
  buildable. Resolve the plan without executing it and print that.
- **`site logs` does not print an envelope.** It streams journalctl or tail straight to
  stdout, so `--json` has nothing to wrap. The panel reads it as text (`Client.RunText`);
  anything that assumes every command answers with an envelope breaks on this one.
- **A panel page that reloads keeps nothing.** The CSRF token lives in the page's memory,
  so `/api/me` has to return it — the first version did not, and a refreshed tab was signed
  in and silently unable to change anything. There is a regression test.
- **A partly-built session object crashes the shell.** Setting `me` from a login reply that
  lacks `capabilities` renders once before the real one arrives, and the layout reads
  `capabilities.manage_team`. `signIn` takes only the token and lets `/api/me` supply the
  shape. One shape or none.
- **Every domain looks like a file extension.** The panel's SPA fallback first used "does
  the path have an extension" to tell an asset from a route, so `/sites/example.com`
  returned 404 on a refresh — a deep link that worked when clicked and broke when pasted.
  The rule is a closed list of static extensions plus everything under `/assets/`.
- **`--dry-run` writing to `state.db` is a recurring bug, not a one-off.** It has now been
  found three times: `site add` (fixed), `scale`/`alias`/`delete`'s `PutSite` (fixed), and
  `site enable`/`disable`'s `SetSiteEnabled` plus `delete`'s `DeleteKey` — where a
  *rehearsed deletion revoked the site's SSH keys for real* while leaving the site and its
  vhost in place. The Runner's dry-run only skips external commands; a `m.State.*` write
  sits below it and has to be guarded by hand, so every new one needs
  `if m.DryRun { log "would …" } else { write }`. Assert the state row, not just the file
  on disk: checking only the nginx symlink is what let the enable/disable case through.
- **A mutation test only counts if the mutation applied.** Two edits to the panel's policy
  and argv code silently did not match (gofmt had realigned the strings), so the tests
  "passed" while proving nothing. Check the file changed before believing the result — the
  third attempt found a real hole in the secret handling.
