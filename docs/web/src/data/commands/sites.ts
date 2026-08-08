import type { CommandGroup } from '../types';

export const sites: CommandGroup = {
  id: 'site',
  title: 'Sites',
  path: '/reference/site',
  blurb: 'An nginx vhost, a document root, and — for node and python — a systemd unit.',
  intro: [
    'A site belongs to exactly one user and is served by nginx from inside that user’s home. For the static runtime that is the whole story. For node and python, the application also runs under its own systemd unit, as that user, behind a Unix socket in /run/ratline/<slug>/.',
    'After `start`, `restart` or `deploy`, ratline waits for health: it polls the socket or port with a real HTTP request until it answers or defaults.health_timeout (30s) elapses. A "successful" deploy that returns 502 is a bug, which is what exit code 7 is for.',
  ],
  commands: [
    {
      id: 'site-add',
      name: 'ratline site add',
      args: '<domain> --user <username> --runtime static|node|python',
      status: 'built',
      summary: 'Create a site: directories, vhost, unit, logs, logrotate, and optionally a certificate.',
      description: [
        'Everything is validated first, then preconditions are checked — the user exists, the domain is not already configured, the port is free, the runtime is installed, there is disk space, the entry point is present — then configs are written to temporary files in their final directory and renamed atomically. nginx -t runs before the reload; systemd-analyze verify runs before daemon-reload. Every created file, directory, symlink, unit, venv and port allocation registers an undo action, so a failure anywhere unwinds in reverse and tells you exactly what was rolled back.',
        'Re-running with identical parameters exits 0 with "already configured". Re-running with different parameters is an error that names the specific update command to use instead — `site scale`, `site runtime`, `site alias add` — rather than silently reconfiguring a live site.',
      ],
      flagGroups: [
        {
          title: 'Common flags',
          flags: [
            {
              name: '--user',
              arg: '<username>',
              type: 'string',
              required: true,
              description: 'The tenant that owns this site. Must already exist.',
            },
            {
              name: '--runtime',
              arg: 'static|node|python',
              type: 'enum',
              required: true,
              description: 'How the site is served. Each runtime adds its own flags below.',
            },
            {
              name: '--alias',
              arg: '<domain>',
              type: 'string',
              repeatable: true,
              description:
                'An additional server_name, for example www.example.com. Repeat for more.',
              note: 'Aliases are validated and de-duplicated against the primary domain, and they become the default SAN list for `cert issue`.',
            },
            {
              name: '--ssl',
              arg: 'letsencrypt|selfsigned|none',
              type: 'enum',
              default: 'letsencrypt if the domain already resolves here, otherwise selfsigned',
              description:
                'Convenience only: runs `cert issue` (or `cert selfsign`) as a final step.',
              note: 'A certificate failure never fails the site creation. That is deliberate — the normal order of operations when a client is still moving their domain is site first, certificate later. When the default falls through to selfsigned, a note is printed saying so.',
            },
            {
              name: '--email',
              arg: '<address>',
              type: 'email',
              description: 'ACME contact address. Certificate expiry warnings go here.',
              note: 'Falls back to acme.email from the configuration file. Validated conservatively: a typo means no expiry warnings.',
            },
            {
              name: '--www-redirect',
              arg: 'apex|www|none',
              type: 'enum',
              description:
                'Which name is canonical. `apex` redirects www → example.com; `www` does the reverse.',
            },
            {
              name: '--no-enable',
              type: 'bool',
              default: 'false',
              description: 'Write the configuration but do not symlink it into sites-enabled or start the unit.',
              note: 'Useful when you want to inspect the generated vhost before anything is served.',
            },
            {
              name: '--repo',
              arg: '<git-url>',
              type: 'url',
              description: 'Clone this repository into the application directory.',
              note: 'https:// and ssh:// only. git://, http://, file:: and ext:: are refused — ext:: runs arbitrary commands, git:// is neither authenticated nor encrypted, and plain HTTP is not encrypted.',
            },
            {
              name: '--branch',
              arg: '<ref>',
              type: 'string',
              default: 'main',
              description: 'Branch or tag to check out.',
            },
          ],
        },
        {
          title: 'static',
          note: 'nginx serves files straight from the document root. Nothing runs, there is no unit and there is no socket.',
          flags: [
            {
              name: '--root',
              arg: '<subdir>',
              type: 'subdir',
              default: 'public',
              description: 'Document root, as a subdirectory of the site directory.',
              note: 'Relative only. Traversal, absolute paths and shell-significant characters are refused rather than cleaned away, and the resolved path must still be inside the owner’s home after symlinks are followed.',
            },
            {
              name: '--spa',
              type: 'bool',
              default: 'false',
              description: 'Render try_files $uri $uri/ /index.html, so client-side routes resolve.',
            },
            {
              name: '--index',
              arg: '<file>',
              type: 'string',
              default: 'index.html',
              description: 'Index filename.',
            },
            {
              name: '--build-command',
              arg: '"<command>"',
              type: 'command',
              description: 'Command run to build the site, as an argv slice.',
              note: 'Parsed by a shell-words parser that refuses ;, &&, ||, |, &, backticks, $(, ${, redirections and newlines. A genuine pipeline goes in a script in the repository, referenced as ./bin/build.',
            },
            {
              name: '--build-output',
              arg: '<subdir>',
              type: 'subdir',
              default: 'dist',
              description: 'Directory the build writes into, which becomes what nginx serves.',
            },
          ],
        },
        {
          title: 'node',
          note: 'nginx reverse-proxies to a Unix socket by default, or to an allocated port. The app runs under ratline-<slug>.service.',
          flags: [
            {
              name: '--entry',
              arg: '<file>',
              type: 'path',
              requiredWhen: 'unless --start-command is given',
              description: 'The file that starts the server, relative to the application directory.',
              note: 'Must be a .js, .mjs, .cjs, .ts, .mts or .cts file, in a directory that stays inside the site.',
            },
            {
              name: '--start-command',
              arg: '"<command>"',
              type: 'command',
              requiredWhen: 'unless --entry is given',
              description: 'Start command, resolved to an argv slice.',
              note: 'Anything needing a shell is refused, and sh, bash, env, exec, sudo, xargs and friends may not be the program: they exist to reinterpret their arguments, which would defeat argv-only execution.',
            },
            {
              name: '--node',
              arg: '<version>',
              type: 'version',
              default: 'runtimes.node_default',
              description: 'Managed Node version. Must already be installed.',
              note: 'ExecStart invokes the managed binary by absolute path — /opt/ratline/runtimes/node/22/bin/node server.js. nvm, shell profiles and login shells are never involved, which is why a site keeps working after someone edits their .bashrc.',
            },
            {
              name: '--package-manager',
              arg: 'npm|pnpm|yarn|bun',
              type: 'enum',
              default: 'npm',
              description: 'Which package manager to use for installs.',
            },
            {
              name: '--listen',
              arg: 'socket|port',
              type: 'enum',
              default: 'socket',
              description: 'Whether the app listens on a Unix socket or a TCP port.',
              note: 'A port is auto-allocated from ports.range_start–ports.range_end (20000–29999). Sockets are preferred: there is no port to collide, and the socket’s 0660 <user>:www-data mode is itself the access control.',
            },
            {
              name: '--daemon',
              arg: 'pm2|direct',
              type: 'enum',
              default: 'pm2',
              description: 'How the site is supervised.',
              note: 'PM2 in cluster mode is the default because it is the only way this site can reload without dropping requests: pm2 reload starts a replacement worker, waits for it, and only then retires the old one. systemd cannot do that for node, which is why a site running without PM2 refuses to reload rather than pretend. systemd still owns the cgroup, so MemoryMax and CPUQuota stay kernel-enforced across PM2 and all of its workers. Use direct for a single-process app that is never reloaded in place — one fewer moving part, and systemd sees the application itself.',
            },
            {
              name: '--install-command',
              arg: '"<command>"',
              type: 'command',
              default: 'npm ci --omit=dev',
              description: 'Dependency install command.',
              note: 'Runs as the site user, never as root.',
            },
            {
              name: '--build-command',
              arg: '"<command>"',
              type: 'command',
              description: 'Build command, run after install.',
            },
            {
              name: '--instances',
              arg: '<n>',
              type: 'int',
              default: '1',
              description: 'Number of application processes.',
              note: 'PM2 cluster workers, all sharing the one socket inside the one unit and the one cgroup. Refused on a node site running --daemon direct (a single process) and on a python site (which scales with --workers), rather than accepted and silently ignored.',
            },
            {
              name: '--public',
              arg: '<subdir>',
              type: 'subdir',
              default: 'public',
              description:
                'Static directory served directly by nginx, bypassing the application.',
            },
          ],
        },
        {
          title: 'python',
          note: 'Gunicorn (WSGI) or Gunicorn with a Uvicorn worker (ASGI), in a per-site virtualenv, behind a Unix socket.',
          flags: [
            {
              name: '--app-module',
              arg: '<module:callable>',
              type: 'import path',
              required: true,
              description: 'Import path to the WSGI or ASGI callable, for example app.main:app.',
              note: 'Matched against ^[A-Za-z_][A-Za-z0-9_.]*:[A-Za-z_][A-Za-z0-9_]*$ and each dotted segment must be a valid Python identifier. This string lands on a Gunicorn command line and inside a unit file, so it is pinned to identifier characters and a single colon.',
            },
            {
              name: '--python',
              arg: '<version>',
              type: 'version',
              default: 'runtimes.python_default',
              description: 'Managed Python version, 3.x or 3.x.y. Python 2 is not supported.',
            },
            {
              name: '--asgi',
              type: 'bool',
              default: 'auto-detected',
              description: 'Force ASGI.',
              note: 'Auto-detection recognises FastAPI and Starlette as ASGI. Pass the flag when detection would get it wrong.',
            },
            {
              name: '--wsgi',
              type: 'bool',
              default: 'auto-detected',
              description: 'Force WSGI — Django, Flask.',
            },
            {
              name: '--server',
              arg: 'gunicorn|uvicorn',
              type: 'enum',
              default: 'gunicorn',
              description: 'Which server runs the app.',
              note: 'ASGI under gunicorn means gunicorn with a UvicornWorker: Gunicorn keeps the process management and Uvicorn does the ASGI protocol.',
            },
            {
              name: '--workers',
              arg: '<n>',
              type: 'int',
              default: '(2 × cores) + 1, capped at defaults.worker_cap (8)',
              description: 'Number of worker processes.',
            },
            {
              name: '--requirements',
              arg: '<file>',
              type: 'path',
              default: 'requirements.txt',
              description: 'Dependency file.',
              note: 'pyproject.toml, uv and poetry layouts are also detected.',
            },
            {
              name: '--static-url',
              arg: '<url path>',
              type: 'string',
              default: '/static',
              description: 'URL prefix nginx serves static files from, bypassing the app.',
            },
            {
              name: '--static-dir',
              arg: '<subdir>',
              type: 'subdir',
              default: 'staticfiles',
              description: 'Directory those static files live in.',
            },
            {
              name: '--manage-py',
              arg: '<file>',
              type: 'path',
              default: 'manage.py',
              description: 'Django management script.',
              note: 'Its presence is what enables `site deploy --migrate` and `--collectstatic`.',
            },
          ],
        },
      ],
      refuses: [
        'A document root that escapes the owning user’s home after cleaning and symlink resolution. A link planted inside a tenant’s home cannot point a document root at /etc or at another tenant’s files.',
        'A --start-command or --build-command that needs a shell, and any command whose program is a shell, env, exec, sudo, xargs or similar.',
        'A repository URL using ext::, file://, git:// or plain http://, or one containing "..", or one starting with a hyphen.',
        'A second site claiming a server_name another vhost already claims.',
        'Re-running with different parameters. It errors and names the specific update command instead of silently reconfiguring a live site.',
      ],
      exits: [
        { code: 2, reason: 'The domain, username, subdirectory, app module, command string or repository URL failed validation.' },
        { code: 3, reason: 'The user does not exist, the domain is already configured, the runtime is not installed, no port is free, or the entry point is missing.' },
        { code: 4, reason: 'nginx -t, systemd-analyze verify, git, npm or pip failed. The raw error is included.' },
        { code: 5, reason: 'Locked.' },
        { code: 6, reason: 'Creation failed and the rollback stack could not fully unwind. Needs a human.' },
        { code: 7, reason: 'The app started but never answered a real HTTP request within 30s. Nothing was enabled in nginx.' },
      ],
      examples: [
        {
          title: 'An Astro or Vite build, served as files',
          lang: 'shell',
          code: `ratline site add example.com --user acme --runtime static \\
  --repo https://github.com/acme/site.git \\
  --build-command "npm ci" \\
  --build-output dist \\
  --alias www.example.com \\
  --www-redirect apex`,
        },
        {
          title: 'A Node server on a Unix socket',
          lang: 'shell',
          code: `ratline site add app.example.com --user acme --runtime node \\
  --entry server.js \\
  --node 22 \\
  --install-command "npm ci --omit=dev" \\
  --build-command "npm run build"`,
        },
        {
          title: 'A FastAPI app: Gunicorn with a Uvicorn worker',
          lang: 'shell',
          code: `ratline site add api.example.com --user acme --runtime python \\
  --app-module app.main:app \\
  --python 3.12 \\
  --asgi \\
  --workers 4`,
        },
        {
          title: 'Create it now, certificate later — DNS has not moved yet',
          lang: 'shell',
          code: `ratline site add example.com --user acme --runtime static --ssl selfsigned
# once DNS points here:
ratline cert issue example.com --email admin@example.com`,
        },
        {
          title: 'See every file, command and permission first',
          lang: 'shell',
          code: 'ratline site add example.com --user acme --runtime static --dry-run',
        },
      ],
      seeAlso: [
        { label: 'The request path', to: '/concepts/model#request-path' },
        { label: 'Deploy a FastAPI app', to: '/guides/fastapi' },
        { label: 'Put a Next.js standalone build behind nginx', to: '/guides/nextjs' },
        { label: 'Publish an Astro static build', to: '/guides/astro' },
      ],
      keywords: ['vhost', 'nginx', 'systemd', 'socket', 'gunicorn', 'uvicorn', 'spa', 'runtime'],
    },
    {
      id: 'site-list',
      name: 'ratline site list',
      status: 'built',
      summary: 'Every site, optionally filtered by owner or runtime.',
      flags: [
        { name: '--user', arg: '<u>', type: 'string', description: 'Only sites owned by this tenant.' },
        { name: '--runtime', arg: '<r>', type: 'enum', description: 'Only static, node or python sites.' },
        { name: '--json', type: 'bool', default: 'false', description: 'JSON envelope instead of a table.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline site list --runtime python' },
        {
          title: 'Every python site, for a scripted restart',
          lang: 'shell',
          code: `for d in $(ratline site list --runtime python --json | jq -r '.data.sites[].domain'); do
  ratline site restart "$d"
done`,
        },
      ],
    },
    {
      id: 'site-show',
      name: 'ratline site show',
      args: '<domain>',
      status: 'built',
      summary: 'Runtime, unit state, socket, certificate expiry and last deploy.',
      exits: [
        { code: 2, reason: 'The domain failed validation.' },
        { code: 3, reason: 'No such site.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline site show api.example.com' }],
    },
    {
      id: 'site-enable',
      name: 'ratline site enable|disable',
      args: '<domain>',
      status: 'built',
      summary: 'Symlink the vhost and start the unit, or stop it and remove the symlink.',
      description: [
        'Disabling is the reversible lever: the configuration stays on disk, the unit is stopped, the symlink comes out of sites-enabled, and nginx is reloaded rather than restarted.',
      ],
      exits: [
        { code: 3, reason: 'No such site.' },
        { code: 4, reason: 'nginx -t failed after the change; the previous config was restored.' },
        { code: 7, reason: 'Enabled and started, but never became healthy.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline site disable staging.example.com' }],
    },
    {
      id: 'site-delete',
      name: 'ratline site delete',
      args: '<domain>',
      status: 'built',
      summary: 'Remove a site, its unit, its vhost and its port allocation.',
      description: [
        'Prints an inventory of exactly what will go — paths, unit, certificate, port, state rows, directory size — and requires you to type the domain.',
      ],
      flags: [
        { name: '--purge', type: 'bool', default: 'false', description: 'Also delete the site directory and its contents.' },
        { name: '--backup', arg: '<dir>', type: 'path', description: 'Write a backup to this directory first.' },
      ],
      exits: [
        { code: 2, reason: 'The typed confirmation did not match; nothing was changed.' },
        { code: 3, reason: 'No such site.' },
        { code: 10, reason: 'Confirmation was needed and there is no terminal.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline site delete old.example.com --purge --backup /var/backups/ratline' },
      ],
    },
    {
      id: 'site-start',
      name: 'ratline site start|stop|restart|status',
      args: '<domain>',
      status: 'built',
      summary: 'Control a dynamic site’s systemd unit, with a real health check on the way up.',
      description: [
        'After start and restart, ratline polls the socket or port with a real HTTP request until it answers or defaults.health_timeout (30s) elapses. On any failed start, the last 20 lines of journalctl -u <unit> are surfaced automatically — the operator never has to go and find them.',
        'These are no-ops for a static site: nothing runs.',
      ],
      exits: [
        { code: 3, reason: 'No such site, or the site is static and has no unit.' },
        { code: 4, reason: 'systemctl failed.' },
        { code: 7, reason: 'Started, but never became healthy within the timeout.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline site restart api.example.com' }],
    },
    {
      id: 'site-reload',
      name: 'ratline site reload',
      args: '<domain>',
      status: 'built',
      summary: 'Zero-downtime reload where the runtime supports it.',
      description: [
        'Gunicorn can replace its workers without dropping the listening socket, so a python site reloads without a gap. Where the runtime cannot do that, this falls back to a restart and says so rather than pretending otherwise.',
      ],
      exits: [
        { code: 4, reason: 'systemctl reload failed.' },
        { code: 7, reason: 'The reloaded process never became healthy.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline site reload api.example.com' }],
    },
    {
      id: 'site-deploy',
      name: 'ratline site deploy',
      args: '<domain>',
      status: 'built',
      summary:
        'Run the pull → install → build → migrate → collectstatic → restart chain, health-check it, and roll back on failure.',
      description: [
        'With no flags, deploy runs the chain configured for the site. Each flag turns on one step explicitly, which is what you want in CI where the steps should not change because someone edited the site’s configuration.',
        'The previous release stays addressable for the duration, and a failed post-deploy health check reverts to it. Installs run as the site user, never as root.',
      ],
      flags: [
        { name: '--pull', type: 'bool', default: 'false', description: 'git fetch and checkout the configured branch.' },
        { name: '--install', type: 'bool', default: 'false', description: 'Run the install command as the site user.' },
        { name: '--build', type: 'bool', default: 'false', description: 'Run the build command.' },
        {
          name: '--migrate',
          type: 'bool',
          default: 'false',
          description: 'Run Django migrations. Requires a detected manage.py.',
        },
        {
          name: '--collectstatic',
          type: 'bool',
          default: 'false',
          description: 'Run Django collectstatic into --static-dir.',
        },
        { name: '--restart', type: 'bool', default: 'false', description: 'Restart the unit at the end and wait for health.' },
      ],
      exits: [
        { code: 3, reason: 'No such site, or --migrate was asked for on a site with no manage.py.' },
        { code: 4, reason: 'git, npm, pip or the build command failed. Raw output is included.' },
        { code: 5, reason: 'Locked.' },
        { code: 7, reason: 'The health check failed after the restart; the previous release was restored.' },
      ],
      examples: [
        {
          title: 'A Django deploy',
          lang: 'shell',
          code: 'ratline site deploy api.example.com --pull --install --migrate --collectstatic --restart',
        },
        {
          title: 'A Node deploy',
          lang: 'shell',
          code: 'ratline site deploy app.example.com --pull --install --build --restart',
        },
      ],
      seeAlso: [{ label: 'Debugging a 502', to: '/guides/debug-502' }],
    },
    {
      id: 'site-logs',
      name: 'ratline site logs',
      args: '<domain>',
      status: 'built',
      summary: 'Application, access or error logs for one site.',
      description: [
        'Logs live at /home/<user>/<domain>/logs/{app,access,error}.log, mode 0640 owned by <user>:adm — which is how an operator in the adm group reads them without being root and without the tenant being able to rewrite history.',
      ],
      flags: [
        { name: '--app', type: 'bool', description: 'The application’s own log (journal plus app.log).' },
        { name: '--access', type: 'bool', description: 'The nginx access log.' },
        { name: '--error', type: 'bool', description: 'The nginx error log.' },
        { name: '--follow', type: 'bool', default: 'false', description: 'Stream new lines as they arrive.' },
        { name: '--lines', arg: '<n>', type: 'int', default: '100', description: 'How many lines of history to show.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline site logs api.example.com --error --lines 50' },
        { lang: 'shell', code: 'ratline site logs api.example.com --app --follow' },
      ],
    },
    {
      id: 'site-scale',
      name: 'ratline site scale',
      args: '<domain>',
      status: 'built',
      summary: 'Change worker count, instance count or cgroup limits.',
      description: [
        'This is the command `site add` points you at when you re-run it with different resource parameters. The unit is re-rendered, verified and restarted rather than rewritten by hand.',
        'A gunicorn worker change is the one case that costs no requests: the master holds the socket and re-forks to the new count on SIGHUP. Everything else restarts, including any change to the cgroup limits — a reload would not re-read those.',
      ],
      flags: [
        { name: '--workers', arg: '<n>', type: 'int', description: 'Gunicorn worker processes.' },
        {
          name: '--instances',
          arg: '<n>',
          type: 'int',
          description: 'PM2 cluster workers on a node site.',
          note: 'All the workers share the one socket inside the one unit, so this changes concurrency without adding a unit or an nginx upstream. Refused where nothing can fan out — a direct-supervised node site, or a python site.',
        },
        {
          name: '--memory-max',
          arg: '<size>',
          type: 'size',
          default: 'defaults.memory_max (512M)',
          description: 'systemd MemoryMax.',
          note: 'MemoryHigh is set to defaults.memory_high_ratio (0.875) of this, so the kernel starts reclaiming before it starts killing.',
        },
        {
          name: '--cpu-quota',
          arg: '<percent>',
          type: 'percent',
          default: 'defaults.cpu_quota (100%)',
          description: 'systemd CPUQuota. Over 100% means more than one core.',
          note: '0% is refused: it would stop the application entirely.',
        },
      ],
      exits: [
        { code: 2, reason: 'A size or percentage failed validation.' },
        { code: 4, reason: 'systemd-analyze verify or systemctl failed; the previous unit was restored.' },
        { code: 7, reason: 'The restarted app never became healthy.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline site scale api.example.com --workers 4 --memory-max 1G' },
        { lang: 'shell', code: 'ratline site scale app.example.com --instances 2 --cpu-quota 150%' },
      ],
    },
    {
      id: 'site-env',
      name: 'ratline site env set|get|unset|list|import',
      args: '<domain>',
      status: 'built',
      summary: 'Manage the site’s .env, which systemd loads as root before dropping privileges.',
      description: [
        '.env lives at /home/<user>/<domain>/.env, mode 0600 owned by the site user, and is loaded by systemd’s EnvironmentFile= — read as root before privileges are dropped, which is why the file can be 0600 and the app can still see it. It is never inside a directory nginx can serve, and nginx additionally denies dotfiles.',
        'Values are masked in `env list` unless --reveal, and redacted in logs and errors. Names must match ^[A-Za-z_][A-Za-z0-9_]*$.',
        '`env set` takes KEY=VALUE, and also a bare KEY — which it asks for, without echoing what you type. Prefer the bare form for anything secret: KEY=VALUE puts the value in argv, world-readable through /proc/PID/cmdline for as long as the command runs, and then in your shell history, which outlives the secret. KEY=VALUE is still the clearer thing to write for LOG_LEVEL=info.',
      ],
      flags: [
        {
          name: '--reveal',
          type: 'bool',
          default: 'false',
          description: '`env list` only: print values instead of masking them.',
        },
        {
          name: '--file',
          arg: '<path>',
          type: 'path',
          description: '`env import` only: the .env file to read.',
        },
        {
          name: '--stdin',
          type: 'bool',
          default: 'false',
          description: 'Read a value from stdin rather than argv.',
          note: 'Secrets never go in argv, where they would be visible in the process list and recorded in the audit log.',
        },
      ],
      refuses: [
        'A value containing a newline: systemd’s EnvironmentFile cannot represent multi-line values. Store the payload in a file inside the site directory and point a variable at it.',
        'A value containing a NUL byte, or over 32768 bytes.',
        'LD_PRELOAD, LD_LIBRARY_PATH, LD_AUDIT and DYLD_INSERT_LIBRARIES: they change how the runtime itself loads code, which is a foot-gun rather than a feature.',
      ],
      exits: [
        { code: 2, reason: 'The variable name or value failed validation.' },
        { code: 3, reason: 'No such site, or `env get` on a name that is not set.' },
      ],
      examples: [
        {
          title: 'A secret: name it, and paste the value at the prompt',
          lang: 'shell',
          code: `ratline site env set api.example.com DATABASE_URL
DATABASE_URL (not echoed): ▏`,
        },
        {
          lang: 'shell',
          title: 'Not a secret: KEY=VALUE is clearer',
          code: `ratline site env set api.example.com LOG_LEVEL=info
ratline site env list api.example.com
ratline site env list api.example.com --reveal`,
        },
        {
          title: 'A secret, out of argv',
          lang: 'shell',
          code: 'ratline site env set api.example.com SECRET_KEY --stdin < /run/secrets/key',
        },
        {
          title: 'Import a whole file',
          lang: 'shell',
          code: 'ratline site env import api.example.com --file ./.env.production',
        },
      ],
    },
    {
      id: 'site-health',
      name: 'ratline site health',
      args: '[domain...]',
      status: 'built',
      summary: 'Ask each site whether it is actually answering.',
      description: [
        'Makes an HTTP request through the site’s own socket or port and records the result, so “is it up” and “since when” both have answers that do not depend on somebody watching. A timer runs it every five minutes.',
        'This is a different question from the rest of doctor. A unit can be perfectly active while the application inside it returns 500 to every request: systemd is happy, nginx is happy, the socket is connectable, and every visitor gets an error page. Nothing noticed that before, because nothing asked.',
        'A 5xx counts as failing. A 4xx does not — a site whose root path legitimately answers 401 or 404 is answering correctly, and treating that as down would make this useless for anything behind authentication.',
        'Static sites are skipped, having no application to ask; disabled sites are skipped, being meant to return 503.',
        'Exits 7 — the same code a deploy uses for “it started but never answered” — when any site is failing, so it works directly as a monitor check.',
      ],
      examples: [
        { lang: 'shell', code: `ratline site health
ratline site health app.example.com
ratline site health --quiet || alert` },
      ],
      seeAlso: [
        { label: 'Health checks', to: '/topics/health' },
        { label: 'ratline doctor', to: '/reference/ops/doctor' },
      ],
    },
    {
      id: 'site-hook',
      name: 'ratline site hook',
      args: '<set|clear>',
      status: 'built',
      summary: 'Run something of your own before or after a deploy.',
      description: [
        'A deploy was a fixed chain — pull, install, build, migrate, restart — so anything site-specific had nowhere to go and ended up in a wrapper script that reimplemented the chain badly.',
        'The pre-deploy hook runs after the pull and before install and build. After the pull deliberately: a hook script lives in the repository, so running it earlier would run the previous deploy’s version of it. A failing pre-deploy hook stops the deploy before anything restarts, so the previous version keeps serving.',
        'The post-deploy hook runs once the site is up and has answered a health check. A failing one reports and exits non-zero but does not roll back: the site is already serving the new code, and reverting it because a notification failed would be worse than the failure.',
        'Both run as the tenant, in the application directory, with the site’s environment — the same conditions as the build command. RATLINE_HOOK and RATLINE_DOMAIN are set, so one script can serve both.',
      ],
      flags: [
        { name: '--before', arg: '<path…>', type: 'string',
          description: 'Run this after the pull, before install and build.' },
        { name: '--after', arg: '<path…>', type: 'string',
          description: 'Run this once the site is up and answering.',
          note: 'Nothing is passed to a shell, so this is an argv: a pipe or redirection is refused rather than handed to the program as arguments.' },
      ],
      examples: [
        { lang: 'shell', code: `ratline site hook set app.example.com --after …/bin/smoke-test
ratline site hook clear app.example.com --after` },
      ],
      seeAlso: [{ label: 'Deploys', to: '/topics/deploys' }],
    },
    {
      id: 'site-clone',
      name: 'ratline site clone',
      args: '<source-domain> <new-domain>',
      status: 'built',
      summary: 'Copy a site’s configuration to a new domain.',
      description: [
        'Every setting the source has, on a new domain: runtime, versions, commands, limits, deploy hooks, and its jobs and workers. Standing up staging by hand means reading `site show` and retyping fifteen flags — and the value of staging is that it is the same as production, while a copy made by hand differs in the one setting somebody forgot.',
        'Three things are deliberately not faithful. Aliases are not copied, because a hostname can only belong to one site and nginx resolves a clash by whichever vhost it read first. Jobs and workers come across switched off, because a staging copy of a nightly job that emails customers should not fire tonight from a server nobody is watching. And TLS is off, because the new domain has no certificate and DNS may not point here yet.',
        'It composes the same commands `ratline new` and `ratline import` do, so a clone cannot develop its own idea of what a site is — and `--dry-run` prints the plan.',
      ],
      flags: [
        { name: '--user', arg: '<tenant>', type: 'string',
          description: 'Tenant to own the copy (default: the source’s).' },
        { name: '--with-files', type: 'bool', default: 'false',
          description: 'Also clone the repository and install and build it.',
          note: 'Clones the repository the source deploys from, at the source’s branch. It does not copy the source’s working tree.' },
        { name: '--with-db', type: 'bool', default: 'false',
          description: 'Also create an empty database and attach it.',
          note: 'Empty, deliberately. Copying the data is `db dump` then `db restore --into <name> --drop`, which it prints.' },
        { name: '--db-name', arg: '<name>', type: 'string', description: 'Name for that database.' },
        { name: '--start', type: 'bool', default: 'false', description: 'Start the copy once it is built.' },
      ],
      examples: [
        { lang: 'shell', code: `ratline site clone app.example.com staging.example.com
ratline site clone app.example.com staging.example.com --with-files --start` },
      ],
      seeAlso: [{ label: 'ratline new', to: '/reference/new' }],
    },
    {
      id: 'site-cron',
      name: 'ratline site cron',
      args: '<add|list|remove|run|logs>',
      status: 'built',
      summary: 'Scheduled jobs for a site, as systemd timers.',
      description: [
        'A job runs on a schedule as the site’s tenant, in the site’s directory, with the site’s .env, sandbox and memory ceiling.',
        'These are systemd timers rather than crontab lines. A crontab line runs outside every limit the site is held to — no memory ceiling, no filesystem protection, no cgroup — and nothing in status, doctor or export knows it is there. A job is on all three: counted by `status`, listed by `site show`, and reported by `doctor` when its last run failed.',
        'Schedules may be written as cron or in systemd’s own syntax. Either way systemd is asked to confirm the result before anything is written, and the next few run times are printed so a translation can be checked.',
      ],
      flags: [
        { name: '--schedule', arg: '<expr>', type: 'string', required: true,
          description: 'When to run: cron (`0 3 * * *`) or systemd (`daily`, `Mon *-*-* 09:00`).',
          note: 'Cron treats day-of-month and day-of-week as “either” when both are set, which a timer cannot express — that one case is refused rather than translated wrongly. @reboot is refused too: a timer fires on a clock, and the answer there is a worker.' },
        { name: '--command', arg: '<path…>', type: 'string', required: true,
          description: 'What to run, as a path and arguments.',
          note: 'systemd parses this itself, so it is an argv rather than a shell line. A pipe or a redirection is refused; anything needing one belongs in a script.' },
        { name: '--persistent', type: 'bool', default: 'false',
          description: 'Run a firing that was missed while the server was off.',
          note: 'cron has no equivalent: a nightly job on a machine that was down at 3am simply does not run, and nothing says so.' },
        { name: '--timeout', arg: '<duration>', type: 'string', description: 'Give up after this long, e.g. 30m.' },
        { name: '--memory-max', arg: '<size>', type: 'string', description: 'Memory ceiling for this job (default: the site’s).' },
        { name: '--disabled', type: 'bool', default: 'false', description: 'Create it without arming the timer.' },
      ],
      examples: [
        { lang: 'shell', code: `ratline site cron add app.example.com nightly \\
    --schedule '0 3 * * *' --command /home/acme/app.example.com/app/bin/nightly` },
        { title: 'Find out it works, rather than waiting until 3am', lang: 'shell',
          code: `ratline site cron run app.example.com nightly
ratline site cron logs app.example.com nightly` },
      ],
      seeAlso: [
        { label: 'Scheduled jobs and workers', to: '/topics/jobs' },
        { label: 'ratline site worker', to: '/reference/site/worker' },
      ],
    },
    {
      id: 'site-worker',
      name: 'ratline site worker',
      args: '<add|list|remove|logs>',
      status: 'built',
      summary: 'Long-running background processes for a site.',
      description: [
        'A queue consumer, a websocket process, a scheduler daemon — running alongside the site’s own service as the same tenant, with the same directory, .env, sandbox and ceiling.',
        'It is bound to the site: stopping the site stops its workers, and deleting the site removes them. A worker left running against a half-removed site is how a queue gets consumed by a process nobody remembers starting.',
      ],
      flags: [
        { name: '--command', arg: '<path…>', type: 'string', required: true,
          description: 'What to run, as a path and arguments.' },
        { name: '--memory-max', arg: '<size>', type: 'string', description: 'Memory ceiling for this worker (default: the site’s).' },
        { name: '--disabled', type: 'bool', default: 'false', description: 'Create it without starting it.' },
      ],
      examples: [
        { lang: 'shell', code: `ratline site worker add app.example.com queue \\
    --command /home/acme/app.example.com/app/bin/worker` },
      ],
      seeAlso: [
        { label: 'Scheduled jobs and workers', to: '/topics/jobs' },
        { label: 'ratline site cron', to: '/reference/site/cron' },
      ],
    },
    {
      id: 'site-alias',
      name: 'ratline site alias add|remove',
      args: '<domain> <alias>',
      status: 'built',
      summary: 'Add or remove an additional server_name.',
      description: [
        'Adding an alias changes the vhost, not the certificate. A new name will not be covered by TLS until you re-issue: `cert issue <domain>` picks the site’s aliases up as its default SAN list.',
      ],
      exits: [
        { code: 2, reason: 'The alias failed domain validation.' },
        { code: 3, reason: 'Another vhost already claims that server_name.' },
        { code: 4, reason: 'nginx -t failed; the previous vhost was restored.' },
      ],
      examples: [
        {
          lang: 'shell',
          code: `ratline site alias add example.com www.example.com
ratline cert issue example.com   # aliases become SANs`,
        },
      ],
    },
    {
      id: 'site-runtime',
      name: 'ratline site runtime',
      args: '<domain>',
      status: 'built',
      summary: 'Move a site onto a different managed Node or Python version, or a different process manager.',
      description: [
        'The unit’s ExecStart is re-rendered against the new absolute interpreter path, the venv is rebuilt for a Python change, and the app is restarted and health-checked. The version must already be installed — see `ratline runtime install`.',
        '`--daemon` moves a node site between PM2 and direct systemd supervision. The change is not only a restart: the unit changes shape, because a PM2 unit is Type=forking with a PIDFile and an ExecStop that a direct unit does not have.',
        'The old supervisor is stopped first, using the unit that is still on disk. Only the PM2 unit carries ExecStop=pm2 kill, so re-rendering before stopping would leave the PM2 daemon and its workers alive until the kill timeout — still holding the socket the replacement is about to bind.',
      ],
      flags: [
        { name: '--node', arg: '<version>', type: 'version', description: 'Target Node version.' },
        { name: '--python', arg: '<version>', type: 'version', description: 'Target Python version.' },
        {
          name: '--daemon',
          arg: '<pm2|direct>',
          type: 'enum',
          description: 'node only: move this site to PM2 or to direct systemd supervision.',
        },
        {
          name: '--relax',
          arg: '<directive>',
          type: 'string',
          description: 'Turn off a named systemd hardening directive for this site. Repeatable.',
        },
      ],
      exits: [
        { code: 2, reason: 'The version string failed validation, --daemon was not pm2 or direct, or --daemon was passed to a site that is not node.' },
        { code: 3, reason: 'That runtime version is not installed, or PM2 is not installed for it.' },
        { code: 7, reason: 'The app did not become healthy on the new runtime; the previous unit was restored.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline site runtime app.example.com --node 22' },
        { lang: 'shell', code: 'ratline site runtime app.example.com --daemon direct' },
      ],
      seeAlso: [{ label: 'Node sites and PM2', to: '/guides/node' }],
    },
    {
      id: 'site-troubleshoot',
      name: 'ratline site troubleshoot',
      args: '<domain>',
      status: 'built',
      summary: 'Walk one site’s request path and stop at the first thing that is broken.',
      description: [
        'It exists because doctor answers the wrong question when a specific site is down. doctor sweeps the whole server and reports findings in whatever order its checks run, which leaves you with a list rather than a cause.',
        'A request arrives at nginx, is proxied to a socket, and is answered by a process — so checking in that order means the first failure *is* the cause, and everything after it is a consequence not worth printing as a separate problem. Steps after the failure are marked as not checked rather than reported as independent findings.',
        'Two of the checks cannot be done any other way. It makes a real HTTP request straight to the application, bypassing nginx, which distinguishes "the application is broken" from "nginx cannot reach a working application". Then it makes the request a visitor would, over the loopback with the site’s Host header, which is the same path minus the network.',
        'It changes nothing, so it is safe to run against a production site that is currently on fire.',
      ],
      refuses: [
        'Nothing — but it needs root, because the socket and the unit are not readable otherwise, and a check that silently could not look would be worse than one that refuses.',
      ],
      exits: [
        { code: 0, reason: 'The walk completed, whether or not it found a failure. The verdict is in the output.' },
        { code: 3, reason: 'Not root, or no such site.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline site troubleshoot app.example.com' },
        {
          title: 'The silent 502, found and named',
          lang: 'text',
          code: `app.example.com

  ok    enabled
  ok    nginx configuration  —  /etc/nginx/sites-available/app.example.com.conf
  ok    nginx accepts the configuration
  ok    site directory  —  /home/acme/app.example.com
  ok    systemd unit  —  active, pid 41822
  ok    pm2 workers  —  4 online
  FAIL  socket permissions  —  the socket is mode 0640; nginx needs 0660 to connect,
        so every request is a 502
  --    the application answers  —  not checked: an earlier step has to pass first

Likely cause: the socket is mode 0640; nginx needs 0660 to connect, so every
              request is a 502
Try:          ratline site restart app.example.com; the full story is in
              'ratline explain sockets'`,
        },
        {
          title: 'Just the cause, for a script',
          lang: 'shell',
          code: `ratline site troubleshoot app.example.com --json | jq -r '.data.likely_cause'`,
        },
      ],
      seeAlso: [
        { label: 'ratline doctor', to: '/reference/ops/doctor' },
        { label: 'ratline status', to: '/reference/ops/status' },
      ],
      keywords: ['502', 'bad gateway', 'debug', 'broken', 'down', 'diagnose', 'socket', 'eacces', 'why'],
    },
  ],
};
