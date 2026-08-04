import type { CommandGroup } from '../types';

export const ops: CommandGroup = {
  id: 'ops',
  title: 'Operations',
  path: '/reference/ops',
  blurb: 'Version, man pages, completion, doctor, reconcile, backup, export, init.',
  intro: [
    'The commands that are about the server rather than about one tenant. Three of them are built today — `version`, `man` and `completion` — which is also why the output on this page is real output rather than an illustration.',
  ],
  commands: [
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
