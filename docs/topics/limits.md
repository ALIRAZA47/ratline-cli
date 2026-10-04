# Resource limits and isolation

> What stops one site from taking down the server.

Every dynamic site runs under its own systemd unit, and the unit carries limits the
kernel enforces:

    MemoryMax=512M       hard ceiling; exceeding it kills this service, not the host
    MemoryHigh=448M      throttling starts here, before the kill
    CPUQuota=100%        one core's worth
    TasksMax=256         the fork-bomb ceiling
    LimitNOFILE=8192

Each is a cgroup limit, so each is a total for the whole unit rather than a
per-process allowance. Four workers holding 200M each is 800M against a 512M ceiling,
which is why raising the worker count without raising the memory is how a site that
was fine starts being OOM-killed under load.

    ratline site scale app.example.com --memory-max 1G --cpu-quota 200%
    ratline site scale api.example.com --workers 6

This is what replaces a PHP-FPM pool's `pm.max_children`. The difference is that the
ceiling is enforced by the kernel rather than by a process that must first notice.

`MemoryHigh` at 87.5% of `MemoryMax` means the kernel starts reclaiming before it
starts killing, which turns some OOM kills into a slowdown you can see coming.

## Hardening

The unit also carries:

    ProtectSystem=strict            the filesystem is read-only except named paths
    ProtectHome=tmpfs + BindPaths   only this tenant's site directory is visible
    PrivateTmp=yes
    NoNewPrivileges=yes
    SystemCallFilter=@system-service

Each directive is verified at install time by starting the unit and health-checking
it. If one breaks the application, ratline reports which one by name — so it can be
relaxed deliberately:

    ratline site add ... --relax ProtectHome
    ratline site runtime app.example.com --relax MemorySeal

The generated unit records which directives are off, in a comment, so the next
person to read it knows.

## PM2 and the cgroup

A tenant's PM2 sites run in one daemon, and a cgroup contains every descendant, so the
kernel's ceiling for them is the daemon's: the sum of their `MemoryMax` and `CPUQuota`,
covering PM2 and every worker. Each site's own `MemoryMax` is also PM2's
`max_memory_restart` for its workers, which restarts a worker that outgrows its site
rather than letting the kernel kill the daemon. One site can take a sibling's share of
the tenant's ceiling; it cannot take another tenant's. `ratline explain node` has the
rest of that trade.

See also: `ratline explain node`, `ratline explain safety`.
