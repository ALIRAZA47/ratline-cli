# The web panel

> A separate service that drives these same commands from a browser, and never
> reimplements one.

`ratline-panel` is a web interface for this tool. It is a separate binary, a separate
service and a separate install: a server that never wants a web interface never has
one, and installing the panel does not change the ratline binary in any way.

It reimplements nothing. Every action it offers runs `ratline <verb> --json` and reads
the envelope, so a deploy started in a browser is staged, verified, committed and
rolled back by exactly the code that would have run had you typed it over SSH — the
same global lock, the same audit record, the same exit codes.

```
    browser ──HTTP──▶ ratline-panel ──argv──▶ ratline ──▶ nginx, systemd, certbot
                            │                    │
                       panel.db             state.db
                    (who asked)          (what exists)
```

Two databases, and the split is the point. `state.db` is ratline's and the panel never
writes to it; `panel.db` holds only what ratline cannot know — that a human called
Dana, signing in from an address, asked for the deploy that ratline recorded as root.

## Installing it

On a server that is already running ratline, with tenants and sites on it. That is the
normal case and nothing about it is special: the panel writes its own configuration,
its own database and its own unit, and touches none of ratline's.

```sh
curl -fsSL https://ratline.alirazakhan.me/panel.sh | sudo sh
```

Or, from a checkout:

```sh
make install-panel
```

It asks for one thing — the address of the first super admin — creates that account,
and prints a generated password once:

```
Panel database at /var/lib/ratline/panel.db (schema 1)
Installed /etc/systemd/system/ratline-panel.service

The panel is running.

  Sign in as   you@example.com
  Password     k7fm-3q9x-2vtb-npd4-h6ws

That password is shown once and is not stored anywhere in the clear.
```

The account is created here rather than by whoever opens the panel first, because
"the first visitor becomes the administrator" is a window, and a window on a machine
nobody is watching is how a server is lost. There is no default password and nothing
to change afterwards except your own.

For a provisioning script, supply both and nothing is printed:

```sh
printf '%s' "$PANEL_PASSWORD" | ratline-panel install \
    --admin-email ops@example.com --admin-password-stdin
```

It listens on `127.0.0.1:8420`, so reach it through a tunnel until it has a domain:

```sh
ssh -L 8420:127.0.0.1:8420 your-server
# then open http://localhost:8420
```

Running it twice is safe: an existing configuration is kept, an existing database is
reused, and an existing account is left alone.

## Putting it on a domain

Once DNS points at this server:

```sh
ratline-panel domain set panel.example.com --email you@example.com
```

That writes an nginx vhost proxying to the panel, obtains a certificate over the ACME
webroot ratline already uses, and rewrites the vhost with TLS. The vhost is staged,
checked with `nginx -t` and rolled back on failure, exactly as ratline's own are. It
carries a `# managed-by: ratline-panel` header and will not overwrite a file without
one.

The panel is not a ratline site and is not registered as one — it has no tenant, no
home directory and no unit running as one, so pretending otherwise to reuse the site
renderer would be a lie the model would then have to keep. Renewal is certbot's own
timer; the deploy hook calls `ratline-panel nginx reload`, because a renewal that does
not reload nginx changes a file on disk and leaves the old certificate being served.

## Who can do what

Two roles.

**Super admin** can do everything an admin can, and two things they cannot: change who
else has access, and run the operations that cannot be undone by running another
command — `user delete`, `site delete`, `db drop`, `cert revoke`, `key prune`,
`user sudo grant`, `db access allow`, `restore`, `import`, `update`, `config set`.

**Admin** runs the server day to day: sites, deploys, certificates, keys, databases,
environment variables, runtimes.

The split is enforced on the server, not in the interface. An admin's browser is never
sent the super-admin operations at all — they are absent from the catalogue it
receives, not hidden in it.

Invitations are links. The panel does not send email, deliberately: doing so would
mean it owned an SMTP configuration, a queue, a bounce problem and a new way for an
invitation to leak through somebody's mail logs. A super admin creates a link, it is
shown once, and they choose how to deliver it. It works once and expires.

## What it does that the command line does not

- **A dry run you can read before you commit.** Every mutating action has a "dry run"
  button beside the real one. ratline implements `--dry-run` at the Runner, so nothing
  is written at any layer, and the plan you read is produced by the same code path
  that would have done the work.
- **A transcript that outlives the tab.** A deploy, an issuance or a runtime build is
  a job with a stored log. Closing the browser does not stop it, and the transcript is
  still there tomorrow.
- **Forms that cannot be wrong.** They are generated from `ratline schema`, which the
  binary produces by walking its own command tree — so a form offers exactly the flags
  the installed ratline takes, and a ratline upgrade that adds one adds a field.

## What it deliberately does not do

- It does not accept secrets as flag values. `/proc/PID/cmdline` is world-readable, so
  a database URL in an argument is a database URL every account on the server can read
  while the command runs. Secrets travel on stdin and appear in no argv and no log.
- It does not run two mutations at once. ratline takes a global lock, so a second one
  would sit inside ratline waiting; the panel queues instead, which turns a failure
  into a position in a line you can watch.
- It does not have a terminal. There is no shell, no file browser and no editor. If
  you need those, you need SSH, and the panel not offering them is the reason it can
  be given to somebody who should not have them.

## Recovering from it

The panel can lock you out of itself: a lost second factor, a forgotten password, or
the only super admin disabled. Every recovery path is a command, run over SSH by
whoever has root — which is not a new privilege, because root can read the panel's
database anyway.

```sh
ratline-panel account list
ratline-panel account role you@example.com superadmin
ratline-panel account password you@example.com     # reads it from stdin
ratline-panel account totp-reset you@example.com
ratline-panel doctor                                # what is wrong, and what to run
```

## The honest part

Signing in to this panel is equivalent to root on the machine. It runs as root because
its job is to invoke a tool that creates system accounts and writes into `/etc`, and
there is no arrangement of privileges that changes what an authenticated user can then
ask for.

So: put it behind nginx with a certificate rather than exposing the port; turn on
`security.require_totp` before it is reachable from the internet; use
`security.allow_from` if you only ever use it from one place; and give people the
admin role rather than super admin unless they need to invite others.

`ratline-panel doctor` checks all of that and says which of it you have not done.
