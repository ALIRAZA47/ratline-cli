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

## One site

```bash
sudo ratline site status app.example.com
sudo ratline site troubleshoot app.example.com
```

`site status` is the unit's state. `site troubleshoot` walks the whole request path and
finds where it breaks — see [troubleshooting.md](troubleshooting.md).

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
