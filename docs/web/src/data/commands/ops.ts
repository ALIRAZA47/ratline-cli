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
        { label: 'ratline doctor', to: '/reference/ops#doctor' },
        { label: 'ratline site troubleshoot', to: '/reference/site#site-troubleshoot' },
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
      status: 'built',
      summary:
        'nginx -t, failed units, dead sockets, certificate expiry, orphaned configs, state-vs-filesystem drift, permission anomalies, and ports allocated but unused.',
      description: [
        'The read-only sweep. It changes nothing, and it is the first thing to run when something is wrong and the second thing to run after anything unusual — a manual edit, a reboot, a restore from backup.',
        'Two of its checks matter more than the rest. State-vs-filesystem drift catches the vhost someone edited by hand and the unit someone disabled with systemctl; it is what turns "mysteriously different behaviour" into a line of output. Permission anomalies catch the home that became 0755 and the .env that became 0644 — both of which are silent until they are not.',
        'A degraded certificate — one whose last renewal failed — surfaces here too.',
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
