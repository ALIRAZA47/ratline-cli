---
name: ratline-panel
description: Install, expose and run ratline-panel, the web interface for a ratline server — the install script or `ratline-panel install` with the first super admin, reaching it over a tunnel, putting it on a domain with a certificate, roles and invitation links, `doctor`, and updating or rolling it back. Use whenever someone wants a web UI, dashboard, control panel or "something my client can click" for their ratline server, asks how the panel relates to the CLI, or has a panel that will not start, cannot be reached, or needs a new admin.
---

# The web panel

`ratline-panel` is a separate binary, a separate systemd service and a separate install.
It **reimplements nothing**: every action runs `ratline <verb> --json` and reads the
envelope, so a deploy started in a browser goes through the same lock, the same
staged-verified-committed path and the same rollback as one typed over SSH. It never writes
to ratline's state; its own database holds only what ratline cannot know, which human asked.
That is why installing it changes nothing about ratline, and why nothing in this skill
configures ratline itself.

Rules shared with every skill in this plugin: look flags up with `ratline-panel <cmd>
--help`; pipe secrets on stdin; read what a command prints rather than assuming. Run
commands as root on the server, or `ssh -T "$RATLINE_HOST" sudo ratline-panel …`.

## Installing

Two ways, same result.

```bash
# together with ratline, on a fresh server
curl -fsSL https://ratline.alirazakhan.me/install.sh \
  | sudo WITH_PANEL=1 PANEL_ADMIN_EMAIL=you@example.com sh

# onto a server already running ratline, which is the normal case
curl -fsSL https://ratline.alirazakhan.me/panel.sh | sudo sh
```

Or, when the binary is already on the box:

```bash
ratline-panel install --admin-email you@example.com
ratline-panel install --admin-email you@example.com --domain panel.example.com --email you@example.com
printf '%s' "$PANEL_PASSWORD" | ratline-panel install --admin-email ops@example.com --admin-password-stdin
```

The installer **creates the first super admin** and prints a generated password once, or
reads one on stdin. There is no window in which the panel is answering and unclaimed, and
no default password. `--no-admin` deliberately puts the claimable state back, for an
installer that will create the account another way; `doctor` calls that a problem until
someone claims it. Running the install twice is safe: existing configuration, database and
accounts are kept.

The panel listens on `127.0.0.1:8420` until it has a domain. Reach it through a tunnel:

```bash
ssh -L 8420:127.0.0.1:8420 "$RATLINE_HOST"       # then open http://localhost:8420
```

## Putting it on a domain

Once DNS points at the server (`dig +short panel.example.com A @1.1.1.1`):

```bash
ratline-panel domain set panel.example.com --email you@example.com --staging   # prove the plumbing, spend no budget
ratline-panel domain set panel.example.com --email you@example.com
ratline-panel domain set panel.example.com --no-tls                            # TLS terminated elsewhere
```

That writes an nginx vhost proxying to the panel over a unix socket only nginx's group can
open, obtains a certificate over the ACME webroot ratline already uses, and rewrites the
vhost with TLS. The vhost is staged, checked with `nginx -t` and rolled back on failure,
carries a `# managed-by: ratline-panel` header and will not overwrite a file without one.
Forwarded headers are believed only on that socket: the loopback port is reachable by every
tenant, so an `X-Forwarded-For` there is never trusted. `doctor` flags a configuration that
still trusts the port.

The panel is not a ratline site and is not registered as one. Do not try to `site add` it.

## Who can do what

Two roles, enforced on the server per request, not hidden in the interface.

**Super admin**: everything an admin can, plus who else has access, plus the operations
another command cannot undo: `user delete`, `site delete`, `db drop`, `cert revoke`,
`key prune`, `user sudo grant`, `db access allow`, `restore`, `import`, `update`,
`config set`. An admin's browser is never even sent those actions.

**Admin**: the server day to day: sites, deploys, certificates, keys, databases,
environment variables, runtimes.

Invitations are links, shown once, single-use, expiring. The panel does not send email, by
design, so the super admin chooses how to deliver the link. A super admin creates it in the
interface; `ratline-panel account --help` covers the command-line side for the first account
or a locked-out one.

Every mutating action has a **dry run** button beside the real one, produced by ratline's
own `--dry-run`. Long operations (deploy, issuance, runtime build) are jobs with a stored
transcript that outlives the tab.

## Checking it

```bash
ratline-panel doctor
ratline-panel config show
systemctl status ratline-panel
ratline-panel nginx --help          # reload the panel's own vhost after a renewal
```

`doctor` reports an unclaimed setup, a vhost that still uses the loopback port, a
certificate problem, a binary out of step with ratline, and a job that has been running too
long.

## Updating and rolling back

```bash
ratline update                     # ratline, and then the panel, on a server running both
ratline-panel update --check       # is there a newer release? changes nothing
ratline-panel update               # the panel on its own
ratline-panel update --version 0.19.0
ratline-panel update --rollback    # the previous binary, and restart
```

`update` checksums the download against the release's `SHA256SUMS`, runs the new binary and
asks its version before installing it, swaps atomically and keeps the old one. It refuses
while one of its own jobs is running rather than killing it; `--force` overrides that and
should not be used without a human's yes. A panel older than the release that added
`ratline-panel update` cannot update itself; take it to a current release once with the
install script (`NO_INSTALL=1` re-downloads the binary only) and it updates with ratline
afterwards.

## Removing

```bash
ratline-panel uninstall
```

Stops the service and removes the unit and the vhost. Tenants, sites and ratline's own state
are untouched, because the panel never owned them.

## When it will not come up

`ratline-panel doctor` first, then `journalctl -u ratline-panel -n 100`. A refreshed tab that
is signed in and cannot change anything is a CSRF token problem the current release fixed
(`update`). A deep link that 404s on refresh but works when clicked is the same story. If
the panel is fine and the action it runs fails, the failure is ratline's and `ratline-diagnose`
applies: the panel shows the envelope it received.
