# ratline

Provision isolated users and their web apps on one bare Ubuntu VPS.

ratline is the provisioning core of a hosting panel — the part that creates a
system account per tenant, an nginx vhost and a systemd service per site, and
manages TLS as a resource with its own lifecycle. There is no web UI, no daemon
and no containers. It is a single static binary you run over SSH.

```bash
ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub
ratline site add api.example.com --user acme --runtime python --app-module app.main:app
ratline cert issue api.example.com --email admin@example.com
```

## Install

```bash
curl -fsSLO https://github.com/ALIRAZA47/ratline-cli/releases/latest/download/ratline-linux-amd64
curl -fsSLO https://github.com/ALIRAZA47/ratline-cli/releases/latest/download/install.sh
sudo sh install.sh
sudo ratline init
```

Or build it:

```bash
git clone https://github.com/ALIRAZA47/ratline-cli && cd ratline
make build && sudo make install
```

Requires Ubuntu 22.04+ or Debian 12+, root, and nginx. `ratline doctor` tells you
what is missing.

## 60-second quickstart

### A static site

```bash
ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub
ratline site add example.com --user acme --runtime static --spa
ratline cert issue example.com --email admin@example.com
```

nginx now serves `/home/acme/example.com/public` over HTTPS, with hashed assets
cached for a year, `index.html` never cached, and a SPA fallback so a refresh on
a deep client-side route returns the app rather than a 404.

Deploy by pushing files into that directory — over rsync, SFTP or git.

### A Python site

```bash
ratline site add api.example.com --user acme --runtime python \
    --app-module app.main:app --workers 3
```

That creates a virtualenv, installs Gunicorn (with a Uvicorn worker if the
project looks like FastAPI or Starlette), writes a systemd unit that runs it as
`acme` behind a Unix socket, and does not report success until a real HTTP request
has come back through that socket.

### A Node site

```bash
ratline runtime install node 22
ratline site add app.example.com --user acme --runtime node --entry server.js --node 22
```

`ExecStart` invokes `/opt/ratline/runtimes/node/22/bin/node server.js` by absolute
path. nvm and shell profiles are never involved, because systemd does not read
them — a unit that depended on them would work when you tested it by hand and fail
on the next boot.

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
                              │ ratline-acme-…​.service  │  runs as acme
                              │ MemoryMax, CPUQuota      │  own /tmp, own namespace
                              │ ProtectSystem=strict     │  cannot see other tenants
                              └──────────────────────────┘
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

The home directory is `0750`, never `0755`. nginx reaches a site's public files by
being a member of the tenant's group — the alternative, making homes
world-readable, would expose every tenant's files to every other tenant.

## What makes it different from a shell script

**Everything is transactional.** A mutation is staged to a temporary file,
verified (`nginx -t` before any reload, `systemd-analyze verify` before any
`daemon-reload`), then committed. Every created file, user, directory, symlink,
unit, virtualenv and port allocation registers an undo action, and a failure
unwinds them in reverse. A half-configured server is the worst outcome for a
provisioning tool, because the operator has to work out what happened before they
can retry.

**Health means health.** After a start, restart or deploy, ratline makes a real
HTTP request through the socket nginx will use and waits for an answer. A
"successful" deploy that returns 502 is a bug, not a race you learn to live with.

**No shell strings, ever.** Every external command is an argv slice, and there is
no shell in the binary registry at all — which makes it a structural property
rather than a coding convention. `--start-command` and `--build-command` are
parsed by a shell-words parser that *refuses* `;`, `&&`, `|`, backticks, `$(` and
redirections, and tells you to put the pipeline in a script in your repository.

**Errors say what to do.** Not `exit status 1`:

```
site add failed: the app did not become healthy within 30s. systemd reports
ratline-acme-api_example_com.service exited 3; the last log line was
"ModuleNotFoundError: No module named 'app'". Nothing was enabled in nginx.
  hint: check --app-module against your project layout, then re-run with --dry-run
```

**It never clobbers what it did not create.** Every generated file carries a
`# managed-by: ratline` header, and a file without one is left alone. Your own
nginx directives go in `/etc/nginx/ratline/custom/<domain>.conf`, which is included
by the generated vhost and never regenerated. Keys you add to `authorized_keys` by
hand sit outside the managed markers and survive `ratline key sync` untouched.

## Every command is safe to run twice

Re-running `site add` with identical parameters exits 0 with "already
configured". With different parameters it refuses and names the command that
would make that change. This matters because the tool is meant to be driven from
Ansible and CI as well as by hand.

## Automation

`--json` puts one object on stdout and every log line on stderr:

```json
{ "ok": true, "command": "ratline site list", "version": "1.0.0", "data": { … } }
```

Exit codes are a contract: `2` usage, `3` precondition, `4` external command,
`5` locked, `6` rollback also failed, `7` unhealthy, `8` ACME, `9` rate-limited,
`10` input required. The full table is in [COMMANDS.md](COMMANDS.md).

`--dry-run` prints every mutation without making it. Reads still execute, because
a preview built from stale facts is worse than no preview.

## Documentation

| | |
|---|---|
| [command-surface.md](command-surface.md) | Every command and flag |
| [SECURITY.md](SECURITY.md) | The isolation model, and honestly where it ends |
| [SSH.md](SSH.md) | The three key scopes, and the lockout recovery runbook |
| [RUNTIMES.md](RUNTIMES.md) | What each runtime generates, and how to debug a 502 |
| [TLS.md](TLS.md) | Issue, attach, renew; rate limits; the orange-cloud trap |

## Configuration

`/etc/ratline/config.yaml`. The shipped file is the reference: every setting has a
comment explaining what it does and why the default is what it is. Delete anything
you do not want to override — a partial file is a valid file, and an unknown key is
an error rather than a silently ignored typo.

## Non-goals

No web UI, no daemon, no API server — though the internal packages are structured
so an HTTP layer could wrap them without a refactor. No containers. No database
provisioning in v1. No DNS or mail management. No PHP yet, but the runtime package
is an interface, so adding PHP-FPM is a new file rather than a refactor.
