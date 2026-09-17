---
name: ratline-diagnose
description: Find out why something on a ratline server is broken — a site returning 502, 503, 504, 404 or 413, a service that will not start, a deploy that failed or was reverted, a certificate that stopped renewing, a key that cannot log in, nginx or sshd complaining — using `troubleshoot`, which walks the dependency chain read-only and names the first fault, then logs, then one fix, then re-check. Use for "my site is down", "getting a 502", "the deploy failed", "cert expired", "can't ssh in", any `ratline doctor` finding, or whenever a ratline-managed site misbehaves, before anyone restarts anything. Use it just as much when the person has ALREADY run `ratline troubleshoot` or captured logs and pasted or saved the output and asks what is actually wrong: the skill says how to read that output, why its "Try" line is the fix rather than a guess, and what the socket, PM2, 502 and certificate findings mean, so the answer does not contradict the tool's own diagnosis with plausible-sounding reasoning.
---

# Diagnosing a ratline server

Two commands, and the difference between them is the whole method:

```bash
ratline doctor            # what is wrong, across the whole server
ratline troubleshoot X    # why X specifically is broken
```

`doctor` sweeps every check and lists what it finds; on a server with five findings it
leaves you to work out which one is the cause and which four are its consequences.
`troubleshoot` takes one subject and walks its preconditions in the order they depend on
each other, stopping at the first failure. Because the order is a dependency order, **the
first failure is the cause**, and the steps after it are reported as not checked rather
than as more problems. Both are read-only and take no lock, so they are safe against a
site that is currently on fire.

Start there, every time, before restarting anything. A restart that "fixes" a 502 hides the
cause until the next time.

Rules shared with every skill in this plugin: look flags up in `ratline schema`; read the
`--json` envelope and branch on the exit code; never hand-edit an nginx file or a unit on
a ratline server. Run commands as root on the server, or `ssh -T "$RATLINE_HOST" sudo
ratline …` from a laptop. If the plugin's MCP server is connected, `ratline_site_troubleshoot`,
`ratline_site_logs`, `ratline_doctor` and `ratline_site_show` are the same reads without
a shell; prefer them.

## The walk

```bash
ratline troubleshoot                      # the host itself
ratline troubleshoot app.example.com      # one site's request path
ratline troubleshoot acme                 # a tenant: account, home, keys, sites
ratline troubleshoot SHA256:AbC…          # can this key log in, and to what
ratline troubleshoot nginx
ratline troubleshoot ssh                  # including the lockout guard
ratline troubleshoot X --all              # show the passing steps too
ratline troubleshoot X --json             # .data.likely_cause, .data.try, .data.background
```

The subject is worked out from the argument; `--kind` settles a name that is both a tenant
and a certificate. `ratline site troubleshoot <domain>` and `ratline doctor <subject>` are
the same walk under other spellings.

Read the last three lines: **Likely cause**, **Try**, **Background**. "Try" is a ratline
command. "Background" is a `ratline explain` topic; read it before acting if the cause is
unfamiliar, because the fix that is obvious is often the one that made things worse.

## Then the logs

```bash
ratline site logs app.example.com --lines 100        # the application's own output
ratline site logs app.example.com --journal          # systemd's view: did the unit start at all
ratline site logs app.example.com --error            # nginx's error log for the vhost
ratline site logs app.example.com --access --follow  # live requests
ratline site status app.example.com                  # unit state, PM2 restart count
ratline site show app.example.com                    # runtime, socket, cert, last deploy and its outcome
```

On a PM2 site the application log is `logs/app.log`, so `--journal` holds only PM2's own
messages; on a Python site the import traceback is in the application log, not the journal.
An **empty** application log is itself a finding: no request ever reached the app.

## The catalogue of causes

**502 Bad Gateway**, in likelihood order. `troubleshoot` checks these in this order and
names the one that failed:

1. The service is not running. `site status`, then `site logs --journal`.
2. Socket permissions. `connect(2)` needs write permission on the socket inode; at `0640`
   nginx gets `EACCES` and the application log stays empty. `ratline explain sockets`; the
   fix is `site restart`, and if it recurs something outside ratline changed `/run/ratline`.
3. The application crashed after starting. `site status` shows PM2's restart count;
   systemd's own counter stays at zero on a PM2 site.
4. It is listening somewhere else. A framework that parses `PORT` as a number and got a
   socket path, or hardcodes `3000`, starts cleanly and answers nothing. The fix is
   `--listen port` on the site or a change to the application, not a restart.

**503**: the site or its owner is disabled (`site enable`, `user enable`), or the
health-check timer has recorded consecutive failures. `site show` says which.

**504**: the app answers too slowly. Workers, memory ceiling (`site scale`), or the
application itself. `site logs --error` shows the upstream timeout.

**404 on a path that exists**: a static SPA without `--spa` (client-side routes 404 on
refresh). `site show` reports the flag. Anything ratline does not model belongs in
`/etc/nginx/ratline/custom/<domain>.conf`, which the generated vhost includes and never
regenerates; after editing it, `ratline doctor` and `ratline site reload <domain>` so the
change is verified with `nginx -t` rather than trusted.

**413**: the upload ceiling, 20M by default. `ratline site scale <domain>
--client-max-body-size 100M`.

**Certificate**: `cert list --expiring 21`, `cert show <domain>`, `cert test-renewal`
(dry-runs every renewal to find breakage before it matters). Renewal needs port 80 from
outside and the name resolving here; a proxy in front (Cloudflare's orange cloud) breaks
HTTP-01, see `ratline explain tls`. Renew one certificate with `cert renew <domain>`;
exit 8 or 9 means stop and do not retry, the hint says why and for how long.

**SSH**: `troubleshoot ssh` and `troubleshoot <fingerprint>`. `key test <key>` says what a
key can reach. `key sync` re-renders every `authorized_keys` from state. One counter-
intuitive rule: `RevokedKeys` pointing at a file sshd cannot read refuses **every** key for
every account; `doctor` reports it and `key sync` repairs it. A full lockout is console-
only recovery; read `ratline explain ssh` and do not experiment from the last working
session.

**A job or worker**: `site cron logs <domain> <name>`, `site worker logs <domain> <name>`,
`site cron run <domain> <name>` to reproduce a scheduled failure now instead of at 3am.
`doctor` reports a job whose last run failed and a worker that keeps restarting.

**A deploy that was reverted (exit 7)**: it started and never answered. The previous
version is serving. `site logs` has the startup error; `site show` records the attempt.
Fix the application, then deploy again; there is nothing on the server to undo.

**Exit 6 from anything**: the operation failed and so did its rollback. Stop. Do not
retry. Run `doctor` and `reconcile` (without `--fix`) to see the state, and hand the
transcript to a human.

**Drift**: `ratline reconcile` reports where the system differs from state, for the case
where someone edited a vhost or a unit by hand. `reconcile --fix` re-renders; it is a
mutation, so `--dry-run` first and confirm with the human, because it will overwrite the
hand edit that may have been the point.

## One fix, then re-check

Make the single change the walk pointed at, then run `troubleshoot` again and `site health
<domain>`. If the first failure moved, you fixed a cause; if it did not, you did not, and
the next change should not be guessed at either. Two changes at once means you will not
know which one worked, and one of them was unnecessary on a production server.

## Reproducing without a browser

```bash
curl -sS -o /dev/null -w '%{http_code}\n' -H 'Host: app.example.com' http://127.0.0.1/
curl --unix-socket /run/ratline/<slug>/app.sock http://localhost/      # past nginx, straight to the app
```

The slug is in `site show`. Going through the socket splits the problem in half: if the
socket answers and nginx does not, it is the vhost; if the socket does not answer, it is
the application.

## Reporting

Say what the walk found, in its words, and what you changed. If you changed nothing, say
so and give the exact command a human should run. A diagnosis that ends with "restarted it
and it works now" and no cause is a diagnosis that will be needed again.
