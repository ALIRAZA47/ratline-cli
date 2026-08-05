# Upgrading

```bash
sudo ratline update
```

One command, and it is safe on a server that is serving traffic.

## What it does before it installs anything

The success case is a file copy; everything interesting is the refusals.

* The download is checksummed against the release's own `SHA256SUMS`. A release
  with no checksum file is a **refusal**, not a warning — an unverified binary
  installed as root on a server holding every tenant's keys is the same
  supply-chain hole the runtime installer already declines to leave open.
  `--allow-unverified` exists and is deliberately awkward.
* The new binary is executed from a staging directory and asked its version, which
  catches the wrong architecture before it goes anywhere near the install path.
* It is then asked to list this server's sites, using the real database. That is
  what catches a downgrade past a schema migration — which would otherwise install
  cleanly and fail on the next command you ran.
* The install is an atomic rename per file, within the same filesystem, so a timer
  firing mid-update sees the old inode or the new one and never a partial file.
* The previous binary is kept beside the new one.

## Nothing is interrupted

Sites are systemd units running an interpreter. They do not exec `ratline`, so
replacing it cannot drop a request.

`ratline-shell` is the one exception: forced commands in `authorized_keys` point at
it by absolute path, which is why it is verified and swapped exactly the same way.

## If it goes wrong

```bash
sudo ratline update --rollback
```

Restores the binary the last update replaced. Then confirm:

```bash
sudo ratline version
sudo ratline doctor
```

## The other flags

```bash
sudo ratline update --check                    # is there one? change nothing
sudo ratline update --version 1.2.0            # a specific release
sudo ratline update --base-url https://mirror.example.internal/ratline
```

`--base-url` is for a server with no route to GitHub, which is a normal thing.
Mirror the release artefacts and point at them; the checksum verification is
unchanged, so a mirror is not a place to be trusted blindly.

## When ratline came from a package

`update` refuses to overwrite a file `dpkg` owns, and says so. Replacing it behind
the package manager's back leaves the package database lying and the next
`apt upgrade` silently reverts you:

```bash
sudo apt-get update && sudo apt-get install --only-upgrade ratline
```

## After any upgrade

```bash
sudo ratline doctor
sudo ratline reconcile --dry-run
sudo ratline reconcile --fix
```

Nothing needs reloading — ratline is not a daemon and reads its configuration on
every invocation. But a new release may generate a better unit or vhost, and
existing sites keep the old one until `reconcile --fix` regenerates them. That is
deliberate: an upgrade should not restart every site on the box without being
asked. `--dry-run` shows exactly what would change.

## The schema

Migrations are append-only and each is applied in one transaction. A released
migration is never edited, so a server upgrading from any version reaches the same
schema as a fresh install. The version is recorded, and a binary older than the
database refuses to run rather than guessing — which is the check `update` performs
in advance, so a bad downgrade never gets installed in the first place.

## Rolling back further

Keep a backup of `/var/lib/ratline/state.db` and `/etc/ratline` before upgrading —
see [backup-and-restore.md](backup-and-restore.md), which is honest about what
`ratline backup` does and does not cover. `update --rollback` restores the binary;
it does not undo a schema migration.
