# Bun sites

> Running TypeScript unbuilt, why there is no PM2 here, and what that costs.

A bun site runs `bun` under its own systemd unit as the tenant, behind a Unix socket.
One process, supervised directly — there is no supervisor between systemd and the
application.

    ratline runtime install bun 1.2
    ratline site add app.example.com --user acme --runtime bun \
      --entry server.ts --bun 1.2

## Why you would pick it over node

Bun transpiles on the way in, so `server.ts` and `src/app.tsx` are entry points rather
than build inputs. A project that exists only to be compiled before it can start loses
a build step, a build output directory and a class of deploy failure with it.
`--entry` accepts `.ts`, `.tsx`, `.jsx` and `.mts` here; on a node site it does not,
because node cannot parse JSX and a unit written from one dies on first start with a
syntax error from deep inside the module loader.

## Why you would not

**There is no graceful reload.** PM2 is what makes `ratline site reload` mean something
on a node site: it starts a replacement worker, waits for it, and only then retires the
old one. Bun has no equivalent signal, and PM2 is a node supervisor — running it here
would mean a Node install and a second daemon to supervise a runtime that has no cluster
mode to fan out into. So `ratline site reload` on a bun site refuses and tells you to
restart, rather than reporting a clean reload while dropping requests.

`--daemon` and `--instances` are refused for the same reason. A bun site is one process.
If zero-downtime reloads matter more than the engine does, that is a node site.

## The interpreter is pinned, and `bun upgrade` cannot move it

    ratline runtime install bun 1.2         # into /opt/ratline/runtimes/bun/1.2
    ratline runtime default bun 1.2         # what new sites use unpinned
    ratline site runtime app.example.com --bun 1.3

The unit's `ExecStart` is an absolute path into the root-owned runtime prefix. That
matters more here than it does for node: `bun upgrade` rewrites the binary in place, so
a unit pointing at `~/.bun/bin/bun` would change interpreter the day a tenant ran it. A
managed bun is root-owned, world-executable and writable by nobody — only ratline
replaces it.

The download is a GitHub release asset verified against the `SHASUMS256.txt` published
beside it. Nothing installs if the checksum does not match.

## AVX2, and the illegal instruction

Bun's default x86-64 build requires AVX2, which a good number of older VPS hosts do not
expose. Without it the process dies on an illegal instruction and says nothing useful
about why. `runtime install` reads `/proc/cpuinfo` and picks the baseline build when the
flag is absent; `--baseline` forces it:

    ratline runtime install bun 1.2 --baseline

## Installing dependencies

`bun install` by default, with `--frozen-lockfile` when there is a `bun.lock` or
`bun.lockb` to freeze — the reproducible form, which fails rather than quietly updating
the lockfile on a server. A project that keeps an npm, pnpm or yarn lockfile can say so
with `--package-manager`, and that installer is used instead while bun still runs the
server.

Dev dependencies are kept whenever the site has a `--build-command`, because that is
where every build tool lives. They are omitted only when there is nothing to build.

## Listening

    --listen socket     the default; nginx reaches it over a Unix socket
    --listen port       a localhost port, allocated automatically

`Bun.serve` takes a socket as its `unix:` option rather than reading one from the
environment, so a socket site has to read the path itself. It is provided three ways —
`RATLINE_SOCKET`, `SOCKET_PATH` and `PORT` — and any of them will do:

```ts
Bun.serve({
  unix: process.env.RATLINE_SOCKET,
  fetch: (req) => new Response("ok"),
});
```

`BUN_PORT` is deliberately not set on a socket site: bun parses it as a port number, so
a path in it is a startup failure rather than a value the application can ignore. On a
port site both `PORT` and `BUN_PORT` are set, which is enough for a default-exported
`Bun.serve` object to come up with no environment handling at all.

## Hardening

`MemoryDenyWriteExecute` is relaxed by default, exactly as it is for node:
JavaScriptCore JITs and needs writable-executable memory. Every other directive in the
sandbox applies — `ratline explain limits` lists them and how to relax one deliberately.

See also: `ratline explain node`, `ratline explain sockets`, `ratline explain deploys`.
