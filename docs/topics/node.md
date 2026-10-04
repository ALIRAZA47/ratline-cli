# Node sites and PM2

> How a node site is supervised, why PM2 is the default, and when to turn it off.

A node site runs as the tenant, behind a Unix socket, with PM2 in cluster mode between
systemd and the application. Every PM2 site of a tenant runs in that tenant's one PM2
daemon, not in a daemon of its own.

## Why PM2 is the default

Because it is the only way `ratline site reload` can mean anything on a node site.
`pm2 reload` starts a replacement worker, waits for it to come up, and only then
retires the old one — so a deploy drops no requests. There is no signal a plain
node process handles that way, which is why a site running without PM2 refuses to
reload rather than pretend, and tells you to restart instead.

## One daemon per tenant

Ten node sites are not ten PM2 daemons. Each tenant has one, per Node version, run by
systemd as the tenant:

    ratline-pm2@acme.node22.service       PM2_HOME=/home/acme/.ratline/pm2/node22

Each site keeps its own unit, `ratline-<slug>.service`, but it is a oneshot bound to
the daemon: starting it runs `pm2 start` on the site's ecosystem file, stopping it runs
`pm2 delete` for that site alone, and reloading it is `pm2 reload`. `ratline site
start`, `stop`, `restart` and `reload` work exactly as before. When the daemon restarts
or crashes, systemd stops the sites bound to it and starts them again with it.

Why per tenant and never one for the whole server: a daemon shared between tenants
would have to run as root to start each tenant's workers as them, which means root
reading every tenant's configuration and opening paths in every tenant's home. Why per
Node version: cluster workers are forked from the daemon's own `node`, so a Node 18
site on a Node 22 daemon would quietly run on 22. A tenant whose sites all use one
version has one daemon.

Adding, removing, enabling or disabling a site rewrites the daemon's unit without
restarting it, so a tenant's other sites keep serving. A change that cannot reach a
running daemon — a `--relax`ed directive, a new PM2 — restarts it, and the restart
takes each of that tenant's PM2 sites down and up with it. ratline says so first.

## What the extra layer does not cost

systemd supervises PM2 and PM2 supervises the application, which is a real trade.
These are the properties that survive it:

* **The resource ceiling is still kernel-enforced, per tenant.** systemd owns the
  daemon's cgroup and a cgroup contains every descendant, so `MemoryMax`, `CPUQuota`
  and `TasksMax` — the sum of the tenant's running PM2 sites — cover PM2 and every
  worker. One site can take a sibling's share; no site can take another tenant's.
* **Each site still has its own memory ceiling.** A site's `MemoryMax` is PM2's
  `max_memory_restart` for its workers, divided between them, so a worker that grows
  past it is restarted on its own rather than the kernel killing the whole daemon.
* **Nothing leaks between tenants.** The daemon runs as its tenant, sandboxed like a
  site, with every other tenant's home hidden. Stopping a site's unit takes its
  application out of the daemon; deleting a tenant's last PM2 site removes the daemon.
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
questions about the unit itself, such as a failed start. The daemon's own messages,
and an OOM kill, are under `journalctl -u ratline-pm2@<user>.<node>.service`.

## Running pm2 yourself

A bare `pm2 list` as root talks to root's own `~/.pm2` and, finding nothing, starts an
empty daemon there. `ratline pm2` runs pm2 as the tenant, against their daemon, with
its own `PM2_HOME` and node:

    ratline pm2 -- list                          every tenant's daemon in turn
    ratline pm2 app.example.com -- logs          that site's application
    ratline pm2 app.example.com -- describe
    ratline pm2 acme -- monit                    the tenant's daemon

Verbs that would fight the units ratline owns — `kill`, `delete`, `stop`, `start`,
`scale`, `save`, `resurrect`, `startup`, `update`, `install` — are refused, each with
the ratline command that does it properly.

## Upgrading from per-site daemons

Releases before this one started a PM2 daemon per site, with `PM2_HOME` at
`<site>/.pm2`. A site keeps that daemon, and keeps working, until its unit is
re-rendered and it restarts:

    ratline reconcile --fix
    ratline site restart app.example.com

The restart is when the site moves into its tenant's daemon. `ratline doctor` lists
every site still on a daemon of its own. The old `<site>/.pm2` directory is left
behind and can be removed once the site has moved.

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

## Instances are cluster workers, not units

`--instances N` sets PM2's cluster worker count. All N workers share the one listening
socket, inside the tenant's daemon, with the site's memory ceiling divided between them
— which is what cluster mode is for, and what lets `pm2 reload` retire one worker at a
time.

There is no nginx upstream pool and no second unit. `--instances` is refused on a node
site running `--daemon direct`, because that is a single process, and on a python site,
which scales with `--workers` instead.

## Cluster mode and non-JavaScript start commands

Cluster mode is node's own `cluster` module, so it can only fan out a JavaScript
entry point. A `--start-command` that runs `npm`, `pnpm` or a binary falls back to
fork mode with `interpreter: none`, and ratline says so when it generates the
configuration — in fork mode a reload is a restart.

Prefer `--entry` pointing at the file that calls `listen()`. A package manager
between systemd and your server also breaks signal delivery and restart counting.

See also: `ratline explain sockets`, `ratline explain deploys`.
