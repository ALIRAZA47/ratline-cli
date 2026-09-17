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
        logs/                            app.log, job-<name>.log — the application's own
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
    /etc/systemd/journald@<slug>.conf            the site's journal namespace, and its size cap
    /var/log/nginx/ratline/<slug>/               access.log and error.log, root's, tenant-readable
    /var/log/journal/<machine-id>.<slug>/        the site's own journal, tenant-readable
    /etc/ratline/config.yaml                     configuration
    /etc/ratline/ssh/                            global keys, revocation list
    /var/lib/ratline/state.db                    0600 — the state database
    /var/log/ratline/audit.log                   every mutation, with its argv
    /opt/ratline/runtimes/                       managed interpreters
    /run/ratline/<slug>/                         the runtime directory, holding the socket
    /var/www/ratline-acme/                       the shared HTTP-01 webroot

## Where the logs are, and who can read them

Three kinds of log, in three places, and none of them needs root to read.

nginx's access and error logs are under `/var/log/nginx/ratline/<slug>/`. They are not
inside the site directory, because nginx's master opens them as root on every reload,
and a symlink a tenant dropped at `logs/access.log` would have been a root append to
any file on the box. The directory is root's; the tenant reads it through their group.

The application's own log — `logs/app.log` under PM2, `logs/job-<name>.log` for a job —
is written by the tenant's own process into the tenant's own directory.

Everything a service writes to stdout goes to the journal, and here is the part that
used to need root. Reading the shared system journal is a group membership, `adm` or
`systemd-journal`, and either one is *every* unit on the machine — sshd, the panel,
the other tenants' applications. So every unit ratline renders for a site carries
`LogNamespace=<slug>`, and its output lands in a journal of the site's own, kept by a
`systemd-journald@<slug>` instance under `/var/log/journal/<machine-id>.<slug>/`.
ratline puts a read ACL for the tenant's group on that directory (an ACL rather than a
group, because systemd puts the tree back to root:root on every start if its owner ever
changes, and chown does not touch ACLs). The tenant reads it with

    journalctl --namespace=<slug> -u ratline-<slug>.service

and sees nothing else. Nobody is ever added to `systemd-journal`.

`ratline site logs <domain>` knows all of this and does not need root either. Run by a
tenant, it finds the site in their own home, reads what their permissions allow — the
nginx logs, the application log, the site's namespace — and refuses a site that is not
theirs. Run by root, it reads any site's. A site-scoped SSH key gets the same through
the `logs` verb of its forced command:

    ssh deploy@server logs --follow

Each namespace is capped at `defaults.journal_max_use` (256M), because journald's own
default — a tenth of the disk, up to 4G — is per instance, and there is one per site.
A site created before this existed logs into the shared journal until
`ratline reconcile --fix` re-renders its units and the site is restarted; `doctor`
reports the gap.

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
