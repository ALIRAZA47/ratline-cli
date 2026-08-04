# Deploying

```bash
sudo ratline site deploy app.example.com
```

1. fetch and check out the branch
2. install dependencies **as the tenant**
3. run the build
3. python: `migrate` and `collectstatic`, if `--manage-py` is set
4. static: publish the build output to the document root
5. reload or restart the service
6. health-check through the socket with a real HTTP request

Step 6 decides whether the deploy succeeded. An "active" unit proves the process
started, not that the application works.

## When a step fails

The deploy stops and unwinds what it did. The previous version keeps serving:
nothing is published, and a static site's document root is only replaced after the
build succeeds. The attempt is recorded either way, so `site show` reflects the last
attempt rather than the last success.

## Reload or restart

```bash
sudo ratline site reload app.example.com     # graceful where possible
sudo ratline site restart app.example.com    # always works, drops in-flight requests
```

| Runtime | `reload` |
|---|---|
| node with PM2 | a true zero-downtime reload |
| node with `--daemon direct` | refused, with both ways forward named |
| python | Gunicorn `SIGHUP`; workers cycle, the socket stays open |
| static | an nginx reload |

## From CI

```bash
sudo ratline site deploy app.example.com --json
```

One JSON object on stdout, logs on stderr, and an exit code from the contract in
[reference/exit-codes.md](../reference/exit-codes.md). A deploy key for the site
authenticates to your repository host:

```bash
sudo ratline site deploy-key app.example.com
```

## Git

```bash
sudo ratline site add app.example.com --user acme --runtime node \
    --repo git@github.com:acme/app.git --branch main --entry server.js
```

The clone runs as the tenant, into `<site>/app`. `site deploy` fetches and checks out
that branch; the recorded SHA appears in `site show`.
