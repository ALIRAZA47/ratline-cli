# Concepts

These are the pages `ratline explain` prints. They are embedded in the binary, so they
work over SSH on a server with no browser and no network — which is the situation in
which someone usually needs them.

```bash
ratline explain            # list them
ratline explain sockets    # read one
ratline explain node | less
```

| Topic | Covers |
|---|---|
| [layout](layout.md) | Where everything lives on disk |
| [sockets](sockets.md) | Unix sockets, ports, and the silent 502 |
| [node](node.md) | Node supervision, PM2, and when to turn it off |
| [bun](bun.md) | TypeScript unbuilt, and what having no PM2 costs |
| [python](python.md) | Gunicorn, virtualenvs, WSGI vs ASGI |
| [static](static.md) | nginx serving files directly |
| [tls](tls.md) | Certificates as a separate resource |
| [ssh](ssh.md) | The three key scopes |
| [deploys](deploys.md) | What a deploy does, and what a failure leaves behind |
| [diagnose](diagnose.md) | When a site is broken, in what order to check |
| [limits](limits.md) | Resource ceilings and systemd hardening |
| [safety](safety.md) | Idempotence, rollback, and the exit-code contract |
| [state](state.md) | The database, the audit log, and backups |

These files are the single source of truth: the binary embeds them and the
documentation site renders them, so the two can never give different answers.
