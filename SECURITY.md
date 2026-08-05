# Security policy

ratline runs as root on a server that holds every tenant's code, keys and TLS private
keys. A bug here is not a crash — it is one tenant reading another's data, or a
credential surviving a revocation. Reports are welcome and taken seriously.

## Reporting a vulnerability

**Do not open a public issue.** Use GitHub's private reporting:

**[Report a vulnerability →](https://github.com/ALIRAZA47/ratline-cli/security/advisories/new)**

If that is unavailable to you, open an issue titled only `security report` with no
detail, and wait to be contacted.

Please include:

- the output of `ratline version` (it names the OS and the runtimes it found)
- the OS and version — ratline targets Debian and Ubuntu
- the smallest sequence of commands that reproduces it
- what you expected to be prevented, and what happened instead

You will get an acknowledgement within **3 working days** and an assessment within
**10**. If a fix is warranted you will be credited in the advisory unless you ask not
to be. Please give a fix a reasonable window before publishing; there is no bounty.

## What is in scope

Anything that breaks one of the properties ratline claims to enforce:

- **Tenant isolation** — one tenant reading, writing or executing inside another's
  home, socket, `.env`, virtualenv or document root.
- **Privilege escalation** — a tenant reaching root, or a site's process escaping the
  systemd unit's confinement.
- **Credential exposure** — a private key, an `.env` value or a DNS API token appearing
  in a log, an error, `--json` output, `ratline export`, a backup archive, argv, or
  anywhere nginx can serve it.
- **Command injection** — any path where an attacker-influenced string becomes part of
  a command. ratline builds argv slices and has no shell in its binary registry; a way
  around that is a serious bug.
- **Path traversal** — a document root, static alias or backup path escaping the site
  directory.
- **Failed revocation** — `key remove` or `user delete` leaving working access behind.
- **Refusals that do not hold** — the list in
  [docs/security/README.md](docs/security/README.md) is a contract. A way to get past
  any of it without the documented flag and confirmation is in scope.

## What is not

- Anything requiring root on the server already. Root is the trust boundary, not a
  target: an operator who can run ratline can do everything ratline does.
- A tenant reading files they were deliberately given, or the contents of their own
  home.
- Rate limits, quotas or resource exhaustion caused by a tenant's own application.
  `ratline explain limits` describes what is and is not bounded.
- Weaknesses in nginx, certbot, systemd, OpenSSH, Node or Python themselves. Report
  those upstream — though if ratline *configures* them insecurely, that is in scope.
- A missing hardening measure with no demonstrated impact. Suggestions are welcome as
  ordinary issues.

## Supported versions

Only the latest release. ratline is a single static binary and
[`ratline update`](docs/operations/upgrading.md) replaces it in one command, keeping the
previous binary for `--rollback`, so there are no maintenance branches to backport to.

## The model, in short

Every tenant is a system account: its own user, group, home and shell. Isolation is
enforced by the kernel — filesystem permissions, systemd unit confinement and Unix
socket ownership — not by anything ratline checks at runtime. That is deliberate: a
permission bit keeps holding after ratline exits, and a check does not.

The full reasoning, including what is *not* enforced and why, is in
[docs/security/model.md](docs/security/model.md). It is also embedded in the binary, so
it is readable on a server with no browser:

```bash
ratline explain safety
```
