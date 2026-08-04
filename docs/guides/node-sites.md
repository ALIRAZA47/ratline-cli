# Node sites

A node site runs under its own systemd unit as the tenant, behind a Unix socket,
with PM2 in cluster mode between systemd and the application.

```bash
sudo ratline runtime install node 22 --with-pm2
sudo ratline site add app.example.com --user acme --runtime node \
    --entry server.js --node 22
```

## Why PM2 is the default

Because it is the only way `ratline site reload` can mean anything here. `pm2 reload`
starts a replacement worker, waits for it, and only then retires the old one, so a
deploy drops no requests. There is no signal a plain node process handles that way —
which is why a site running without PM2 refuses to reload rather than pretend, and
tells you to restart instead.

## What the extra layer costs, and does not

systemd supervises PM2 and PM2 supervises the application. That is a real trade, and
these are the properties that survive it:

* **The ceiling is still kernel-enforced.** systemd owns the cgroup and a cgroup
  contains every descendant, so `MemoryMax`, `CPUQuota` and `TasksMax` cover PM2 and
  all of its workers. PM2's own `max_memory_restart` is deliberately unset, because it
  would fire first and mask the limit that actually holds.
* **No shared daemon.** `PM2_HOME` is inside the site directory, so each site has its
  own daemon, socket and process list. Nothing outlives the site; nothing leaks
  between tenants.
* **Nothing is orphaned.** `ExecStop` runs `pm2 kill`.
* **The configuration is data.** ratline generates `ecosystem.config.json`, not the
  usual `ecosystem.config.js`. A JavaScript config is code PM2 evaluates as the
  tenant, and a settings file has no business being executable.

What genuinely changes: systemd's restart counter stays at zero, because PM2 does the
restarting. `site status` and `doctor` read PM2's counter instead and label it as
PM2's. And PM2 captures worker output into `logs/app.log`, so `site logs` reads the
file while `--journal` shows the unit's own messages.

## Running without it

```bash
sudo ratline site add app.example.com --user acme --runtime node \
    --entry server.js --daemon direct
sudo ratline site runtime app.example.com --daemon direct
```

Server-wide, in `/etc/ratline/config.yaml`:

```yaml
runtimes:
  node_process_manager: direct
```

`direct` is the better choice for a single-process application that is never reloaded
in place: one fewer moving part, and systemd sees the application itself.

## Telling your server where to listen

There is no standard, so three variables are set to the same socket path:

```
PORT=/run/ratline/acme-app_example_com/app.sock
RATLINE_SOCKET=/run/ratline/acme-app_example_com/app.sock
SOCKET_PATH=/run/ratline/acme-app_example_com/app.sock
```

`PORT` holds a path deliberately: `server.listen(process.env.PORT)` accepts a path in
that argument, so most applications need no change.

For `--listen port`, `PORT` is a number and `HOST` is `127.0.0.1`.

## Cluster mode needs a JavaScript entry point

Cluster mode is node's own `cluster` module, so it can only fan out a `.js`, `.mjs` or
`.cjs` file. A `--start-command` that runs `npm`, `pnpm` or a binary falls back to
fork mode with `interpreter: none`, and ratline says so — in fork mode a reload is a
restart.

Prefer `--entry` pointing at the file that calls `listen()`. A package manager between
systemd and your server also breaks signal delivery and restart counting.

## Instances

```bash
sudo ratline site scale app.example.com --instances 4
```

Four PM2 workers sharing one listening socket, inside one cgroup and one memory
ceiling. Not four units.

## Package managers

Detected from the lockfile: `pnpm-lock.yaml`, `yarn.lock`, `bun.lockb`,
`package-lock.json`. `npm ci` is preferred over `npm install` when a lockfile exists,
because it fails rather than silently updating the lockfile — which is what you want
on a server. Override with `--package-manager`.

Dependencies are installed **as the tenant**, never as root.

## Changing versions

```bash
sudo ratline site runtime app.example.com --node 24
```

Dependencies are reinstalled, because native modules are compiled against the ABI.
