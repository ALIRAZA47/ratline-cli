# Where everything lives

> The filesystem layout: what ratline creates, and where to look for it.

Every tenant is a system user with a home under `/home`, and every site is a
directory inside that home. Nothing a tenant owns lives outside their home, which
is what makes `userdel -r` a complete removal and what stops one tenant reading
another's files.

## A tenant's home

    /home/acme/                          0750, acme:acme
      .ssh/authorized_keys               0600 — user-scoped keys
      app.example.com/                   0750 — one directory per site
        app/                             the application, or the repository clone
        public/                          the document root for a static site
        logs/                            access.log, error.log, app.log
        tmp/                             scratch, bound into the unit as writable
        .env                             0600 — secrets, never under a document root
        .ratline/                        generated per-site files
          ecosystem.config.json          the PM2 configuration, for a node site
        .pm2/                            that site's own PM2 daemon state
        venv/                            the virtualenv, for a python site

The home is `0750` and never `0755`. nginx reaches the document root because it is
added to the tenant's group, not because the world can read it. `ratline doctor`
reports a home whose mode has drifted, because `0755` is the single worst
permission mistake available on a shared server.

## What ratline owns outside a home

    /etc/nginx/sites-available/<domain>.conf     the generated vhost
    /etc/nginx/sites-enabled/<domain>.conf       a symlink; removing it disables the site
    /etc/nginx/ratline/                          shared snippets
    /etc/nginx/ratline/custom/<domain>.conf      yours, never regenerated
    /etc/systemd/system/ratline-<slug>.service   one unit per dynamic site
    /etc/ratline/config.yaml                     configuration
    /etc/ratline/ssh/                            global keys, revocation list
    /var/lib/ratline/state.db                    0600 — the state database
    /var/log/ratline/audit.log                   every mutation, with its argv
    /opt/ratline/runtimes/                       managed interpreters
    /run/ratline/<slug>/                         the runtime directory, holding the socket
    /var/www/ratline-acme/                       the shared HTTP-01 webroot

## The two files that are yours

`/etc/nginx/ratline/custom/<domain>.conf` is included by the generated vhost and
is never rewritten. Anything you put there survives `ratline reconcile`.

`/etc/ratline/config.yaml` is read on every invocation, so there is nothing to
reload after an edit.

## Generated files are marked

Every file ratline writes starts with `# managed-by: ratline`. ratline refuses to
overwrite a file at one of its own paths that lacks that header, because the
absence of it means a human wrote the file and losing it would be worse than
failing.

See also: `ratline explain sockets`, `ratline explain state`.
