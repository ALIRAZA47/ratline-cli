# ratline — command surface reference

This file is the authoritative specification of `ratline`'s CLI surface. It is the
source every other piece of documentation is derived from. Do not invent
commands, flags, defaults or exit codes that are not written here.

Invocation is always `ratline <group> <verb> [args]`.

Every command listed here is implemented. What remains unbuilt is noted
explicitly where it applies: `ratline db` provisions MongoDB only and is behind
`features.db_provisioning` because it needs an admin connection string, and PHP,
Go and Ruby runtimes are not present (the runtime package is an interface, so
each is a new file rather than a refactor).

---

## Global flags

| Flag | Effect |
|---|---|
| `--json` | Machine-readable output on stdout; logs go to stderr |
| `--quiet`, `-q` | Errors only |
| `--verbose`, `-v` | Debug logging |
| `--dry-run` | Print every mutation (files, commands, permissions) without executing it. Reads still run, so the preview reflects the real system |
| `--yes`, `-y` | Assume yes; required for destructive operations without a terminal |
| `--interactive`, `-i` | Launch the guided wizard, prompting only for what was not supplied as a flag |
| `--no-input` | Never prompt; error instead. Implied when stdout is not a TTY |
| `--config <path>` | Configuration file. Default `/etc/ratline/config.yaml`; `RATLINE_CONFIG` is also honoured |

Flag combinations that are refused as usage errors (exit 2), rather than one
silently winning: `--quiet` with `--verbose`, `--json` with `--interactive`,
`--interactive` with `--no-input`, `--interactive` with `--yes`.

## Exit codes

| Code | Name | Meaning |
|---|---|---|
| 0 | `ok` | Success |
| 1 | `error` | Unclassified failure |
| 2 | `usage` | Bad flags, bad arguments, failed validation |
| 3 | `precondition_failed` | The system is not in a state where this can run |
| 4 | `external_command_failed` | An external command failed |
| 5 | `locked` | Another ratline invocation holds the lock |
| 6 | `rollback_failed` | The operation failed and so did its rollback — needs a human |
| 7 | `health_check_failed` | Started, but never became healthy |
| 8 | `acme_challenge_failed` | ACME challenge failed |
| 9 | `rate_limited` | Would exceed a CA rate limit; the message includes a retry-after |
| 10 | `input_required` | A prompt was needed but input is unavailable |

## `--json` envelope

Every `--json` invocation emits exactly one object on stdout:

```json
{
  "ok": true,
  "command": "ratline site list",
  "version": "1.0.0",
  "data": {},
  "error": {
    "code": 3,
    "name": "precondition_failed",
    "message": "…",
    "hint": "…",
    "fields": {}
  }
}
```

`data` is present on success, `error` on failure. Private key material never
appears in any JSON output.

---

## USERS

```
ratline user add <username>
    [--ssh-key <path|url|->]     Public key(s); '-' reads stdin; repeatable
    [--password-login]           Default: disabled, keys only
    [--shell <path>]             Default /bin/bash; /usr/sbin/nologin to disable
    [--sftp-only]                Chroot to home via internal-sftp, no shell
    [--quota <size>]             e.g. 20G, if filesystem quotas are available
    [--memory-max <size>]        Default cgroup ceiling inherited by their sites
    [--comment <text>]
ratline user list [--json]
ratline user show <username>     Home, sites, disk usage, keys, running services
ratline user disable <username>  Lock login, stop all their site services, serve 503
ratline user enable <username>
ratline user delete <username> [--purge] [--backup <dir>]
                                 Refuses if sites exist unless --purge
ratline user password set <username> [--stdin]
```

A created user gets: its own group, a locked password, a `0750` home, and no
sudo. `nginx` is granted read access to the site's `public/` by being added to
the user's group — never by loosening world permissions.

## SSH KEYS

One command group covers all three scopes. Every key carries a **required**
human label, so an operator can tell "Ali MacBook" from "CI runner" two years
later.

```
ratline key add --label "<label>" (--key <path|url|-> | --from-github <u> | --from-gitlab <u>)
    --scope global|user|site
    [--user <username>]          Required for --scope user and --scope site
    [--site <domain>]            Required for --scope site
    [--sftp-only]                Force SFTP, no shell (default for site scope)
    [--allow-shell]              Site scope only; opt-in, and warned about
    [--from <cidr,cidr>]         Source IP restriction → from="…" option
    [--expires <date|duration>]  e.g. 2026-12-31 or 90d → expiry-time="…" option
    [--no-agent-forwarding] [--no-port-forwarding] [--no-pty]
    [--command <name>]           Named preset: rsync-only, git-only, sftp-only
ratline key list [--scope <s>] [--user <u>] [--site <d>] [--unused <days>]
                 [--expiring <days>] [--json]
ratline key show <fingerprint|label>
ratline key remove <fingerprint|label> [--scope <s>] [--user <u>] [--site <d>]
ratline key revoke <fingerprint|label> --everywhere   Remove from every scope
ratline key move <fingerprint> --to-scope site --site <d>
ratline key audit                Duplicates across users, weak algorithms,
                                 never-used keys, expired-but-present, keys
                                 added outside ratline
ratline key test <fingerprint>   Explain exactly what this key can reach
ratline key sync                 Re-render every authorized_keys file from state
ratline site deploy-key create <domain> [--type ed25519]
                                 An *outbound* keypair so the site can pull from
                                 a private repo; prints the public key
ratline site deploy-key show|rotate|remove <domain>
```

Aliases: `ratline user key add|list|remove` map onto `key … --scope user`.

### The three scopes

| Scope | Grants | Lands in | Typical holder |
|---|---|---|---|
| `global` | Server administration: shell as the admin user, permission to run `ratline` | the admin user's `authorized_keys` | you and your ops team |
| `user` | Full access to one tenant: interactive shell, every site that user owns | `/home/<user>/.ssh/authorized_keys` | the client who owns those sites |
| `site` | SFTP / rsync / git confined to **one site directory**, no interactive shell by default | the same file, with `restrict` + a forced command | a contractor or CI runner on one site |

Default options for every scope start from OpenSSH's `restrict` (no port
forwarding, agent forwarding, X11, PTY or user rc), with `pty` re-enabled only
for scopes that get a shell. Permissiveness is opted into, never out of.

### What site scope actually enforces

Site scope is a **blast-radius and usability boundary, not a kernel-enforced
one**. The key still authenticates as the site owner's UID; the confinement is
sshd's forced command plus the `ratline-shell` wrapper. That reliably prevents
accidents and stops a contractor wandering into a sibling site. It does not stop
a determined attacker who already has code execution as that UID, and
`--allow-shell` removes most of it.

Where genuine per-site isolation is required, the answer is **one ratline user
per site**. `user add` is cheap for exactly this reason.

### `ratline key test` output

```
Key       SHA256:x9K…   "Deploy CI"   ed25519
Scope     site → example.com  (owner: alice)
Login     alice@server — forced command only, no interactive shell
Allowed   sftp, rsync, git-upload-pack, git-receive-pack
          confined to /home/alice/example.com (symlinks resolved)
Denied    shell, port forwarding, agent forwarding, X11, PTY
Source    203.0.113.0/24 only
Expires   2027-01-01 (149 days)
Last use  2026-08-02 14:11 from 203.0.113.19
Note      Runs as UID alice. Not a kernel boundary — see SECURITY.md.
```

### Key validation policy

- Every key is validated with `ssh-keygen -l -f` before it goes near a file.
- `ssh-dss` is refused outright; RSA under 3072 bits is refused, under 4096 warned; `ed25519` preferred.
- **Any options the submitted line already carries are stripped.** A pasted key
  bringing its own `command=` or `permitopen=` is an escalation vector: ratline
  parses out algorithm, blob and comment, discards the rest, and applies only the
  options it derived from the flags.
- Keys with newlines, NULs or over-long lines are refused; the file size is capped.
- A fingerprint already present anywhere on the box is refused unless
  `--allow-duplicate`, and the message names where it already exists.
- `--from-github <user>` fetches `https://github.com/<user>.keys` with full
  certificate verification, validates each line independently, then shows every
  fingerprint and asks for confirmation.

### Lockout safety

Bricking SSH on a remote VPS has no recovery path, so:

1. Back up the config, apply the change, run `sshd -t`. On failure, restore and reload.
2. **Reload, never restart** — existing sessions survive.
3. After reloading, prove login still works. If verification cannot run or fails, restore and report the change as rejected.
4. Never touch `PermitRootLogin`, `PasswordAuthentication`, `AllowUsers` or `Port` without an explicit flag *and* a typed confirmation, printing the rollback command first.
5. `key remove` and `user delete` refuse to remove the last working global credential without `--force` and a typed confirmation.

## SITES

### Common flags

```
ratline site add <domain> --user <username> --runtime static|node|bun|python
    [--alias www.example.com]    Repeatable
    [--ssl letsencrypt|selfsigned|none]
                                 Convenience only: runs `cert issue` (or
                                 `cert selfsign`) as a final step. Default:
                                 letsencrypt if the domain already resolves here,
                                 otherwise selfsigned with a printed note.
                                 A cert failure never fails the site creation.
    [--email admin@example.com]  ACME contact
    [--www-redirect apex|www|none]
    [--no-enable]                Write config but do not symlink or start
    [--repo <git-url>] [--branch main]
```

### `--runtime static`

nginx serves files straight from the document root. Nothing runs.

```
    [--root public]              Subdir under the site dir; default "public"
    [--spa]                      try_files $uri $uri/ /index.html
    [--index index.html]
    [--build-command "npm run build"] [--build-output dist]
```

### `--runtime node`

nginx reverse-proxies to a Unix socket (default) or an allocated port. The app
runs under `ratline-<slug>.service`.

```
    --entry server.js            OR --start-command "npm run start"
    [--node 22]                  Must be installed; see `ratline runtime`
    [--package-manager npm|pnpm|yarn|bun]
    [--listen socket|port]       Default socket; port auto-allocated 20000-29999
    [--install-command "npm ci --omit=dev"]
    [--build-command "npm run build"]
    [--instances 1]              >1 = PM2 cluster workers on one socket (node only)
    [--public public]            Static dir served by nginx, bypassing the app
```

`ExecStart` invokes the managed Node binary by absolute path
(`/opt/ratline/runtimes/node/22/bin/node server.js`). nvm, shell profiles and
login shells are never involved. `--start-command` is resolved to an argv slice;
anything needing a shell is refused.

### `--runtime bun`

bun straight under systemd, behind a Unix socket (default) or an allocated port.
There is no PM2 here, so `--daemon` and `--instances` are refused rather than
ignored, and `site reload` on a bun site is a restart.

```
    --entry server.ts            OR --start-command "bun run start"
    [--bun 1.2]                  Must be installed; see `ratline runtime`
    [--package-manager npm|pnpm|yarn|bun]   Default bun
    [--listen socket|port]       Default socket; port auto-allocated 20000-29999
    [--install-command "bun install --frozen-lockfile"]
    [--build-command "bun run build"]   Often unnecessary: TS and JSX run unbuilt
    [--public public]            Static dir served by nginx, bypassing the app
```

`--entry` takes everything the node runtime does plus `.jsx` and `.tsx`, because
bun transpiles on the way in. `ExecStart` invokes the managed binary by absolute
path (`/opt/ratline/runtimes/bun/1.2/bin/bun server.ts`), which is what stops
`bun upgrade` in a tenant's home from changing the interpreter a unit executes.

### `--runtime python`

Gunicorn (WSGI) or Gunicorn with a Uvicorn worker (ASGI), in a per-site
virtualenv, behind a Unix socket.

```
    --app-module app.main:app    Import path to the WSGI/ASGI callable
    [--python 3.12]
    [--asgi | --wsgi]            Default: auto-detect (FastAPI/Starlette → ASGI)
    [--server gunicorn|uvicorn]  Default gunicorn; ASGI → gunicorn + UvicornWorker
    [--workers 3]                Default (2 × cores) + 1, capped at 8
    [--requirements requirements.txt]  Also detects pyproject/uv/poetry
    [--static-url /static --static-dir staticfiles]
    [--manage-py manage.py]      Enables `site deploy --migrate|--collectstatic`
```

### Lifecycle and operations

```
ratline site list [--user <u>] [--runtime <r>] [--json]
ratline site show <domain>       Runtime, unit state, socket, cert expiry, last deploy
ratline site enable|disable <domain>
ratline site delete <domain> [--purge] [--backup <dir>]
ratline site start|stop|restart|status <domain>
ratline site reload <domain>     Zero-downtime where the runtime supports it
ratline site deploy <domain> [--pull] [--install] [--build] [--migrate]
                              [--collectstatic] [--restart]
                                 Runs the chain, health-checks, rolls back on failure
ratline site logs <domain> [--app|--access|--error] [--follow] [--lines 100]
ratline site scale <domain> [--workers 4] [--instances 2] [--memory-max 512M]
                            [--cpu-quota 50%]
ratline site env set <domain> KEY=VALUE [KEY=VALUE …]
ratline site env get|unset <domain> KEY
ratline site env list <domain> [--reveal]     Values masked unless --reveal
ratline site env import <domain> --file .env
ratline site alias add|remove <domain> <alias>
ratline site runtime <domain> --node 22 | --bun 1.2 | --python 3.12
```

After `start`, `restart` or `deploy`, ratline **waits for health**: it polls the
socket or port with a real HTTP request until it answers or the timeout (default
30s) elapses. A "successful" deploy that returns 502 is a bug.

## CERTIFICATES

TLS is a first-class, independently managed resource, not a flag on `site add`. A
site can be created and serving HTTP before DNS has propagated, then have a real
certificate issued and attached later — which is the normal order of operations
when a client is still moving their domain.

```
ratline cert issue <domain>
    [--alias www.example.com]    Repeatable; adds SANs. Defaults to site aliases
    [--san <domain>]             Extra SAN not registered as a site alias
    [--challenge http|dns]       Default http (webroot). Wildcards force dns
    [--dns-provider cloudflare|route53|digitalocean|…]
    [--dns-credentials <path>]   Must be 0600; validated before use
    [--dns-propagation 60]       Seconds to wait before validation
    [--email admin@example.com]
    [--staging]                  Let's Encrypt staging — use while testing
    [--key-type ecdsa|rsa]       Default ecdsa (P-256)
    [--force]                    Re-issue even if a valid cert exists
    [--attach|--no-attach]       Default: attach and reload nginx
    [--dry-run]                  Full validation, no rate-limit cost
ratline cert attach <domain> [--cert <name>]
ratline cert detach <domain>
ratline cert list [--expiring <days>] [--orphaned] [--json]
ratline cert show <domain>
ratline cert renew [<domain>] [--all] [--force] [--dry-run]
ratline cert revoke <domain> [--reason keycompromise|superseded|cessationofoperation]
ratline cert delete <domain> [--keep-files]    Refuses while a site uses it
ratline cert import <domain> --cert fullchain.pem --key privkey.pem [--chain chain.pem]
ratline cert selfsign <domain> [--days 365]
ratline cert auto-renew status|enable|disable [<domain>]
ratline cert test-renewal
ratline cert account show|register --email <e>
```

### Preflight, before an ACME attempt is spent

All of these run and report **every** problem at once:

1. The site exists in state and its vhost is enabled.
2. **DNS**: A/AAAA records for the domain and every SAN are resolved (following CNAMEs) and compared against this server's public addresses. A mismatch is refused with the observed values, unless `--force`.
3. **Proxy detection**: if the resolved address belongs to a known proxy range, HTTP-01 will fail unless the record is DNS-only. Suggests `--challenge dns` or an Origin certificate via `cert import`.
4. **Reachability**: a random token is written to the shared webroot and fetched over HTTP. A 200 with the exact token is the only pass.
5. **Conflicts**: no other vhost claims the same `server_name`.
6. **Tooling**: certbot present, and the DNS plugin installed; if not, the exact install command is printed.
7. **Rate-limit budget** (below).
8. A wildcard request forces `--challenge dns`, and says why HTTP-01 cannot work.

### Rate limits — tracked, not discovered

Every issuance attempt, successful or not, is recorded. The remaining budget per
registered domain is computed before acting, and an attempt that would exceed it
is **refused with a countdown**. Default budgets (configurable, because they are
CA policy and do change): 50 certificates per registered domain per week, 5
duplicate certificates per week, 5 failed validations per hostname per hour, 300
new orders per 3 hours.

`--staging` certificates are marked visibly untrusted in `cert list` so nobody
ships one to production by accident.

### Issue → attach is transactional

Preflight → certbot (argv only, never a shell string) → parse and translate the
result → stage the vhost → `nginx -t` → reload → **verify for real** by opening a
TLS connection with SNI and asserting the served chain matches the expected
fingerprint, covers the requested SANs and validates against the system root
store → record in state. A certificate that exists on disk but is not being
served is a failure, not a success.

Any failure restores the previous vhost and reloads.

### `cert list` columns

`DOMAIN | SANS | ISSUER | KEY | EXPIRES | DAYS | STATUS | SITES | AUTO-RENEW`

Status is one of: `valid`, `expiring` (<21d, yellow), `critical` (<7d, red),
`expired`, `degraded` (last renewal failed), `staging`, `self-signed`,
`orphaned` (no site attached), `unattached-mismatch` (the cert exists but the
vhost points elsewhere). Certificates issued by hand outside ratline are detected
and listed too — that is exactly the residue someone leaves behind.

### Renewal

A timer runs twice daily with a randomised delay. certbot's own timer is
neutralised at install time so the two never race. Renewal is attempted under 30
days remaining. certbot invokes `ratline cert deploy-hook`, which maps the
renewed lineage to its sites, runs `nginx -t`, and reloads **only** the affected
site. Never a blanket restart.

On failure: exponential backoff, the previous certificate is retained, the cert
is marked `degraded` in state, and `doctor` surfaces it.

## RUNTIMES

```
ratline runtime list             Installed Node, Bun and Python versions, and
                                 which sites use each
ratline runtime install node 22 [--with-pm2]
ratline runtime install bun 1.2 [--baseline]
ratline runtime install python 3.12
ratline runtime default node 22 | bun 1.2 | python 3.12
```

Bun is a GitHub release asset verified against the `SHASUMS256.txt` beside it,
extracted in-process rather than through `unzip`, and landed root-owned so
`bun upgrade` cannot move the binary a unit executes. `--baseline` forces the
build for x86-64 CPUs without AVX2; without it `/proc/cpuinfo` decides, because
getting that wrong kills the process on an illegal instruction with no message
of its own.

## OPERATIONS

```
ratline version                  Version, commit, build date, OS, nginx version,
                                 available runtimes — enough for a bug report
ratline man [--dir <path>]       Write man pages for every command
ratline completion bash|zsh|fish|powershell
ratline doctor                   nginx -t, failed units, dead sockets, cert expiry,
                                 orphaned configs, state-vs-filesystem drift,
                                 permission anomalies, ports allocated but unused
ratline reconcile [--fix]        Re-render all configs from state; report or repair
ratline backup <user|domain> --out <dir>
ratline export --json            Full state dump for migration
ratline init                     First-run server setup wizard
```

## INTERACTIVE MODE

```
ratline                          No args on a TTY → interactive main menu
ratline user add -i
ratline site add -i
ratline key add -i
ratline cert issue -i
ratline init
```

Trigger rules:

- `ratline` with no arguments **on a TTY** → the main menu, with a live server summary: users, sites by runtime, failed units, certs expiring soon.
- `ratline <command> -i` → the wizard for that command, pre-filled with the flags already supplied.
- Missing required flags **on a TTY** → offer the wizard instead of dumping usage: `Missing --user and --runtime. Run with -i for a guided setup, or see 'ratline site add --help'.`
- **Not a TTY, or `--no-input`, or `--yes`** → never prompt, under any circumstance. Fail with exit 2 naming every missing flag. A prompt in a CI pipeline is a hung build.
- `NO_COLOR` is respected, and the interface degrades to plain line-based prompts when `TERM=dumb` or the terminal is narrower than 60 columns.

The wizard is a flag collector, never a second implementation: both paths call
the same internal APIs. **It always echoes the equivalent command** — a summary
panel of every resolved value plus the exact non-interactive invocation that
reproduces it, with `[Run] [Copy] [Edit a field] [Cancel]`. That is how operators
graduate from the wizard to scripting it.

Destructive operations show a precise inventory of what will be deleted (paths,
unit, cert, port, state rows, home directory size) and require typing the domain
or username — never a bare y/N.

---

## Filesystem layout

Per user:

```
/home/<user>/                       0750 <user>:<user>   (nginx joins the user's group)
├── .ssh/authorized_keys            0600, dir 0700
├── logs/
└── <domain>/                       0750 <user>:<user>
    ├── app/                        Application code
    ├── public/                     Static assets served directly by nginx  0750
    ├── venv/                       python runtime only, 0750
    ├── logs/{app,access,error}.log 0640 <user>:adm
    ├── tmp/                        0700
    ├── .env                        0600 <user>:<user>  ← secrets
    └── .ratline/site.yaml          0640  ← rendered manifest, for reconcile
```

System paths:

```
/etc/ratline/config.yaml
/etc/nginx/sites-available/<domain>.conf   → symlink in sites-enabled/
/etc/nginx/ratline/                        Shared snippets
/etc/nginx/ratline/custom/<domain>.conf    Operator additions, never regenerated
/etc/systemd/system/ratline-<user>-<domain>.service
/run/ratline/<user>-<domain>/app.sock      0660 <user>:www-data
/var/lib/ratline/state.db                  SQLite, 0600 root:root
/var/log/ratline/audit.log
/etc/logrotate.d/ratline-<domain>
/opt/ratline/runtimes/node/<ver>/
/opt/ratline/runtimes/bun/<ver>/
/opt/ratline/runtimes/python/<ver>/
```

The slug rule: `<user>-<domain>` with dots replaced by underscores, lowercased,
truncated with a digest suffix if too long, and collision-checked against
existing units. `alice` + `example.com` → `alice-example_com`, giving
`ratline-alice-example_com.service`. The length limit is driven by
`sockaddr_un.sun_path`, which is 108 bytes.

Rules the implementation enforces:

- A document root is **always** under the owning user's home. Any path that
  escapes it after cleaning *and symlink resolution* is refused.
- nginx needs read access to `public/` only, granted by adding `www-data` to the
  site user's group — never by loosening world permissions. The home stays `0750`.
- `.env` is `0600`, owned by the site user, loaded by systemd's
  `EnvironmentFile=` (read as root before privileges are dropped), and never
  inside a directory nginx can serve. nginx additionally denies dotfiles.
- `umask 027` for all provisioning writes.

## Process supervision

One systemd unit per dynamic site, with hardening verified at install time. If a
directive breaks the application, ratline reports **which** directive to relax
and offers `--relax <directive>` rather than silently dropping hardening.

Key directives: `User`/`Group` (the site owner), `WorkingDirectory`,
`EnvironmentFile`, `RuntimeDirectory`, `UMask=0027`, `Restart=always`,
`MemoryMax`/`MemoryHigh`/`CPUQuota`/`TasksMax`/`LimitNOFILE`,
`NoNewPrivileges`, `PrivateTmp`, `PrivateDevices`, `ProtectSystem=strict`,
`ProtectHome=tmpfs` with `BindPaths` for the site directory,
`ProtectKernelTunables`, `ProtectKernelModules`, `ProtectControlGroups`,
`RestrictNamespaces`, `RestrictSUIDSGID`, `LockPersonality`,
`SystemCallFilter=@system-service`.

A `ratline.target` lets an operator `systemctl stop ratline.target` to stop every
managed site at once.

## Security model

- **Never build shell strings.** Every invocation is an argv slice. There is no
  shell in the binary registry at all, which makes this structural rather than a
  convention. `--start-command` and `--build-command` are parsed by a
  shell-words parser that *refuses* `;`, `&&`, `|`, backticks, `$(`,
  redirections and newlines; a genuine pipeline goes in a script in the repo.
- Absolute paths for all external binaries, resolved at startup. `PATH` is never
  inherited for lookups, and children get a scrubbed environment.
- `npm install` and `pip install` run **as the site user**, never as root.
- Username: `^[a-z_][a-z0-9_-]{0,31}$`, no reserved names, no `/etc/passwd` or
  `/etc/group` collision.
- Domain: per-label `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`, ≤253 characters, ≥2
  labels, a valid public suffix, IDN converted to punycode before use.
- App module: `^[A-Za-z_][A-Za-z0-9_.]*:[A-Za-z_][A-Za-z0-9_]*$` — this string
  lands on a command line and inside a unit file.
- Refuses to run unless EUID 0, and refuses if its own binary is group- or
  world-writable.
- Secrets never in argv; `env set` supports `--stdin`; values are redacted in
  logs, errors and `env list` unless `--reveal`.
- Created users get no sudo.

The isolation model and its limits are stated plainly: a shared kernel, systemd
sandboxing as defence in depth rather than virtualization, tenants can still see
process names, and cgroup limits are advisory unless configured.

## Transactional behaviour

Every mutating operation is staged, verified, then committed:

1. Validate all inputs before touching the system.
2. Check preconditions: user exists, domain not already configured, port free,
   runtime installed, disk space available, entry point present.
3. Write configs to temporary files in the same directory, then rename (atomic).
4. `nginx -t` **before** reload; `systemd-analyze verify` before `daemon-reload`.
   On failure, restore the previous config and return non-zero with the raw error.
5. `systemctl reload`, not restart, for nginx.
6. Maintain a rollback stack: every created file, user, directory, symlink, unit,
   venv and port allocation registers an undo action. On error, unwind in reverse
   and report exactly what was rolled back and what could not be.
7. `site deploy` keeps the previous release addressable and reverts if the
   post-deploy health check fails.
8. Full idempotency: re-running `site add` with identical parameters exits 0 with
   "already configured"; with different parameters it errors and names the
   specific update command.

An exclusive `flock` is held for the duration of any mutating command; a second
invocation fails fast with exit 5 and names the holder.

## Error style

Errors state what failed, why, and the next action.

Bad: `error: exit status 1`

Good:

```
site add failed: the app did not become healthy within 30s. systemd reports
ratline-alice-example_com.service exited 3; the last log line was
"ModuleNotFoundError: No module named 'app'". Nothing was enabled in nginx.
Check --app-module against your project layout, then re-run with --dry-run to
preview.
```

On any failed start, the last 20 lines of `journalctl -u <unit>` are surfaced
automatically — the operator never has to go and find them.
