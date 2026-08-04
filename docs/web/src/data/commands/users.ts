import type { CommandGroup } from '../types';

export const users: CommandGroup = {
  id: 'user',
  title: 'Users',
  path: '/reference/user',
  blurb: 'Tenant sandboxes: a system account, its group, its home and its keys.',
  intro: [
    'A ratline user is a tenant sandbox. Creating one gets you a system account with its own group, a locked password, a 0750 home directory and no sudo. Everything that user owns lives under that home.',
    'nginx is granted read access to a site’s public/ directory by being added to the user’s group — never by loosening world permissions. The home stays 0750. That is the whole reason the group-per-user rule exists: it is the only way to let one specific daemon read one specific tree without making the tree readable to every account on the box.',
    'Because a user is cheap, it is also the unit of real isolation. Where genuine per-site separation matters, create one ratline user per site rather than leaning on site-scoped SSH keys.',
  ],
  commands: [
    {
      id: 'user-add',
      name: 'ratline user add',
      args: '<username>',
      status: 'built',
      summary: 'Create a tenant: system account, group, 0750 home, keys, no sudo.',
      description: [
        'Creates the account and its group, locks the password, creates the home at 0750 with .ssh/ at 0700 and a logs/ directory, and installs any keys given. The account gets no sudo.',
        'The username is validated before anything is touched: it must match ^[a-z_][a-z0-9_-]{0,31}$, must not be on the reserved list, and must not collide with an existing entry in /etc/passwd or /etc/group. ratline will not adopt a group it did not create.',
      ],
      flags: [
        {
          name: '--ssh-key',
          arg: '<path|url|->',
          type: 'path | url | -',
          repeatable: true,
          description:
            'Public key(s) to install for this user. `-` reads from stdin. Repeat the flag for more than one key.',
          note: 'Every key is validated with ssh-keygen -l -f before it goes near a file, and any options the submitted line already carries are stripped.',
        },
        {
          name: '--password-login',
          type: 'bool',
          default: 'disabled (keys only)',
          description: 'Permit password authentication for this account.',
        },
        {
          name: '--shell',
          arg: '<path>',
          type: 'path',
          default: '/bin/bash',
          description:
            'Login shell. Pass /usr/sbin/nologin to disable interactive login entirely.',
        },
        {
          name: '--sftp-only',
          type: 'bool',
          default: 'false',
          description: 'Chroot to the home directory via internal-sftp, with no shell.',
        },
        {
          name: '--quota',
          arg: '<size>',
          type: 'size',
          description: 'Disk quota, for example 20G, if filesystem quotas are available.',
          note: 'Refused rather than silently ignored when users.quota_enabled is false. Sizes are powers of 1024, matching systemd.',
        },
        {
          name: '--memory-max',
          arg: '<size>',
          type: 'size',
          description:
            'Default cgroup memory ceiling inherited by this user’s sites.',
          note: 'A site can override it with `site scale --memory-max`. MemoryHigh is derived as defaults.memory_high_ratio (0.875) of this, so the kernel starts reclaiming before it starts killing.',
        },
        {
          name: '--comment',
          arg: '<text>',
          type: 'string',
          description: 'GECOS comment — who this tenant is, for the next operator.',
        },
      ],
      refuses: [
        'A username that fails the syntax rule, is on the reserved list, or collides with an existing user or group.',
        '--quota when users.quota_enabled is false, rather than accepting the flag and ignoring it.',
        'Granting sudo. users.allow_sudo only permits the escape hatch to exist; each grant is still validated with visudo -c.',
      ],
      exits: [
        { code: 2, reason: 'The username failed validation.' },
        {
          code: 3,
          reason: 'The name is reserved, or the user or group already exists.',
        },
        { code: 4, reason: 'useradd, chown or chmod failed.' },
        { code: 5, reason: 'Another ratline invocation holds the lock.' },
        {
          code: 6,
          reason:
            'Creation failed and the rollback could not remove what it had already made.',
        },
      ],
      examples: [
        {
          title: 'A tenant with key-only SSH access',
          lang: 'shell',
          code: 'ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub --comment "Acme Ltd"',
        },
        {
          title: 'Two keys, a quota, and a memory ceiling inherited by their sites',
          lang: 'shell',
          code: `ratline user add acme \\
  --ssh-key ~/.ssh/ali.pub \\
  --ssh-key ~/.ssh/sam.pub \\
  --quota 20G \\
  --memory-max 512M`,
        },
        {
          title: 'A deploy-only account: SFTP, no shell',
          lang: 'shell',
          code: 'ratline user add acme-drop --sftp-only --ssh-key - < key.pub',
        },
        {
          title: 'Preview everything it would do, touching nothing',
          lang: 'shell',
          code: 'ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub --dry-run',
        },
      ],
      seeAlso: [
        { label: 'The three SSH key scopes', to: '/concepts/ssh-scopes' },
        { label: 'Filesystem and permission layout', to: '/concepts/filesystem' },
      ],
      keywords: ['tenant', 'useradd', 'home', 'group', 'quota', 'sandbox'],
    },
    {
      id: 'user-list',
      name: 'ratline user list',
      status: 'built',
      summary: 'Every ratline-managed user.',
      flags: [
        {
          name: '--json',
          type: 'bool',
          default: 'false',
          description: 'Emit the JSON envelope instead of a table.',
        },
      ],
      exits: [{ code: 0, reason: 'Always, unless state cannot be read.' }],
      examples: [
        { lang: 'shell', code: 'ratline user list' },
        {
          title: 'Names only, for a shell loop',
          lang: 'shell',
          code: 'ratline user list --json | jq -r \'.data.users[].name\'',
        },
      ],
    },
    {
      id: 'user-show',
      name: 'ratline user show',
      args: '<username>',
      status: 'built',
      summary: 'Home, sites, disk usage, keys and running services for one user.',
      description: [
        'The single view an operator wants when a client emails. It answers where their files are, what is deployed, how much disk they are using, which keys can reach them and which of their services are up.',
      ],
      exits: [
        { code: 2, reason: 'The username failed validation.' },
        { code: 3, reason: 'No such ratline user.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline user show acme' }],
    },
    {
      id: 'user-disable',
      name: 'ratline user disable',
      args: '<username>',
      status: 'built',
      summary: 'Lock login, stop all their site services, serve 503.',
      description: [
        'The non-destructive lever for a non-paying or compromised tenant. Login is locked, every one of their site units is stopped, and nginx serves 503 for their vhosts rather than a connection refused or a stale page. Nothing is deleted, so `user enable` puts it all back.',
      ],
      exits: [
        { code: 3, reason: 'No such user.' },
        { code: 4, reason: 'systemctl or usermod failed.' },
        { code: 5, reason: 'Locked.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline user disable acme' }],
    },
    {
      id: 'user-enable',
      name: 'ratline user enable',
      args: '<username>',
      status: 'built',
      summary: 'Reverse a disable: unlock login, start their services, drop the 503.',
      exits: [
        { code: 3, reason: 'No such user.' },
        { code: 4, reason: 'systemctl or usermod failed.' },
        { code: 7, reason: 'A site started but never became healthy.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline user enable acme' }],
    },
    {
      id: 'user-delete',
      name: 'ratline user delete',
      args: '<username>',
      status: 'built',
      summary: 'Remove a tenant. Refuses while sites exist unless --purge.',
      description: [
        'Deletion prints a precise inventory first — paths, units, certificates, ports, state rows, the home directory size — and requires you to type the username. Never a bare y/N: the thing being deleted is somebody’s site.',
      ],
      flags: [
        {
          name: '--purge',
          type: 'bool',
          default: 'false',
          description:
            'Also delete the user’s sites, their units, their vhosts and their home directory.',
          note: 'Without it, a user who still owns sites is refused rather than half-removed.',
        },
        {
          name: '--backup',
          arg: '<dir>',
          type: 'path',
          description: 'Write a backup of the home directory and state to this directory first.',
        },
      ],
      refuses: [
        'Deleting a user who still owns sites, unless --purge is given.',
        'Removing the last working global SSH credential — that needs --force and a typed confirmation, because bricking SSH on a remote VPS has no recovery path.',
      ],
      exits: [
        { code: 2, reason: 'The typed confirmation did not match; nothing was changed.' },
        { code: 3, reason: 'No such user, or the user still owns sites and --purge was not given.' },
        { code: 10, reason: 'Confirmation was needed and there is no terminal. Pass --yes.' },
      ],
      examples: [
        {
          title: 'The safe order: back up, then purge',
          lang: 'shell',
          code: `ratline backup acme --out /var/backups/ratline
ratline user delete acme --purge --backup /var/backups/ratline`,
        },
      ],
      seeAlso: [{ label: 'Transactional behaviour', to: '/concepts/transactions' }],
    },
    {
      id: 'user-password-set',
      name: 'ratline user password set',
      args: '<username>',
      status: 'built',
      summary: 'Set a password for an account that has password login enabled.',
      flags: [
        {
          name: '--stdin',
          type: 'bool',
          default: 'false',
          description: 'Read the password from stdin instead of prompting.',
          note: 'Secrets are never accepted in argv, where they would be visible in the process list and in the audit log.',
        },
      ],
      exits: [
        { code: 3, reason: 'No such user.' },
        { code: 10, reason: 'A prompt was needed and there is no terminal; pass --stdin.' },
      ],
      examples: [
        {
          lang: 'shell',
          code: 'ratline user password set acme --stdin < /run/secrets/acme.pw',
        },
      ],
    },
  ],
};
