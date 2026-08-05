import { users } from './commands/users';
import { keys } from './commands/keys';
import { sites } from './commands/sites';
import { certs } from './commands/certs';
import { databases } from './commands/databases';
import { runtimes } from './commands/runtimes';
import { ops } from './commands/ops';
import type { CommandGroup } from './types';

export const commandGroups: CommandGroup[] = [users, keys, sites, certs, runtimes, databases, ops];

export const groupByPath = new Map(commandGroups.map((g) => [g.path, g]));

export interface NavItem {
  label: string;
  to: string;
  /** One line, used as the description in search results and index cards. */
  blurb?: string;
  /** Extra terms the search index matches on. See searchKeywords below. */
  keywords?: string[];
}

export interface NavSection {
  title: string;
  items: NavItem[];
}

/**
 * Search terms per page, kept separate from the nav so the nav stays readable.
 *
 * A concept or guide page is not findable by its title alone: nobody searches
 * for "the Cloudflare orange-cloud trap" — they search for "orange cloud",
 * "proxied", or "http-01 failing". These are the words someone types when they
 * have the problem rather than the vocabulary.
 */
const searchKeywords: Record<string, string[]> = {
  '/': ['what is ratline', 'overview', 'ploi', 'runcloud', 'dokku', 'vps', 'ubuntu', 'scope'],
  '/quickstart': [
    'install', 'getting started', 'first site', 'go build', 'ratline init', 'https', 'setup',
  ],
  '/reference': ['all commands', 'verbs', 'groups', 'index', 'status', 'built', 'planned'],
  '/concepts/model': [
    'object model', 'request path', 'slug', 'unit name', 'socket path', 'static node python',
    'containment', 'group per user',
  ],
  '/concepts/ssh-scopes': [
    'global user site', 'authorized_keys', 'restrict', 'forced command', 'ratline-shell',
    'blast radius', 'kernel boundary', 'allow-shell', 'ed25519', 'ssh-dss', 'lockout',
  ],
  '/concepts/tls-lifecycle': [
    'certificate', 'acme', 'letsencrypt', 'certbot', 'http-01', 'dns-01', 'webroot', 'san',
    'wildcard', 'attach', 'renew', 'degraded', 'orphaned', 'preflight', 'sni',
  ],
  '/concepts/rate-limits': [
    'letsencrypt limits', 'duplicate certificate', 'failed validation', 'registered domain',
    'etld+1', 'staging', 'budget', 'retry-after',
  ],
  '/concepts/transactions': [
    'rollback', 'atomic rename', 'nginx -t', 'systemd-analyze verify', 'flock', 'lock',
    'idempotent', 'already configured', 'drift', 'doctor', 'reconcile', 'umask',
  ],
  '/concepts/filesystem': [
    'paths', 'permissions', '0750', '0600', '0640', 'www-data group', 'environmentfile', 'socket',
    'runtimedirectory', 'logrotate', 'state.db', 'audit log', 'custom nginx',
  ],
  '/concepts/supervision': [
    'systemd', 'unit', 'hardening', 'protecthome', 'protectsystem', 'systemcallfilter',
    'memorymax', 'cpuquota', 'instances', 'workers', 'template unit', 'ratline.target', 'relax',
    'health check',
  ],
  '/concepts/security': [
    'threat model', 'argv', 'no shell', 'euid 0', 'root', 'shared kernel', 'isolation limits',
    'secrets', 'redaction', 'sudo', 'destructive confirmation',
  ],
  '/concepts/interactive': [
    'wizard', 'prompt', 'tty', 'no-input', 'ci', 'hung build', 'typed confirmation', 'no_color',
    'term=dumb', 'equivalent command',
  ],
  '/reference/global-flags': [
    '--json', '--quiet', '--verbose', '--dry-run', '--yes', '--interactive', '--no-input',
    '--config', 'ratline_config', 'contradict',
  ],
  '/reference/exit-codes': [
    'status codes', 'rlerr', 'usage', 'precondition', 'locked', 'rollback_failed',
    'health_check_failed', 'acme_challenge_failed', 'rate_limited', 'input_required',
  ],
  '/reference/json': [
    'envelope', 'stdout', 'machine readable', 'jq', 'automation', 'error payload', 'hint', 'fields',
  ],
  '/reference/validation': [
    'regex', 'username rule', 'domain rule', 'app module', 'path containment', 'symlink',
    'shell words', 'slug', 'sockaddr_un', 'fingerprint', 'cidr', 'expiry',
  ],
  '/reference/config': [
    'config.yaml', 'defaults.yaml', 'settings', 'defaults', 'timeouts', 'rate_limits', 'paths',
  ],
  '/guides/fastapi': [
    'python', 'asgi', 'gunicorn', 'uvicorn', 'uvicornworker', 'django', 'wsgi', 'app-module',
    'venv', 'workers', 'collectstatic', 'migrate',
  ],
  '/guides/nextjs': [
    'node', 'next.js', 'standalone', 'server.js', '_next/static', 'build memory', 'oom',
    'instances', 'npm ci',
  ],
  '/guides/astro': [
    'static', 'vite', 'hugo', 'eleventy', 'spa', 'try_files', 'dist', 'build-output', 'caching',
    'asset_max_age',
  ],
  '/guides/contractor-access': [
    'site scope', 'sftp', 'rsync', 'expires', 'from cidr', 'contractor', 'agency', 'designer',
    'rsync-only', 'key audit',
  ],
  '/guides/new-laptop-key': [
    'rotate key', 'add key', 'remove key', 'revoke', 'lost laptop', 'from-github', 'global scope',
    'verify login',
  ],
  '/guides/ci-deploy-keys': [
    'ci', 'github actions', 'deploy key', 'inbound', 'outbound', 'private repo', 'read-only',
    'ssh-keyscan', 'rotate',
  ],
  '/guides/issue-cert': [
    'dns moved', 'preflight', 'dry-run', 'cutover', 'no-attach', 'self-signed', 'ttl',
    'www cname', 'openssl s_client',
  ],
  '/guides/cloudflare': [
    'orange cloud', 'grey cloud', 'proxied', 'origin certificate', 'full strict',
    'http-01 fails', 'dns-01', 'fastly', 'akamai', 'waf', 'proxy',
  ],
  '/guides/renewal-runbook': [
    'renewal failed', 'expiring', 'degraded', 'redirect swallowing', 'well-known', 'port 80',
    'two timers', 'deploy hook', 'unattached-mismatch', 'expired',
  ],
  '/guides/ssh-lockout': [
    'locked out', 'permission denied publickey', 'console', 'serial console', 'recovery',
    'bad ownership or modes', 'sshd -t', 'reload not restart', 'key sync',
  ],
  '/guides/debug-502': [
    '502', '504', '413', '403', 'bad gateway', 'upstream', 'socket', 'permission denied', 'oom',
    'protecthome', 'curl unix-socket', 'journalctl', 'troubleshoot', 'eacces', 'empty log',
  ],
  '/guides/node': [
    'pm2', 'cluster mode', 'graceful reload', 'zero downtime', 'daemon', 'direct', 'ecosystem',
    'pm2_home', 'wait_ready', 'instances', 'restart count', 'node_env', 'type=forking',
    'memorydenywriteexecute', 'jit', 'with-pm2', 'app.log',
  ],
  '/guides/inherited-server': [
    'status', 'explain', 'troubleshoot', 'doctor', 'inventory', 'what is on this server',
    'took over', 'handover', 'first look', 'overview', 'completion', 'tab completion',
    'read-only', 'dry-run',
  ],
};

const rawNav: NavSection[] = [
  {
    title: 'Start here',
    items: [
      { label: 'What ratline is', to: '/', blurb: 'The scope of the tool, and what it deliberately is not.' },
      {
        label: '60-second quickstart',
        to: '/quickstart',
        blurb: 'Install, a user, a site, working HTTPS — for each of the three runtimes.',
      },
      {
        label: 'Command surface at a glance',
        to: '/reference',
        blurb: 'Every group, every verb, with build status.',
      },
    ],
  },
  {
    title: 'Concepts',
    items: [
      {
        label: 'Users, sites and runtimes',
        to: '/concepts/model',
        blurb: 'The object model, and the request path from browser to application.',
      },
      {
        label: 'The three SSH scopes',
        to: '/concepts/ssh-scopes',
        blurb: 'Global, user, site — and honestly what site scope does not enforce.',
      },
      {
        label: 'TLS resource lifecycle',
        to: '/concepts/tls-lifecycle',
        blurb: 'Issue, attach, renew, and the HTTP-01 vs DNS-01 decision.',
      },
      {
        label: 'Rate limits',
        to: '/concepts/rate-limits',
        blurb: 'The CA’s published limits, tracked locally and budgeted before acting.',
      },
      {
        label: 'Staged, verified, committed',
        to: '/concepts/transactions',
        blurb: 'The transaction model and the rollback stack.',
      },
      {
        label: 'Filesystem and permissions',
        to: '/concepts/filesystem',
        blurb: 'Every path ratline owns, and why the home stays 0750.',
      },
      {
        label: 'Process supervision',
        to: '/concepts/supervision',
        blurb: 'One systemd unit per site, with hardening verified at install time.',
      },
      {
        label: 'Security model',
        to: '/concepts/security',
        blurb: 'What is enforced, and where the isolation stops.',
      },
      {
        label: 'Interactive mode',
        to: '/concepts/interactive',
        blurb: 'When the wizard appears, when it must never appear.',
      },
    ],
  },
  {
    title: 'Command reference',
    items: [
      {
        label: 'Global flags',
        to: '/reference/global-flags',
        blurb: 'Flags every command accepts, and the combinations that are refused.',
      },
      {
        label: 'Exit codes',
        to: '/reference/exit-codes',
        blurb: 'The contract automation branches on.',
      },
      {
        label: 'JSON envelope',
        to: '/reference/json',
        blurb: 'One object on stdout, success or failure.',
      },
      ...commandGroups.map((g) => ({ label: g.title, to: g.path, blurb: g.blurb })),
      {
        label: 'Validation rules',
        to: '/reference/validation',
        blurb: 'The rules the code actually enforces.',
      },
      {
        label: 'Configuration',
        to: '/reference/config',
        blurb: 'Every setting, its default, and why.',
      },
    ],
  },
  {
    title: 'Guides — running sites',
    items: [
      {
        label: 'Node sites and PM2',
        to: '/guides/node',
        blurb: 'Why PM2 supervises by default, what it costs, and how to turn it off.',
      },
      {
        label: 'FastAPI behind Gunicorn',
        to: '/guides/fastapi',
        blurb: 'An ASGI app on a Unix socket, with static files served by nginx.',
      },
      {
        label: 'Next.js standalone',
        to: '/guides/nextjs',
        blurb: 'A standalone build behind nginx, with _next/static bypassed.',
      },
      {
        label: 'An Astro static build',
        to: '/guides/astro',
        blurb: 'No unit, no socket, nothing running.',
      },
    ],
  },
  {
    title: 'Guides — access and TLS',
    items: [
      {
        label: 'Contractor on one site',
        to: '/guides/contractor-access',
        blurb: 'Site-scoped SSH, expiring, source-restricted.',
      },
      {
        label: 'A key from a new laptop',
        to: '/guides/new-laptop-key',
        blurb: 'Add, verify, then remove the old one — in that order.',
      },
      {
        label: 'CI deploy keys, both ways',
        to: '/guides/ci-deploy-keys',
        blurb: 'Inbound push access and outbound repo access are different things.',
      },
      {
        label: 'Issue a cert after DNS moves',
        to: '/guides/issue-cert',
        blurb: 'The normal order of operations, and what preflight checks.',
      },
      {
        label: 'The Cloudflare orange cloud',
        to: '/guides/cloudflare',
        blurb: 'Why HTTP-01 fails behind a proxy, and the three ways out.',
      },
    ],
  },
  {
    title: 'Runbooks — when it breaks',
    items: [
      {
        label: 'A server you did not set up',
        to: '/guides/inherited-server',
        blurb: 'status, doctor, troubleshoot, explain — in that order, changing nothing.',
      },
      {
        label: 'Debugging a 502',
        to: '/guides/debug-502',
        blurb: 'Six causes, in order of likelihood.',
      },
      {
        label: 'My cert didn’t renew',
        to: '/guides/renewal-runbook',
        blurb: 'A runbook, in the order that finds it fastest.',
      },
      {
        label: 'I’m locked out of SSH',
        to: '/guides/ssh-lockout',
        blurb: 'Console-only recovery. Read before you need it.',
      },
    ],
  },
];

export const nav: NavSection[] = rawNav.map((section) => ({
  ...section,
  items: section.items.map((item) => ({ ...item, keywords: searchKeywords[item.to] })),
}));

/** Flat lookup of every page, for search and for prev/next. */
export const allNavItems: NavItem[] = nav.flatMap((s) => s.items);
