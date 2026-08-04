# Deploys

> What `site deploy` does, in what order, and what happens when a step fails.

    ratline site deploy app.example.com

    1. fetch and check out the branch
    2. install dependencies as the tenant, never as root
    3. run the build
    4. python: migrate and collectstatic, if --manage-py is set
    5. static: publish the build output to the document root
    6. reload or restart the service
    7. health-check through the socket with a real HTTP request

Step 7 is the one that decides whether the deploy succeeded. A unit that is
"active" proves the process started, not that the application works; only a request
that comes back proves that.

## Failure

A failed step stops the deploy and unwinds what it did. The previous version keeps
serving — nothing is published, and a static site's document root is only replaced
after the build succeeds. The deploy is recorded either way, so `ratline site show`
shows the last attempt and not just the last success.

## Reload versus restart

`ratline site reload` is graceful where graceful is possible:

* **node with PM2** — a true zero-downtime reload.
* **node without PM2** — refused, with the two ways forward named. Claiming a
  graceful reload while dropping requests would be worse than refusing.
* **python** — Gunicorn's `SIGHUP` reload, which cycles workers.
* **static** — an nginx reload.

## Deploy keys

    ratline site deploy-key app.example.com

Generates a key for the site, prints the public half to add to your repository host,
and keeps the private half `0600` inside the site directory. It is site-scoped, so a
compromised CI credential reaches one repository and one site.

## Secrets

    ratline site env set app.example.com DATABASE_URL --stdin

`--stdin` because a value in argv is visible in `ps` output to every user on the
machine for as long as the command runs. Values are redacted in logs, in errors and
in `env list` unless `--reveal` is passed. `.env` is `0600` and lives outside the
document root, so nginx has no path by which it could serve it.

See also: `ratline explain node`, `ratline explain diagnose`.
