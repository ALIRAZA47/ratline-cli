# Backup and restore

```bash
sudo ratline backup
sudo ratline backup --output /mnt/nas
sudo ratline restore /var/backups/ratline/ratline-2026-08-04.tar.gz
```

## What a backup holds

* the state database
* `/etc/ratline` — configuration, global keys, the revocation list, DNS credentials
* the generated nginx configuration and systemd units
* the certificate inventory, and the certificates themselves

## What it does not hold

* **Tenant application code.** That is what the repository is for. A backup that
  included `node_modules` would be enormous and no more useful.
* **Private key material for keys ratline did not generate.** It never had them.

So a restore brings back the server's *configuration*, and `site deploy` brings back
the code. That split is deliberate: it keeps backups small enough to actually be taken.

## Restoring onto a new server

```bash
sudo ratline init
sudo ratline restore <archive>
sudo ratline reconcile --fix
sudo ratline doctor
```

`reconcile --fix` regenerates anything the archive did not carry, and `doctor` reports
what still needs attention — most often certificates whose DNS now points somewhere
else.

## Exporting state instead

```bash
sudo ratline export --format json > inventory.json
```

A JSON dump of everything ratline knows, carrying **no private key material at all** by
design — so it is safe to hand to a monitoring system or a configuration-management
tool.

## Per-site and per-tenant archives

```bash
sudo ratline backup --site app.example.com
sudo ratline backup --user acme
```

These *do* include the site directory, code and all, because that is what someone
asking for one site's archive wants. `site delete` takes one automatically unless
`--purge` says otherwise.
