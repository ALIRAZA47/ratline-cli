---
name: ratline-secrets
description: Manage a ratline site's environment variables and secrets safely — set, rotate, import and remove values through `site env` over stdin so nothing reaches argv or shell history, tell build-time from runtime variables, rotate a database password, and know when a change needs a reload versus nothing. Use whenever an environment variable, API key, token, database URL, or a .env file has to reach (or leave) a site on a ratline server, when someone asks to "add an env var", "rotate the secret", "what env does the site have", or wants to see a value.
---

# Environment and secrets on a ratline site

Every site has one `.env`: `<site>/.env`, mode `0600`, owned by the tenant, outside every
document root so nginx has no path by which it could serve it. The service and every job
and worker of the site read it as the tenant. ratline manages it with `site env`, and the
reason to use `site env` rather than `echo >> .env` is not convenience: it restarts the
service where that is the right thing to do, it validates the key names, it redacts, and it
keeps root from ever opening a path a tenant could have swapped for a symlink.

Rules shared with every skill in this plugin: look flags up in `ratline schema`; read the
`--json` envelope and branch on the exit code; never edit a tenant's files as root by
hand. Run commands as root on the server, or `ssh -T "$RATLINE_HOST" sudo ratline …`
from a laptop.

## The one rule

**A value in argv is world-readable.** `/proc/PID/cmdline` can be read by every account on
the machine for as long as the command runs, and then the value sits in shell history,
which outlives the secret. So:

```bash
# not secret: say it outright
ratline site env set app.example.com LOG_LEVEL=info NODE_ENV=production

# secret, at a terminal: name it, and paste at the prompt (not echoed)
ratline site env set app.example.com DATABASE_URL

# secret, from an agent or a script: KEY=value lines on stdin
ratline site env set app.example.com --stdin < vars.env
ssh -T "$RATLINE_HOST" sudo ratline site env set app.example.com --stdin < vars.env

# a whole file, merged into what is there
ratline site env import app.example.com --file .env.production   # then restart; see below
```

Two traps when producing the stdin: `printf '%s'` is fine, but `printf "$value"` reads a
`%` in the secret as a format verb and truncates it silently; and a `!` can be eaten by
history expansion in an interactive bash. Write the lines to a file with an editor or a
heredoc with a quoted delimiter, feed the file, then delete it. Never leave `vars.env` in a
repository or a working tree.

`KEY=VALUE` positionally still works, with a warning, because scripts exist. Do not use it
for anything you would mind seeing in `ps`.

## Reading

```bash
ratline site env list app.example.com             # names, values masked
ratline site env get app.example.com DATABASE_URL # masked
ratline site env list app.example.com --reveal    # only when the human asked to see values
```

Values are redacted in logs, in errors, in `--json` and in `env list`. `--reveal` prints
them, and only then. Do not reveal to answer "is it set"; `list` answers that. If you must
reveal, do not paste the value back into the conversation; describe its shape.

## When does a change take effect

`env set` and `env unset` **restart the service** as part of the command, so a running site
picks the change up with nothing further. `env import` does **not**: it writes the values and
prints the `site restart` line for you to run, because importing a file is usually one step
of several and restarting between each would cycle the service repeatedly.

For a change you want to be graceful on a PM2 site, `site reload` afterwards passes
`--update-env` so replacement workers get the new values; without that a plain reload keeps
the old environment, which is why ratline does not offer a reload that skips it.

**Build-time variables are different.** `VITE_*`, `NEXT_PUBLIC_*`, `PUBLIC_*`,
`REACT_APP_*` and anything read through `import.meta.env` are baked into the bundle when
the build runs. Setting one changes nothing until the next `site deploy … --build`. This
is the source of "I changed the API URL and it still points at the old one".

## What not to set

- `PORT`, `HOST`: ratline allocates the socket or port and passes it in. A value in `.env`
  fights the allocation, and the site answers where nginx is not proxying.
- The database variable ratline wrote (`MONGODB_URI` or your `--env-key`): rotate it
  through `db`, below, not by pasting a new URL.
- Anything from a local `.env`: never rsync or import a development file onto a
  production site. It overwrites the credentials ratline created.

## Rotating

The order is: add the new, prove it, retire the old. Never the reverse.

**An API key or token**: set the new value (the service restarts), check `site health
<domain>` and the application log for an auth error, then revoke the old key at the
provider.

**A database password**, when ratline provisioned the user:

```bash
ratline db user password acmeshop_app --attach app.example.com
ratline db user password acmeshop_app --all-sites     # every site that uses this user
```

That generates the new password, sets it on the database server and rewrites the URL in
every attached `.env`; with a site attached it prints only the list of sites it updated, so
the value passes through nobody. It does **not** restart anything: an application reads its
environment at startup, so follow it with `ratline site restart <domain>` for each site it
named, then `site health`. Without `--attach` or `--all-sites` the new connection string is
printed once, and whoever reads it owns where it goes next. An external database's URL you
compose yourself must be percent-encoded (`@` → `%40`, `&` → `%26`,
`:` → `%3A`, `/` → `%2F`, `?` → `%3F`, `#` → `%23`, `%` → `%25`); a raw `@` truncates the
host and the error points nowhere near the cause.

**Removing**:

```bash
ratline site env unset app.example.com OLD_KEY
```

## Where secrets are allowed to exist

In the site's `.env`, set through `site env`. Nowhere else: not in the repository, not in a
CI variable shipped on each deploy, not in a `--build-command`, not in a hook's arguments,
not in a chat transcript, not in a backup you copy somewhere world-readable (`ratline
backup` archives include the `.env`, which is why the archive is `0600` in a `0700`
directory). If a value appears in a log or an error, treat it as leaked and rotate it.
