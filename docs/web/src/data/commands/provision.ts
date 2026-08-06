import type { CommandGroup } from '../types';

export const provision: CommandGroup = {
  id: 'new',
  title: 'One-command stacks',
  path: '/reference/new',
  blurb: 'A tenant, a site, a database and a certificate in a single command.',
  intro: [
    'Everything here is already possible with four commands. The difference is what happens when one of them fails: four commands leave you a tenant and a site and no database, and a command that has already exited. `ratline new` removes everything it created and tells you what it removed.',
    'Each step is the same command you would have typed — the same validation, the same refusals, the same messages — so this cannot develop its own idea of what a node site is, and a flag added to `site add` tomorrow is available here tomorrow. The equivalent commands are printed at the end, because this is a shortcut for the common case rather than a replacement for knowing the tool.',
    'An existing tenant is used as it is, and is not removed if a later step fails: it was not this command’s to create, so it is not its to delete. A certificate is not revoked either — it has already been counted against the rate limit, and throwing it away would cost another one to get back.',
    '`--dry-run` prints the plan: every command with its defaults filled in, in the order they would run. It does not run them, not even as dry runs of their own — each step preconditions on the one before it having really happened, so rehearsing them in order means the site step is told there is no such user, which is not true and never was. What it does check is everything this command decides for you: the domain, the tenant name, the database name derived from the domain. What only the server knows — whether that runtime is installed, whether the domain is taken — is still open. Rehearse a single step with its own `--dry-run`.',
  ],
  commands: [
    {
      id: 'new-node',
      name: 'ratline new node',
      args: '<domain>',
      status: 'built',
      summary: 'A tenant, a Node site and optionally a database, in one command.',
      description: [
        'Defaults suited to a Node application: PM2 supervision, a Unix socket, and the package manager detected from the lockfile.',
        'A framework that cannot bind a socket — Next.js standalone is the common one — needs --listen port.',
      ],
      flags: [
        { name: '--user', arg: '<tenant>', type: 'string', required: true,
          description: 'Tenant that owns this site; created if it does not exist.' },
        { name: '--ssh-key', arg: '<key|path|url>', type: 'string',
          description: 'Public key for the tenant: the key itself, a path, or an https URL.' },
        { name: '--with-db', type: 'bool', default: 'false',
          description: 'Also create a MongoDB database and attach it to the site.',
          note: 'The name is derived from the domain — app.example.com becomes app_example_com — unless --db-name says otherwise. MongoDB forbids a dot in a database name.' },
        { name: '--db-name', arg: '<name>', type: 'string', description: 'Name for that database.' },
        { name: '--db-env-key', arg: '<NAME>', type: 'string',
          description: 'Variable the connection string is written to (default MONGODB_URI).' },
        { name: '--tls', type: 'bool', default: 'false',
          description: 'Also issue a certificate. DNS must already point here.',
          note: 'Issued last, so a certificate failure — the likeliest step to fail, and the one with a rate limit attached — does not take the site down with it.' },
        { name: '--email', arg: '<address>', type: 'string', description: 'ACME contact address, for --tls.' },
        { name: '--node', arg: '<version>', type: 'string', description: 'Managed Node version, e.g. 24.' },
        { name: '--entry', arg: '<file>', type: 'string', default: 'server.js',
          description: 'The file that starts the server.' },
        { name: '--listen', arg: '<socket|port>', type: 'string', description: 'socket (default) or port.' },
        { name: '--build-command', arg: '<cmd>', type: 'string',
          description: 'Build command; a multi-step build belongs in a script.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline new node app.example.com --user acme --with-db' },
        {
          title: 'Next.js standalone, which binds a port rather than a socket',
          lang: 'shell',
          code: `ratline new node app.example.com --user acme --listen port \\
  --entry .next/standalone/server.js --build-command ./bin/build`,
        },
        {
          title: 'See the plan first — the resolved commands, with nothing written',
          lang: 'shell',
          code: 'ratline new node app.example.com --user acme --with-db --dry-run',
        },
      ],
      seeAlso: [{ label: 'Deploy a Node app, start to finish', to: '/guides/deploy-node' }],
    },
    {
      id: 'new-python',
      name: 'ratline new python',
      args: '<domain>',
      status: 'built',
      summary: 'A tenant, a Python site and optionally a database, in one command.',
      description: [
        'Defaults suited to a Python application: a virtualenv, Gunicorn on a Unix socket, and workers derived from the CPU count.',
        '--asgi for FastAPI, Starlette or Django’s asgi.py; leave it off for Flask and for Django’s wsgi.py.',
      ],
      flags: [
        { name: '--app-module', arg: '<module:callable>', type: 'string', required: true,
          description: 'Import path of the callable, e.g. app.main:app.' },
        { name: '--asgi', type: 'bool', default: 'false', description: 'Treat the application as ASGI.' },
        { name: '--python', arg: '<version>', type: 'string', description: 'Managed Python version, e.g. 3.12.' },
        { name: '--manage-py', arg: '<path>', type: 'string',
          description: 'Django manage.py, enabling --migrate and --collectstatic on deploys.' },
        { name: '--workers', arg: '<n>', type: 'int', description: 'Gunicorn workers.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline new python api.example.com --user acme --app-module app.main:app --asgi --with-db' },
        {
          title: 'Django',
          lang: 'shell',
          code: `ratline new python site.example.com --user acme \\
  --app-module project.wsgi:application --manage-py manage.py`,
        },
      ],
      seeAlso: [{ label: 'Deploy a Python app, start to finish', to: '/guides/deploy-python' }],
    },
    {
      id: 'new-static',
      name: 'ratline new static',
      args: '<domain>',
      status: 'built',
      summary: 'A tenant and a static site, in one command.',
      description: [
        'No unit, no socket, nothing running: nginx serves the files and that is all.',
        '--spa serves the index document for unmatched paths, which is what a client-side router needs and what a plain static site must not have.',
      ],
      flags: [
        { name: '--spa', type: 'bool', default: 'false',
          description: 'Serve the index document for unmatched paths.' },
        { name: '--build-output', arg: '<dir>', type: 'string',
          description: 'Directory the build writes, published as the document root.' },
      ],
      refuses: [
        '--with-db. Nothing is running to read the connection string; a static site is files and nginx.',
      ],
      examples: [
        { lang: 'shell', code: 'ratline new static www.example.com --user acme --tls --email ops@example.com' },
      ],
    },
  ],
};
