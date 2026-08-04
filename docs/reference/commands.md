# Commands

Generated from the binary itself with `make docs-commands`, so it cannot drift
from the real flags. For *why* each command behaves as it does, read the
[guides](../guides/), the [security notes](../security/), or the concept pages the
binary itself carries — `ratline explain`.

## Exit codes

Automation branches on these, so they are a contract and are never renumbered.

| Code | Name | Meaning | What to do about it |
|---|---|---|---|
| 0 | `ok` | Success | — |
| 1 | `error` | Unclassified failure | Re-run with `--verbose`; this usually means a bug |
| 2 | `usage` | Bad flags, arguments, or input that failed validation | Read the message: it names every missing or wrong flag at once |
| 3 | `precondition_failed` | The system is not in a state where this can run | The message says which precondition. Nothing was changed |
| 4 | `external_command_failed` | nginx, systemctl, certbot or git failed | The last meaningful line of its output is in the message |
| 5 | `locked` | Another ratline invocation holds the lock | Wait. The message names the holding command and its pid |
| 6 | `rollback_failed` | The operation failed **and so did its rollback** | A human is needed. Run `ratline doctor`, then `ratline reconcile` |
| 7 | `health_check_failed` | It started, but never answered a real request | The last 20 journal lines are attached to the error |
| 8 | `acme_challenge_failed` | The certificate authority could not validate | Usually port 80 or DNS; the message says which |
| 9 | `rate_limited` | Would exceed a certificate authority rate limit | The message includes a countdown. Use `--dry-run` meanwhile |
| 10 | `input_required` | A prompt was needed but there is no terminal | Pass the flag, or `--yes` for a confirmation |

## The `--json` envelope

Every `--json` invocation emits exactly one object on stdout; logs go to stderr.

```json
{
  "ok": true,
  "command": "ratline site list",
  "version": "1.0.0",
  "data": {},
  "error": { "code": 3, "name": "precondition_failed", "message": "…", "hint": "…" }
}
```

`data` on success, `error` on failure. Private key material never appears in it.

## Recipes

```bash
# A FastAPI application behind Gunicorn and Uvicorn, then TLS once DNS points here
ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub
ratline site add api.example.com --user acme --runtime python \
    --app-module app.main:app --workers 3
ratline cert issue api.example.com --email admin@example.com

# A Next.js standalone build. Next binds TCP rather than a socket, so --listen port
ratline runtime install node 22
ratline site add app.example.com --user acme --runtime node --node 22 \
    --entry .next/standalone/server.js --listen port \
    --install-command "npm ci" --build-command "npm run build"

# An Astro static build, published from the build output
ratline site add www.example.com --user acme --runtime static \
    --repo git@github.com:acme/site.git \
    --build-command "npm run build" --build-output dist

# Move a site between tenants
ratline backup example.com --out /var/backups/ratline
ratline site delete example.com --purge
ratline site add example.com --user newowner --runtime static
# then restore the archive into /home/newowner/example.com

# Bulk-provision from a CSV, checking each result rather than hoping
while IFS=, read -r domain user runtime; do
  ratline --json site add "$domain" --user "$user" --runtime "$runtime" \
    | jq -e '.ok' >/dev/null || echo "failed: $domain"
done < sites.csv

# Give a contractor one site for ninety days, from one network — then verify it
ratline key add --scope site --site example.com --label "Contractor" \
    --key contractor.pub --from 203.0.113.0/24 --expires 90d
ratline key test SHA256:…

# Find what has gone stale
ratline key list --unused 90
ratline cert list --expiring 21
ratline doctor
```

## Hidden and unimplemented commands

Two commands exist but are hidden, so they do not appear in the reference below.

`ratline cert deploy-hook` is invoked by certbot after a renewal, not by hand. It
reads `RENEWED_LINEAGE`, maps it back to sites through state, and reloads only
those — never a blanket restart.

`ratline db` is a stub. Database provisioning is out of scope for v1; the command
exists so that typing it gives an answer rather than "unknown command", and so the
intended shape is settled. It becomes visible when `features.db_provisioning` is
on. Until it lands, provision by hand and set the connection string:

```bash
ratline site env set example.com DATABASE_URL=postgres://…
```

That is deliberately the same interface the built-in version will use, so nothing
about your application has to change later.

There are also no PHP, Go or Ruby runtimes. `internal/runtime` is an interface
(`Provision`, `Install`, `Build`, `StartCommand`, `Reload`, `Teardown`), so each
would be a new file rather than a refactor.

---

# Reference

## `ratline`

```
ratline provisions and manages isolated users, their sites and their
certificates on a single server.

Each user is a tenant sandbox: its own home, group, shell and SSH keys. Each
site belongs to one user, is served by nginx from inside that user's home, and —
for the node and python runtimes — runs under its own systemd unit as that user
behind a Unix socket.

Every command is safe to run twice, and every mutation is staged, verified and
rolled back as a unit.

Usage:
  ratline [flags]
  ratline [command]

USERS
  user         Create and manage tenant accounts

SSH KEYS
  key          Add, inspect and revoke SSH keys across the three scopes

SITES
  site         Create and manage sites

CERTIFICATES
  cert         Issue, attach, renew and import TLS certificates

RUNTIMES
  runtime      Install and select managed Node and Python versions

OPERATIONS
  init         Set up this server: configuration, directories and defaults
  backup       Archive a user's home or a single site
  doctor       Check the server, or diagnose one thing on it
  status       Show everything on this server on one screen
  troubleshoot Find why something is broken, in the order things depend on each other
  explain      Explain how part of ratline works
  reconcile    Report or repair drift between state and the system
  export       Dump the full state as JSON, for migration
  version      Print the version, the host and the available runtimes
  man          Write man pages for every command

OTHER
  completion   Generate the autocompletion script for the specified shell
  help         Help about any command

Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -h, --help            help for ratline
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  # A tenant with key-only SSH access
  ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub

  # A FastAPI application behind Gunicorn and Uvicorn on a Unix socket
  ratline site add api.example.com --user acme --runtime python --app-module app.main:app

  # A certificate once DNS points at this server
  ratline cert issue api.example.com --email admin@example.com

Use "ratline [command] --help" for more information about a command.
```

### `ratline user`

```
Each user is a sandbox: its own system account, group, home tree and SSH keys.
Making one is cheap on purpose — one user per site is the recommended pattern
whenever a site is run by someone you do not fully trust.

Usage:
  ratline user [command]

Available Commands:
  add         Create a tenant account with a home tree and key-only SSH
  list        List tenant accounts
  show        Show a user's home, sites, keys, disk usage and services
  enable      Re-enable a user and restart their sites
  disable     Lock a user's login and stop all their sites
  delete      Delete a user, refusing while they still own sites unless --purge
  password    Manage passwords (keys are preferred)
  sudo        Grant or revoke a narrow sudo permission (off unless users.allow_sudo)

Flags:
  -h, --help   help for user

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Use "ratline user [command] --help" for more information about a command.
```

### `ratline key`

```
Keys are a managed resource with three scopes:

  global  server administration — a shell as the administrator, and permission to run ratline
  user    one tenant — an interactive shell and every site that user owns
  site    one site directory — sftp, rsync and git, with no interactive shell

Site scope is a blast-radius boundary, not a kernel one: the session still runs as
the site owner's UID. Where real isolation is needed, use one user per site.
'ratline key test' spells out what any given key can reach.

Usage:
  ratline key [command]

Available Commands:
  add         Install a public key at one of the three scopes
  list        List keys, optionally filtered by scope, staleness or expiry
  show        Show one key in full
  remove      Remove a key from one scope
  revoke      Remove a key from every scope and add it to the revoked list
  move        Move a key to a different scope
  test        Explain in plain English exactly what a key can reach
  audit       Report duplicate, weak, stale, expired and unmanaged keys
  sync        Re-render every authorized_keys file from state
  prune       Remove expired keys and record key usage

Flags:
  -h, --help   help for key

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Use "ratline key [command] --help" for more information about a command.
```

### `ratline site`

```
A site is one domain owned by one user, served by nginx from inside that user's
home. There are three runtimes:

  static  nginx serves files directly; nothing runs
  node    nginx proxies to a Node server under its own systemd unit
  python  nginx proxies to Gunicorn in a per-site virtualenv

TLS is managed separately with 'ratline cert', so a site can be created and
serving before DNS has been pointed at this server.

Usage:
  ratline site [command]

Available Commands:
  add          Provision a site: directories, vhost, service and TLS
  list         List sites
  show         Show a site's runtime, service state, socket, certificate and last deploy
  enable       Enable a site and start its service
  disable      Take a site offline, returning 503 while keeping certificate renewal working
  start        Start a site's service
  stop         Stop a site's service
  restart      Restart a site's service
  reload       Reload a site's workers without dropping requests, where the runtime allows
  status       Show a site's service state
  scale        Change workers, instances or resource ceilings
  delete       Delete a site, its vhost, its service and its logs
  alias        Add or remove a site's additional server names
  logs         Show a site's application, access or error log
  env          Manage a site's environment variables
  deploy       Pull, install, build, migrate and restart, rolling back if it fails
  runtime      Change a site's interpreter version, then rebuild and restart
  deploy-key   Manage the outbound key a site uses to clone a private repository
  troubleshoot Walk one site's request path and find where it breaks

Flags:
  -h, --help   help for site

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Use "ratline site [command] --help" for more information about a command.
```

### `ratline cert`

```
Certificates are a resource with their own lifecycle, not a flag on 'site add'.

That is deliberate: a site can be created and serving HTTP before DNS has been
pointed at this server, and have a real certificate issued and attached later —
which is the normal order of operations when a client is still moving a domain.

Usage:
  ratline cert [command]

Available Commands:
  issue        Obtain a certificate and attach it to the site
  attach       Point a site's vhost at an existing certificate
  detach       Revert a site to plain HTTP
  list         List every certificate on this server, including ones issued by hand
  show         Show a certificate in full
  renew        Renew certificates that are due
  revoke       Ask the certificate authority to revoke a certificate
  delete       Delete a certificate, refusing while a site still uses it
  import       Install a third-party certificate
  selfsign     Generate an untrusted placeholder so a site can serve HTTPS immediately
  auto-renew   Inspect or change automatic renewal
  test-renewal Dry-run every certificate, to find breakage before it matters
  account      Inspect the ACME account

Flags:
  -h, --help   help for cert

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Use "ratline cert [command] --help" for more information about a command.
```

### `ratline runtime`

```
Managed interpreters live under /opt/ratline/runtimes and are invoked by absolute
path from each unit's ExecStart.

That is the point: nvm, pyenv and shell profiles are never involved, because
systemd does not read them. A unit that depended on them would work when you
tested it by hand and fail on the next boot.

Usage:
  ratline runtime [command]

Available Commands:
  list        List installed versions and which sites use each
  install     Install a managed interpreter into /opt/ratline/runtimes
  default     Set the version new sites use when they do not pin one

Flags:
  -h, --help   help for runtime

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Use "ratline runtime [command] --help" for more information about a command.
```

### `ratline init`

```
Seeds /etc/ratline/config.yaml, creates the directories ratline needs, and
records the ACME contact address and the administrator account.

Safe to re-run: it reviews and updates settings rather than starting over, and
never overwrites a configuration file you have edited.

Usage:
  ratline init [flags]

Flags:
      --admin-user string   Account that holds global-scope SSH keys
      --agree-tos           Accept the certificate authority's subscriber agreement
      --email string        ACME contact address
  -h, --help                help for init
      --write-config-only   Seed the configuration and directories, then stop

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

### `ratline backup`

```
Writes a gzipped tar of the tenant's home or the site directory, including the
application code, the logs and the .env.

The archive therefore contains secrets. It is written 0600 in a 0700 directory,
and where it goes afterwards is your responsibility.

Usage:
  ratline backup <user|domain> [flags]

Flags:
  -h, --help         help for backup
      --out string   Directory to write the archive into

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline backup acme --out /var/backups/ratline
  ratline backup example.com --out /mnt/backup
```

### `ratline doctor`

```
With no argument, runs every check ratline knows how to run: the nginx
configuration, failed services, dead sockets, certificate expiry, orphaned
configuration, drift between state and the filesystem, permission anomalies,
allocated but unused ports, and the SSH key audit. Exit code 0 means healthy,
which makes it usable from cron.

With a subject — a domain, a tenant, a key fingerprint, a certificate, or
'nginx', 'ssh' or 'server' — it diagnoses that one thing instead, walking its
preconditions in order and stopping at the first failure. That is the same as
'ratline troubleshoot <subject>', which is the explicit spelling.

The difference is worth knowing: the sweep tells you what is wrong across the
server, and the walk tells you why one thing is.

Usage:
  ratline doctor [subject] [flags]

Flags:
      --all    With a subject: show every step, not only the ones that need attention
  -h, --help   help for doctor

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline doctor                       # everything, as a cron job would
  ratline doctor app.example.com       # why is this site broken
  ratline doctor ssh                   # including the lockout guard
```

### `ratline status`

```
The inventory and the health of it: tenants, sites and what state each one is
in, certificates that need attention, and a count of anything 'ratline doctor'
would report.

Unlike doctor, this always prints. doctor says what is wrong; status says what
is here.

Usage:
  ratline status [flags]

Flags:
  -h, --help    help for status
      --quiet   Only the summary counts, without the per-site table

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline status
  ratline status --json | jq '.sites_detail[] | select(.needs_attention)'
```

### `ratline troubleshoot`

```
Diagnoses anything ratline manages. The subject is worked out from the
argument — a domain is a site, a name is a tenant, SHA256:… is a key — and
'nginx', 'ssh' and 'server' name the subsystems. With no argument it
diagnoses the server.

Checks run in dependency order and stop at the first failure, so the first
failure is the cause: a socket nginx cannot open explains the 502, and the
502 is not reported as a second problem. Steps that depended on it are
marked as not checked rather than guessed at.

Read-only. It never takes the lock, so it is safe against a site that is
currently on fire.

Usage:
  ratline troubleshoot [subject] [flags]

Flags:
      --all                      Show every step, not only the ones that need attention
  -h, --help                     help for troubleshoot
      --kind string              Say what the subject is when the name is ambiguous: server, site, user, key, certificate, nginx, ssh
      --probe-timeout duration   How long any single network probe may take (default 3s)

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline troubleshoot                          # the server
  ratline troubleshoot app.example.com          # one site's request path
  ratline troubleshoot acme                     # a tenant: account, home, keys, sites
  ratline troubleshoot SHA256:AbC…              # can this key log in, and to what
  ratline troubleshoot nginx
  ratline troubleshoot ssh                      # including the lockout guard
  ratline troubleshoot app.example.com --json | jq -r .data.likely_cause
```

### `ratline explain`

```
Longer-form answers than a help page can carry: how sites are laid out on
disk, why a node site is supervised by PM2, what turns a working application
into a silent 502, what happens when a deploy fails halfway.

Run without a topic to list them. The pages are built into the binary, so
this works on a server with no browser and no network.

Usage:
  ratline explain [topic] [flags]

Flags:
  -h, --help   help for explain
      --raw    Print the markdown source without terminal formatting

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline explain
  ratline explain sockets
  ratline explain node | less
```

### `ratline reconcile`

```
The filesystem, the systemd units and /etc/passwd are the source of truth; the
state database is an index. This command compares the two, and with --fix
re-renders every configuration file from state.

Usage:
  ratline reconcile [flags]

Flags:
      --fix    Repair what can be repaired, rather than only reporting
  -h, --help   help for reconcile

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

### `ratline export`

```
Contains no private key material: public key blobs and fingerprints only.
Certificate private keys are never read, let alone exported.

Usage:
  ratline export [flags]

Flags:
  -h, --help   help for export

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

### `ratline version`

```
Print the version, the host and the available runtimes

Usage:
  ratline version [flags]

Flags:
  -h, --help   help for version

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

### `ratline man`

```
Generates one roff page per command. With no --dir the top-level page is
written to stdout, which is handy for previewing:

  ratline man | man -l -

Usage:
  ratline man [flags]

Flags:
      --dir string   Directory to write pages into (default: stdout)
  -h, --help         help for man

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline user add`

```
Create a tenant account with a home tree and key-only SSH

Usage:
  ratline user add <username> [flags]

Flags:
      --comment string        Description recorded in /etc/passwd
  -h, --help                  help for add
      --memory-max string     Default memory ceiling inherited by this user's sites, e.g. 512M
      --password-login        Allow password login (default: keys only)
      --quota string          Disk quota, e.g. 20G (needs filesystem quota support)
      --sftp-only             SFTP only, chrooted to the home directory, with no shell
      --shell string          Login shell (default from config; /usr/sbin/nologin to disable)
      --ssh-key stringArray   Public key: a path, an https URL, or - for stdin (repeatable)

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub
  ratline user add contractor --sftp-only --quota 5G
  cat key.pub | ratline user add ci --ssh-key -
```

#### `ratline user list`

```
List tenant accounts

Usage:
  ratline user list [flags]

Flags:
  -h, --help   help for list

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline user show`

```
Show a user's home, sites, keys, disk usage and services

Usage:
  ratline user show <username> [flags]

Flags:
  -h, --help   help for show

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline user enable`

```
Re-enable a user and restart their sites

Usage:
  ratline user enable <username> [flags]

Flags:
  -h, --help   help for enable

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline user disable`

```
Lock a user's login and stop all their sites

Usage:
  ratline user disable <username> [flags]

Flags:
  -h, --help   help for disable

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline user delete`

```
Delete a user, refusing while they still own sites unless --purge

Usage:
  ratline user delete <username> [flags]

Aliases:
  delete, rm

Flags:
      --backup string   Archive the home directory into this directory first
  -h, --help            help for delete
      --purge           Also delete every site the user owns

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline user password`

```
Manage passwords (keys are preferred)

Usage:
  ratline user password [command]

Available Commands:
  set         Set a password, read from stdin

Flags:
  -h, --help   help for password

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Use "ratline user password [command] --help" for more information about a command.
```

#### `ratline user sudo`

```
Created users get no sudo. This exists because a real deployment occasionally
needs one specific command — a client's CI restarting their own service.

Every rule pins the full argument list. A grant of just the program name would
let the tenant pass any arguments to it, and most programs with arbitrary
arguments are equivalent to root.

Usage:
  ratline user sudo [command]

Available Commands:
  grant       Install a sudo rule for exactly the commands named
  revoke      Remove a tenant's sudo grant
  list        List the tenants with a ratline-installed sudo grant

Flags:
  -h, --help   help for sudo

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Use "ratline user sudo [command] --help" for more information about a command.
```

#### `ratline key add`

```
Install a public key at one of the three scopes

Usage:
  ratline key add [flags]

Flags:
      --allow-duplicate       Permit a fingerprint that is already installed elsewhere
      --allow-shell           Site scope only: permit an interactive shell (weakens the confinement)
      --command string        Named preset from config: sftp-only, rsync-only or git-only
      --expires string        Expiry as a date (2026-12-31) or a duration (90d)
      --from strings          Restrict to these source addresses, e.g. 203.0.113.0/24
      --from-github string    Fetch a user's public keys from github.com
      --from-gitlab string    Fetch a user's public keys from gitlab.com
  -h, --help                  help for add
      --isolation string      Site scope only: default, or strict to add a chroot (needs features.strict_isolation)
      --key stringArray       Public key: a path, an https URL, or - for stdin (repeatable)
      --label string          Human label, so this key can be recognised later (required)
      --no-agent-forwarding   Refuse agent forwarding (the default outside global scope)
      --no-port-forwarding    Refuse port forwarding (already the default)
      --no-pty                Refuse a PTY
      --scope string          global, user or site (required)
      --sftp-only             Force SFTP with no shell
      --site string           Domain, for --scope site
      --user string           Tenant, for --scope user

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  # your own laptop, for server administration
  ratline key add --scope global --label "Ali MacBook" --key ~/.ssh/id_ed25519.pub

  # a client, who gets a shell and all of their sites
  ratline key add --scope user --user acme --label "Acme ops" --from-github acme-ops

  # a contractor, confined to one site, from one network, for 90 days
  ratline key add --scope site --site example.com --label "Contractor" \
      --key contractor.pub --from 203.0.113.0/24 --expires 90d
```

#### `ratline key list`

```
List keys, optionally filtered by scope, staleness or expiry

Usage:
  ratline key list [flags]

Flags:
      --expiring int   Only keys expiring within this many days
  -h, --help           help for list
      --scope string   Only this scope
      --site string    Only this site
      --unused int     Only keys not seen in this many days
      --user string    Only this tenant

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline key list
  ratline key list --scope site --site example.com
  ratline key list --unused 90        # stale contractor access
```

#### `ratline key show`

```
Show one key in full

Usage:
  ratline key show <fingerprint|label|id> [flags]

Flags:
  -h, --help   help for show

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline key remove`

```
Remove a key from one scope

Usage:
  ratline key remove <fingerprint|label|id> [flags]

Aliases:
  remove, rm

Flags:
      --everywhere     Remove the key from every scope on this server
      --force          Proceed even if this is the last credential for a scope
  -h, --help           help for remove
      --scope string   Only this scope
      --site string    Only this site
      --user string    Only this tenant

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline key revoke`

```
Remove a key from every scope and add it to the revoked list

Usage:
  ratline key revoke <fingerprint|label|id> [flags]

Flags:
      --force          Proceed even if this is the last credential for a scope
  -h, --help           help for revoke
      --scope string   Only this scope
      --site string    Only this site
      --user string    Only this tenant

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline key move`

```
Move a key to a different scope

Usage:
  ratline key move <fingerprint|label|id> [flags]

Flags:
  -h, --help              help for move
      --site string       Domain, for --to-scope site
      --to-scope string   New scope: global, user or site
      --user string       Tenant, for --to-scope user

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  # narrow a contractor's access to one site
  ratline key move SHA256:x9K… --to-scope site --site example.com
```

#### `ratline key test`

```
Answers the question that matters before someone finds out the hard way:
what can this key actually do on this server?

Usage:
  ratline key test <fingerprint|label|id> [flags]

Flags:
  -h, --help   help for test

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline key audit`

```
Report duplicate, weak, stale, expired and unmanaged keys

Usage:
  ratline key audit [flags]

Flags:
  -h, --help   help for audit

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline key sync`

```
Rewrites the managed block in each file. Keys an operator added by hand outside
the markers are preserved exactly as they are.

Usage:
  ratline key sync [flags]

Flags:
  -h, --help   help for sync

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline key prune`

```
Run daily by ratline-key-prune.timer.

Two jobs, both of which have to happen on a schedule. Expired keys are removed:
OpenSSH 8.2+ already refuses them through expiry-time=, but this is what takes
the line out of the file, and on an older daemon it is the only mechanism.
And key usage is scraped from the journal, because logs rotate — a key last used
four months ago leaves no trace by the time anyone asks.

Usage:
  ratline key prune [flags]

Flags:
  -h, --help   help for prune

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline site add`

```
Provision a site: directories, vhost, service and TLS

Usage:
  ratline site add <domain> [flags]

Flags:
      --alias stringArray             Additional server name (repeatable)
      --app-module string             python: import path of the callable, e.g. app.main:app
      --asgi                          python: treat the application as ASGI
      --branch string                 Branch to clone (default "main")
      --build-command string          Build command
      --build-output string           Directory the build writes, published as the document root
      --client-max-body-size string   Upload limit, e.g. 20M
      --cpu-quota string              CPU ceiling, e.g. 100%
      --daemon string                 node: pm2 (default, reloads without dropping requests) or direct (node straight under systemd)
      --email string                  ACME contact address
      --entry string                  node: the file that starts the server
  -h, --help                          help for add
      --hsts                          Send Strict-Transport-Security (only with a trusted certificate)
      --index string                  static: index document (default "index.html")
      --install-command string        Dependency install command
      --instances int                 node: PM2 cluster workers, all sharing the one socket inside the one unit (default 1)
      --listen string                 node: socket or port (default "socket")
      --manage-py string              python: Django manage.py, enabling --migrate and --collectstatic
      --memory-max string             Memory ceiling, e.g. 512M
      --no-enable                     Write the configuration without enabling or starting it
      --node string                   node: managed Node version, e.g. 22
      --package-manager string        node: npm, pnpm, yarn or bun (detected from the lockfile)
      --public string                 Directory nginx serves directly, bypassing the application
      --python string                 python: managed Python version, e.g. 3.12
      --relax strings                 Turn off a named systemd hardening directive for this site
      --repo string                   Clone this repository into the application directory
      --requirements string           python: requirements file (detected by default)
      --root string                   static: document root under the site directory (default public)
      --runtime string                static, node or python (required)
      --server string                 python: gunicorn or uvicorn (default gunicorn)
      --spa                           static: serve the index document for unmatched paths
      --ssl string                    letsencrypt, selfsigned or none (default "letsencrypt")
      --start-command string          Start command, as an argv (no shell)
      --static-dir string             python: directory behind --static-url
      --static-url string             python: URL prefix nginx serves from disk, e.g. /static
      --user string                   Owning tenant (required)
      --workers int                   python: worker processes (default (2 x cores) + 1, capped)
      --wsgi                          python: treat the application as WSGI
      --www-redirect string           Canonical host: apex, www or none (default "none")

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline site add static.example.com --user acme --runtime static --spa

  ratline site add api.example.com --user acme --runtime python \
      --app-module app.main:app --workers 3

  ratline site add app.example.com --user acme --runtime node \
      --entry server.js --node 22
```

#### `ratline site list`

```
List sites

Usage:
  ratline site list [flags]

Flags:
  -h, --help             help for list
      --runtime string   Only this runtime
      --user string      Only this tenant's sites

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline site show`

```
Show a site's runtime, service state, socket, certificate and last deploy

Usage:
  ratline site show <domain> [flags]

Flags:
  -h, --help   help for show

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline site enable`

```
Enable a site and start its service

Usage:
  ratline site enable <domain> [flags]

Flags:
  -h, --help   help for enable

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline site disable`

```
Take a site offline, returning 503 while keeping certificate renewal working

Usage:
  ratline site disable <domain> [flags]

Flags:
  -h, --help   help for disable

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline site start`

```
Start a site's service

Usage:
  ratline site start <domain> [flags]

Flags:
  -h, --help   help for start

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline site stop`

```
Stop a site's service

Usage:
  ratline site stop <domain> [flags]

Flags:
  -h, --help   help for stop

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline site restart`

```
Restart a site's service

Usage:
  ratline site restart <domain> [flags]

Flags:
  -h, --help   help for restart

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline site reload`

```
Reload a site's workers without dropping requests, where the runtime allows

Usage:
  ratline site reload <domain> [flags]

Flags:
  -h, --help   help for reload

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline site status`

```
Show a site's service state

Usage:
  ratline site status <domain> [flags]

Flags:
  -h, --help   help for status

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline site scale`

```
Change workers, instances or resource ceilings

Usage:
  ratline site scale <domain> [flags]

Flags:
      --client-max-body-size string   Upload ceiling, e.g. 100M — the commonest cause of a mystery 413
      --cpu-quota string              CPU ceiling, e.g. 100%
  -h, --help                          help for scale
      --instances int                 node: PM2 cluster workers
      --memory-max string             Memory ceiling, e.g. 512M
      --workers int                   Worker processes

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline site scale api.example.com --workers 6
  ratline site scale api.example.com --memory-max 1G --cpu-quota 200%
  ratline site scale www.example.com --client-max-body-size 100M
```

#### `ratline site delete`

```
Delete a site, its vhost, its service and its logs

Usage:
  ratline site delete <domain> [flags]

Aliases:
  delete, rm

Flags:
      --backup string   Archive the site into this directory first
  -h, --help            help for delete
      --purge           Also delete the site directory and its contents

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline site alias`

```
Add or remove a site's additional server names

Usage:
  ratline site alias [command]

Available Commands:
  add         Add an alias and re-render the vhost
  remove      Remove an alias and re-render the vhost

Flags:
  -h, --help   help for alias

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Use "ratline site alias [command] --help" for more information about a command.
```

#### `ratline site logs`

```
Where the application log comes from depends on how the site is supervised.

Under PM2 — the default for node — the application's stdout is captured by
PM2 into logs/app.log, and the journal holds only PM2's own messages. So
--app reads the file, and --journal is there for when the question is about
the unit itself: a failed start, or an OOM kill.

Without PM2 the application writes straight to the journal, and --app reads
that.

Usage:
  ratline site logs <domain> [flags]

Flags:
      --access      The nginx access log
      --app         The application log (the default for a dynamic site)
      --error       The nginx error log
      --follow      Keep printing as lines arrive
  -h, --help        help for logs
      --journal     The systemd journal for the unit rather than the application's own log
      --lines int   How many lines to show (default 100)

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline site env`

```
Values live in the site's .env, which is 0600 and owned by the tenant. systemd
reads it as root before dropping privileges, so the application receives values
nginx can never serve.

Values are masked in output unless --reveal, and redacted in the audit log.

Usage:
  ratline site env [command]

Available Commands:
  set         Set one or more variables and restart the service
  get         Show one variable, masked unless --reveal
  unset       Remove a variable and restart the service
  list        List variables, masked unless --reveal
  import      Merge a .env file into a site's environment

Flags:
  -h, --help   help for env

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Use "ratline site env [command] --help" for more information about a command.
```

#### `ratline site deploy`

```
Runs the chain you ask for, health checks the result, and reverts to the previous
commit if the application does not come back healthy.

With no step flags, a sensible default chain runs: pull, install, build, restart.

Usage:
  ratline site deploy <domain> [flags]

Flags:
      --build           Run the build command
      --collectstatic   Run Django collectstatic (needs --manage-py)
  -h, --help            help for deploy
      --install         Install dependencies
      --migrate         Run Django migrations (needs --manage-py)
      --pull            git pull in the application directory
      --restart         Restart the service and wait for health

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline site deploy api.example.com
  ratline site deploy api.example.com --pull --install --migrate --collectstatic --restart
```

#### `ratline site runtime`

```
Change a site's interpreter version, then rebuild and restart

Usage:
  ratline site runtime <domain> [flags]

Flags:
      --daemon string   node: move this site to pm2 or direct supervision
  -h, --help            help for runtime
      --node string     Node version to move to
      --python string   Python version to move to
      --relax strings   Turn off a named systemd hardening directive for this site

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline site runtime app.example.com --node 22
  ratline site runtime api.example.com --python 3.12
```

#### `ratline site deploy-key`

```
The private half never leaves this server: it is 0600, owned by the site user,
and used only by git over SSH. The public half is printed for you to paste into
the repository's deploy keys.

Usage:
  ratline site deploy-key [command]

Available Commands:
  create      Generate an outbound keypair for a site
  show        Print a site's outbound public key
  rotate      Replace a site's outbound keypair
  remove      Delete a site's outbound keypair

Flags:
  -h, --help   help for deploy-key

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Use "ratline site deploy-key [command] --help" for more information about a command.
```

#### `ratline site troubleshoot`

```
The site-scoped spelling of 'ratline troubleshoot', which diagnoses anything.

Usage:
  ratline site troubleshoot <domain> [flags]

Flags:
      --all    Show every step, not only the ones that need attention
  -h, --help   help for troubleshoot

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline site troubleshoot app.example.com
```

#### `ratline cert issue`

```
Runs every preflight check first and reports all the problems at once, because
fixing one per attempt is a poor way to spend a rate-limit budget and the
certificate authority counts failed validations.

The result is verified over the network: a certificate that exists on disk but is
not being served is a failure, not a success.

Usage:
  ratline cert issue <domain> [flags]

Flags:
      --alias stringArray        SAN to include, replacing the site's own aliases (repeatable)
      --challenge string         http (webroot) or dns; a wildcard forces dns (default "http")
      --dns-credentials string   Credentials file for the DNS plugin, which must be 0600
      --dns-propagation int      Seconds to wait for the TXT record before validating
      --dns-provider string      certbot DNS plugin, e.g. cloudflare or route53
      --dry-run                  Validate fully without issuing, and without spending budget
      --email string             ACME contact address
      --force                    Re-issue even if a valid certificate exists, and proceed past preflight
  -h, --help                     help for issue
      --key-type string          ecdsa or rsa (default from config)
      --no-attach                Obtain the certificate without pointing the vhost at it
      --san stringArray          Extra SAN not registered as a site alias (repeatable)
      --staging                  Use the staging endpoint: real exchange, untrusted certificate, generous limits

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline cert issue example.com --email admin@example.com

  # test without spending an attempt
  ratline cert issue example.com --dry-run

  # a wildcard, which requires DNS-01
  ratline cert issue '*.example.com' --challenge dns \
      --dns-provider cloudflare --dns-credentials /etc/ratline/dns/cloudflare.ini
```

#### `ratline cert attach`

```
How one SAN certificate serves several vhosts, and how an imported or
already-issued certificate is put to use without a new ACME exchange.

Usage:
  ratline cert attach <domain> [flags]

Flags:
      --cert string   Certificate to attach (default: one named after the domain)
  -h, --help          help for attach

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline cert detach`

```
Revert a site to plain HTTP

Usage:
  ratline cert detach <domain> [flags]

Flags:
  -h, --help   help for detach

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline cert list`

```
List every certificate on this server, including ones issued by hand

Usage:
  ratline cert list [flags]

Flags:
      --expiring int   Only certificates expiring within this many days
  -h, --help           help for list
      --orphaned       Only certificates no site uses

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline cert show`

```
Show a certificate in full

Usage:
  ratline cert show <domain> [flags]

Flags:
  -h, --help   help for show

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline cert renew`

```
Run twice daily by ratline-cert-renew.timer.

A failure is not an emergency: the existing certificate is valid for weeks yet,
which is why the window is 30 days. One certificate failing never stops the
others; the failure is recorded and surfaced by 'ratline doctor'.

Usage:
  ratline cert renew [<domain>] [flags]

Flags:
      --all       Every certificate
      --dry-run   Exercise the challenge without replacing anything
      --force     Renew even if not due
  -h, --help      help for renew

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline cert revoke`

```
Ask the certificate authority to revoke a certificate

Usage:
  ratline cert revoke <domain> [flags]

Flags:
  -h, --help            help for revoke
      --reason string   keycompromise, superseded or cessationofoperation

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline cert delete`

```
Delete a certificate, refusing while a site still uses it

Usage:
  ratline cert delete <domain> [flags]

Aliases:
  delete, rm

Flags:
  -h, --help         help for delete
      --keep-files   Remove the state record but leave the files on disk

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline cert import`

```
For a Cloudflare Origin certificate, ZeroSSL, or a corporate CA.

Everything is validated before anything is written: the PEM parses, the private
key matches the certificate, the chain builds, the dates are sane, and the SANs
cover the site's names. Each failure names its own reason.

Nothing renews an imported certificate. 'ratline doctor' warns as expiry
approaches, because nothing else will.

Usage:
  ratline cert import <domain> [flags]

Flags:
      --cert string    Certificate, ideally the full chain (required)
      --chain string   Intermediates, if not already in --cert
  -h, --help           help for import
      --key string     Private key, not passphrase-encrypted (required)
      --no-attach      Install without pointing the vhost at it

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline cert import example.com --cert origin.pem --key origin.key
```

#### `ratline cert selfsign`

```
So a site can serve HTTPS the moment it is created, before DNS is pointed.

Recorded distinctly, never counted as valid, always flagged in 'cert list' and
'doctor', and replaced cleanly by 'cert issue' later. HSTS is refused on one.

Usage:
  ratline cert selfsign <domain> [flags]

Flags:
      --days int    Validity in days (default 365)
  -h, --help        help for selfsign
      --no-attach   Generate without pointing the vhost at it

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline cert auto-renew`

```
Inspect or change automatic renewal

Usage:
  ratline cert auto-renew [command]

Available Commands:
  status      Report whether renewal is actually wired up
  enable      Enable automatic renewal for one certificate
  disable     Disable automatic renewal for one certificate

Flags:
  -h, --help   help for auto-renew

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Use "ratline cert auto-renew [command] --help" for more information about a command.
```

#### `ratline cert test-renewal`

```
Exercises the real challenge for every managed certificate without replacing
anything and without spending rate-limit budget.

Worth a monthly cron: it finds a closed port 80 or a moved DNS record weeks
before the certificate would actually expire.

Usage:
  ratline cert test-renewal [flags]

Flags:
  -h, --help   help for test-renewal

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline cert account`

```
Inspect the ACME account

Usage:
  ratline cert account [command]

Available Commands:
  show        Show the ACME account state
  register    Record the ACME contact address and accept the terms

Flags:
  -h, --help   help for account

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Use "ratline cert account [command] --help" for more information about a command.
```

#### `ratline runtime list`

```
List installed versions and which sites use each

Usage:
  ratline runtime list [flags]

Flags:
  -h, --help   help for list

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

#### `ratline runtime install`

```
Install a managed interpreter into /opt/ratline/runtimes

Usage:
  ratline runtime install <node|python> <version> [flags]

Flags:
  -h, --help                 help for install
      --pm2-version string   node: pin PM2 to this version rather than the latest
      --with-pm2             node: also install PM2, which is what a node site is supervised by unless --daemon direct is used

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline runtime install node 22 --with-pm2
  ratline runtime install python 3.12
```

#### `ratline runtime default`

```
Set the version new sites use when they do not pin one

Usage:
  ratline runtime default <node|python> <version> [flags]

Flags:
  -h, --help   help for default

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline user password set`

```
The password is never taken from the command line, where it would appear in
the process table, the shell history and the audit log.

Usage:
  ratline user password set <username> [flags]

Flags:
  -h, --help    help for set
      --stdin   Read the password from stdin

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline user password set alice --stdin < password.txt
```

##### `ratline user sudo grant`

```
Install a sudo rule for exactly the commands named

Usage:
  ratline user sudo grant <username> [flags]

Flags:
      --command stringArray   An absolute command with its full arguments (repeatable, required)
  -h, --help                  help for grant

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline user sudo grant acme \
      --command '/usr/bin/systemctl restart ratline-acme-example_com.service'
```

##### `ratline user sudo revoke`

```
Remove a tenant's sudo grant

Usage:
  ratline user sudo revoke <username> [flags]

Flags:
  -h, --help   help for revoke

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline user sudo list`

```
List the tenants with a ratline-installed sudo grant

Usage:
  ratline user sudo list [flags]

Flags:
  -h, --help   help for list

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline site alias add`

```
Add an alias and re-render the vhost

Usage:
  ratline site alias add <domain> <alias> [flags]

Flags:
  -h, --help   help for add

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline site alias remove`

```
Remove an alias and re-render the vhost

Usage:
  ratline site alias remove <domain> <alias> [flags]

Flags:
  -h, --help   help for remove

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline site env set`

```
Set one or more variables and restart the service

Usage:
  ratline site env set <domain> KEY=VALUE [KEY=VALUE ...] [flags]

Flags:
  -h, --help    help for set
      --stdin   Read KEY=VALUE lines from stdin, keeping secrets out of argv

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal

Examples:
  ratline site env set api.example.com LOG_LEVEL=info

  # a secret, kept out of the process table and the shell history
  printf 'DATABASE_URL=%s' "$url" | ratline site env set api.example.com --stdin
```

##### `ratline site env get`

```
Show one variable, masked unless --reveal

Usage:
  ratline site env get <domain> KEY [flags]

Flags:
  -h, --help     help for get
      --reveal   Print the real value

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline site env unset`

```
Remove a variable and restart the service

Usage:
  ratline site env unset <domain> KEY [flags]

Flags:
  -h, --help   help for unset

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline site env list`

```
List variables, masked unless --reveal

Usage:
  ratline site env list <domain> [flags]

Flags:
  -h, --help     help for list
      --reveal   Print the real values

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline site env import`

```
Merge a .env file into a site's environment

Usage:
  ratline site env import <domain> --file .env [flags]

Flags:
      --file string   The .env file to import (required)
  -h, --help          help for import

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline site deploy-key create`

```
Generate an outbound keypair for a site

Usage:
  ratline site deploy-key create <domain> [flags]

Flags:
  -h, --help          help for create
      --type string   Key type: ed25519 or rsa (default "ed25519")

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline site deploy-key show`

```
Print a site's outbound public key

Usage:
  ratline site deploy-key show <domain> [flags]

Flags:
  -h, --help   help for show

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline site deploy-key rotate`

```
Replace a site's outbound keypair

Usage:
  ratline site deploy-key rotate <domain> [flags]

Flags:
  -h, --help          help for rotate
      --type string   Key type: ed25519 or rsa (default "ed25519")

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline site deploy-key remove`

```
Delete a site's outbound keypair

Usage:
  ratline site deploy-key remove <domain> [flags]

Aliases:
  remove, rm

Flags:
  -h, --help   help for remove

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline cert auto-renew status`

```
Report whether renewal is actually wired up

Usage:
  ratline cert auto-renew status [flags]

Flags:
  -h, --help   help for status

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline cert auto-renew enable`

```
Enable automatic renewal for one certificate

Usage:
  ratline cert auto-renew enable <domain> [flags]

Flags:
  -h, --help   help for enable

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline cert auto-renew disable`

```
Disable automatic renewal for one certificate

Usage:
  ratline cert auto-renew disable <domain> [flags]

Flags:
  -h, --help   help for disable

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline cert account show`

```
Show the ACME account state

Usage:
  ratline cert account show [flags]

Flags:
  -h, --help   help for show

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

##### `ratline cert account register`

```
Record the ACME contact address and accept the terms

Usage:
  ratline cert account register [flags]

Flags:
      --email string   Contact address (required)
  -h, --help           help for register

Global Flags:
      --config string   Configuration file (default /etc/ratline/config.yaml)
      --dry-run         Print every mutation without making it
  -i, --interactive     Prompt for whatever was not supplied as a flag
      --json            Machine-readable output on stdout; logs on stderr
      --no-input        Never prompt; fail instead (implied when stdout is not a terminal)
  -q, --quiet           Errors only
  -v, --verbose         Debug logging
  -y, --yes             Assume yes; required for destructive operations without a terminal
```

