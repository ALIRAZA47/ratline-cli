# Contributing

Thanks for looking. ratline provisions tenants on servers that other people's sites run
on, so the bar here is about blast radius rather than ceremony: a bug can take a site
down or leak a key, and a half-finished mutation is worse than a refusal.

Please read [the security policy](SECURITY.md) before reporting anything that looks like
a vulnerability — not as an issue.

## Getting set up

Go 1.25 or newer; the version in `go.mod` is authoritative and CI reads it from there.

```bash
git clone https://github.com/ALIRAZA47/ratline-cli
cd ratline-cli
make build          # both binaries into ./bin
make test           # unit tests with the race detector
```

`make help` lists every target.

You do **not** need a server to develop. The unit tests use a fake command runner, so
nothing shells out. What you cannot check on a laptop is whether the thing actually
works against real nginx, systemd and certbot — that is what the integration suite is
for.

## Before you open a pull request

```bash
make fmt            # gofmt -s
make vet
make lint           # golangci-lint; the version is pinned in the Makefile
make test
make integration    # Docker; ~10 minutes
```

CI runs all of these on amd64 and arm64, plus a short fuzz pass over the validators.
`make integration` needs Docker and outbound network for the base images.

## The integration suite

It boots Ubuntu with systemd as PID 1, plus [Pebble](https://github.com/letsencrypt/pebble)
as a local ACME CA, and drives the real binary: real users, real nginx, real systemd
units, a real certificate issued over a real ACME exchange.

```bash
make integration
# the full transcript lands in test/integration/results/suite.txt
```

If you are iterating on the suite itself, the image bakes in both `run.sh` and the
binary — so a cached image runs *old code*. Shadow both with a compose override rather
than rebuilding each time, and cross-compile for the image's architecture:

```yaml
# override.yml
services:
  harness:
    build: !reset null
    image: integration-harness:latest
    volumes:
      - ./run.sh:/usr/local/bin/ratline-integration:ro
      - /abs/path/to/ratline-linux:/usr/local/bin/ratline:ro
```

Then confirm with a plain `make integration` before you push, because that is the path
CI takes.

## What the code has to do

These are not style preferences. Each one is a property the tool promises, and a change
that breaks one will be sent back.

**Never build a shell command.** `exec.Command(bin, args...)` with an argv slice, always.
No `sh -c`, no `fmt.Sprintf` into a command line. Go through `system.Runner`, which
resolves binaries to absolute paths from a registry and does not inherit `PATH`. The
linter enforces this; if you find yourself wanting a pipeline, put it in a script in the
repository.

**Secrets never touch argv.** `/proc` is world-readable. Values arrive on stdin
(`env set --stdin`) and are redacted in output unless `--reveal` is passed.

**Never install as root what belongs to a tenant.** `npm install` and `pip install` run
as the site user. Root running a tenant's `postinstall` script is a full escalation.

**Every mutation is staged, verified and reversible.** Render to a temp file, validate
it (`nginx -t`, `visudo -c`, `sshd -T`), then rename atomically — and push a rollback
step. A command that fails halfway must leave the server as it was, still serving.

**Refuse rather than guess.** If two flags contradict each other, say so; do not pick a
winner. If a file at one of ratline's paths lacks the `# managed-by: ratline` header, do
not touch it.

**Everything is safe to run twice.** Re-running `user add` for an existing user reports
what exists; it does not fail and does not change anything.

**`--dry-run` writes nothing.** Not a state row, not a port reservation, not a file. This
has been broken before, which is why there are tests at the manager level for it.

## Tests

Every fix needs a test that fails without it. Please write the test so it says *what
property is being protected and what went wrong before* — a comment naming the real
failure is worth more than the assertion. There is a lot of that in the existing tests;
match it.

Two things to avoid, both learned the hard way here:

- **A vacuous assertion is worse than no assertion**, because it reads as coverage. A
  `jq` filter that silently matches nothing, or a check that passes because the command
  refused before reaching the code, is a test that will never fail. Prove a negative
  case too: break the thing, watch the test catch it, put it back.
- **Do not restate a value the code already computed.** Compare against the property
  (`sha256.Sum256(body)`), not against a digest pasted into the test.

## Commits and pull requests

Explain *why*, in prose, in the body. The diff shows what changed; the message should
say what was wrong and what it cost, so that someone reading `git log` in a year
understands the reasoning rather than re-deriving it. Subject in the imperative, no
trailing full stop.

One concern per pull request. If you find something unrelated on the way, mention it —
do not fold it in.

## Documentation

Two places, and they must agree:

- `docs/` — Markdown, and `docs/topics/*.md` is **embedded into the binary** for
  `ratline explain`. A topic page has to read well in a terminal over SSH.
- `docs/web/` — the React documentation site (`npm --prefix docs/web run build`).

`docs/reference/commands.md` is generated. Change the flag's help text in the Go source
and run `make docs-commands`; do not hand-edit it.

## Reporting a bug

Include `ratline version`, the OS, the exact commands, and what you expected. If a
command failed, the error text matters — ratline tries hard to say what to do next, and
an error that failed to do so is itself a bug worth reporting.
