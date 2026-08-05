# ratline

Provision isolated users and their web apps on one bare Ubuntu VPS.

ratline is the provisioning core of a hosting panel — the part that creates a system
account per tenant, an nginx vhost and a systemd service per site, and manages TLS as a
resource with its own lifecycle. There is no web UI, no daemon and no containers. It is
a single static binary you run over SSH.

```bash
ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub
ratline site add api.example.com --user acme --runtime python --app-module app.main:app
ratline cert issue api.example.com --email admin@example.com
```

---

## Where to start

**New here?** [getting-started/](getting-started/) takes a bare server to a site
serving over HTTPS, in four pages.

**Something is broken?** [operations/troubleshooting.md](operations/troubleshooting.md),
or on the server itself — this works on a site, a tenant, a key, a certificate,
nginx, sshd, or the host:

```bash
sudo ratline troubleshoot app.example.com
```

**Looking a flag up?** [reference/commands.md](reference/commands.md).

**On a server with no browser?** Everything conceptual is built into the binary:

```bash
ratline explain            # list the topics
ratline explain sockets    # read one
```

---

## The documentation, by section

### [Getting started](getting-started/)

Read in order. Install, first site, TLS, access.

| | |
|---|---|
| [installation.md](getting-started/installation.md) | Install the binary, run `init`, install a runtime |
| [first-site.md](getting-started/first-site.md) | A tenant and its first site, for each runtime |
| [adding-tls.md](getting-started/adding-tls.md) | A certificate once DNS points here |
| [giving-access.md](getting-started/giving-access.md) | Keys, scopes, deploy keys, rotation |

### [Guides](guides/)

Task-shaped. Pick the one that matches what you are doing.

| | |
|---|---|
| [runtimes.md](guides/runtimes.md) | What each runtime generates, side by side |
| [static-sites.md](guides/static-sites.md) | SPAs, builds, caching, upload limits |
| [node-sites.md](guides/node-sites.md) | PM2, cluster mode, sockets, graceful reload |
| [python-sites.md](guides/python-sites.md) | Gunicorn, WSGI vs ASGI, Django |
| [deploying.md](guides/deploying.md) | `site deploy`, reload vs restart, what a failure leaves |
| [secrets-and-env.md](guides/secrets-and-env.md) | `.env`, `--stdin`, redaction |
| [scaling.md](guides/scaling.md) | Workers, instances, ceilings, hardening |

### [Operations](operations/)

Running a server that already exists.

| | |
|---|---|
| [monitoring.md](operations/monitoring.md) | `status` vs `doctor`, and the audit log |
| [troubleshooting.md](operations/troubleshooting.md) | 502, 404, 413, and the order to check in |
| [backup-and-restore.md](operations/backup-and-restore.md) | What `backup` archives, what `restore` rebuilds, and what neither covers |
| [drift-and-reconcile.md](operations/drift-and-reconcile.md) | When someone edited nginx by hand |
| [upgrading.md](operations/upgrading.md) | `ratline update`, rollback, schema migrations |

### [Reference](reference/)

Exhaustive rather than task-shaped.

| | |
|---|---|
| [commands.md](reference/commands.md) | Every command, generated from the binary |
| [command-surface.md](reference/command-surface.md) | The command surface, with the reasoning |
| [configuration.md](reference/configuration.md) | Every setting in `config.yaml` |
| [exit-codes.md](reference/exit-codes.md) | The exit-code contract |
| [json-output.md](reference/json-output.md) | The `--json` envelope |
| [filesystem.md](reference/filesystem.md) | Every path ratline reads or writes |

### [Security](security/)

| | |
|---|---|
| [model.md](security/model.md) | What is enforced, and where the isolation ends |
| [ssh-keys.md](security/ssh-keys.md) | The three scopes, and the lockout runbook |
| [tls.md](security/tls.md) | Rate limits, wildcards, the orange-cloud trap |

### [Concepts](topics/)

The pages `ratline explain` prints — embedded in the binary, rendered by the
documentation site, one source of truth.

`layout` · `sockets` · `node` · `python` · `static` · `tls` · `ssh` · `deploys` ·
`diagnose` · `limits` · `safety` · `state` · `databases`

---

## The shape of it

```
                    ┌─────────────────────────────────────────────┐
   :443 ─── nginx ──┤  static   root /home/acme/example.com/public │
                    │                                             │
                    │  node     proxy → unix:/run/ratline/…/app.sock
                    │  python   proxy → unix:/run/ratline/…/app.sock
                    └──────────────────────┬──────────────────────┘
                                           │
                              ┌────────────┴────────────┐
                              │ ratline-acme-….service  │  runs as acme
                              │ MemoryMax, CPUQuota     │  own /tmp, own namespace
                              │ ProtectSystem=strict    │  cannot see other tenants
                              └─────────────────────────┘
```

Per tenant:

```
/home/acme/                       0750 acme:acme   (nginx joins the acme group)
├── .ssh/authorized_keys          0600
└── example.com/                  0750
    ├── app/                      your code
    ├── public/                   nginx serves this directly
    ├── venv/                     python only
    ├── logs/{app,access,error}.log
    ├── tmp/                      0700
    ├── .env                      0600 — secrets, loaded by systemd, never served
    └── .ratline/site.yaml        the rendered manifest, so reconcile can rebuild
```

The home directory is `0750`, never `0755`. nginx reaches a site's public files by being
a member of the tenant's group — the alternative, making homes world-readable, would
expose every tenant's files to every other tenant.

Full layout: [reference/filesystem.md](reference/filesystem.md).

---

## What makes it different from a shell script

**Everything is transactional.** A mutation is staged to a temporary file, verified
(`nginx -t` before any reload, `systemd-analyze verify` before any `daemon-reload`),
then committed. Every created file, user, directory, symlink, unit, virtualenv and port
allocation registers an undo action, and a failure unwinds them in reverse. A
half-configured server is the worst outcome for a provisioning tool, because the
operator has to work out what happened before they can retry.

**Health means health.** After a start, restart or deploy, ratline makes a real HTTP
request through the socket nginx will use and waits for an answer. A "successful" deploy
that returns 502 is a bug, not a race you learn to live with.

**No shell strings, ever.** Every external command is an argv slice, and there is no
shell in the binary registry at all — which makes it a structural property rather than a
coding convention. `--start-command` and `--build-command` are parsed by a shell-words
parser that *refuses* `;`, `&&`, `|`, backticks, `$(` and redirections, and tells you to
put the pipeline in a script in your repository.

**Errors say what to do.** Not `exit status 1`:

```
site add failed: the app did not become healthy within 30s. systemd reports
ratline-acme-api_example_com.service exited 3; the last log line was
"ModuleNotFoundError: No module named 'app'". Nothing was enabled in nginx.
  hint: check --app-module against your project layout, then re-run with --dry-run
```

**It never clobbers what it did not create.** Every generated file carries a
`# managed-by: ratline` header, and a file without one is left alone. Your own nginx
directives go in `/etc/nginx/ratline/custom/<domain>.conf`, which is included by the
generated vhost and never regenerated. Keys you add to `authorized_keys` by hand sit
outside the managed markers and survive `ratline key sync` untouched.

**Every command is safe to run twice.** Re-running `site add` with identical parameters
exits 0 with "already configured". With different parameters it refuses and names the
command that would make that change. This matters because the tool is meant to be driven
from Ansible and CI as well as by hand.

---

## Automation

`--json` puts one object on stdout and every log line on stderr:

```json
{ "ok": true, "command": "ratline site list", "version": "1.0.0", "data": { … } }
```

Exit codes are a contract — the table is in
[reference/exit-codes.md](reference/exit-codes.md). `--dry-run` prints every mutation
without making it; reads still execute, because a preview built from stale facts is
worse than no preview.

---

## Configuration

`/etc/ratline/config.yaml`, read on every invocation. The shipped file is the reference:
every setting has a comment explaining what it does and why the default is what it is.
Delete anything you do not want to override — a partial file is a valid file, and an
unknown key is an error rather than a silently ignored typo.

See [reference/configuration.md](reference/configuration.md).

---

## Non-goals

No web UI, no daemon, no API server — though the internal packages are structured so an
HTTP layer could wrap them without a refactor. No containers. Databases are MongoDB
only — `ratline db` provisions them and their users, and nothing else is supported yet.
No DNS or mail management. No PHP yet, but the runtime package is an interface, so
adding PHP-FPM is a new file rather than a refactor.
