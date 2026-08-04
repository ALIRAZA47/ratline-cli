# Drift and reconcile

State is an index of what *should* exist. The filesystem is what does. When they
disagree, the filesystem wins and `reconcile` is how you decide what to do about it.

```bash
sudo ratline reconcile          # report; changes nothing
sudo ratline reconcile --fix    # regenerate configuration from state
```

## What counts as drift

* a site in state with no vhost on disk, or a vhost that is not linked into
  `sites-enabled`
* a unit file that is missing, or one whose contents no longer match what ratline
  would generate
* a vhost or unit on disk that state knows nothing about — the residue of a
  hand-edited server
* a tenant in state with no system account, or an account with no home
* a port allocated in state and not used by any site

## What `--fix` will not touch

Anything without a `# managed-by: ratline` header. If someone hand-wrote
`/etc/nginx/sites-available/legacy.conf`, reconcile reports it and leaves it alone:
losing a file a human wrote is worse than failing to tidy up.

Your own directives in `/etc/nginx/ratline/custom/<domain>.conf` are never regenerated,
so they survive every reconcile.

## After an upgrade

```bash
sudo ratline reconcile --fix
```

Worth running when a new version changes a template — the units and vhosts are
regenerated from state, so the improvement reaches sites that already exist rather than
only new ones. `--dry-run` first shows exactly what would change.
