# Monitoring

Two commands, answering two different questions.

## `ratline status` — what is here

```bash
sudo ratline status
```

```
web-1.example.net — ratline 1.0.0
Ubuntu 24.04.1 LTS, up 41d 6h

3 tenants, 5 sites, 7 SSH keys, 4 certificates

    DOMAIN                OWNER   RUNTIME  STATE     TLS                 NOTE
    www.example.com       acme    static   serving   https
    app.example.com       acme    node     running   https               4 workers
  ! api.example.com       acme    python   failed    https
    blog.example.org      beta    static   serving   https (expiring)
  ! stage.example.org     beta    node     running   http                2 of 4 workers online

Certificates needing attention:
  blog.example.org                         expiring, 6 days left

2 problems found. See them with 'ratline doctor'.
```

It always prints. That is the point: a healthy server still needs an inventory, and
`doctor` on a healthy server prints nothing.

For scripting:

```bash
sudo ratline status --json | jq '.sites_detail[] | select(.needs_attention)'
```

## `ratline doctor` — what is wrong

```bash
sudo ratline doctor
sudo ratline doctor --fix
```

Every check ratline knows how to run: the nginx configuration, failed services, dead
sockets, socket permissions, certificate expiry, orphaned configuration, drift between
state and the filesystem, permission anomalies, allocated but unused ports, and the SSH
key audit. Exit 0 means healthy, which makes it a usable cron job:

```
0 7 * * * /usr/local/bin/ratline doctor --json || mail -s "ratline: $(hostname)" ops@example.com
```

The problem count in `status` comes from `doctor` itself rather than a second
implementation, so the two can never disagree about whether the server is healthy.

## `ratline troubleshoot <subject>` — why one thing is broken

```bash
sudo ratline troubleshoot app.example.com    # a site's request path
sudo ratline troubleshoot acme               # a tenant
sudo ratline troubleshoot SHA256:AbC…        # a key
sudo ratline troubleshoot nginx
sudo ratline troubleshoot ssh
sudo ratline troubleshoot                    # the host
```

The third command, and the one worth understanding. `doctor` sweeps and reports
findings; `troubleshoot` takes one subject and walks its preconditions in the order
they depend on each other, stopping at the first failure. Because the order is a
dependency order, the first failure **is** the cause — and the steps it broke are
reported as not-checked rather than as more problems to rank.

`ratline doctor <subject>` runs the same walk, for when that is the spelling that
comes to mind. Both are read-only and neither takes the lock.

See [troubleshooting.md](troubleshooting.md) for what each walk checks.

## Restart counts under PM2

systemd's `NRestarts` stays at **zero** on a PM2-supervised node site, because PM2 does
the restarting. `status`, `site status` and `doctor` all read PM2's counter instead and
label it as PM2's. Reading systemd's number directly from `systemctl` on such a site
will tell you everything is fine while the application crash-loops.

## The audit log

`/var/log/ratline/audit.log` records every mutation: the command, its argv, the UID, the
`sudo` user, the target, the result, the exit code and the duration. Secrets are
redacted.

```bash
sudo ratline export --format json | jq '.events[-20:]'
```
