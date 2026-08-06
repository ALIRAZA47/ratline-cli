# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`ratline` is a single static Go binary that runs **as root on a bare Ubuntu/Debian VPS** and
provisions isolated system users and their web apps (static, node, python). It is the
provisioning core of Ploi/RunCloud/Dokku without the web UI and without containers: it
configures nginx, systemd, SSH, certbot and MongoDB rather than installing or replacing them.

Because it runs as root on someone's server, the invariants below are not style preferences —
they are the properties the tool promises.

## Commands

```bash
make build            # both binaries into bin/ (CGO_ENABLED=0, ldflags from git)
make test             # go test -race ./...
make lint             # golangci-lint, pinned to the version CI uses
make check            # fmt + vet + test
make integration      # the real suite: Ubuntu + systemd PID 1 + Pebble ACME + MongoDB
```

One test, one package:

```bash
go test -run TestGrantSudo ./internal/user
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
- **A command that composes other commands cannot rehearse itself by running them with
  `--dry-run`.** Each step preconditions on the previous one having really happened, so the
  second is told "no such user" and the preview reports a failure for something perfectly
  buildable. Resolve the plan without executing it and print that.
