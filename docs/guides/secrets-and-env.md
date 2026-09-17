# Secrets and environment

```bash
sudo ratline site env set app.example.com DATABASE_URL --stdin
sudo ratline site env list app.example.com
sudo ratline site env unset app.example.com OLD_KEY
sudo ratline site env import app.example.com --file .env.production
```

## Why `--stdin`

A value in argv is visible in `ps` output to every user on the machine for as long as
the command runs. `--stdin` reads it from a pipe or a prompt instead:

```bash
echo -n "$SECRET" | sudo ratline site env set app.example.com DATABASE_URL --stdin
```

A value passed positionally still works, with a warning, because scripts exist — but
the warning is there because the exposure is real.

## Redaction

Values are redacted in logs, in error messages and in `env list`. `--reveal` prints
them, and only then.

```bash
sudo ratline site env list app.example.com --reveal
```

## Where it lives

`<site>/.env`, mode `0600`, owned by the tenant, outside every document root — so
nginx has no path by which it could serve it.

The unit carries **no** `EnvironmentFile=`. That directive has systemd read the file
**as root, before privileges are dropped** — and `.env` sits in a directory the tenant
owns, so a tenant who swapped it for a symlink and crash-looped their own service would
have PID 1 load whatever the link pointed at into their environment. A `0600`,
tenant-owned file must never be opened by PID 1.

Instead the unit's `ExecStart` runs

```
ratline-shell exec --env-file <site>/.env -- <program> [args]
```

By then `User=` has taken effect, so the file is opened by a process that is already the
tenant. The wrapper merges its values over the unit's `Environment=` lines — the same
order systemd applies — and execs the program in place, so nothing of it remains in the
running service. A `0600` file owned by the tenant is read by the tenant, and the
application still receives every value.

A missing `.env` is not an error: a site with no secrets yet must still start. `doctor`
reports a unit that still carries `EnvironmentFile=`, and `ratline reconcile --fix`
re-renders it.

## Picking up a change

```bash
sudo ratline site reload app.example.com
```

Under PM2 the reload passes `--update-env`, so replacement workers get the new
values. Without that a reload would keep the old environment, which makes `env set`
followed by `reload` a silent no-op — so it is not optional.

For a site running `--daemon direct`, a restart is required, and `reload` says so.
