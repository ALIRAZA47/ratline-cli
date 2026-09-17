# Deciding what a repository is, in ratline's terms

Read this with the repository open. The output is a set of `ratline site add` (or
`ratline new`) flags and a list of environment variables, split into build-time and
runtime. When two rules disagree, the framework's own configuration wins over the
presence of a file, and what the code actually passes to `listen()` wins over everything.

## 1. Signals

| you see | it usually means |
|---|---|
| `index.html` at the root and no server code | `--runtime static` |
| `vite.config.*`, `astro.config.*` (no server adapter), Hugo, Eleventy, Docusaurus, Gatsby, CRA | `static` with `--build-command` and `--build-output` |
| a client-side router (React Router, Vue Router, SvelteKit static adapter) | add `--spa` |
| `package.json` with a server entry (Express, Fastify, Koa, Hono, NestJS, Remix, SvelteKit adapter-node, Nuxt) | `--runtime node` |
| `next.config.*` **without** `output: 'export'` | `node`, standalone build, `--listen port` (see below) |
| `next.config.*` **with** `output: 'export'` | `static`, build output `out` |
| `bun.lockb` or `bun.lock`, `Bun.serve` in the source | `--runtime bun` |
| `requirements.txt`, `pyproject.toml`, `manage.py`, `wsgi.py`, `asgi.py` | `--runtime python` |
| `manage.py` | Django: `--manage-py manage.py`, `--static-url /static/ --static-dir staticfiles` |
| `FastAPI(`, `Starlette(`, `asgi.py` | `--asgi` |
| `Flask(`, `wsgi.py` | `--wsgi` (the default) |
| `Dockerfile` only, `compose.yaml`, `go.mod`, `Gemfile`, `composer.json`, `Cargo.toml`, `pom.xml`, `*.csproj` | **not hostable by ratline**; say so |

More than one of these in different directories is more than one site. A `frontend/` and
an `api/` are two `site add` calls, two domains (`app.example.com`, `api.app.example.com`),
usually one tenant, and a CORS setting in the API for the frontend's origin.

## 2. Package manager, install, build

Detected from the lockfile: `pnpm-lock.yaml`, `yarn.lock`, `bun.lock*`,
`package-lock.json`. `npm ci` is preferred over `npm install` when a lockfile exists,
because it fails rather than silently updating the lockfile. Override with
`--package-manager` only when detection is wrong. Dev dependencies are installed when there
is a build command (Tailwind, TypeScript and Vite live there) and skipped otherwise.

`--install-command`, `--build-command` and `--start-command` are each **one argv**. A shell
pipeline, `&&`, `;` or a redirection is refused, not passed through. Anything with more than
one step is a script committed to the repository, `chmod +x`, referenced as
`--build-command ./bin/build`. This is not a limitation to work around: it is what stops a
domain or a label from ever becoming a shell operator on a root-run server.

Python: a `requirements.txt` is detected; `--requirements` names another file. A project
with only `pyproject.toml` needs a requirements file the server can install from; export one
(`uv export --no-dev -o requirements.txt`, `poetry export`, or `pip-compile`) and commit it
rather than teaching the server about a build tool.

## 3. Entry point

**Node and Bun**: `--entry` is the file that calls `listen()`, relative to the application
directory. Prefer it over `--start-command npm start`: cluster mode can only fan out a
JavaScript file, so a package manager between systemd and the server means fork mode, a
reload that is a restart, broken signal delivery and a restart count that lies. Compiled
TypeScript: `--entry dist/server.js` with a build that produces it. Bun runs TypeScript
unbuilt, so `--entry server.ts` is fine there.

**Next.js**: set `output: 'standalone'` in `next.config.*`, entry
`.next/standalone/server.js`, and a build script that copies `.next/static` and `public`
into the standalone tree, because `next build` leaves them behind and the server then
serves pages with no CSS:

```sh
#!/bin/sh
# bin/build
set -eu
npm run build
mkdir -p .next/standalone/.next
cp -r .next/static .next/standalone/.next/static
[ -d public ] && cp -r public .next/standalone/public
```

Add `--public public` so nginx serves that directory itself. Builds are memory-hungry;
`--memory-max 2G` on `site add`, or `site scale` later, if the build is OOM-killed.

**Python**: `--app-module` is an import path resolved from the application directory,
`package.module:callable`, the same string you would give gunicorn. Not a file path: no
`.py`, no slashes. `app/main.py` containing `app = FastAPI()` is `app.main:app`;
`main.py` at the root is `main:app`. This mismatch is the single most common failure, and
the traceback lands in `logs/app.log`, not the journal. Django: `myproject.wsgi:application`
(or `.asgi:application` with `--asgi`).

## 4. Socket or port

ratline puts the application on a Unix socket by default and tells it where through three
variables set to the same path: `PORT`, `RATLINE_SOCKET` and `SOCKET_PATH`. `PORT` holds a
path deliberately, because Node's `server.listen(process.env.PORT)` accepts one. So:

- `app.listen(process.env.PORT)` (Express, Koa, plain `http`), or a framework that honours
  `SOCKET_PATH` (SvelteKit adapter-node): **socket works, keep the default.**
- The server parses `PORT` as a number, or calls `listen(port, host)`: Next.js standalone,
  Nuxt/Nitro, Astro's node adapter, Fastify's `listen({port})`, most Bun servers:
  **`--listen port`.** ratline allocates a loopback port and sets `PORT` and `HOST`.
- Python is Gunicorn on the socket and needs nothing from the code.

A framework that ignores both and binds `3000` starts cleanly and answers nothing. If the
code hardcodes a port, that is a change to the application, not a ratline flag.

## 5. Environment

Grep for what the code reads, and sort it:

```bash
grep -rhoE 'process\.env\.[A-Z_][A-Z0-9_]*' --include='*.{js,mjs,cjs,ts,tsx,jsx,svelte,vue,astro}' . | sort -u
grep -rhoE 'import\.meta\.env\.[A-Z_][A-Z0-9_]*' . | sort -u
grep -rhoE 'os\.(environ(\.get)?|getenv)\(?\[?["'"'"'][A-Z_][A-Z0-9_]*' --include='*.py' . | sort -u
```

- **Build-time**: `VITE_*`, `NEXT_PUBLIC_*`, `PUBLIC_*`, `REACT_APP_*`, `import.meta.env.*`.
  Baked into the bundle. Set them before the first deploy; a change to one needs a rebuild.
- **Runtime**: everything else. Set with `site env set`; the service restarts.
- **Secrets**: anything that would matter if leaked. Stdin only.
- **Never set**: `PORT`, `HOST`, `HOSTNAME` (Next.js wants `HOSTNAME=127.0.0.1`, which the
  documentation sets explicitly; that one is fine), and any variable ratline writes itself.
- **Provided by ratline**: the database URL from `db create --attach`, under
  `MONGODB_URI` by default or the `--env-key` you choose (`DATABASE_URL` for a MySQL app,
  `REDIS_URL` for Redis).

Applications that validate their environment at import time fail before the health check
with an error that points at the validator, not at the missing variable. Read the list you
grepped against the list you set before deploying.

## 6. Database

| the code uses | ask for |
|---|---|
| mongoose, pymongo, motor, the MongoDB driver | `db create <name> --owner <tenant> --attach <domain>` |
| mysql2, Prisma or Drizzle with a MySQL provider, SQLAlchemy `mysql://` | `db --engine mysql create …` |
| ioredis, redis, celery or bullmq broker URLs | `db --engine redis create …` |
| SQLite, a file path | nothing; it lives in the application directory, which is the tenant's |
| PostgreSQL | not provided; the application needs an external database URL set as a secret |

If `db list` says provisioning is not enabled, the server has no database attached; the
`ratline-databases` skill covers `db install` (put MongoDB on this host) versus `db connect`
(point at one that exists). When the URL comes from outside ratline and you compose it by
hand, percent-encode the password: `@` → `%40`, `&` → `%26`, `:` → `%3A`, `/` → `%2F`,
`?` → `%3F`, `#` → `%23`, `%` → `%25`. A raw `@` truncates the host and produces an error
that points nowhere near the cause.

## 7. Ceilings and hardening

Defaults suit a small application. Raise deliberately: `--memory-max 1G`, `--cpu-quota 200%`,
`--instances 4` (PM2 workers sharing one socket, one unit, one ceiling), `--workers 4`
(Gunicorn). Four workers at 200M each is 800M against a 512M ceiling, which is how a site
that was fine starts being OOM-killed under load. `--client-max-body-size 100M` for uploads;
the default 20M is the commonest cause of a mystery 413.

If a site fails its first start with a message naming a systemd directive (`ProtectHome`,
`MemoryDenyWriteExecute`, …), ratline is telling you which piece of the sandbox the
application cannot live inside. `--relax <Directive>` turns off exactly that one and records
it in the unit. Relax one, by name, after reading why; never the sandbox as a whole.

## 8. What to carry into the handover

The runtime and why, the entry or app-module, listen mode, the install and build
commands, every variable and where it was set, the database and its env key, and anything
you could not determine from the repository and had to ask or assume.
