# Backup and restore

```bash
sudo ratline backup acme                       # one tenant's home
sudo ratline backup app.example.com            # one site directory
sudo ratline backup acme --out /mnt/nas

sudo ratline restore /var/backups/ratline/app.example.com-20260105T120000Z.tar.gz
```

## What `backup` archives

**One tenant's home, or one site directory** — the application code, the logs, the
`.env`, and for a site the manifest at `.ratline/site.yaml`.

Because it holds the `.env`, **it holds secrets**. It is written `0600` inside a `0700`
directory, and where it goes afterwards is your responsibility.

`ratline site delete` writes one of these automatically unless `--purge` says otherwise,
so the archive of a site you removed by mistake already exists.

## What `restore` rebuilds

The archive is a directory. It does not contain the state database, the nginx vhost, the
systemd unit, or the tenant's uid — so `restore` rebuilds all four:

* the **state row** from the manifest that travelled with the files
* the **vhost and unit**, re-rendered from that row rather than restored from the archive,
  because they hold absolute paths and a uid and this ratline may generate better ones
* **ownership**, from the account as it exists on *this* server — the uids inside the
  archive belong to whichever machine wrote it
* the **port**, reallocated, since the one in the manifest was free somewhere else

Then it starts the service and waits for a real HTTP response, the same as a deploy.

```bash
sudo ratline restore app.example.com-20260105T120000Z.tar.gz
sudo ratline restore acme-20260105T120000Z.tar.gz          # a whole home, sites and all
sudo ratline restore app.example.com-...tar.gz --dry-run   # say what it would do
sudo ratline restore app.example.com-...tar.gz --no-start   # put it back, leave it stopped
```

Restoring a home rebuilds every site inside it, because a home full of site directories
with no vhosts and no units looks exactly like a successful restore until someone visits
one.

### The owning account has to exist first

```bash
sudo ratline user add acme      # then restore
```

An account is a uid, a group, a shell, a home and a set of SSH keys. None of that is in
the archive, and inventing it would produce a tenant nobody can log in as, owning files
whose uid matches nothing.

### Restoring over something that is already there

Refused unless you pass `--force`, and then confirmed. The previous directory is moved
aside and removed only once the state row, the vhost, the unit and the health check have
all succeeded — so a failure at any of them puts back what was serving.

### Archives are treated as untrusted

`restore` extracts as root, and an archive may have been copied between servers, kept on a
share, or handed over by whoever is migrating in. So:

* a member with an absolute or traversing path is **refused**, not sanitised
* symlinks are chowned with `lchown` rather than followed, so a link pointing outside the
  tree cannot hand a tenant ownership of a file elsewhere
* the manifest's domain and owner are validated as if typed, the owner is checked against
  the reserved list — `root` is a well-formed username — and the slug is recomputed rather
  than trusted, because it names the systemd unit

## What `backup` still does not cover

It is not a server backup. It does **not** include:

* the state database at `/var/lib/ratline/state.db`
* `/etc/ratline` — configuration, global keys, the revocation list, DNS credentials
* the certificates

For a whole-server rebuild, back those up with whatever already backs up the rest of the
host. `restore` handles a site or a tenant; it does not reconstruct the server.

## Rebuilding a server by hand

```bash
sudo ratline init
# restore /var/lib/ratline/state.db and /etc/ratline from your host backup
sudo ratline reconcile --fix     # regenerate the vhosts and units from state
sudo ratline doctor
sudo ratline troubleshoot        # the host: clock, disk, tooling, state
```

`reconcile --fix` regenerates everything derivable from state. `doctor` then reports what
still needs attention — most often certificates whose DNS now points elsewhere, and
application code that has not been deployed yet.

If the state database is the thing you lost, the manifests are the fallback: each site
directory carries one, and `ratline restore` reads it. Archive the homes and you can
rebuild the sites from those alone.

## Exporting state

```bash
sudo ratline export > inventory.json
```

A JSON dump of everything ratline knows, carrying **no private key material at all** by
design — so it is safe to hand to a monitoring system or a configuration-management tool.
It is a record, not a restore path.
