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

    ratline export | jq '.data.events[-10:]'

## Archives

    ratline backup acme                  # one tenant's home
    ratline backup app.example.com       # one site directory
    ratline backup acme --out /mnt/nas   # somewhere other than /var/backups/ratline

`backup` archives **one tenant's home or one site directory** — the application code,
the logs and the `.env`. Nothing else. It is not a server backup, and it does not hold
the state database, `/etc/ratline`, the generated nginx or systemd configuration, or
the certificates.

Because it holds the `.env`, **it holds secrets**. It is written 0600 inside a 0700
directory, and where it goes after that is your responsibility.

`site delete` takes one of these automatically unless `--purge` says otherwise, so an
archive of a site you deleted by mistake already exists.

## What is not covered

There is **no `ratline restore`**. Nothing unpacks an archive back into place, and
nothing backs up the server's own configuration — so a full-server recovery today
means restoring the files by hand and then `ratline reconcile --fix` to regenerate the
nginx and systemd configuration from state, which itself has to be restored from
whatever backs up `/var/lib/ratline/state.db`.

If you rely on this server, back up `/var/lib/ratline/state.db` and `/etc/ratline`
with whatever already backs up the rest of the host. `ratline export` is useful
alongside that:

    ratline export > inventory.json

A JSON dump of everything ratline knows, carrying no private key material at all by
design — so it is safe to hand to a monitoring system or a configuration-management
tool. It is a record rather than a restore path.

## Schema upgrades

Migrations are append-only and each is applied in one transaction. A released
migration is never edited, so a server upgrading from any version reaches the same
schema as a fresh install. The version is recorded, and an upgrade to a schema
version older than the database refuses rather than guessing.

See also: `ratline explain safety`.
