# Upgrading

```bash
sudo sh install.sh          # or: dpkg -i the new .deb
sudo ratline init           # idempotent; adds what is missing
sudo ratline reconcile --dry-run
sudo ratline reconcile --fix
sudo ratline doctor
```

## The schema

Migrations are append-only, and each is applied in one transaction. A released
migration is never edited, so a server upgrading from any version reaches the same
schema as a fresh install. The version is recorded; a binary older than the database
refuses to run rather than guessing at a schema it does not know.

Nothing needs to be reloaded after an upgrade: ratline is not a daemon, and it reads
its configuration on every invocation.

## Templates

A new version may generate a better unit or vhost. Existing sites keep the old one
until `reconcile --fix` regenerates them — deliberately, so an upgrade does not
restart every site on the box without being asked. Preview with `--dry-run`.

## Rolling back

Keep the previous binary. State is forward-compatible within a major version, and an
older binary refuses a newer schema rather than corrupting it — so a rollback means
restoring the binary *and* the database from the backup taken before the upgrade.

```bash
sudo ratline backup    # before upgrading, every time
```
