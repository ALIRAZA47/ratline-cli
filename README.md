# ratline

**Provision isolated users and their web apps on one bare Ubuntu VPS.**

[![ci](https://github.com/ALIRAZA47/ratline-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/ALIRAZA47/ratline-cli/actions/workflows/ci.yml)

ratline is the provisioning core of a hosting panel — the part that creates a system
account per tenant, an nginx vhost and a systemd service per site, and manages TLS as a
resource with its own lifecycle. No web UI, no daemon, no containers. A single static
binary you run over SSH.

```bash
ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub
ratline site add api.example.com --user acme --runtime python --app-module app.main:app
ratline cert issue api.example.com --email admin@example.com
```

Three commands, and a Python app is serving over HTTPS from its own system account,
behind its own Unix socket, under a systemd unit with memory and CPU ceilings.

## Why

A shell script that provisions a tenant is easy to write and horrible to own, because
the interesting cases are all failures. ratline is built around those instead:

- **Every mutation is transactional.** Staged to a temporary file, verified (`nginx -t`
  before any reload, `sshd -T` before any sshd change), then committed. Every file,
  user, unit, symlink, virtualenv and port allocation registers an undo action, and a
  failure unwinds them in reverse. A half-configured server is the worst outcome for a
  provisioning tool, because someone has to work out what happened before they can
  retry.
- **Health means health.** After a start, restart or deploy, ratline makes a real HTTP
  request through the socket nginx will use, and waits for an answer. A "successful"
  deploy that returns 502 is a bug, not a race to live with.
- **No shell strings, ever.** Every external command is an argv slice, and there is no
  shell in the binary registry at all — a structural property, not a convention. The
  linter enforces it.
- **It never clobbers what it did not create.** Generated files carry a
  `# managed-by: ratline` header; a file without one is left alone. Your own nginx
  directives live in an include that is never regenerated.
- **Everything is safe to run twice.** Re-running `site add` with the same parameters
  exits 0 with "already configured". With different ones it refuses and names the
  command that would make that change — because this is meant to be driven from Ansible
  and CI as much as by hand.
- **Errors say what to do next.** Not `exit status 1`, but which unit exited with what,
  the last log line, what was left unchanged, and the flag to look at.

## Install

```bash
curl -fsSL https://ratline-cli.vercel.app/install.sh | sudo sh
```

One command on a bare Debian or Ubuntu server. It resolves the latest release, downloads
the binaries for this architecture, **verifies them against the release's own
`SHA256SUMS`**, installs them, and runs `ratline init` — configuration, directory layout,
and the renewal and key-pruning timers started. It offers to `apt-get install` nginx and
certbot if they are missing, naming them first.

Piping into a root shell is worth being deliberate about. The script refuses rather than
warns if a checksum is missing or wrong, but that cannot protect you from a compromised
release — so read it first if you would rather, which is the same two commands:

```bash
curl -fsSLO https://ratline-cli.vercel.app/install.sh
less install.sh && sudo sh install.sh
```

`RATLINE_VERSION=v0.2.0` pins a release, `ASSUME_YES=1` suits Ansible and cloud-init, and
`NO_INIT=1` installs the binaries and stops. From source, or from a tarball or `.deb`:
[installation.md](docs/getting-started/installation.md).

Upgrading later is one command too — `ratline update` checksums the new release, proves
the binary runs and can read this server's state, swaps it atomically, and keeps the old
one for `--rollback`.

## Documentation

**[ratline-cli.vercel.app](https://ratline-cli.vercel.app)** — searchable, with every
command and flag. The same content as [docs/](docs/), which is the source.

| | |
|---|---|
| [Getting started](docs/getting-started/) | Bare server to HTTPS, in four pages |
| [Guides](docs/guides/) | Task-shaped: static, node, python, deploys, secrets, scaling |
| [Operations](docs/operations/) | Monitoring, troubleshooting, backups, drift, upgrades |
| [Reference](docs/reference/) | Every command, setting, exit code and path |
| [Security](docs/security/) | What is enforced, and where the isolation ends |

Everything conceptual is also embedded in the binary, because the situation in which you
need it is usually a server with no browser:

```bash
ratline explain            # list the topics
ratline explain sockets    # read one
```

And when something is broken, one command walks the dependency chain and stops at the
first failure — on a site, a tenant, a key, a certificate, nginx, sshd or the host:

```bash
sudo ratline troubleshoot app.example.com
```

## Automation

`--json` puts one object on stdout and every log line on stderr:

```json
{ "ok": true, "command": "ratline site list", "version": "1.0.0", "data": { … } }
```

Exit codes are a contract, documented in
[reference/exit-codes.md](docs/reference/exit-codes.md). `--dry-run` prints every
mutation without making it.

## Testing

```bash
make test           # unit tests, race detector
make lint           # golangci-lint, pinned version
make integration    # Ubuntu + systemd + real nginx + a local ACME CA, in Docker
```

The integration suite boots systemd as PID 1 and drives the real binary against real
nginx, real systemd units and a real certificate issued over a real ACME exchange
against [Pebble](https://github.com/letsencrypt/pebble). It is where the bugs that
matter get found.

## Non-goals

No web UI, no daemon, no API server — though the internal packages are structured so an
HTTP layer could wrap them without a refactor. No containers. No database provisioning
in v1. No DNS or mail management. No PHP yet, but the runtime package is an interface, so
adding PHP-FPM is a new file rather than a refactor.

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md) covers the setup, the test commands, and the
properties a change has to preserve. Please read [SECURITY.md](SECURITY.md) before
reporting anything that looks like a vulnerability — privately, not as an issue.

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## Licence

[GNU AGPL-3.0](LICENSE). Copyright © 2026 Ali Raza Khan.

Use it, run it, change it, contribute to it. The one condition is that the freedom
travels with it: if you distribute a modified version, or **run one as a network
service**, the people using it are entitled to that version's source under the same
licence.

That second clause is the reason for AGPL rather than GPL. ratline is the provisioning
core of a hosting panel, so the obvious way to take it without giving anything back is
to wrap it in a panel and sell access — which is network use, not distribution, and
which plain GPL does not reach.

Using ratline to host your own or your clients' sites is not network use of ratline.
Selling a hosting product whose control plane *is* ratline is.
