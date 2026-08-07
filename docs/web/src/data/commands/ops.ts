import type { CommandGroup } from '../types';

export const ops: CommandGroup = {
  id: 'ops',
  title: 'Operations',
  path: '/reference/ops',
  blurb: 'status, doctor, explain, init, reconcile, backup, export, version, man.',
  intro: [
    'The commands that are about the server rather than about one tenant.',
    'Two of them answer questions that look similar and are not. `status` always prints the inventory and marks what needs attention — it says what is here. `doctor` runs every check and prints only what is wrong, so on a healthy server it prints nothing — it says what is broken. doctor is what a cron job runs; status is what a human runs first. The problem count in status comes from doctor itself rather than a second implementation, so the two can never disagree.',
    '`explain` is the third: longer-form answers than a help page can carry, embedded in the binary so they work over SSH on a server with no browser and no network.',
  ],
  commands: [
    {
      id: 'update',
      name: 'ratline update',
      status: 'built',
      summary: 'Update ratline itself, in place, on a server that is serving traffic.',
      description: [
        'One command. The success case is a file copy, so the whole design is the refusals.',
        'The download is checksummed against the release’s own SHA256SUMS, and a release with no checksum file is a refusal rather than a warning — an unverified binary installed as root on a server holding every tenant’s keys is the same supply-chain hole the runtime installer already declines to leave open.',
        'The new binary is then executed from a staging directory and asked its version, which catches the wrong architecture before it goes near the install path; and asked to list this server’s sites against the real database, which is what catches a downgrade past a schema migration. That would otherwise install cleanly and fail on the next command you ran.',
        'The install is an atomic rename per file within one filesystem, so a timer firing mid-update sees the old inode or the new one and never a partial file. The previous binary is kept beside it for --rollback.',
        'No site is interrupted. Sites are systemd units running an interpreter; they do not exec this binary. ratline-shell is the one exception, because forced commands in authorized_keys point at it by absolute path — which is why it is verified and swapped the same way.',
      ],
      refuses: [
        'A release with no SHA256SUMS, unless --allow-unverified is given deliberately.',
        'A downloaded binary that does not run here, or reports a different version from the one requested.',
        'A binary that cannot read this server’s state — the signature of a downgrade past a migration.',
        'Overwriting a file dpkg owns. Doing so leaves the package database lying and the next apt upgrade silently reverts the update, so it names the apt command instead.',
      ],
      flags: [
        { name: '--check', type: 'bool', default: 'false', description: 'Report whether an update is available and change nothing.' },
        { name: '--version', arg: '<version>', type: 'string', description: 'Install this release rather than the latest.' },
        { name: '--rollback', type: 'bool', default: 'false', description: 'Restore the binary the last update replaced.' },
        {
          name: '--base-url',
          arg: '<url>',
          type: 'url',
          description: 'Where release artefacts live.',
          note: 'For a server with no route to GitHub, which is normal. Checksum verification is unchanged, so a mirror is not a place to be trusted blindly.',
        },
        {
          name: '--allow-unverified',
          type: 'bool',
          default: 'false',
          description: 'Install even when the release publishes no checksums.',
          note: 'Deliberately awkward. Verification is the default and should stay that way.',
        },
      ],
      exits: [
        { code: 0, reason: 'Updated, already current, or --check completed.' },
        { code: 3, reason: 'Not root, or the binary belongs to a package manager.' },
        { code: 4, reason: 'The download failed, or did not match the published checksum.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline update' },
        { lang: 'shell', code: 'ratline update --check' },
        { lang: 'shell', code: 'ratline update --rollback' },
        {
          title: 'A server that cannot reach GitHub',
          lang: 'shell',
          code: 'ratline update --base-url https://mirror.example.internal/ratline --version 1.2.0',
        },
      ],
      seeAlso: [{ label: 'Upgrading', to: '/reference/ops/version' }],
      keywords: ['upgrade', 'self-update', 'new version', 'rollback', 'downgrade', 'checksum', 'mirror'],
    },
    {
      id: 'troubleshoot',
      name: 'ratline troubleshoot',
      args: '[subject]',
      status: 'built',
      summary: 'Diagnose anything ratline manages, stopping at the first failure — which is the cause.',
      description: [
        'The causal half of the diagnostics. doctor sweeps the server and reports what is wrong, in whatever order its checks happen to run; that is right for a cron job and it leaves a human with a list of five findings to rank themselves.',
        'This takes one subject and walks its preconditions in the order they depend on each other. Because the order is a dependency order, the first failure *is* the cause — and the steps it broke are reported as not-checked, naming the step that has to pass first, rather than appearing as more problems competing for attention.',
        'The subject is worked out from the argument: a domain is a site, a bare name is a tenant, SHA256:… is a key, and "nginx", "ssh" and "server" name the subsystems. With no argument it diagnoses the host, which is the right first question when several things are wrong at once — a skewed clock, a full disk or a missing binary explains a dozen downstream symptoms. A name that is genuinely ambiguous is reported as such rather than guessed at.',
        'Every subject is the same shape of question, which is why one engine covers all of them. A site: nginx, directories, unit, workers, socket, the application, nginx end to end, TLS, DNS. A tenant: account, home mode, ownership, shell, authorized_keys, key sync, its sites, quota. A key: revoked, expired, scope target, installed, file permissions, revocation list, whether sshd reads that file, and what the key can actually do. A certificate: files, permissions, parse, validity, renewal history, attachment, and whether it is the certificate actually being served.',
        'Read-only, and it never takes the lock — so it is safe against a site that is currently on fire.',
      ],
      flags: [
        {
          name: '--all',
          type: 'bool',
          default: 'false',
          description: 'Show every step, not only the ones that need attention.',
          note: 'Passing steps are folded into a count by default, because on a broken subject the answer is the last line and a dozen ok rows push it off the screen.',
        },
        {
          name: '--kind',
          arg: '<server|site|user|key|certificate|nginx|ssh>',
          type: 'enum',
          description: 'Say what the subject is when the name is ambiguous.',
          note: 'Needed only when a name is both a tenant and a certificate lineage. A certificate named after its own site resolves to the site, because the certificate is one of that site’s checks.',
        },
        {
          name: '--probe-timeout',
          arg: '<duration>',
          type: 'duration',
          default: '3s',
          description: 'How long any single network probe may take.',
          note: 'One knob for the DNS lookup, the request to the application, the request through nginx and the TLS handshake — because what matters is how long the whole diagnosis takes, and a dozen independent five-second waits turns it into a minute of silence on exactly the broken host where somebody is waiting.',
        },
      ],
      exits: [
        { code: 0, reason: 'The walk completed, whether or not it found a failure. The verdict is in the output.' },
        { code: 2, reason: 'The subject is ambiguous, or --kind was not one of the seven.' },
        { code: 3, reason: 'Not root, or nothing on this server goes by that name.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline troubleshoot app.example.com' },
        {
          title: 'The silent 502, found and named',
          lang: 'text',
          code: `app.example.com  —  node, owned by acme

  FAIL  the application is listening where nginx expects  —  the socket is mode
        0640; nginx needs 0660 to connect, so every request is a 502
  --    the application answers a request  —  not checked: listening has to pass first
  warn  a current certificate is attached  —  6 days left
  ok    5 checks passed

Likely cause: the socket is mode 0640; nginx needs 0660 to connect, so every
              request is a 502
Try:          ratline site restart app.example.com
Background:   ratline explain sockets`,
        },
        {
          title: 'Anything, not only a site',
          lang: 'shell',
          code: `ratline troubleshoot acme                 # a tenant
ratline troubleshoot SHA256:AbC...       # can this key log in, and to what
ratline troubleshoot nginx
ratline troubleshoot ssh                 # including the lockout guard
ratline troubleshoot                     # the host`,
        },
        {
          title: 'Just the cause, for a script',
          lang: 'shell',
          code: `ratline troubleshoot app.example.com --json | jq -r '.data.likely_cause'
ratline troubleshoot app.example.com --json | jq '.data.steps[] | select(.verdict=="failed")'`,
        },
      ],
      seeAlso: [
        { label: 'ratline doctor', to: '/reference/ops/doctor' },
        { label: 'ratline status', to: '/reference/ops/status' },
      ],
      keywords: [
        '502', 'bad gateway', 'broken', 'down', 'debug', 'diagnose', 'why', 'cause',
        'root cause', 'socket', 'eacces', 'permission denied publickey', 'lockout',
        'dependency order', 'first failure',
      ],
    },
    {
      id: 'status',
      name: 'ratline status',
      status: 'built',
      summary: 'The whole server on one screen: tenants, sites, their state, certificates and a problem count.',
      description: [
        'Written to be the first command you run on a server you have not touched in a month. It always prints, which is the difference between it and doctor: a healthy server still needs an inventory, and doctor on a healthy server prints nothing at all.',
        'Sites that need attention are marked. A disabled site counts as needing attention, because nginx is not serving it and that is almost never what someone reading this screen expects.',
        'The restart count shown for a node site is PM2\'s, not systemd\'s. Under PM2 systemd\'s own counter stays at zero because PM2 does the restarting, so reading systemd\'s number would report a crash-looping application as healthy.',
      ],
      flags: [
        {
          name: '--quiet',
          type: 'bool',
          default: 'false',
          description: 'Only the summary counts, without the per-site table.',
        },
      ],
      exits: [
        { code: 0, reason: 'Always, whether or not problems were found. The count is in the output; use doctor for a non-zero exit.' },
        { code: 3, reason: 'Not root. The state database is 0600 and the sockets are not readable otherwise.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline status' },
        {
          title: 'A server with two things wrong',
          lang: 'text',
          code: `web-1.example.net — ratline 1.0.0
Ubuntu 24.04.1 LTS, up 41d 6h

3 tenants, 5 sites, 7 SSH keys, 4 certificates

    DOMAIN                OWNER   RUNTIME  STATE     TLS                 NOTE
    www.example.com       acme    static   serving   https
    app.example.com       acme    node     running   https               4 workers
  ! api.example.com       acme    python   failed    https
    blog.example.org      beta    static   serving   https (expiring)
  ! stage.example.org     beta    node     running   http                2 of 4 workers online

Certificates needing attention:
  blog.example.org                         expiring, 6 days left

2 problems found. See them with 'ratline doctor'.`,
        },
        {
          title: 'Only the sites that need attention',
          lang: 'shell',
          code: `ratline status --json | jq '.data.sites_detail[] | select(.needs_attention)'`,
        },
      ],
      seeAlso: [
        { label: 'ratline doctor', to: '/reference/ops/doctor' },
        { label: 'ratline site troubleshoot', to: '/reference/site/troubleshoot' },
      ],
      keywords: ['overview', 'inventory', 'dashboard', 'summary', 'what is on this server', 'uptime'],
    },
    {
      id: 'explain',
      name: 'ratline explain',
      args: '[topic]',
      status: 'built',
      summary: 'Longer-form answers than a help page can carry, built into the binary.',
      description: [
        'An operator meets ratline over SSH on a server they just built: no browser, no manual pages beyond the one ratline installs, and this site is on a machine they are not looking at. `--help` answers "what are the flags", which is the wrong shape for "why does my socket 502" or "what does PM2 actually buy me".',
        'The pages are embedded at build time, so this works with no network. They are the same markdown files this site renders, so the binary and the website can never give different answers.',
        'Run without a topic to list them. A mistyped or approximate name is corrected rather than refused: `explain 502` finds the diagnosis page, `explain cert` finds the TLS page.',
      ],
      flags: [
        {
          name: '--raw',
          type: 'bool',
          default: 'false',
          description: 'Print the markdown source instead of formatting it for a terminal.',
        },
      ],
      refuses: [
        'Nothing. It needs neither root nor a configuration file nor a state database, so documentation is readable before `ratline init` has ever run and by a tenant with an account but no privileges.',
      ],
      exits: [
        { code: 0, reason: 'The topic was printed, or the list was printed.' },
        { code: 2, reason: 'No such topic. The nearest match, or the full list, is in the hint.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline explain' },
        { lang: 'shell', code: 'ratline explain sockets' },
        { lang: 'shell', code: 'ratline explain node | less' },
        {
          title: 'The topic list',
          lang: 'text',
          code: `TOPIC     WHAT IT COVERS
deploys   What \`site deploy\` does, in what order, and what happens when a step fails.
diagnose  The order to check things in, and which command answers each question.
layout    The filesystem layout: what ratline creates, and where to look for it.
limits    What stops one site from taking down the server.
node      How a node site is supervised, why PM2 is the default, and when to turn it off.
python    Gunicorn, a per-site virtualenv, and the WSGI/ASGI choice.
safety    What ratline promises about running twice, failing halfway, and what it refuses.
sockets   Why a node or python site listens on a Unix socket, and the one permission
          mistake that turns every request into a 502 with nothing in the log.
ssh       Three scopes, what each one means, and why a key is never trusted as submitted.
state     What ratline records, where, and how to get it back.
static    nginx serving files directly, with no process to supervise.
tls       TLS is a separate resource, on purpose.

Read one with 'ratline explain <topic>'.`,
        },
      ],
      keywords: ['docs', 'documentation', 'help', 'concepts', 'why', 'offline', 'manual', 'topics'],
    },
    {
      id: 'version',
      name: 'ratline version',
      status: 'built',
      summary:
        'Version, commit, build date, OS, nginx version and available runtimes — enough for a bug report.',
      description: [
        'Deliberately more than a version string. Almost every report that starts "it does not work" is answered by one of these lines: the wrong OS, no nginx, no certbot, no systemd, or no runtimes installed. Printing them together means a single paste contains the whole answer.',
        'This is one of the few commands that runs without root.',
      ],
      exits: [{ code: 0, reason: 'Always.' }],
      examples: [
        { lang: 'shell', code: 'ratline version' },
        {
          title: 'Real output on a machine with nothing installed',
          lang: 'text',
          code: `ratline dev commit=none built=unknown darwin/arm64 go1.26.5
os               darwin
config           /etc/ratline/config.yaml (not present; using built-in defaults)
nginx            not installed
certbot          not installed
openssh          10.2p1
systemd          not installed
node runtimes    none installed
python runtimes  none installed`,
        },
        {
          title: 'The same thing, as the JSON envelope',
          lang: 'json',
          code: `{
  "ok": true,
  "command": "ratline version",
  "version": "dev",
  "data": {
    "version": "dev",
    "commit": "none",
    "build_date": "unknown",
    "go": "go1.26.5",
    "platform": "darwin/arm64",
    "os": "darwin",
    "os_supported": false,
    "openssh": "10.2p1",
    "config": "/etc/ratline/config.yaml",
    "config_loaded": false
  }
}`,
        },
      ],
      keywords: ['bug report', 'diagnostics', 'buildinfo'],
    },
    {
      id: 'man',
      name: 'ratline man',
      status: 'built',
      summary: 'Write man pages for every command.',
      description: [
        'Generates one roff page per command. With no --dir the top-level page goes to stdout, which is handy for previewing.',
      ],
      flags: [
        {
          name: '--dir',
          arg: '<path>',
          type: 'path',
          default: 'stdout',
          description: 'Directory to write pages into.',
        },
      ],
      examples: [
        {
          title: 'Preview without writing anything',
          lang: 'shell',
          code: 'ratline man | man -l -',
        },
        {
          title: 'Install them',
          lang: 'shell',
          code: 'ratline man --dir /usr/local/share/man/man1',
        },
      ],
    },
    {
      id: 'completion',
      name: 'ratline completion',
      args: 'bash|zsh|fish|powershell',
      status: 'built',
      summary: 'Generate the shell completion script.',
      examples: [
        {
          lang: 'shell',
          code: `ratline completion bash > /etc/bash_completion.d/ratline
ratline completion zsh  > "\${fpath[1]}/_ratline"`,
        },
      ],
    },
    {
      id: 'doctor',
      name: 'ratline doctor',
      args: '[subject]',
      status: 'built',
      summary:
        'The sweep: nginx -t, failed units, dead sockets, certificate expiry, orphaned configs, drift, permission anomalies, unused ports. With a subject, the causal walk instead.',
      description: [
        'The read-only sweep. It changes nothing, and it is the first thing to run when something is wrong and the second thing to run after anything unusual — a manual edit, a reboot, a restore from backup.',
        'Two of its checks matter more than the rest. State-vs-filesystem drift catches the vhost someone edited by hand and the unit someone disabled with systemctl; it is what turns "mysteriously different behaviour" into a line of output. Permission anomalies catch the home that became 0755 and the .env that became 0644 — both of which are silent until they are not.',
        'A degraded certificate — one whose last renewal failed — surfaces here too.',
        'Given a subject, it runs the dependency-ordered walk instead — the same engine as `ratline troubleshoot`, so the two can never disagree about the same server. The sweep says what is wrong across the box; the walk says why one thing is. When the sweep’s findings are mostly about one resource, it says so and names the walk to run next.',
      ],
      flags: [
        {
          name: '--all',
          type: 'bool',
          default: 'false',
          description: 'With a subject: show every step, not only the ones that need attention.',
        },
      ],
      exits: [
        { code: 0, reason: 'No findings, or findings reported. A finding is information, not a failure.' },
        { code: 3, reason: 'Reserved for a check that could not run at all, for example nginx not being installed.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline doctor' }],
      seeAlso: [{ label: 'Transactional behaviour', to: '/concepts/transactions' }],
      keywords: ['diagnose', 'drift', 'health', 'orphaned', 'permissions'],
    },
    {
      id: 'reconcile',
      name: 'ratline reconcile',
      status: 'built',
      summary: 'Re-render every config from state; report the differences, or repair them.',
      description: [
        'Where `doctor` reports, `reconcile` acts. State is authoritative and every config is derived from it, so re-rendering is always safe in principle — but it also overwrites hand edits, which is why the report comes first and --fix is separate.',
        'Operator additions under /etc/nginx/ratline/custom/<domain>.conf are included by the generated vhost and never regenerated. That directory is the supported way to add something ratline does not model; anything you put directly in the generated vhost will be lost here.',
      ],
      flags: [
        {
          name: '--fix',
          type: 'bool',
          default: 'false',
          description: 'Actually re-render and reload, rather than reporting the differences.',
        },
      ],
      exits: [
        { code: 4, reason: 'nginx -t failed after re-rendering; the previous configs were restored.' },
        { code: 5, reason: 'Locked.' },
        { code: 6, reason: 'A repair failed and could not be undone.' },
      ],
      examples: [
        {
          lang: 'shell',
          code: `ratline reconcile              # report only
ratline reconcile --fix --dry-run
ratline reconcile --fix`,
        },
      ],
    },
    {
      id: 'backup',
      name: 'ratline backup',
      args: '<user|domain>',
      status: 'built',
      summary: 'Back up one tenant or one site.',
      flags: [
        { name: '--out', arg: '<dir>', type: 'path', required: true, description: 'Destination directory.' },
      ],
      description: [
        'The default destination in configuration is paths.backup_dir (/var/backups/ratline), but --out is required here so that a backup never lands somewhere you did not choose.',
      ],
      examples: [
        { lang: 'shell', code: 'ratline backup acme --out /var/backups/ratline' },
        { lang: 'shell', code: 'ratline backup example.com --out /var/backups/ratline' },
      ],
      seeAlso: [{ label: 'ratline restore', to: '/reference/ops/restore' }],
    },
    {
      id: 'restore',
      name: 'ratline restore',
      args: '<archive.tar.gz>',
      status: 'built',
      summary: 'Put a backup archive back, and rebuild what serves it.',
      description: [
        'The archive is a directory — code, logs, .env, and for a site its manifest. It does not contain the state database, the nginx vhost, the systemd unit or the tenant’s uid, so restore rebuilds all four: the state row from the manifest that travelled with the files, the vhost and unit re-rendered from that row, ownership from the account as it exists on *this* server, and a freshly allocated port. Then it starts the service and waits for a real HTTP response.',
        'The owning account has to exist first. An account is a uid, a group, a shell and a set of keys, none of which is in the archive — inventing it would produce a tenant nobody can log in as, owning files whose uid matches nothing.',
        'Restoring a home rebuilds every site inside it. A home full of site directories with no vhosts and no units looks exactly like a successful restore until someone visits one.',
        'Archives are treated as untrusted, because this extracts as root and an archive may have been handed over by whoever is migrating in: an absolute or traversing member is refused rather than sanitised, symlinks are chowned rather than followed, and the manifest’s domain and owner are validated as if typed — with the slug recomputed, since it names the systemd unit.',
      ],
      flags: [
        {
          name: '--force',
          type: 'bool',
          default: 'false',
          description: 'Replace the directory if it already exists.',
          note: 'Confirmed as well as flagged. The previous directory is moved aside and removed only once the state row, the vhost, the unit and the health check have all succeeded — so a failure at any of them puts back what was serving.',
        },
        { name: '--no-start', type: 'bool', default: 'false', description: 'Restore without starting the service.' },
      ],
      exits: [
        { code: 3, reason: 'The archive is unreadable, the owning account does not exist, or the target exists and --force was not given.' },
        { code: 4, reason: 'The archive is not a readable tar, or contains an absolute or traversing path.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline restore /var/backups/ratline/example.com-20260105T120000Z.tar.gz' },
        { lang: 'shell', code: 'ratline restore acme-20260105T120000Z.tar.gz --force' },
        { lang: 'shell', code: 'ratline restore example.com-20260105T120000Z.tar.gz --dry-run' },
      ],
      seeAlso: [{ label: 'Inheriting a server', to: '/guides/inherited-server' }],
    },
    {
      id: 'export',
      name: 'ratline export',
      status: 'built',
      summary: 'Full state dump, for migration to another server.',
      flags: [
        { name: '--json', type: 'bool', required: true, description: 'The output format. Machine-readable by definition.' },
      ],
      description: ['Private key material never appears in the output.'],
      examples: [{ lang: 'shell', code: 'ratline export --json > acme-server-state.json' }],
      seeAlso: [{ label: 'ratline import', to: '/reference/ops/import' }],
    },
    {
      id: 'import',
      name: 'ratline import',
      args: '<file>',
      status: 'built',
      summary: 'Rebuild tenants and sites on this server from an export.',
      description: [
        'Reads what `ratline export` wrote on another server and rebuilds the shape here: the tenants, their SSH keys, their sites with every setting, the aliases, and which sites were disabled.',
        'One transaction — if a step fails, everything it created is removed — and safe to run twice, so a tenant or site that is already there is reported and left alone. A key that was revoked on the old server is not restored: re-adding one would hand back access somebody deliberately took away.',
        'It does not bring the application code, the environment values, the certificates or the database contents, because an export holds none of those. What was left out is listed when it finishes, so a clean exit does not read as a finished migration.',
        'Sites come back with TLS off. The private key was never exported, and issuing a certificate before DNS moves is the one request guaranteed to fail.',
      ],
      flags: [
        { name: '--only', arg: '<tenant,…>', type: 'stringSlice',
          description: 'Import just these tenants, and their sites.',
          note: 'Scopes the keys too: a global key belongs to no tenant, and a site key belongs to whoever owns the site, so neither is in scope when you have named a list.' },
        { name: '--skip-keys', type: 'bool', default: 'false', description: 'Do not restore SSH keys.' },
        { name: '--skip-sites', type: 'bool', default: 'false', description: 'Restore the tenants but not their sites.' },
      ],
      examples: [
        { title: 'The whole thing, over SSH', lang: 'shell',
          code: 'ssh old-server ratline export | ratline import -' },
        { title: 'See the plan first — the resolved commands, with nothing written', lang: 'shell',
          code: 'ratline import server.json --dry-run' },
        { title: 'One tenant', lang: 'shell', code: 'ratline import server.json --only acme' },
      ],
      seeAlso: [
        { label: 'ratline export', to: '/reference/ops/export' },
        { label: 'State, audit and backups', to: '/topics/state' },
      ],
    },
    {
      id: 'init',
      name: 'ratline init',
      status: 'built',
      summary: 'First-run server setup wizard.',
      description: [
        'Writes /etc/ratline/config.yaml, chooses the admin user that will hold global-scope SSH keys, records that you have accepted the CA’s terms (acme.tos_agreed), sets up the shared ACME webroot and the renewal timer, and neutralises certbot’s own timer so the two never race.',
        'Until it has run, mutating commands warn that there is no configuration file and that the built-in defaults are in use.',
      ],
      exits: [
        { code: 3, reason: 'Not running as root, or the binary is group- or world-writable.' },
        { code: 10, reason: 'A prompt was needed and there is no terminal.' },
      ],
      examples: [{ lang: 'shell', code: 'sudo ratline init' }],
      seeAlso: [{ label: 'Configuration reference', to: '/reference/config' }],
    },
  ],
};
