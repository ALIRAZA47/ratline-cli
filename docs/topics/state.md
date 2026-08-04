# State, audit and backups

> What ratline records, where, and how to get it back.

## The database

`/var/lib/ratline/state.db` is SQLite at mode `0600`. It holds users, sites,
aliases, keys, certificates, port allocations, deployments and events. It is the
source of truth for what *should* exist; the filesystem is what does.

`ratline reconcile` compares the two and reports the difference. `--fix` regenerates
the configuration from state.

## The audit log

`/var/log/ratline/audit.log` records every mutation: what ran, its argv, which UID
and which `sudo` user, the target, the result, the exit code and the duration.
Secrets are redacted — a value passed with `env set --stdin` never reaches it.

    ratline export --format json | jq '.events[-10:]'

## Backups

    ratline backup                       # to /var/backups/ratline
    ratline backup --output /mnt/nas
    ratline restore <archive>

A backup holds the database, `/etc/ratline`, the generated nginx and systemd
configuration, and the certificate inventory. It does **not** hold tenant
application code or `node_modules` — that is what the repository is for — and it
does not hold private key material for keys it did not generate.

`ratline export` is the other half: a JSON dump of state, for feeding to something
else. It carries no private key material at all, by design, so it is safe to hand
to a monitoring system.

## Schema upgrades

Migrations are append-only and each is applied in one transaction. A released
migration is never edited, so a server upgrading from any version reaches the same
schema as a fresh install. The version is recorded, and an upgrade to a schema
version older than the database refuses rather than guessing.

See also: `ratline explain safety`.
