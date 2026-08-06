/**
 * Every hand-written page: its label, its one-line blurb, and the words somebody would
 * search for to reach it.
 *
 * This used to live inside the navigation array, which meant the navigation was both the
 * structure and the content — so moving a page between sections meant moving its prose
 * too, and the search keywords sat in a second parallel object keyed by path. Splitting
 * them lets subjects.ts say only *where* a page belongs and nothing about what it says.
 *
 * A page missing from here still renders; it just gets its path as its label, which is
 * ugly enough to notice. That is deliberate — the alternative is a build that fails on a
 * page somebody added correctly and forgot to describe.
 */

export interface PageMeta {
  label: string;
  blurb: string;
  /**
   * Extra search terms.
   *
   * A concept or guide page is not findable by its title alone: nobody searches for "the
   * Cloudflare orange-cloud trap" — they search for "orange cloud", "proxied", or "http-01
   * failing". These are the words someone types when they have the problem rather than the
   * vocabulary.
   */
  keywords?: string[];
}

export const pageMeta: Record<string, PageMeta> = {
  '/': {
    label: 'What ratline is',
    blurb: 'The scope of the tool, and what it deliberately is not.',
    keywords: ['what is ratline', 'overview', 'ploi', 'runcloud', 'dokku', 'vps', 'ubuntu', 'scope'],
  },
  '/quickstart': {
    label: '60-second quickstart',
    blurb: 'Install, a user, a site, working HTTPS — for each of the three runtimes.',
    keywords: [
      'install', 'getting started', 'first site', 'go build', 'ratline init', 'https', 'setup',
      'one command install', 'curl', 'install.sh',
    ],
  },
  '/releases': {
    label: 'Release notes',
    blurb: 'What changed in each version, and what is still missing.',
    keywords: [
      'changelog', 'release notes', 'whats new', 'what is new', 'versions', 'upgrade',
      'update', 'history', 'v0.1.0', 'v0.2.0', 'v0.3.0', 'v0.4.0', 'v0.5.0', 'v0.6.0', 'v0.6.1', 'v0.7.0', 'v0.8.0', 'v0.9.0', 'v0.9.1',
    ],
  },

  // ── Cross-cutting reference ───────────────────────────────────────────────────────
  '/reference': {
    label: 'Command surface at a glance',
    blurb: 'All 8 groups and 86 verbs, with build status.',
    keywords: ['all commands', 'verbs', 'groups', 'index', 'status', 'built', 'planned'],
  },
  '/reference/global-flags': {
    label: 'Global flags',
    blurb: 'Flags every command accepts, and the combinations that are refused.',
    keywords: [
      '--json', '--quiet', '--verbose', '--dry-run', '--yes', '--interactive', '--no-input',
      '--config', 'ratline_config', 'contradict',
    ],
  },
  '/reference/exit-codes': {
    label: 'Exit codes',
    blurb: 'The contract automation branches on.',
    keywords: [
      'status codes', 'rlerr', 'usage', 'precondition', 'locked', 'rollback_failed',
      'health_check_failed', 'acme_challenge_failed', 'rate_limited', 'input_required',
    ],
  },
  '/reference/json': {
    label: 'JSON envelope',
    blurb: 'One object on stdout, success or failure.',
    keywords: [
      'envelope', 'stdout', 'machine readable', 'jq', 'automation', 'error payload', 'hint', 'fields',
    ],
  },
  '/reference/validation': {
    label: 'Validation rules',
    blurb: 'The rules the code actually enforces.',
    keywords: [
      'regex', 'username rule', 'domain rule', 'app module', 'path containment', 'symlink',
      'shell words', 'slug', 'sockaddr_un', 'fingerprint', 'cidr', 'expiry',
    ],
  },
  '/reference/config': {
    label: 'Every setting',
    blurb: 'All 13 sections of config.yaml, each default, and why it is that.',
    keywords: [
      'config.yaml', 'defaults.yaml', 'settings', 'defaults', 'timeouts', 'rate_limits', 'paths',
    ],
  },
  '/topics': {
    label: 'All topics',
    blurb: 'The 13 long-form pages the binary carries, grouped.',
    keywords: ['explain', 'in depth', 'manual', 'offline docs', 'no browser'],
  },

  // ── Concepts ──────────────────────────────────────────────────────────────────────
  '/concepts/model': {
    label: 'Users, sites and runtimes',
    blurb: 'The object model, and the request path from browser to application.',
    keywords: [
      'object model', 'request path', 'slug', 'unit name', 'socket path', 'static node python',
      'containment', 'group per user',
    ],
  },
  '/concepts/ssh-scopes': {
    label: 'The three SSH scopes',
    blurb: 'Global, user, site — and honestly what site scope does not enforce.',
    keywords: [
      'global user site', 'authorized_keys', 'restrict', 'forced command', 'ratline-shell',
      'blast radius', 'kernel boundary', 'allow-shell', 'ed25519', 'ssh-dss', 'lockout',
    ],
  },
  '/concepts/tls-lifecycle': {
    label: 'TLS resource lifecycle',
    blurb: 'Issue, attach, renew, and the HTTP-01 vs DNS-01 decision.',
    keywords: [
      'certificate', 'acme', 'letsencrypt', 'certbot', 'http-01', 'dns-01', 'webroot', 'san',
      'wildcard', 'attach', 'renew', 'degraded', 'orphaned', 'preflight', 'sni',
    ],
  },
  '/concepts/rate-limits': {
    label: 'Rate limits',
    blurb: 'The CA’s published limits, tracked locally and budgeted before acting.',
    keywords: [
      'letsencrypt limits', 'duplicate certificate', 'failed validation', 'registered domain',
      'etld+1', 'staging', 'budget', 'retry-after',
    ],
  },
  '/concepts/transactions': {
    label: 'Staged, verified, committed',
    blurb: 'The transaction model and the rollback stack.',
    keywords: [
      'rollback', 'atomic rename', 'nginx -t', 'systemd-analyze verify', 'flock', 'lock',
      'idempotent', 'already configured', 'drift', 'doctor', 'reconcile', 'umask',
    ],
  },
  '/concepts/filesystem': {
    label: 'Filesystem and permissions',
    blurb: 'Every path ratline owns, and why the home stays 0750.',
    keywords: [
      'paths', 'permissions', '0750', '0600', '0640', 'www-data group', 'environmentfile', 'socket',
      'runtimedirectory', 'logrotate', 'state.db', 'audit log', 'custom nginx',
    ],
  },
  '/concepts/supervision': {
    label: 'Process supervision',
    blurb: 'One systemd unit per site, with hardening verified at install time.',
    keywords: [
      'systemd', 'unit', 'hardening', 'protecthome', 'protectsystem', 'systemcallfilter',
      'memorymax', 'cpuquota', 'instances', 'workers', 'template unit', 'ratline.target', 'relax',
      'health check',
    ],
  },
  '/concepts/security': {
    label: 'Security model',
    blurb: 'What is enforced, and where the isolation stops.',
    keywords: [
      'threat model', 'argv', 'no shell', 'euid 0', 'root', 'shared kernel', 'isolation limits',
      'secrets', 'redaction', 'sudo', 'destructive confirmation',
    ],
  },
  '/concepts/interactive': {
    label: 'Interactive mode',
    blurb: 'When the wizard appears, when it must never appear.',
    keywords: [
      'wizard', 'prompt', 'tty', 'no-input', 'ci', 'hung build', 'typed confirmation', 'no_color',
      'term=dumb', 'equivalent command',
    ],
  },

  // ── Guides and runbooks ───────────────────────────────────────────────────────────
  '/guides/agents': {
    label: 'Driving ratline from an AI agent',
    blurb: 'A published command contract, and an MCP server that is read-only until you say otherwise.',
    keywords: [
      'ai', 'agent', 'llm', 'mcp', 'model context protocol', 'claude', 'automation',
      'schema', 'machine readable', 'json', 'tool use', 'copilot', 'autonomous deploy',
      'ratline mcp', 'ratline schema',
    ],
  },
  '/guides/github-actions': {
    label: 'Deploy from GitHub Actions',
    blurb: 'A workflow with a key that can run exactly one command as root and nothing else.',
    keywords: [
      'github actions', 'ci', 'cd', 'continuous deployment', 'workflow', 'pipeline',
      'deploy on push', 'deploy on merge', 'secrets', 'ssh key', 'known_hosts',
      'automate deploys', 'gitlab ci', 'sudo grant', 'deploy key',
    ],
  },
  '/guides/deploy-node': {
    label: 'Deploy a Node app, start to finish',
    blurb: 'Bare server to a Next.js app on HTTPS with a database — every command in order.',
    keywords: [
      'deploy', 'next.js', 'nextjs', 'node', 'start to finish', 'end to end', 'first deploy',
      'standalone', 'bin/build', 'rsync', 'npm install', 'devdependencies', 'tailwind',
      'how do i deploy', 'walkthrough', 'tutorial',
    ],
  },
  '/guides/deploy-python': {
    label: 'Deploy a Python app, start to finish',
    blurb: 'Bare server to a FastAPI app on a socket with a database, and the two failures worth knowing.',
    keywords: [
      'deploy', 'fastapi', 'python', 'django', 'start to finish', 'end to end', 'gunicorn',
      'uvicorn', 'venv', 'app-module', 'collectstatic', 'walkthrough', 'tutorial',
      'permission denied socket',
    ],
  },
  '/guides/node': {
    label: 'Node sites and PM2',
    blurb: 'Why PM2 supervises by default, what it costs, and how to turn it off.',
    keywords: [
      'pm2', 'cluster mode', 'graceful reload', 'zero downtime', 'daemon', 'direct', 'ecosystem',
      'pm2_home', 'wait_ready', 'instances', 'restart count', 'node_env', 'type=forking',
      'memorydenywriteexecute', 'jit', 'with-pm2', 'app.log',
    ],
  },
  '/guides/fastapi': {
    label: 'FastAPI behind Gunicorn',
    blurb: 'An ASGI app on a Unix socket, with static files served by nginx.',
    keywords: [
      'python', 'asgi', 'gunicorn', 'uvicorn', 'uvicornworker', 'django', 'wsgi', 'app-module',
      'venv', 'workers', 'collectstatic', 'migrate',
    ],
  },
  '/guides/nextjs': {
    label: 'Next.js standalone',
    blurb: 'A standalone build behind nginx, with _next/static bypassed.',
    keywords: [
      'node', 'next.js', 'standalone', 'server.js', '_next/static', 'build memory', 'oom',
      'instances', 'npm ci',
    ],
  },
  '/guides/astro': {
    label: 'An Astro static build',
    blurb: 'No unit, no socket, nothing running.',
    keywords: [
      'static', 'vite', 'hugo', 'eleventy', 'spa', 'try_files', 'dist', 'build-output', 'caching',
      'asset_max_age',
    ],
  },
  '/guides/debug-502': {
    label: 'Debugging a 502',
    blurb: 'Six causes, in order of likelihood.',
    keywords: [
      '502', '504', '413', '403', 'bad gateway', 'upstream', 'socket', 'permission denied', 'oom',
      'protecthome', 'curl unix-socket', 'journalctl', 'troubleshoot', 'eacces', 'empty log',
    ],
  },
  '/guides/contractor-access': {
    label: 'Contractor on one site',
    blurb: 'Site-scoped SSH, expiring, source-restricted.',
    keywords: [
      'site scope', 'sftp', 'rsync', 'expires', 'from cidr', 'contractor', 'agency', 'designer',
      'rsync-only', 'key audit',
    ],
  },
  '/guides/new-laptop-key': {
    label: 'A key from a new laptop',
    blurb: 'Add, verify, then remove the old one — in that order.',
    keywords: [
      'rotate key', 'add key', 'remove key', 'revoke', 'lost laptop', 'from-github', 'global scope',
      'verify login',
    ],
  },
  '/guides/ci-deploy-keys': {
    label: 'CI deploy keys, both ways',
    blurb: 'Inbound push access and outbound repo access are different things.',
    keywords: [
      'ci', 'github actions', 'deploy key', 'inbound', 'outbound', 'private repo', 'read-only',
      'ssh-keyscan', 'rotate',
    ],
  },
  '/guides/ssh-lockout': {
    label: 'I’m locked out of SSH',
    blurb: 'Console-only recovery. Read before you need it.',
    keywords: [
      'locked out', 'permission denied publickey', 'console', 'serial console', 'recovery',
      'bad ownership or modes', 'sshd -t', 'reload not restart', 'key sync',
    ],
  },
  '/guides/issue-cert': {
    label: 'Issue a cert after DNS moves',
    blurb: 'The normal order of operations, and what preflight checks.',
    keywords: [
      'dns moved', 'preflight', 'dry-run', 'cutover', 'no-attach', 'self-signed', 'ttl',
      'www cname', 'openssl s_client',
    ],
  },
  '/guides/cloudflare': {
    label: 'The Cloudflare orange cloud',
    blurb: 'Why HTTP-01 fails behind a proxy, and the three ways out.',
    keywords: [
      'orange cloud', 'grey cloud', 'proxied', 'origin certificate', 'full strict',
      'http-01 fails', 'dns-01', 'fastly', 'akamai', 'waf', 'proxy',
    ],
  },
  '/guides/renewal-runbook': {
    label: 'My cert didn’t renew',
    blurb: 'A runbook, in the order that finds it fastest.',
    keywords: [
      'renewal failed', 'expiring', 'degraded', 'redirect swallowing', 'well-known', 'port 80',
      'two timers', 'deploy hook', 'unattached-mismatch', 'expired',
    ],
  },
  '/guides/mongodb': {
    label: 'Give a site a database',
    blurb: 'Four commands, two of them once per server — and the password never reaches your scrollback.',
    keywords: [
      'mongodb', 'mongo', 'database', 'atlas', 'managed cluster', 'access list', 'mongodb_uri',
      'connection string', 'db connect', 'db create', 'rotate password', 'readwrite', 'auth',
      'mongosh', 'timeout', 'hangs', 'least privilege',
    ],
  },
  '/guides/inherited-server': {
    label: 'A server you did not set up',
    blurb: 'status, doctor, troubleshoot, explain — in that order, changing nothing.',
    keywords: [
      'status', 'explain', 'troubleshoot', 'doctor', 'inventory', 'what is on this server',
      'took over', 'handover', 'first look', 'overview', 'completion', 'tab completion',
      'read-only', 'dry-run',
    ],
  },
};

/** meta looks a page up, falling back to something visibly wrong rather than crashing. */
export function meta(path: string): PageMeta {
  return pageMeta[path] ?? { label: path, blurb: '' };
}
