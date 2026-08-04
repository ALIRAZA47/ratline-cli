# Guides

Task-shaped documentation: pick the one that matches what you are doing.

| | |
|---|---|
| [runtimes.md](runtimes.md) | What each runtime generates, side by side |
| [static-sites.md](static-sites.md) | nginx serving files, SPAs, builds, caching |
| [node-sites.md](node-sites.md) | PM2, cluster mode, sockets, graceful reload |
| [python-sites.md](python-sites.md) | Gunicorn, WSGI vs ASGI, Django |
| [deploying.md](deploying.md) | `site deploy`, reload vs restart, rollback |
| [secrets-and-env.md](secrets-and-env.md) | `.env`, `env set --stdin`, redaction |
| [scaling.md](scaling.md) | Workers, instances, memory and CPU ceilings |

The same material in shorter form, readable on the server with no browser:

```bash
ratline explain            # list the topics
ratline explain node
```
