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

systemd reads it as `EnvironmentFile=` **as root, before privileges are dropped**,
which is how a `0600` file owned by the tenant can hold secrets the application still
receives.

## Picking up a change

```bash
sudo ratline site reload app.example.com
```

Under PM2 the reload passes `--update-env`, so replacement workers get the new
values. Without that a reload would keep the old environment, which makes `env set`
followed by `reload` a silent no-op — so it is not optional.

For a site running `--daemon direct`, a restart is required, and `reload` says so.
