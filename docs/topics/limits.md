# Resource limits and isolation

> What stops one site from taking down the server.

Every dynamic site runs under its own systemd unit, and the unit carries limits the
kernel enforces:

    MemoryMax=512M       hard ceiling; exceeding it kills this service, not the host
    MemoryHigh=448M      throttling starts here, before the kill
    CPUQuota=100%        one core's worth
    TasksMax=256         the fork-bomb ceiling
    LimitNOFILE=8192

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

A cgroup contains every descendant, so a PM2-supervised node site's limits still
cover PM2 and all of its workers. The extra supervision layer does not weaken the
ceiling. `ratline explain node` has the rest of that trade.

See also: `ratline explain node`, `ratline explain safety`.
