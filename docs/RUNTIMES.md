# Runtimes

Three runtimes, and the difference between them is what nginx does with a request.

| Runtime | nginx | Process | Reload |
|---|---|---|---|
| `static` | Serves files from disk | none | not applicable |
| `node` | Proxies to a socket or port | `ratline-<slug>.service` | restart only |
| `python` | Proxies to a socket | Gunicorn in a per-site venv | graceful (SIGHUP) |

## static

```bash
ratline site add example.com --user acme --runtime static --spa
```

Generates a vhost with `root /home/acme/example.com/public`, and no proxy block at
all. Assets under `/assets/`, `/static/` and `/_next/static/` get
`Cache-Control: public, immutable, max-age=31536000`; the index document gets
`no-cache, must-revalidate`, because a cached `index.html` means a deploy is
invisible until the browser gives up on it.

`--spa` changes `try_files $uri $uri/ =404` to `try_files $uri $uri/ /index.html`,
so a refresh on a deep client-side route serves the app instead of a 404.

With a build:

```bash
ratline site add example.com --user acme --runtime static \
    --repo git@github.com:acme/site.git \
    --build-command "npm run build" --build-output dist
```

The build runs as the tenant, and the output directory is published by symlinking
the document root at it — atomic, and the previous build stays on disk. ratline
refuses to symlink over a document root that already holds files.

## python

```bash
ratline site add api.example.com --user acme --runtime python \
    --app-module app.main:app --workers 3
```

What it does:

1. Creates `/home/acme/api.example.com/venv` with the managed interpreter, **as the
   tenant**, so pip never runs as root.
2. Installs Gunicorn, plus `uvicorn[standard]` if the application is ASGI.
3. Installs the project's own dependencies — `requirements.txt` if present,
   otherwise `pyproject.toml` or `setup.py` via `pip install .`.
4. Writes a unit whose `ExecStart` is the venv's Gunicorn by absolute path.
5. Starts it and waits for a real HTTP response through the socket.

The generated `ExecStart`:

```
/home/acme/api.example.com/venv/bin/gunicorn \
    --workers 3 --bind unix:/run/ratline/acme-api_example_com/app.sock \
    --access-logfile …/logs/access.log --error-logfile …/logs/app.log \
    --capture-output --max-requests 2000 --max-requests-jitter 200 \
    --graceful-timeout 30 --timeout 60 --umask 0117 \
    --worker-class uvicorn.workers.UvicornWorker app.main:app
```

Two details worth knowing:

**`--umask 0117`** makes the socket `0660`. `connect(2)` on a Unix socket needs
*write* permission on the socket inode, and nginx is only a member of the tenant's
group — at the `0027` umask used everywhere else the socket lands at `0640`, nginx
gets EACCES, and every request is a 502 with nothing useful in any log. This is the
single most confusing failure mode in this whole design, and it is why socket-based
units also set `UMask=0007`.

**`--max-requests`** recycles workers to bound the effect of a slow leak, with
jitter so they do not all restart at once.

ASGI is auto-detected by reading the module for `FastAPI(`, `Starlette(` and the
like, and ratline says what it decided. `--asgi` and `--wsgi` override it.

Django:

```bash
ratline site add app.example.com --user acme --runtime python \
    --app-module myproject.wsgi:application --manage-py manage.py \
    --static-url /static --static-dir staticfiles
ratline site deploy app.example.com --pull --install --migrate --collectstatic --restart
```

`--static-url` and `--static-dir` make nginx serve those files from disk, so the
application never handles an asset request.

### Zero-downtime reload

```bash
ratline site scale api.example.com --workers 6
ratline site reload api.example.com
```

Gunicorn's master keeps the listening socket open and replaces workers one at a
time on SIGHUP, so no connection is refused. `site scale` uses a reload rather than
a restart when only the worker count changed.

`--server uvicorn` cannot do this — uvicorn has no graceful reload — and ratline
refuses the reload rather than dropping requests while claiming not to.

## node

```bash
ratline runtime install node 22
ratline site add app.example.com --user acme --runtime node --entry server.js --node 22
```

`ExecStart` is `/opt/ratline/runtimes/node/22/bin/node <entry>`, an absolute path
into a managed installation. nvm, `.bashrc` and login shells are never involved:
systemd does not read them, so a unit that depended on them would work when you
tested it by hand and fail on the next boot.

Your server has to listen on what ratline tells it. For a socket site the
environment carries `PORT`, `RATLINE_SOCKET` and `SOCKET_PATH`, all set to the
socket path:

```js
const path = process.env.RATLINE_SOCKET ?? process.env.PORT;
app.listen(path, () => console.log(`listening on ${path}`));
```

Express, Fastify and Next.js standalone all accept a path where they accept a port.
If yours does not, use `--listen port` and read `PORT` as a number.

The socket mode problem applies here too. Node creates the socket with the process
umask, so the unit sets `UMask=0007` and adds an `ExecStartPost` that waits for the
socket and chmods it to `0660` as root. If you see a 502 with nothing in the
application log, check the socket's mode first — `ratline doctor` reports it
explicitly.

`MemoryDenyWriteExecute` is relaxed by default for Node, because V8's JIT needs
writable-executable memory. The generated unit records this rather than hiding it.

Next.js standalone:

```bash
ratline site add app.example.com --user acme --runtime node \
    --entry .next/standalone/server.js --node 22 \
    --install-command "npm ci" --build-command "npm run build"
```

`--package-manager` is detected from the lockfile, and the install prefers the
reproducible form (`npm ci`, `pnpm install --frozen-lockfile`) so a deploy fails
rather than silently updating the lockfile.

### Multiple instances

```bash
ratline site scale app.example.com --instances 3
```

Generates an nginx `upstream` with `least_conn` across three sockets. Since a Node
site cannot reload gracefully, this is how you get a restart without dropped
requests: nginx shifts traffic to the healthy instances.

## Debugging a 502

In the order that finds it fastest.

**1. Ask ratline.**

```bash
ratline doctor
ratline site show app.example.com
```

`doctor` checks the socket by connecting to it, not by looking for the file — a
socket left behind by a crashed process still exists. It reports a wrong socket mode
explicitly, which is the cause you would otherwise spend an hour on.

**2. Is the service running?**

```bash
ratline site status app.example.com
ratline site logs app.example.com
```

A restart count above zero means a crash loop. The last log line is almost always
the answer.

**3. Is the socket answering?**

```bash
sudo -u acme curl --unix-socket /run/ratline/acme-app_example_com/app.sock http://localhost/
```

If that works and nginx still 502s, it is a permission problem:

```bash
ls -l /run/ratline/acme-app_example_com/app.sock   # want srw-rw---- acme www-data
id www-data                                        # want the tenant's group listed
```

`ratline site restart` fixes the mode. If www-data is not in the group,
`ratline reconcile --fix` re-adds it.

**4. What does nginx say?**

```bash
ratline site logs app.example.com --error
```

`connect() to unix:/… failed (13: Permission denied)` is the socket mode.
`(111: Connection refused)` means nothing is listening — the application is not up,
or it bound to something other than what ratline told it.

**5. Did the sandbox break it?**

`ModuleNotFoundError`, `Read-only file system` or `Operation not permitted` on start
usually means a hardening directive. ratline recognises the common cases and names
the directive in the error. To confirm:

```bash
ratline site restart app.example.com --relax ProtectSystem
```

If that fixes it, the application writes outside its site directory. Prefer moving
those writes into `tmp/` over leaving the directive off.

## Changing versions

```bash
ratline runtime list
ratline runtime install node 22
ratline site runtime app.example.com --node 22
```

That rebuilds and restarts, and waits for health before reporting success.
