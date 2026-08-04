# Node sites and PM2

> How a node site is supervised, why PM2 is the default, and when to turn it off.

A node site runs under its own systemd unit as the tenant, behind a Unix socket,
with PM2 in cluster mode between systemd and the application.

## Why PM2 is the default

Because it is the only way `ratline site reload` can mean anything on a node site.
`pm2 reload` starts a replacement worker, waits for it to come up, and only then
retires the old one — so a deploy drops no requests. There is no signal a plain
node process handles that way, which is why a site running without PM2 refuses to
reload rather than pretend, and tells you to restart instead.

## What the extra layer does not cost

systemd supervises PM2 and PM2 supervises the application, which is a real trade.
These are the properties that survive it:

* **The resource ceiling is still kernel-enforced.** systemd owns the cgroup and a
  cgroup contains every descendant, so `MemoryMax`, `CPUQuota` and `TasksMax` cover
  PM2 and all of its workers. PM2's own `max_memory_restart` is deliberately not
  set, because it would fire first and mask the limit that actually holds.
* **No shared daemon.** `PM2_HOME` is inside the site directory, so each site has
  its own daemon, socket and process list. Nothing outlives the site and nothing
  leaks between tenants.
* **Nothing is orphaned.** `ExecStop` runs `pm2 kill`, so stopping the unit stops
  the daemon too.
* **The configuration is data.** ratline generates `ecosystem.config.json`, not the
  more common `ecosystem.config.js`. A JavaScript config is code that PM2 evaluates
  as the tenant; a settings file has no business being executable.

## What genuinely changes

systemd's own restart counter stays at zero, because PM2 does the restarting. So
`ratline site status` and `ratline doctor` read PM2's counter instead and label it
as PM2's, and `doctor` additionally reports workers that died and did not come
back.

PM2 captures worker output into `logs/app.log`, so the journal holds only PM2's own
messages. `ratline site logs <domain>` reads the file; `--journal` is there for
questions about the unit itself, such as a failed start or an OOM kill.

## Turning it off

    ratline site add app.example.com --user acme --runtime node --daemon direct
    ratline site runtime app.example.com --daemon direct

`direct` runs node straight under systemd. One fewer moving part, systemd sees the
application itself, and `reload` becomes a restart. It is the better choice for a
single-process application that is never reloaded in place.

Server-wide, in `/etc/ratline/config.yaml`:

    runtimes:
      node_process_manager: direct

## Installing PM2

PM2 is installed per Node version, into the root-owned runtime prefix:

    ratline runtime install node 22 --with-pm2

Per version because a PM2 resolved against Node 18 is not the one a Node 22 site
should run, and because one shared install would mean `runtime default` silently
changed the supervisor binary under every existing site. Root-owned because a
supervisor binary a tenant could modify is a way to run arbitrary code from inside
a service unit.

## Cluster mode and non-JavaScript start commands

Cluster mode is node's own `cluster` module, so it can only fan out a JavaScript
entry point. A `--start-command` that runs `npm`, `pnpm` or a binary falls back to
fork mode with `interpreter: none`, and ratline says so when it generates the
configuration — in fork mode a reload is a restart.

Prefer `--entry` pointing at the file that calls `listen()`. A package manager
between systemd and your server also breaks signal delivery and restart counting.

See also: `ratline explain sockets`, `ratline explain deploys`.
