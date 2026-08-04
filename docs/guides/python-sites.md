# Python sites

Gunicorn under its own systemd unit as the tenant, behind a Unix socket, from a
virtualenv at `<site>/venv`.

```bash
sudo ratline runtime install python 3.12
sudo ratline site add api.example.com --user acme --runtime python \
    --app-module app.main:app --workers 3
```

## One virtualenv per site, not per tenant

Two sites owned by the same tenant get two virtualenvs. Sharing one would mean a
dependency bump for one site silently changing the other, which is the failure mode
that is hardest to diagnose months later.

## WSGI or ASGI

```
--app-module app.main:app     the import path of the callable
--wsgi                        Django, Flask — sync workers
--asgi                        FastAPI, Starlette — uvicorn workers
```

Detected from the project when neither flag is given. Getting it wrong is not
subtle: a FastAPI application under sync workers fails to start, and the log says the
callable is not callable.

`--workers` defaults to `(2 × cores) + 1`, capped by `worker_cap`.

## Django

```bash
sudo ratline site add app.example.com --user acme --runtime python \
    --app-module myproject.wsgi:application \
    --manage-py manage.py \
    --static-url /static/ --static-dir staticfiles
```

`--manage-py` makes `site deploy` run `migrate` and `collectstatic`. `--static-url`
and `--static-dir` make nginx serve the collected files directly rather than through
Gunicorn.

## Zero-downtime reload

```bash
sudo ratline site reload api.example.com
```

Gunicorn's `SIGHUP` cycles workers: the master keeps the socket open, new workers are
started, old ones finish their in-flight requests. In-flight requests are not dropped.

## The socket umask

Gunicorn is started with `--umask 0117` on a socket site. This is not decoration:
`connect(2)` needs *write* permission on the socket inode, and Gunicorn applies its own
umask regardless of the unit's. At `0640` nginx gets `EACCES` and every request is a
502 with an empty application log. `ratline explain sockets` has the whole story.

## requirements

`requirements.txt` by default; `--requirements` for another path. Installed **as the
tenant**, never as root.

## Changing versions

```bash
sudo ratline site runtime api.example.com --python 3.13
```

The virtualenv is rebuilt, because one is built against a specific interpreter and
stops working when that interpreter is replaced.
