# Filesystem

Every path ratline reads or writes. All of them are configurable under `paths:` in
`/etc/ratline/config.yaml`; these are the defaults.

## A tenant's home

```
/home/acme/                          0750  acme:acme
  .ssh/authorized_keys               0600  user-scoped keys, in managed markers
  app.example.com/                   0750  one directory per site
    app/                                   the application, or the repository clone
    public/                                the document root for a static site
    logs/                                  access.log, error.log, app.log
    tmp/                               0700
    .env                             0600  secrets; never under a document root
    .ratline/site.yaml                     the rendered manifest, so reconcile can rebuild
    .ratline/ecosystem.config.json         the PM2 configuration, for a node site
    .pm2/                                  that site's own PM2 daemon state
    venv/                                  the virtualenv, for a python site
```

The home is `0750` and never `0755`. nginx reaches the document root by being a member
of the tenant's group. `doctor` reports a home whose mode has drifted.

## Server paths

```
/etc/nginx/sites-available/<domain>.conf      the generated vhost
/etc/nginx/sites-enabled/<domain>.conf        a symlink; removing it disables the site
/etc/nginx/ratline/                           shared snippets
/etc/nginx/ratline/custom/<domain>.conf       yours — included, never regenerated
/etc/systemd/system/ratline-<slug>.service    one unit per dynamic site
/etc/systemd/system/ratline.target            stops or starts every site at once
/etc/ratline/config.yaml                      configuration
/etc/ratline/ssh/global.authorized_keys       global-scope keys
/etc/ratline/ssh/revoked_keys                 consulted by sshd regardless of any
                                              authorized_keys file
/etc/ratline/certs/                     0600  imported certificates
/etc/ratline/dns/                       0600  DNS provider credentials
/etc/ssh/sshd_config.d/60-ratline.conf        ratline's sshd additions
/var/lib/ratline/state.db               0600  the state database
/var/log/ratline/audit.log                    every mutation, with its argv
/var/backups/ratline/                         backup archives
/opt/ratline/runtimes/node/<version>/         managed interpreters, root-owned
/opt/ratline/runtimes/python/<version>/
/usr/local/lib/ratline/ratline-shell          the forced-command wrapper
/run/ratline/<slug>/app.sock                  the socket nginx proxies to
/run/ratline.lock                             the global mutation lock
/var/www/ratline-acme/                        the shared HTTP-01 webroot
```

## The `# managed-by: ratline` header

Every generated file carries it. ratline **refuses** to overwrite a file at one of its
own paths that lacks it, because the absence of the header means a human wrote the file
and losing it is worse than failing.

## The two files that are yours

`/etc/nginx/ratline/custom/<domain>.conf` — included by the generated vhost, never
regenerated.

`/etc/ratline/config.yaml` — read on every invocation, so no reload is needed.

## Why `/run` for the socket

`/run` is a tmpfs, so a stale socket cannot survive a reboot. The directory is created
by systemd's `RuntimeDirectory=`, which also removes it when the unit stops — so a
crashed process cannot leave a socket file that nginx keeps trying to connect to.
