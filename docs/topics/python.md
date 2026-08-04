# Python sites

> Gunicorn, a per-site virtualenv, and the WSGI/ASGI choice.

A python site runs Gunicorn under its own systemd unit as the tenant, behind a Unix
socket, from a virtualenv at `<site>/venv`.

## The virtualenv is per site, not per user

Two sites owned by the same tenant get two virtualenvs. Sharing one would mean a
dependency bump for one site silently changing the other, which is exactly the
failure that is hardest to diagnose later.

`ratline site runtime <domain> --python 3.12` rebuilds it, because a virtualenv is
built against one interpreter and stops working when that interpreter is replaced.

## WSGI or ASGI

    --app-module app.main:app       the import path of the callable
    --wsgi                          Django, Flask — sync workers
    --asgi                          FastAPI, Starlette — uvicorn workers

ASGI is detected from the framework when neither flag is given. Getting it wrong is
not subtle: a FastAPI application under sync workers fails to start, and the error
in the log is about the callable not being callable.

`--workers` defaults to `(2 × cores) + 1`, capped by `worker_cap` in configuration.

## Django

    ratline site add app.example.com --user acme --runtime python \
      --app-module myproject.wsgi:application \
      --manage-py manage.py \
      --static-url /static/ --static-dir staticfiles

`--manage-py` makes `ratline site deploy` run `migrate` and `collectstatic` as part
of the deploy. `--static-url` and `--static-dir` make nginx serve the collected
static files directly rather than through Gunicorn.

## The socket umask

Gunicorn is started with `--umask 0117` on a socket site. This is not decoration:
`connect(2)` needs write permission on the socket inode, and Gunicorn sets its own
umask regardless of the unit's. `ratline explain sockets` has the full story.

See also: `ratline explain sockets`, `ratline explain deploys`.
