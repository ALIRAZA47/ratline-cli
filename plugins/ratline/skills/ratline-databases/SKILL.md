---
name: ratline-databases
description: Provision and operate databases through ratline — install MongoDB on the host or attach one that exists (Atlas, another server), MySQL and Redis via `--engine`, create a database with a least-privilege user attached to a site, dump and restore data, rotate a database password, and control which addresses may reach the port. Use whenever an app on a ratline server needs a database, a connection string, a DB user, a data backup, or another machine needs to reach the database, and for "does this server have MongoDB", "create a database for the site", "give me the URI".
---

# Databases on a ratline server

`ratline db` creates databases and users whose only role is on their own database, and
writes the connection string into a site's `.env` so the password never appears in your
scrollback. It works against any server you point it at; a local `mongod` and a managed
cluster differ only in the admin connection string.

Two things it will not do, and both are load-bearing: it never installs a database server as
a side effect of anything (`site add`, `db create`, a wizard), and it never runs `ufw
enable`. The one command that installs anything is `db install`, whose whole job is that,
for MongoDB by default and for MySQL or Redis with `--engine`.

Rules shared with every skill in this plugin: look flags up in `ratline schema` (the `db`
group carries a persistent `--engine mongo|mysql|redis`); rehearse with `--dry-run`; pipe
secrets on stdin; read the envelope and branch on the exit code. Run commands as root on
the server, or `ssh -T "$RATLINE_HOST" sudo ratline …` from a laptop. If the MCP server is
connected, `ratline_db_list` answers "what exists" without a shell.

## Is there a database server at all

```bash
ratline db ping
ratline db list
```

`db ping` proves the stored admin credential works and that the server enforces
authorization. If provisioning is off, both say so, and the next question is which of these
two situations you are in.

### A fresh VPS with no MongoDB anywhere: install one

```bash
ratline db install --dry-run          # the resolved plan: repo, version, config, users
ratline db install                    # prompts for the admin password, not echoed
ratline db install --stdin < /root/mongo-admin.pass   # from a script
```

That adds MongoDB's official apt repository with a signing key that ships **inside the
ratline binary** (nothing about the root of trust is downloaded), installs `mongodb-org`,
creates a root-role admin, replaces `/etc/mongod.conf` with a managed one that **enables
authorization** and binds **localhost only**, restarts, and proves the running server
enforces authorization and accepts the credential before storing anything. A failure at any
step is unwound. A MongoDB already on the host that ratline did not set up is refused, not
adopted; enable authorization yourself and attach it as below. `--mongodb-version` picks a
release series.

### A MongoDB that exists elsewhere: attach it

```bash
ratline db connect                             # prompts for the admin connection string
ratline db connect --stdin < /root/mongodb.uri
ratline db connect --from-file /root/mongodb.uri
ratline db connect --force                     # replace a stored string
```

The string is the root credential for every database on that server, which is why it lives
`0600` at `/etc/ratline/db/mongodb.uri` and not in `config.yaml`, and why it is never a flag.
Do not pipe it through `printf`: a `%` in the password is a format verb. `db connect`
proves the credentials work before committing anything.

### MySQL and Redis

```bash
ratline db install --engine redis          # put Redis on this host, secured, localhost only
ratline db install --engine mysql          # the same for MySQL; --stdin carries the admin password
ratline db --engine mysql connect          # or attach a MySQL server that already exists
ratline db --engine mysql create shop --owner acme --attach shop.example.com --env-key DATABASE_URL
ratline db --engine redis create cache --owner acme --attach app.example.com --env-key REDIS_URL
ratline db ping --engine redis
```

Same verbs, same state, scoped by engine. `--engine` is accepted right after `db` or after
the verb; the default env key comes from MongoDB's configuration, so name one with
`--env-key` for the other engines. PostgreSQL is not an engine ratline provides: an app that
needs it gets an external URL, set as a secret.

## Creating a database for a site

```bash
ratline db create acmeshop --owner acme --attach app.example.com
ratline db create acmeshop --owner acme --attach app.example.com --env-key DATABASE_URL
ratline db create analytics --owner acme --role dbOwner
ratline db create legacy --owner acme --no-user        # adopt an existing schema
ratline db roles                                       # what each role allows
```

One command: the database, a user named `<database>_app` (or `--user`) whose only role is on
that database, and, with `--attach`, the connection string written into the site's `.env`
under `MONGODB_URI` (or `--env-key`). The application reads its environment at startup, so a
site that is already running needs `ratline site restart <domain>` afterwards (the command
prints that line); a site that has not been deployed yet picks it up on its first start.
Without `--attach` the string is printed **once**; if that is the path, the person reading
it is responsible for where it goes next, and it should not be pasted into a chat.

`--owner` is the tenant. `ratline new … --with-db` does the same thing as part of building
a whole stack.

## Looking

```bash
ratline db list                    # what ratline provisioned
ratline db list --live             # everything on the server, provisioned or not
ratline db show acmeshop --live    # users, collections, size
ratline db user list --database acmeshop
```

No command prints a password. `--json` never contains one either.

## Users

```bash
ratline db user add reporter --database acmeshop --role read
ratline db user grant reporter --role readWrite
ratline db user password acmeshop_app --attach app.example.com     # rotate, and rewrite the site's .env
ratline db user password acmeshop_app --all-sites                  # every site that uses this user
ratline db user delete reporter
```

Rotation through `db user password` is the safe path: a new password is generated, set on
the server and written into every attached `.env` without the value passing through you
(it is printed once only when no site is attached). It does not restart the applications;
run `ratline site restart <domain>` for each site it lists, because an application reads
its environment at startup.

## Backups of data

```bash
ratline db dump acmeshop --out /var/backups/ratline
ratline db restore /var/backups/ratline/acmeshop-20260917T031500Z.archive.gz --into acmeshop_restored
ratline db restore <archive> --drop                # replace the live database: confirm with the human first
```

`ratline backup <user|domain>` archives files and the `.env`, **not** the database; the two
are separate because they are restored separately. A dump holds every document, so treat the
file as the secret it is.

## Who can reach the port

After `db install`, `mongod` listens on `127.0.0.1` only. When another machine genuinely
needs in:

```bash
ratline db access allow 203.0.113.19 --note 'dana laptop'
ratline db access allow 10.8.0.0/24 --note vpn
ratline db access list
ratline db access revoke 203.0.113.19
```

Reachability is two facts that must agree, what `mongod` binds and what the firewall
admits, and `db access` owns both together: the first allowed address adds the ufw rule
**before** widening the bind, and revoking the last one puts `mongod` back on localhost.
Both restart `mongod` and verify against the listening socket, not the config file.

Three refusals, each with a different fix, and none of them is yours to work around:

- **ufw not installed**: without a firewall, an allow-list is fiction.
- **ufw inactive**: ratline never runs `ufw enable`. The operator allows SSH first, then
  enables it, from a session they can afford to lose.
- **default incoming policy is allow**: an allow-list on an allow-by-default firewall
  restricts nobody.

For a database ratline did not install (Atlas, another host) the access list lives with that
server, and these commands refuse. Prefer the narrowest address that works; for a laptop
that only needs occasional admin access, an SSH tunnel is narrower still:

```bash
ssh -N -L 27099:127.0.0.1:27017 "$RATLINE_HOST"     # then connect to 127.0.0.1:27099 locally
```

## Composing a URL by hand

Only for a database outside ratline. Percent-encode the password: `@` → `%40`, `&` → `%26`,
`:` → `%3A`, `/` → `%2F`, `?` → `%3F`, `#` → `%23`, `%` → `%25`. Left raw, an `@`
truncates the host and produces a misleading error. Then set it with the `ratline-secrets`
skill, on stdin.

## Removing

```bash
ratline db drop acmeshop --keep-database     # remove the user and ratline's record, keep the data
ratline db drop acmeshop                     # the data too; `--force` skips the prompt, never without a human's yes
```

`drop` is one of the operations the web panel reserves for a super admin. Treat it the same
way: dump first, confirm the name back, then drop.
