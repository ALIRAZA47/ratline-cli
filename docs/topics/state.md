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

It also does not hold **the contents of any database ratline provisioned**. A site's
files and its data are backed up separately, and an archive of a site with a MongoDB
database attached is not a restore point for that site.

Because it holds the `.env`, **it holds secrets**. It is written 0600 inside a 0700
directory, and where it goes after that is your responsibility.

`site delete` takes one of these automatically unless `--purge` says otherwise, so an
archive of a site you deleted by mistake already exists.

## Putting one back

`ratline restore` unpacks an archive and rebuilds what the archive does not contain:

    ratline restore /var/backups/ratline/app.example.com-20260105T120000Z.tar.gz

An archive holds a directory — the code, the logs, the `.env`, and for a site its
manifest. It does not hold the state row, the vhost, the unit, the tenant's uid or the
port. So restore rebuilds the row from the manifest that travelled with the files,
re-renders the vhost and unit rather than restoring them, takes ownership from the
account as it exists on *this* server, reallocates the port, and then waits for a real
HTTP response before reporting success.

The owning account has to exist first: an account is a uid, a group, a shell and a set
of keys, none of which is in the archive.

## Moving to another server

    ratline export > server.json          # on the old server
    ratline import server.json            # on the new one

`export` is a JSON dump of everything ratline knows, carrying no private key material at
all by design — so it is also safe to hand to a monitoring system or a
configuration-management tool.

`import` reads one back and rebuilds the shape: the tenants, their SSH keys, their sites
with every setting, the aliases, and which sites were disabled. It is one transaction —
if a step fails, everything it created is removed — and it is safe to run twice, so a
tenant or site that is already there is reported and left alone.

    ssh old-server ratline export | ratline import -
    ratline import server.json --dry-run     # the plan, writing nothing
    ratline import server.json --only acme   # one tenant, and its sites

What an export does not hold, `import` cannot bring, and it lists all of this when it
finishes rather than letting a clean exit imply a finished migration:

  - **Application code.** Nothing is cloned; `ratline site deploy` does that.
  - **Environment values.** `.env` is secrets, so it is not exported.
  - **Certificates.** Private keys are never exported, so each one has to be re-issued
    here — which is right anyway, since the old certificate was issued for a host that
    is about to stop answering. Sites come back with TLS off for the same reason:
    issuing before DNS moves is the one request guaranteed to fail.
  - **Database contents.** The export records that a site had one attached, not what was
    in it.

A key that was revoked on the old server is not restored. Re-adding one would hand back
access somebody deliberately took away, in the middle of a migration, for a key nobody
is thinking about.

## What is still not covered

`restore` handles one site or one tenant, and `import` rebuilds the shape of a whole
server but none of its contents. Neither is a substitute for backing up
`/var/lib/ratline/state.db` and `/etc/ratline` with whatever already backs up the rest
of the host — followed by `ratline reconcile --fix` to regenerate the nginx and systemd
configuration from state.

## Schema upgrades

Migrations are append-only and each is applied in one transaction. A released
migration is never edited, so a server upgrading from any version reaches the same
schema as a fresh install. The version is recorded, and an upgrade to a schema
version older than the database refuses rather than guessing.

See also: `ratline explain safety`.
