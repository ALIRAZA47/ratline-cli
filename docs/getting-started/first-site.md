# Your first site

## A tenant

```bash
sudo ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub
```

That creates the system user `acme`, its group, its home at `0750`, and its
`authorized_keys` — with the key restricted to what it needs and nothing more. The
tenant gets no sudo. nginx is added to the `acme` group, which is how it reaches the
tenant's public files without any home being world-readable.

## A static site

```bash
sudo ratline site add example.com --user acme --runtime static --spa
```

nginx now serves `/home/acme/example.com/public`. Content-hashed assets are cached
for a year, `index.html` is never cached, and `--spa` returns the index document for
unmatched paths so a refresh on a client-side route does not 404.

Deploy by putting files in that directory — rsync, SFTP or git.

## A Python site

```bash
sudo ratline site add api.example.com --user acme --runtime python \
    --app-module app.main:app --workers 3
```

A virtualenv, Gunicorn (with a Uvicorn worker if the project looks like FastAPI or
Starlette), and a systemd unit that runs it as `acme` behind a Unix socket. The
command does not report success until a real HTTP request has come back through that
socket.

## A Node site

```bash
sudo ratline site add app.example.com --user acme --runtime node \
    --entry server.js --node 22
```

PM2 in cluster mode under the site's own systemd unit, behind a Unix socket. PM2 is
the default because it is the only way `site reload` can deploy without dropping
requests; `--daemon direct` runs node straight under systemd instead.

## Look at what was created

```bash
sudo ratline site list
sudo ratline site show app.example.com
sudo ratline site status app.example.com
```

## Try it first

Every mutating command takes `--dry-run`, which prints the files, their contents and
every command that would run, and changes nothing:

```bash
sudo ratline site add test.example.com --user acme --runtime node \
    --entry server.js --dry-run
```

## If something is wrong

```bash
sudo ratline site troubleshoot app.example.com
```

It walks the request path in order — nginx configuration, directories, unit, socket,
the application, then nginx end to end — and stops at the first failure, which is
the cause. Everything after it is a consequence.

Next: [adding-tls.md](adding-tls.md).
