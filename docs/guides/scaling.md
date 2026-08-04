# Scaling and limits

```bash
sudo ratline site scale app.example.com --instances 4
sudo ratline site scale api.example.com --workers 6
sudo ratline site scale app.example.com --memory-max 1G --cpu-quota 200%
sudo ratline site scale www.example.com --client-max-body-size 100M
```

## What the unit enforces

```
MemoryMax=512M       a hard ceiling; exceeding it kills this service, not the host
MemoryHigh=448M      reclaim starts here, before the kill
CPUQuota=100%        one core's worth
TasksMax=256         the fork-bomb ceiling
LimitNOFILE=8192
```

These are cgroup limits, so each one is a **total for the unit** rather than a
per-process allowance. Four gunicorn workers holding 200M each is 800M against a 512M
ceiling — which is why raising `--workers` without raising `--memory-max` is how a
site that was fine starts being OOM-killed under load.

This is what replaces a PHP-FPM pool's `pm.max_children`. The difference is that the
kernel enforces it, rather than a process that has to notice first.

`MemoryHigh` at 87.5% of `MemoryMax` means the kernel starts reclaiming before it
starts killing, which turns some OOM kills into a slowdown you can watch coming.

## Workers versus instances

Both are concurrency *inside* one unit. Neither adds a unit, and there is never an
nginx upstream pool: a site is one unit binding one socket, and both supervisors share
that one listening handle across their workers.

**`--workers`** is Gunicorn's worker count on a python site. The master holds the
socket and re-forks to the new count on `SIGHUP`, so a worker change is a reload and
costs no requests.

**`--instances`** is PM2's cluster worker count on a node site. Node's `cluster`
module shares the listening handle, which is also what lets `pm2 reload` cut over one
worker at a time.

So `--instances` is refused where nothing can act on it — a node site running
`--daemon direct` is a single process, and a python site scales with `--workers`. Each
refusal names the flag that does work, rather than accepting the value and quietly
ignoring it.

## Choosing a memory ceiling

Watch first, then set:

```bash
sudo ratline site status app.example.com     # current usage
sudo ratline status                          # every site at once
```

A ceiling below the application's steady state turns into a restart loop, which
`doctor` reports as one — under PM2 it reads PM2's restart counter, since systemd's
stays at zero there.

## Hardening, and relaxing it deliberately

The unit also carries `ProtectSystem=strict`, `ProtectHome=tmpfs` with `BindPaths`,
`PrivateTmp`, `NoNewPrivileges` and `SystemCallFilter=@system-service`. Each is
verified at install time by starting the unit and health-checking it, so a directive
that breaks the application is reported **by name**:

```bash
sudo ratline site add ... --relax ProtectHome
sudo ratline site runtime app.example.com --relax MemorySeal
```

The generated unit records which directives are off, in a comment, so the next person
to read it knows.
