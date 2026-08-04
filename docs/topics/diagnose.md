# When a site is broken

> The order to check things in, and which command answers each question.

Start here. It checks everything ratline knows about and prints only what is wrong:

    ratline doctor

For one site, with the checks in dependency order and a verdict per step:

    ratline site troubleshoot app.example.com

## 502 Bad Gateway

nginx is running and your application is not reachable from it. In likelihood
order:

1. **The service is not running.** `ratline site status <domain>`. If it failed,
   `ratline site logs <domain>` — and on a PM2 site add `--journal` to see whether
   the unit itself failed to start.
2. **The socket permission.** This is the one that produces an empty application
   log, because no request ever arrives. `ratline explain sockets` has the detail;
   `ratline doctor` names it directly when it sees it.
3. **The application crashed after starting.** `ratline site status` shows PM2's
   restart count on a node site — systemd's own counter stays at zero there,
   because PM2 does the restarting.
4. **The application is listening somewhere else.** A framework that ignores `PORT`
   and binds `3000` will start cleanly and answer nothing.

## 404 on a path that exists

For a static SPA, `--spa` is missing: unmatched paths need to return the index
document for a client-side router to work. `ratline site show <domain>` shows
whether it is set.

For a proxied site, check the custom include at
`/etc/nginx/ratline/custom/<domain>.conf` — it is included by the generated vhost
and never regenerated, so a stale rule there survives everything else.

## 413 Request Entity Too Large

`client_max_body_size`, which defaults to 20M.

    ratline site scale app.example.com --client-max-body-size 100M

## The certificate is not being served

A certificate on disk that nginx never loaded looks fine in every check except a
handshake. `ratline cert show <domain>` reports what is actually served, because it
connects and looks.

## Configuration on disk that state does not know about

    ratline reconcile          # report the drift
    ratline reconcile --fix    # regenerate from state

This is the residue left by editing nginx or systemd by hand. `reconcile` reports
first and changes nothing until asked.

## Nothing above matched

    ratline status                       the whole server on one screen
    ratline site show <domain>           every setting for this site
    journalctl -u ratline-<slug> -n 50   the unit's own messages
    nginx -t                             the configuration nginx sees

See also: `ratline explain sockets`, `ratline explain node`.
