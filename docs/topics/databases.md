# Databases

> MongoDB databases and users, one per tenant, with roles that cannot reach past their
> own database.

## ratline does not install MongoDB

It manages what lives inside a server you point it at. A database server is a stateful
thing with backups and a replication topology, and a tool that silently `apt-get`s one has
made a decision belonging to whoever owns the data — the same reasoning that has ratline
configure nginx and drive certbot without installing either.

A local `mongod` and a managed cluster work identically. The only difference is the
connection string.

## Setting it up

One command, and it asks:

    ratline db connect
    MongoDB admin connection string (not echoed): ▏

That creates the directory at `0700`, writes the connection string at `0600` root-owned,
turns on `features.db_provisioning`, and proves the credentials work before committing any
of it. If the server cannot be reached, or rejects them, nothing is left behind — a stored
string that does not work is indistinguishable from a server that is down.

It is **never a flag value**. Anything in argv is world-readable through `/proc`, so a
password passed as an argument is visible to every account on the box for as long as the
command runs — and it lands in your shell history, which outlives the password.

Do not pipe it through `printf` either. `printf` reads a `%` in the password as a format
verb and truncates the string, usually leaving something with no host in it at all; `!`
can go the same way under history expansion. The prompt has nothing in between.

Where there is no terminal — a provisioning script, a CI job — the two non-interactive
ways in are `--stdin` and `--from-file`:

    ratline db connect --stdin < /root/mongodb.uri
    ratline db connect --from-file /root/mongodb.uri

It lives in a file rather than in `config.yaml` because it is the root password for every
database on the server, and `config.yaml` is a file operators paste into support tickets.

## The file

`paths.mongo_uri_file`, `/etc/ratline/db/mongodb.uri` by default. `db connect` writes it
for you; this is what it looks like afterwards, and what to write by hand for
`--from-file`:

    # managed-by: ratline
    # MongoDB admin connection string. This is the root credential for every
    # database on that server, which is why this file is 0600 and root-owned
    # and why it is not in config.yaml.
    #
    # Replace it with:  ratline db connect --force
    # Check it with:    ratline db ping

    mongodb://ratline_admin:CHANGE_ME@127.0.0.1:27017/?authSource=admin

Blank lines and `#` comments are ignored, so the note explaining what the file is costs
nothing — and the next person to find an unexplained credential in `/etc` will thank you.
Two connection strings in one file is an error rather than a guess.

The line itself depends on where the server is:

    # the same machine
    mongodb://ratline_admin:CHANGE_ME@127.0.0.1:27017/?authSource=admin

    # another machine you run — mongod binds 127.0.0.1 by default, so this
    # needs bindIp opened and a firewall that admits only this server
    mongodb://ratline_admin:CHANGE_ME@db.internal:27017/?tls=true&authSource=admin

    # a managed cluster; the SRV record carries the ports, so do not name one
    mongodb+srv://ratline_admin:CHANGE_ME@cluster0.ab12c.mongodb.net/?retryWrites=true

Whatever options are on that line — `tls`, `replicaSet`, `retryWrites` — are carried into
every tenant's connection string too, because they are properties of the deployment rather
than of the credential.

The file must be `0600` and owned by root. ratline refuses to read it at any mode another
account could see, rather than warning and continuing: at `0644` every tenant on the box
can read the admin password for every database on the server.

## Checking it

    ratline db ping

`ping` reports the version, the topology, and whether the server enforces authentication.
That last one is worth reading: a `mongod` started without `--auth` answers every command
from anyone who can reach the port, so the users ratline creates would restrict nothing.

`ratline db disable` turns provisioning back off without touching anything on the MongoDB
server — the databases, their users and every site holding a credential keep working.
`--forget` also removes the stored string, which is what handing a server over looks like.

## A database per tenant

    ratline db create shop --owner acme

That creates the database, creates a user whose only role is on that database, and prints
the connection string once.

`--owner` is required. It names the tenant, which is how a later `user delete --purge`
knows what to revoke — without it a database outlives the account it was for.

MongoDB has no `createDatabase`: a database exists once something is written into it. So an
initial collection is created too, because otherwise a new database is invisible to
`db list` until the application writes, which reads as the create having silently failed.

## The password is shown once

MongoDB stores a hash and will not give it back. ratline could not display a password later
even if it wanted to, which is the right shape rather than a limitation: there is no
credential store here to be stolen, and a lost password is rotated rather than recovered.

    ratline db user password shop_app

## Handing it to a site

    ratline db create shop --owner acme --attach shop.example.com

`--attach` writes the connection string into the site's `.env` — mode 0600, owned by the
tenant, never inside a document root — instead of printing it. That keeps the password out
of your shell history and out of the terminal scrollback, both of which outlive every
rotation.

The application picks it up on its next start, because an environment variable is read at
startup:

    ratline site restart shop.example.com

When rotating, `--all-sites` updates every site recorded as holding that credential:

    ratline db user password shop_app --all-sites

That is the difference between a rotation and an outage. The old password stops working the
moment the new one is set, so a site still holding it is broken until its `.env` catches up.

## Roles are scoped to one database

    ratline db roles

| role | allows |
|---|---|
| `read` | read every collection in the database |
| `readWrite` | read and write every collection — the default |
| `dbAdmin` | manage indexes and statistics, but not read the data |
| `dbOwner` | readWrite plus dbAdmin plus userAdmin, for this database only |

The cluster-wide roles — `root`, `readWriteAnyDatabase`, `userAdminAnyDatabase` — are
deliberately absent. Granting one to a tenant's application hands it every other tenant's
data, which is the thing ratline exists to prevent, and it would be one flag away if the
list were open. If you genuinely need one, use `mongosh` directly; ratline will not be the
thing that made it easy.

`grant` replaces rather than adds:

    ratline db user grant reports --role read

That is deliberate too. `grant` means "this user should have exactly this access", and
accumulating roles quietly is how a read-only user ends up able to write.

## More than one user per database

    ratline db user add reports --database shop --role read

A second, narrower credential is how you give something less access than the application
has: a reporting job with `read`, a migration tool with `dbAdmin`, each revocable on its
own without touching the others.

## What state records, and what it does not

`db list` reads ratline's index. `db list --live` asks the server and marks anything it does
not recognise:

    ratline db list --live

The difference is the useful part. A database on the server with no row was created outside
ratline, and nothing will revoke its users when the tenant is deleted.

No password is ever stored. The index holds names, owners, roles and which site holds which
credential — enough to revoke things, and nothing worth stealing.

## Removing things

    ratline db user delete reports        # a credential; the data is untouched
    ratline db drop shop                  # the database and every user of it
    ratline db drop shop --keep-database   # the users and the record; the data stays

`drop` needs the database's name typed back, and tells you how many documents are about to
go. `--keep-database` is what handing a database over to someone else's tooling looks like.

Users are removed before the database, deliberately: dropping a database does not remove
its users, and a user left behind still authenticates and still holds a role on a database
that springs back into existence the moment anything writes through it.

## How this talks to MongoDB

Through `mongosh`, running one static JavaScript file embedded in the binary. Every value —
the connection string, the database, the username, the password, the role — arrives through
the environment, so no operator input is ever interpolated into JavaScript.

That matters more than it sounds. The alternative is building an `--eval` string, and then a
username containing a quote can close it and run whatever follows, as root, against a server
holding every tenant's data. There is no escaping to get right because there is no string to
escape into.

The admin connection string never appears in `argv` either. `mongosh` normally takes it as
its first argument, and `/proc/PID/cmdline` is world-readable — so any local account could
read the admin password for every database on the box. `--nodb` starts it unconnected and
the script connects using the environment, which only the process owner can read.

## When something is wrong

    ratline doctor

It reports an unreachable server, an admin file at the wrong mode, a server that is not
enforcing authentication, a database recorded here but missing there, and — the one that
matters most — a user a site still holds credentials for but which the server no longer has.
That last case is an application failing to authenticate right now.
