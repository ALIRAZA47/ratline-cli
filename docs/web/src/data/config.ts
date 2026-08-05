import type { SettingSection } from './types';

/**
 * Generated from `internal/config/defaults.yaml`.
 *
 * That file is both the source of every built-in default and the commented
 * reference an operator reads, which is why the comments are carried across here
 * as prose rather than paraphrased. Values are shown exactly as the YAML writes
 * them. Anything the operator does not want to override can be deleted from
 * their own file.
 */

export const configPreamble = [
  'ratline reads /etc/ratline/config.yaml on every invocation, so there is nothing to reload. Values shown here are what it uses when the setting is absent, which means an operator’s file only needs to contain what they are actually changing.',
  'The path can be overridden with --config or the RATLINE_CONFIG environment variable, in that order of precedence. A missing file is not an error: the built-in defaults are used, and mutating commands warn that `ratline init` has not run.',
];

export const configSections: SettingSection[] = [
  {
    key: 'version',
    title: 'version',
    blurb:
      'Schema version. Bumped only when a change needs a migration rather than a merge.',
    settings: [{ key: 'version', value: '1', type: 'int' }],
  },
  {
    key: 'server',
    title: 'server',
    blurb: 'Facts about this host that are expensive to detect.',
    settings: [
      {
        key: 'server.hostname',
        value: '""',
        type: 'string',
        note: 'Left empty, this is detected once and cached in state. Setting it explicitly is useful on a host whose public address differs from any locally configured one — behind NAT, or on a provider with a floating IP.',
      },
      {
        key: 'server.public_ipv4',
        value: '[]',
        type: 'list of string',
        note: 'Same rule as hostname. These are the addresses `cert issue` preflight compares the domain’s A records against, so on a NAT’d or floating-IP host, setting them is the difference between a correct refusal and a wrong one.',
      },
      { key: 'server.public_ipv6', value: '[]', type: 'list of string' },
      {
        key: 'server.admin_user',
        value: '""',
        type: 'string',
        note: 'The account that holds global-scope SSH keys. Empty means the account that ran `ratline init`.',
      },
    ],
  },
  {
    key: 'paths',
    title: 'paths',
    blurb: 'Every filesystem location ratline owns or reads.',
    settings: [
      { key: 'paths.state_db', value: '/var/lib/ratline/state.db', type: 'path' },
      { key: 'paths.audit_log', value: '/var/log/ratline/audit.log', type: 'path' },
      { key: 'paths.lock', value: '/run/ratline.lock', type: 'path' },
      { key: 'paths.run_dir', value: '/run/ratline', type: 'path' },
      { key: 'paths.home_base', value: '/home', type: 'path' },
      { key: 'paths.nginx_sites_available', value: '/etc/nginx/sites-available', type: 'path' },
      { key: 'paths.nginx_sites_enabled', value: '/etc/nginx/sites-enabled', type: 'path' },
      { key: 'paths.nginx_snippets', value: '/etc/nginx/ratline', type: 'path' },
      {
        key: 'paths.nginx_custom',
        value: '/etc/nginx/ratline/custom',
        type: 'path',
        note: 'Anything an operator puts here is included by the generated vhost and is never regenerated, so hand-written additions survive `ratline reconcile`.',
      },
      { key: 'paths.systemd_dir', value: '/etc/systemd/system', type: 'path' },
      { key: 'paths.logrotate_dir', value: '/etc/logrotate.d', type: 'path' },
      {
        key: 'paths.acme_webroot',
        value: '/var/www/ratline-acme',
        type: 'path',
        note: 'One shared HTTP-01 webroot for every site, served before any redirect so renewal never depends on an application being up.',
      },
      { key: 'paths.letsencrypt_dir', value: '/etc/letsencrypt', type: 'path' },
      { key: 'paths.imported_certs', value: '/etc/ratline/certs', type: 'path' },
      { key: 'paths.dns_credentials', value: '/etc/ratline/dns', type: 'path' },
      { key: 'paths.ssh_dir', value: '/etc/ratline/ssh', type: 'path' },
      { key: 'paths.sshd_dropin', value: '/etc/ssh/sshd_config.d/60-ratline.conf', type: 'path' },
      { key: 'paths.runtimes_dir', value: '/opt/ratline/runtimes', type: 'path' },
      { key: 'paths.shell_wrapper', value: '/usr/local/lib/ratline/ratline-shell', type: 'path' },
      { key: 'paths.backup_dir', value: '/var/backups/ratline', type: 'path' },
    ],
  },
  {
    key: 'defaults',
    title: 'defaults',
    blurb: 'Values a site inherits unless it says otherwise.',
    settings: [
      { key: 'defaults.shell', value: '/bin/bash', type: 'path' },
      { key: 'defaults.umask', value: '"0027"', type: 'string' },
      {
        key: 'defaults.client_max_body_size',
        value: '20M',
        type: 'size',
        note: 'The most common cause of a mystery 413.',
      },
      {
        key: 'defaults.health_timeout',
        value: '30s',
        type: 'duration',
        note: 'How long `site start` waits for an application to answer a real request before calling the deploy a failure.',
      },
      {
        key: 'defaults.lock_timeout',
        value: '30s',
        type: 'duration',
        note: 'How long a command waits for another ratline invocation to release the lock.',
      },
      { key: 'defaults.proxy_read_timeout', value: '60s', type: 'duration' },
      { key: 'defaults.restart_sec', value: '3s', type: 'duration' },
      { key: 'defaults.stop_timeout', value: '30s', type: 'duration' },
      { key: 'defaults.memory_max', value: '512M', type: 'size' },
      {
        key: 'defaults.memory_high_ratio',
        value: '0.875',
        type: 'float',
        note: 'MemoryHigh is set to this fraction of MemoryMax, so the kernel starts reclaiming before it starts killing.',
      },
      { key: 'defaults.cpu_quota', value: '100%', type: 'percent' },
      { key: 'defaults.tasks_max', value: '256', type: 'int' },
      { key: 'defaults.limit_nofile', value: '8192', type: 'int' },
      {
        key: 'defaults.worker_cap',
        value: '8',
        type: 'int',
        note: 'Ceiling for the automatic (2 × cores) + 1 worker calculation.',
      },
      {
        key: 'defaults.hsts',
        value: 'false',
        type: 'bool',
        note: 'HSTS is opt-in. Enabling it for one site can break a tenant’s unrelated subdomains, and that decision is not ratline’s to make. ratline refuses to enable it on a self-signed or staging certificate.',
      },
      { key: 'defaults.hsts_max_age', value: '31536000', type: 'int (seconds)' },
    ],
  },
  {
    key: 'users',
    title: 'users',
    blurb: 'Tenant account policy.',
    settings: [
      {
        key: 'users.reserved',
        value: '[]',
        type: 'list of string',
        note: 'Extends the built-in reserved list; both are checked.',
      },
      {
        key: 'users.allow_sudo',
        value: 'false',
        type: 'bool',
        note: 'Created users get no sudo. Turning this on only permits the escape hatch to exist; each grant is still validated with visudo -c.',
      },
      {
        key: 'users.quota_enabled',
        value: 'false',
        type: 'bool',
        note: 'Requires the filesystem to be mounted with quota support. ratline refuses --quota rather than silently ignoring it when this is false.',
      },
      { key: 'users.nginx_user', value: 'www-data', type: 'string' },
      { key: 'users.log_group', value: 'adm', type: 'string' },
      {
        key: 'users.home_mode',
        value: '"0750"',
        type: 'string (octal)',
        note: 'Homes stay 0750 and nginx is added to the user’s group. Never 0755.',
      },
      { key: 'users.site_mode', value: '"0750"', type: 'string (octal)' },
    ],
  },
  {
    key: 'ssh',
    title: 'ssh',
    blurb: 'Key policy, algorithm policy, and the lockout safeguards.',
    settings: [
      { key: 'ssh.min_rsa_bits', value: '3072', type: 'int' },
      { key: 'ssh.warn_rsa_bits', value: '4096', type: 'int' },
      {
        key: 'ssh.allowed_algorithms',
        value: `ssh-ed25519
sk-ssh-ed25519@openssh.com
ecdsa-sha2-nistp256
ecdsa-sha2-nistp384
ecdsa-sha2-nistp521
sk-ecdsa-sha2-nistp256@openssh.com
rsa-sha2-256
rsa-sha2-512
ssh-rsa`,
        type: 'list of string',
      },
      {
        key: 'ssh.rejected_algorithms',
        value: 'ssh-dss',
        type: 'list of string',
        note: 'ssh-dss is refused outright; 1024-bit DSA has no place on a new server.',
      },
      { key: 'ssh.max_key_line_bytes', value: '8192', type: 'int (bytes)' },
      { key: 'ssh.max_authorized_keys_bytes', value: '262144', type: 'int (bytes)' },
      {
        key: 'ssh.allow_root_keys',
        value: 'false',
        type: 'bool',
        note: 'Keys are never added to root’s authorized_keys unless this is true and the operator asks for it explicitly.',
      },
      {
        key: 'ssh.site_scope_sftp_only',
        value: 'true',
        type: 'bool',
        note: 'Site-scoped keys get sftp, rsync and git only. --allow-shell overrides it per key, with a warning.',
      },
      {
        key: 'ssh.prune_expired',
        value: 'true',
        type: 'bool',
        note: 'A daily timer removes keys past their expiry, whether or not sshd supports the expiry-time option.',
      },
      {
        key: 'ssh.command_presets',
        value: `sftp-only: internal-sftp
rsync-only: rsync
git-only: git`,
        type: 'map',
      },
      { key: 'ssh.revoked_keys', value: '/etc/ratline/ssh/revoked_keys', type: 'path' },
      { key: 'ssh.global_keys_file', value: '/etc/ratline/ssh/global.authorized_keys', type: 'path' },
      {
        key: 'ssh.verify_after_change',
        value: 'true',
        type: 'bool',
        note: 'After changing anything under /etc/ssh, prove that login still works and roll back if it does not. Turning this off on a remote server is how people lock themselves out.',
      },
      {
        key: 'ssh.usage_scan_enabled',
        value: 'true',
        type: 'bool',
        note: 'Scan the auth log for accepted-publickey lines to populate last-used data.',
      },
      { key: 'ssh.key_fetch_timeout', value: '15s', type: 'duration' },
      { key: 'ssh.max_fetched_key_bytes', value: '65536', type: 'int (bytes)' },
    ],
  },
  {
    key: 'nginx',
    title: 'nginx',
    blurb: 'What the generated vhosts turn on.',
    settings: [
      { key: 'nginx.reload_timeout', value: '30s', type: 'duration' },
      { key: 'nginx.gzip', value: 'true', type: 'bool' },
      {
        key: 'nginx.brotli',
        value: 'false',
        type: 'bool',
        note: 'Only rendered when the brotli module is present.',
      },
      { key: 'nginx.server_tokens', value: 'false', type: 'bool' },
      {
        key: 'nginx.asset_max_age',
        value: '31536000',
        type: 'int (seconds)',
        note: 'Cache lifetime in seconds for content-hashed assets.',
      },
    ],
  },
  {
    key: 'runtimes',
    title: 'runtimes',
    blurb: 'Managed Node and Python versions.',
    settings: [
      {
        key: 'runtimes.node_default',
        value: '""',
        type: 'string',
        note: 'Empty until `ratline runtime install` or `ratline init` has run.',
      },
      { key: 'runtimes.python_default', value: '""', type: 'string' },
      { key: 'runtimes.node_mirror', value: 'https://nodejs.org/dist', type: 'url' },
      { key: 'runtimes.install_timeout', value: '30m', type: 'duration' },
      { key: 'runtimes.build_timeout', value: '20m', type: 'duration' },
    ],
  },
  {
    key: 'acme',
    title: 'acme',
    blurb: 'Certificate authority settings and the locally tracked rate-limit budget.',
    settings: [
      {
        key: 'acme.email',
        value: '""',
        type: 'string',
        note: 'ACME contact address. Certificate expiry warnings go here.',
      },
      {
        key: 'acme.directory_url',
        value: 'https://acme-v02.api.letsencrypt.org/directory',
        type: 'url',
      },
      {
        key: 'acme.staging_url',
        value: 'https://acme-staging-v02.api.letsencrypt.org/directory',
        type: 'url',
      },
      { key: 'acme.key_type', value: 'ecdsa', type: 'enum' },
      { key: 'acme.renew_before_days', value: '30', type: 'int (days)' },
      { key: 'acme.dns_propagation_seconds', value: '60', type: 'int (seconds)' },
      {
        key: 'acme.tos_agreed',
        value: 'false',
        type: 'bool',
        note: 'Set by `ratline init` once the operator has accepted the CA’s terms.',
      },
      { key: 'acme.preflight_timeout', value: '20s', type: 'duration' },
      { key: 'acme.issue_timeout', value: '5m', type: 'duration' },
      {
        key: 'acme.rate_limits.certs_per_registered_domain_per_week',
        value: '50',
        type: 'int',
        note: 'The certificate authority’s published limits, tracked locally so ratline can refuse an attempt with a countdown instead of discovering the limit the hard way. These are policy and do change: check the CA’s documentation if a refusal looks wrong.',
      },
      { key: 'acme.rate_limits.duplicate_certs_per_week', value: '5', type: 'int' },
      { key: 'acme.rate_limits.failed_validations_per_hour', value: '5', type: 'int' },
      { key: 'acme.rate_limits.new_orders_per_3_hours', value: '300', type: 'int' },
      { key: 'acme.alerts.webhook_url', value: '""', type: 'url' },
      { key: 'acme.alerts.email', value: '""', type: 'string' },
      {
        key: 'acme.alerts.warn_days',
        value: '7',
        type: 'int (days)',
        note: 'Warn when a certificate has fewer than this many days left.',
      },
    ],
  },
  {
    key: 'ports',
    title: 'ports',
    blurb: 'The TCP allocation window.',
    settings: [
      {
        key: 'ports.range_start',
        value: '20000',
        type: 'int',
        note: 'Allocation window for node sites that listen on TCP rather than a socket.',
      },
      { key: 'ports.range_end', value: '29999', type: 'int' },
    ],
  },
  {
    key: 'logging',
    title: 'logging',
    blurb: 'Log level and colour.',
    settings: [
      {
        key: 'logging.level',
        value: 'info',
        type: 'enum: debug | info | warn | error',
        note: 'A --verbose or --quiet flag beats this file; otherwise the file decides.',
      },
      {
        key: 'logging.color',
        value: 'auto',
        type: 'enum: auto | always | never',
        note: 'auto means colour when stderr is a terminal. NO_COLOR and TERM=dumb turn it off regardless, and --json turns it off unconditionally.',
      },
    ],
  },
  {
    key: 'databases',
    title: 'databases',
    blurb: 'MongoDB provisioning: the role a new user gets, and how long an operation may take.',
    settings: [
      {
        key: 'databases.mongodb.default_role',
        value: 'readWrite',
        type: 'enum',
        note: 'Granted to a user created without --role. readWrite rather than dbOwner: an application reads and writes its own collections, and does not need to create users or drop the database it lives in.',
      },
      {
        key: 'databases.mongodb.env_key',
        value: 'MONGODB_URI',
        type: 'string',
        note: 'The variable a connection string is written to in a site’s .env.',
      },
      {
        key: 'databases.mongodb.timeout',
        value: '30s',
        type: 'duration',
        note: 'One mongosh invocation. A managed cluster behind an access list does not refuse a connection, it hangs — so this is what turns that into an error naming the access list rather than a command that never returns.',
      },
      {
        key: 'databases.mongodb.initial_collection',
        value: 'ratline',
        type: 'string',
        note: 'Created so a new database is visible to `db list`. MongoDB has no createDatabase; a database exists once something is written into it.',
      },
    ],
  },
  {
    key: 'features',
    title: 'features',
    blurb: 'Switches for things that are not finished, or are off for a reason.',
    settings: [
      {
        key: 'features.db_provisioning',
        value: 'false',
        type: 'bool',
        note: 'Turns `ratline db` on. Off by default because it needs a MongoDB server and an admin connection string at paths.mongo_uri_file, and a command that cannot work is better hidden than offered. `ratline db enable` sets it.',
      },
      {
        key: 'features.strict_isolation',
        value: 'false',
        type: 'bool',
        note: 'Adds a chroot and a bind mount to site-scoped SSH keys. Off by default because a misconfigured chroot generates support tickets.',
      },
    ],
  },
];
