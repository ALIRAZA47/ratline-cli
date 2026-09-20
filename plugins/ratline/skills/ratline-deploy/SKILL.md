---
name: ratline-deploy
description: Deploy an application of any kind — a static site or SPA, a Node or Bun server, Next.js, Python (FastAPI, Flask, Django) — to a VPS that ratline manages, end to end. Reads the repository, picks the runtime and flags, provisions the tenant, site, database and TLS through the ratline CLI, ships the code, verifies the site answers over HTTPS and hands over. Use this whenever someone wants to put an app or website live on a server running ratline, add a subdomain or a second app to one, "deploy this to my VPS", "get this on the server", set up a new site, or move an app onto a ratline box — even when they do not say the word ratline. Also use it to answer "can ratline host this?" for a PHP, Laravel, WordPress, Ruby, Rails, Go, Rust, Java, .NET or Docker-only application: the skill knows what ratline cannot run and says so plainly instead of improvising a unit or an nginx file.
---

# Deploying to a ratline server

You are about to make a server serve someone's application. ratline already knows how to do
every step of that safely: create the tenant, render the vhost and the unit, install
dependencies as the tenant, issue the certificate, health-check the result and unwind what
failed. Your job is to work out *what to ask it for*, ask in the right order, and verify.
You are a caller of the CLI, exactly as ratline's own web panel is. Nothing here is done by
hand-writing an nginx file, a systemd unit, a sudoers line or a certbot command.

## Ground rules

- **ratline owns the server.** It refuses to overwrite a file it did not create and
  re-renders the ones it did, so a hand edit is either refused now or lost later. If there
  is no ratline command for what you need, say so and stop. Do not improvise around it.
- **Look flags up; do not remember them.** `ratline schema` prints every command, argument
  and flag of the *installed* binary as JSON. A flag from memory is a flag from some other
  version. `ratline <command> --help` is the short form.
- **Rehearse first.** Every mutating command takes `--dry-run`, which writes nothing at any
  layer. Run it, read the plan, show the plan, then run the real thing.
- **Secrets never touch argv.** `/proc/PID/cmdline` is world-readable on the server, and
  argv lands in shell history. Values go in on stdin (`--stdin`). Never `printf` a secret
  into a pipe: a `%` in it is a format verb and truncates the value silently.
- **Read the envelope, branch on the exit code.** `--json` gives one object on stdout:
  `{ok, command, version, data}` or `{ok:false, error:{code, name, message, hint}}`. The
  codes are a contract; the table at the end says what each one asks of you.
- **Safe to run twice.** Re-running a command that already applied reports what exists.
  Do not "clean up" before a retry, and never retry a certificate blindly.
- **Other people's production is on this box.** Never restart, reconfigure or remove
  anything you did not create in this session without being asked to by name.

## Reaching the server

ratline runs as root on the server. From a laptop, run it over SSH. `RATLINE_HOST` is the
plugin's convention for the destination, `root@203.0.113.5` or an `~/.ssh/config` alias:

```bash
ssh -T "$RATLINE_HOST" sudo ratline status --json
```

`ssh` hands the remote one string that the login shell re-splits, so single-quote any value
with a space or a shell character, and send anything secret on stdin:

```bash
ssh -T "$RATLINE_HOST" sudo ratline site env set app.example.com --stdin < vars.env
```

On the server itself, drop the `ssh` prefix. If the plugin's MCP server is connected, the
`ratline_*` tools answer the read-only questions (status, site show, troubleshoot, logs,
doctor, explain) without a shell; prefer them for reads.

## The flow

### 0. Inventory before anything else

```bash
ssh -T "$RATLINE_HOST" sudo ratline version
ssh -T "$RATLINE_HOST" sudo ratline status --json
ssh -T "$RATLINE_HOST" sudo ratline runtime list --json
ssh -T "$RATLINE_HOST" sudo ratline schema > ratline-schema.json
```

Know what exists before naming anything: tenants, domains, which Node and Python versions
are installed, whether a database server is attached (`db list` refuses if not). If the
version the app needs is missing, `runtime install node 24 --with-pm2` (or `python 3.12`,
`bun 1.2`) touches nothing that exists. If ratline itself is missing, stop and use the
`ratline-server-setup` skill first.

### 1. Read the repository and decide the shape

Open [references/runtime-detection.md](references/runtime-detection.md) and work through it
with the repository in front of you. You are deciding:

- **runtime**: `static`, `node`, `bun` or `python`, and whether there is more than one
  deployable (a frontend and an API are two sites on two domains under one tenant).
- **entry point**: `--entry` (the file that calls `listen()`) or `--app-module`
  (`package.module:app`), never a package-manager command if you can avoid it.
- **install and build**: detected from the lockfile; a multi-step build is a script in the
  repository, because ratline runs an argv and refuses `&&`, `|` and redirections.
- **listen mode**: a Unix socket by default; `--listen port` when the server parses `PORT`
  as a number. Getting this wrong is a 502 that looks like a crash.
- **environment**: every variable the code reads, split into build-time and runtime, and
  which are secrets.
- **database**: whether one is needed and which engine.

### 2. Confirm with the human

Do not guess a domain, a tenant name, a key or a password. Ask, in one round:

1. Domain per deployable, and whether `www` should redirect.
2. Tenant: a new short lowercase account, or an existing one. One tenant per untrusted
   application is the real isolation boundary; two apps of the same owner can share one.
3. Database: none, or which engine; new or existing.
4. The public SSH key to authorise for the tenant. Ask them to paste it. Never invent one.
5. Contact email for the certificate.
6. Whether to wire CI afterwards (`ratline-ci` skill).

Then check DNS yourself, because a certificate attempt against a name that does not point
here costs one of a small hourly budget:

```bash
dig +short app.example.com A @1.1.1.1
```

If it does not resolve to the server yet, create the site with `--ssl none` and issue the
certificate in step 7 once it does. Never ask the user to fix DNS and then retry in a loop.

### 3. Rehearse

`ratline new <runtime>` is the one-command version of tenant, site, database and
certificate, and it prints the equivalent single commands. With `--dry-run` it prints the
whole plan and does nothing:

```bash
ssh -T "$RATLINE_HOST" sudo ratline new node app.example.com --user acme \
  --ssh-key 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA… you@laptop' \
  --node 24 --entry server.js --with-db --dry-run
```

Show that plan to the human. If it names something wrong, fix the flags, not the plan.

### 4. Provision

Run the same command without `--dry-run`, with `--json`, and read the envelope. `new`
creates the tenant if it is missing, the site, and the attached database; if any step
fails it removes everything it created and says what it removed. Add `--tls --email` only
when DNS already resolves to this server.

`new` covers the common shape. Reach for the single commands when you need a flag it does
not carry: `--repo` and `--branch` for a pull-from-git site, `--alias`, `--www-redirect`,
`--memory-max`, `--cpu-quota`, `--client-max-body-size`, `--daemon direct`,
`--start-command`, `--relax`, or `--ssl none` while DNS is pending.

```bash
ssh -T "$RATLINE_HOST" sudo ratline user add acme --ssh-key 'ssh-ed25519 AAAA… you@laptop'
ssh -T "$RATLINE_HOST" sudo ratline site add app.example.com --user acme --runtime node \
  --node 24 --entry .next/standalone/server.js --public public --listen port \
  --install-command 'npm ci' --build-command ./bin/build --ssl none
ssh -T "$RATLINE_HOST" sudo ratline db create acmeshop --owner acme --attach app.example.com
```

A site created before its code exists is configured and left stopped. That is normal; step
6 brings it up.

### 5. Environment and secrets

`db create --attach` has already written the connection string into the site's `.env`
(`MONGODB_URI`, or the name given with `--env-key`), so the password never reached your
scrollback. For the rest:

```bash
# not secret: say it outright
ssh -T "$RATLINE_HOST" sudo ratline site env set app.example.com NODE_ENV=production
# secret: write KEY=value lines to a local file that is never committed, then
ssh -T "$RATLINE_HOST" sudo ratline site env set app.example.com --stdin < vars.env
rm -P vars.env 2>/dev/null || rm -f vars.env
```

Do not set `PORT` or `HOST`; ratline allocates them and a value in `.env` fights it. A
database URL you compose yourself for a database outside ratline (Neon, Atlas, RDS) must have
its password percent-encoded (`@` → `%40`, `&` → `%26`, `:` → `%3A`, `/` → `%2F`, `?` → `%3F`,
`#` → `%23`, `%` → `%25`); a raw `@` truncates the host and the error points nowhere near it.
Build-time variables (`VITE_*`, `NEXT_PUBLIC_*`, `PUBLIC_*`) have to be set *before* the
first deploy, because the build runs on the server with the site's environment, and a later
change to one takes effect only after the next `site deploy … --build`, not on restart.
`env set` restarts the service, so a runtime variable needs nothing further.

### 6. Code, then deploy

Three ways for the code to arrive. Prefer the first; it is the one that repeats.

**Pull from git.** `site add --repo git@github.com:acme/app.git --branch main` clones as
the tenant. For a private repository, generate the site's outbound key and add its public
half at the repository host as a read-only deploy key:

```bash
ssh -T "$RATLINE_HOST" sudo ratline site deploy-key create app.example.com
ssh -T "$RATLINE_HOST" sudo ratline site deploy-key show app.example.com
```

**Push from here.** rsync into the application directory, excluding `.git`,
`node_modules`, `.next`, `venv` and every `.env*`. Do it as the tenant with a user-scoped
key so the files are owned correctly, or as root and then `chown -R acme:acme` the
directory, as the documentation shows. Never rsync a local `.env`: it puts development
secrets on a production box and overwrites the credential ratline just wrote.

**Push from CI.** The `ratline-ci` skill.

Then deploy. Choose the steps for the runtime; with no step flags the default chain is
pull, install, build, restart:

```bash
ssh -T "$RATLINE_HOST" sudo ratline site deploy app.example.com --install --build --restart --json
# python without a build:      --install --restart
# Django:                      --install --migrate --collectstatic --restart
# static with a build:         --install --build
```

The health check decides success. An active unit proves the process started; only an HTTP
response through the socket proves the application works. Any HTTP answer counts, a 404
from a router with no `/` route included; only no answer within the timeout fails, so an
API that serves nothing at `/` does not need a health route added for ratline's sake. A
failed step leaves the previous version serving.

**A step the chain does not have** — a seed, a data migration, a `package.json` script
like `npm run bootstrap` — is `site exec`. It runs one command as the tenant, in the
application directory, with the site's `.env` and the site's own runtime on PATH, which
is why `npm` and `python` resolve there and nowhere else on the box:

```bash
ssh -T "$RATLINE_HOST" sudo ratline site exec app.example.com --dry-run -- npm run bootstrap
ssh -T "$RATLINE_HOST" sudo ratline site exec app.example.com -- npm run bootstrap
```

Everything after `--` is an argv, so a flag there belongs to the program and not to
ratline:

```bash
ssh -T "$RATLINE_HOST" sudo ratline site exec app.example.com -- npm run build --if-present
```

A pipe or an `&&` is refused rather than passed to the program — put that in a script in
the repository and exec the script. If the command belongs to *every* deploy it is a hook,
not an exec; if it belongs to a schedule it is a job (`ratline-jobs`).

### 7. TLS

Only once `dig` shows the server's own address for every name on the certificate:

```bash
ssh -T "$RATLINE_HOST" sudo ratline cert issue app.example.com --email ops@example.com --dry-run
ssh -T "$RATLINE_HOST" sudo ratline cert issue app.example.com --email ops@example.com
```

The dry run validates everything, including that port 80 is reachable from outside,
without spending budget. Use `--staging` while debugging an issuance problem. A proxy in
front (Cloudflare's orange cloud) breaks the HTTP challenge; `ratline explain tls` has the
ways out, and `--challenge dns` with a `--dns-provider` is one of them. `--hsts` only after
a trusted certificate is attached, never on a staging or self-signed one.

### 8. Verify, and mean it

```bash
ssh -T "$RATLINE_HOST" sudo ratline site show app.example.com --json
ssh -T "$RATLINE_HOST" sudo ratline site health app.example.com
ssh -T "$RATLINE_HOST" sudo ratline troubleshoot app.example.com
curl -sS -o /dev/null -w '%{http_code}\n' https://app.example.com/
ssh -T "$RATLINE_HOST" sudo ratline site logs app.example.com --lines 50
```

`site health` skips static sites, so the `curl` is the check for those. A `4xx` on `/` can
be legitimate for an API behind authentication; hit a path you know answers. Read the last
fifty log lines for a warning the health check would not catch. If anything is off, the
`ratline-diagnose` skill, starting with `troubleshoot`, which is read-only and takes no lock.

### 9. Hand over

Report what you actually observed, not what you expected. Use this shape:

```
Live: https://app.example.com  (HTTP 200, certificate valid until …)
Created: tenant acme, site app.example.com (node 24, PM2, socket), database acmeshop
Equivalent commands: <the list printed by the new command, or the ones you ran>
Deploy again: ssh -T $RATLINE_HOST sudo ratline site deploy app.example.com --install --build --restart
Environment: ssh -T $RATLINE_HOST sudo ratline site env list app.example.com
Undo: ratline site delete app.example.com writes a backup archive first unless --purge
Not done / needs you: <DNS not pointed yet, certificate pending, CI not wired, …>
```

## When a command fails

| exit | name | what it asks of you |
|---|---|---|
| 2 | usage | fix the arguments; the message names every wrong flag at once. Check `schema` |
| 3 | precondition_failed | read the hint and change the thing it names. Nothing was changed |
| 4 | external_command_failed | nginx, systemd, git or a package manager failed; its last lines are in the message |
| 5 | locked | another ratline run holds the lock; wait a few seconds and run the same command again |
| 6 | rollback_failed | the operation failed **and so did its undo**. Stop. Report exactly what ran. A human runs `doctor` and `reconcile` |
| 7 | health_check_failed | it started but never answered; the previous version is still serving. `site logs`, then fix the app, then deploy again |
| 8 | acme_challenge_failed | DNS or port 80; the hint says which. Do not retry until it is fixed |
| 9 | rate_limited | the CA's limit; the hint has the countdown. Do not retry. `--staging` to keep debugging |
| 10 | input_required | a prompt was needed and there is no terminal; pass the flag, or `--yes` for a confirmation |

## What ratline will not host

Say it plainly and early rather than improvising: PHP, Ruby, Go, Rust, Java and .NET
services, Docker or Compose stacks, anything needing a second long-running daemon beyond a
database ratline provisions (`db --engine mongo|mysql|redis`), and anything that must bind
a public port other than 80 and 443. Do not wrap a foreign binary in `--start-command` or
hand-write a unit to get round it. Background work *for* a supported site is fine: the
`ratline-jobs` skill covers cron jobs and workers.
