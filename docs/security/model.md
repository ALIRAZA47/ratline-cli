# Security model

This document is about what ratline actually enforces, and where that enforcement
stops. The limits matter more than the features: a boundary you believe in but
which does not hold is worse than no boundary at all, because you make decisions
based on it.

## The short version

ratline gives each tenant a real system account and puts each site's application
in its own systemd sandbox. That is a genuine boundary — one tenant cannot read
another's files, and one site cannot exhaust the host's memory. It is **not**
virtualization: the kernel is shared, and nothing here contains a kernel exploit.

Where you need a boundary you would bet a customer's data on, use separate
machines. Where you need a boundary strong enough that a compromised application
cannot reach a sibling application's secrets, ratline's per-user model is the right
tool — **provided you give each untrusted site its own user.**

## What is enforced

### Filesystem separation

Each tenant gets a system account, its own group, and a home at `0750`. A tenant
cannot read another tenant's home, because the mode does not permit it and there is
no shared group.

nginx reads a site's public files by being a member of the tenant's group. The
alternative — making homes world-readable — would expose every tenant's files to
every other tenant, so ratline never does it, and `doctor` reports a home whose mode
has drifted.

`.env` is `0600`, owned by the tenant, and lives outside every document root.
systemd reads it as root before dropping privileges, which is how a file no web
server can serve still reaches the application. nginx additionally denies dotfiles,
`.git`, `node_modules`, lockfiles and a list of extensions, so a misconfigured
document root does not become a data breach.

### Process isolation

Every dynamic site runs under its own systemd unit with:

- `User=` and `Group=` set to the tenant. The process cannot open another tenant's
  files, because the kernel refuses it.
- `ProtectHome=tmpfs` — every other home directory is not merely unreadable, it is
  not present in the process's view of the filesystem.
- `PrivateTmp=true` — its own `/tmp`, so temporary files cannot be read or
  raced by another tenant.
- `ProtectSystem=strict` — the entire filesystem is read-only except the paths
  ratline names.
- `NoNewPrivileges=true` — a setuid binary cannot be used to gain privileges.
- `SystemCallFilter=@system-service` — syscalls outside what a service needs return
  EPERM.
- `RestrictNamespaces`, `RestrictSUIDSGID`, `LockPersonality`,
  `ProtectKernelTunables`, `ProtectKernelModules`, `ProtectControlGroups`.

Each directive is verified at install time by starting the service and health
checking it. When one breaks an application, ratline names it and offers
`--relax <directive>`, so the sandbox is reduced deliberately by one directive
rather than abandoned.

`MemoryDenyWriteExecute` is relaxed by default for Node, because V8's JIT needs
writable-executable memory. That is a real reduction in hardening and it is
documented in the generated unit file rather than hidden.

### Resource limits

`MemoryMax`, `MemoryHigh`, `CPUQuota`, `TasksMax` and `LimitNOFILE` are set per
site. These are enforced by the kernel: a site that exceeds its memory ceiling is
killed by its own cgroup rather than taking the host down. `MemoryHigh` is set
below `MemoryMax` so the kernel reclaims and throttles before it kills, which turns
a hard OOM into back pressure the application may survive.

This is what replaces a PHP-FPM pool's `pm.max_children`, and it is strictly
stronger: a worker count is a hope, a cgroup limit is a fact.

### Privilege discipline

- ratline refuses to run without EUID 0, and refuses to run if its own binary is
  group- or world-writable, or lives in a directory that is. Anyone who can write
  `/usr/local/bin/ratline` can run code as root the next time you type `sudo ratline`.
- Every external command is an argv slice. There is no `sh -c` anywhere, and no
  shell in the binary registry, so building a shell string is not a thing a
  contributor can accidentally do.
- Binaries are resolved to absolute paths from a fixed candidate list. `PATH` is
  never used for lookups, and children get a scrubbed environment.
- `npm install` and `pip install` run **as the tenant**. A postinstall script in a
  dependency tree is code the tenant chose to trust; running it as root would make
  every dependency install a route to compromising the whole server.
- Created users get no sudo. The escape hatch is config-gated and validated with
  `visudo -c` before installation.
- Secrets never appear in argv. `env set` and `password set` read from stdin, and
  values are redacted in logs, in errors, and in `env list` unless `--reveal`.

## What is not enforced

### The kernel is shared

Every tenant runs on one kernel. A local privilege escalation vulnerability
compromises every site on the box. systemd's sandboxing raises the cost of an
attack considerably — a seccomp filter blocks most published exploit primitives —
but it is defence in depth, not a boundary.

**If you host mutually hostile tenants, use separate machines or VMs.** No
configuration of this tool changes that.

### Site-scoped SSH keys are not a kernel boundary

This is the most important limit in this document, and the one most likely to be
misunderstood.

A site-scoped key authenticates **as the site owner's UID**. The confinement is
sshd's forced command plus the `ratline-shell` wrapper, which parses the requested
command, refuses anything outside sftp/rsync/git, and asserts that every path
resolves inside one site directory after symlink resolution.

That reliably prevents accidents: a contractor's `rsync` cannot wander into a
sibling site, and a misconfigured CI job cannot overwrite the wrong directory. It
is a real and useful boundary against mistakes.

It does **not** stop someone who already has code execution as that UID. If a
contractor can run arbitrary code — through an application vulnerability, a
malicious dependency, or a bug in the wrapper — they have the tenant's UID and can
reach every site that tenant owns. `--allow-shell` removes almost all of it by
design, and `ratline key test` says so for every key it describes.

**Where you need genuine per-site isolation, give the site its own user.**
`ratline user add` is deliberately cheap for exactly this reason. One user per
site turns a usability boundary into a kernel-enforced one.

### Tenants can see each other exist

`/proc` is not hidden. A tenant can list processes and see other tenants' command
lines and usernames. `hidepid=2` on `/proc` would fix this and is not configured by
default, because it interferes with some monitoring agents. Nothing in a command
line should be a secret in any case — which is why ratline never puts one there.

### Resource limits are only as good as your configuration

`MemoryMax` bounds memory. It does not bound disk I/O, network bandwidth, or
inode consumption. A tenant filling the disk affects every site. Filesystem quotas
address disk and are off by default because a fresh VPS is not mounted with quota
support; `--quota` refuses rather than silently ignoring you when it is unavailable.

### ratline trusts its own state file only as an index

`/var/lib/ratline/state.db` is `0600 root:root`. It is an index and an audit log,
not the source of truth: the filesystem, the systemd units and `/etc/passwd` are.
`reconcile` rebuilds state by scanning them. This is deliberate — a provisioning
tool that cannot survive the loss of its own database is a liability.

## The SSH lockout guarantees

Bricking SSH on a remote VPS has no recovery path short of a provider console, so
every change under `/etc/ssh` follows the same sequence:

1. Back up the previous file, write the new one, run `sshd -t`. On failure, restore
   and reload immediately.
2. **Reload, never restart** — existing sessions survive a reload, so the session
   making the change is not the one that dies from it.
3. Prove login still works. ratline asks sshd for the *effective* configuration it
   would apply to each managed account (`sshd -T -C user=…`) and asserts that public
   key authentication is on, that the authorized_keys path is the one ratline
   writes, and that no `Match` block excludes the account — then confirms the daemon
   is answering on its port. If any of that fails, the change is reverted and
   reported as rejected.
4. `PermitRootLogin`, `PasswordAuthentication`, `AllowUsers` and `Port` are never
   touched. Not with a flag, not with a confirmation — ratline does not own them.
5. `key remove` refuses to remove the last credential that can still administer the
   server without `--force` and a typed confirmation, and the warning names exactly
   what access would remain.

On step 3, note what ratline does *not* do: it holds no private key of yours, so it
cannot literally log in as you. A truly end-to-end test would mean ratline
generating and installing a key of its own — a permanent extra credential on the box
in exchange for a marginally stronger check. That trade is not worth it, and this
is what it does instead.

## Reporting a vulnerability

Open a private security advisory on the repository. Please include the OS version,
the ratline version from `ratline version`, and the smallest set of commands that
reproduces the problem.
