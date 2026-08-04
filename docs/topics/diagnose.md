# When something is broken

> Which command answers which question, and the order to check things in.

Two commands, and the difference between them is the whole of it.

    ratline doctor            what is wrong, across the whole server
    ratline troubleshoot X    why X specifically is broken

`doctor` sweeps every check and prints what it finds. That is right for a cron job,
and on a server with five findings it leaves you to work out which one is the cause
and which four are its consequences.

`troubleshoot` takes one subject and walks its preconditions in the order they
depend on each other, stopping at the first failure. Because the order is a
dependency order, the first failure *is* the cause — and the steps it broke are
reported as "not checked" rather than as more problems.

    ratline troubleshoot                      the server itself
    ratline troubleshoot app.example.com      one site's request path
    ratline troubleshoot acme                 a tenant: account, home, keys, sites
    ratline troubleshoot SHA256:AbC...        can this key log in, and to what
    ratline troubleshoot nginx
    ratline troubleshoot ssh                  including the lockout guard

The subject is worked out from the argument, so there is nothing to remember. A
name that is both a tenant and a certificate is reported as ambiguous rather than
guessed at; `--kind` settles it. `ratline doctor <subject>` is the same walk, for
when that is the spelling that comes to mind.

Everything here is read-only and takes no lock, so it is safe against a site that is
currently on fire.

## What a walk looks like

    $ ratline troubleshoot api.example.com
    api.example.com  —  python, owned by acme

      FAIL  the application is listening where nginx expects  —  the socket is
            mode 0640; nginx needs 0660 to connect, so every request is a 502
      --    the application answers a request  —  not checked: listening has to
            pass first
      warn  a current certificate is attached  —  6 days left
      ok    5 checks passed

    Likely cause: the socket is mode 0640; nginx needs 0660 to connect, so every
                  request is a 502
    Try:          ratline site restart api.example.com
    Background:   ratline explain sockets

Passing steps are folded into a count, because on a broken subject the answer is
the last line and a dozen `ok` rows push it off the screen. `--all` shows them.

The certificate warning is a warning and not the cause. That distinction is
deliberate: a certificate expiring in six days is worth reading and is not why this
site is down.

## 502 Bad Gateway

`ratline troubleshoot <domain>` checks all of the following in order and names the
one that failed. What each means, if you want to understand what it found:

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

    ratline troubleshoot                 the host: clock, disk, tooling, state
    ratline status                       the whole server on one screen
    ratline site show <domain>           every setting for this site
    journalctl -u ratline-<slug> -n 50   the unit's own messages
    nginx -t                             the configuration nginx sees

The host walk is worth running before anything else when several things are wrong at
once: a skewed clock, a full disk or a missing binary explains a dozen unrelated
symptoms, and diagnosing any one of those symptoms first is wasted work.

## What each walk checks

    server   configuration, platform, clock, tooling, disk, state, audit log,
             stale lock, nginx, ssh, tenants, sites, certificates, drift
    site     enabled, owner, vhost, nginx config, directories, then either the
             document root or the unit → workers → socket → the application,
             then nginx end to end, the certificate, and DNS
    user     account, disabled, home mode, ownership, shell, authorized_keys,
             key sync, their sites, quota
    key      revoked, expired, scope target, installed, file permissions,
             revocation list, sshd reads it, what it can do, last used
    cert      files, key permissions, parses, validity, renewal, attached,
             and whether it is the certificate actually being served
    nginx     installed, config valid, running, snippets, ACME webroot, orphans
    ssh       installed, config valid, listening, pubkey auth, the drop-in,
             the revocation list, expiry enforcement, the lockout guard

See also: `ratline explain sockets`, `ratline explain node`.
