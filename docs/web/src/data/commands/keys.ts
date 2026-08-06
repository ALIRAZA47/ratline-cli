import type { CommandGroup } from '../types';

export const keys: CommandGroup = {
  id: 'key',
  title: 'SSH keys',
  path: '/reference/key',
  blurb: 'One command group, three scopes: server, tenant, one site.',
  intro: [
    'One command group covers all three scopes. Every key carries a required human label, so an operator can tell "Ali MacBook" from "CI runner" two years later — which is the difference between confidently removing a key and leaving it in place forever.',
    'Default options for every scope start from OpenSSH’s restrict: no port forwarding, agent forwarding, X11, PTY or user rc. `pty` is re-enabled only for scopes that get a shell. Permissiveness is opted into, never out of.',
    'Aliases: `ratline user key add|list|remove` map onto `ratline key … --scope user`.',
  ],
  commands: [
    {
      id: 'key-add',
      name: 'ratline key add',
      status: 'built',
      summary: 'Install a public key at global, user or site scope.',
      description: [
        'The key source is exactly one of --key, --from-github or --from-gitlab. The label is required in every case.',
        'Before anything reaches a file, the key is validated with ssh-keygen -l -f, checked against the algorithm policy, and stripped of any options it arrived with. Then the authorized_keys file is written atomically, sshd config is validated with sshd -t if it changed, and — because this is the one operation that can lock you out of a remote box — login is proven to still work afterwards.',
      ],
      flags: [
        {
          name: '--label',
          arg: '"<label>"',
          type: 'string',
          required: true,
          description: 'Human-readable name for the key. Required for every key at every scope.',
          note: 'At most 64 characters, printable, no double quote and no backslash — the label is rendered inside a label="…" comment, and a validator that cannot produce an ambiguous file is worth more than one that accepts every character.',
        },
        {
          name: '--key',
          arg: '<key|path|url|->',
          type: 'path | url | -',
          description: 'The public key. `-` reads from stdin.',
        },
        {
          name: '--from-github',
          arg: '<user>',
          type: 'string',
          description: 'Fetch https://github.com/<user>.keys.',
          note: 'Fetched with full certificate verification. Every line is validated independently, then every fingerprint is shown and confirmation is asked for — an account can have keys on it you did not expect.',
        },
        {
          name: '--from-gitlab',
          arg: '<user>',
          type: 'string',
          description: 'Fetch the equivalent key list from GitLab, under the same rules.',
        },
        {
          name: '--scope',
          arg: 'global|user|site',
          type: 'enum',
          required: true,
          description: 'What the key is allowed to reach. See the scope table below.',
        },
        {
          name: '--user',
          arg: '<username>',
          type: 'string',
          requiredWhen: '--scope user or --scope site',
          description: 'The tenant the key belongs to.',
        },
        {
          name: '--site',
          arg: '<domain>',
          type: 'string',
          requiredWhen: '--scope site',
          description: 'The single site the key is confined to.',
        },
        {
          name: '--sftp-only',
          type: 'bool',
          default: 'true for site scope',
          description: 'Force SFTP with no shell.',
        },
        {
          name: '--allow-shell',
          type: 'bool',
          default: 'false',
          description: 'Site scope only: grant an interactive shell anyway.',
          note: 'Opt-in, and warned about. It removes most of what site scope was for — see what site scope actually enforces.',
        },
        {
          name: '--from',
          arg: '<cidr,cidr>',
          type: 'cidr list',
          description: 'Restrict the source address, rendered as the from="…" option.',
          note: 'Bare addresses are widened to a single-host prefix and every entry is canonicalised, so 203.0.113.19 becomes 203.0.113.19/32.',
        },
        {
          name: '--expires',
          arg: '<date|duration>',
          type: 'date | duration',
          description:
            'Expiry, as an absolute date (2026-12-31) or a duration from now (90d). Rendered as expiry-time="…".',
          note: 'A bare date means valid through the end of that day, in UTC, which is what an operator writing 2026-12-31 intends. A daily timer removes keys past their expiry whether or not sshd supports the option (ssh.prune_expired).',
        },
        {
          name: '--no-agent-forwarding',
          type: 'bool',
          default: 'already off',
          description: 'Deny agent forwarding. Included for explicitness; restrict already denies it.',
        },
        {
          name: '--no-port-forwarding',
          type: 'bool',
          default: 'already off',
          description: 'Deny port forwarding.',
        },
        {
          name: '--no-pty',
          type: 'bool',
          default: 'already off except where a shell is granted',
          description: 'Deny PTY allocation.',
        },
        {
          name: '--command',
          arg: '<name>',
          type: 'enum',
          description:
            'A named forced-command preset: rsync-only, git-only or sftp-only.',
          note: 'The presets map to real programs in ssh.command_presets — sftp-only → internal-sftp, rsync-only → rsync, git-only → git. Only a named preset is accepted; an arbitrary command string is not, because that is exactly the escalation vector option stripping exists to close.',
        },
        {
          name: '--allow-duplicate',
          type: 'bool',
          default: 'false',
          description:
            'Permit a fingerprint that is already present somewhere on this box.',
          note: 'Without it, a duplicate is refused and the message names where the key already exists.',
        },
      ],
      refuses: [
        'ssh-dss outright. RSA under 3072 bits is refused; under 4096 it is warned about. ed25519 is preferred.',
        'Any options the submitted line already carries — a pasted key bringing its own command= or permitopen= is an escalation vector. ratline parses out algorithm, blob and comment, discards the rest, and applies only the options it derived from the flags.',
        'Keys containing newlines or NUL bytes, and lines over ssh.max_key_line_bytes (8192). The whole file is capped at ssh.max_authorized_keys_bytes (262144).',
        'A fingerprint already present anywhere on the box, unless --allow-duplicate.',
        'Adding keys to root’s authorized_keys unless ssh.allow_root_keys is true and you ask explicitly.',
        'A change to /etc/ssh that leaves login broken: the config is backed up, sshd -t is run, sshd is reloaded rather than restarted, and login is verified. If verification cannot run or fails, the previous config is restored and the change is reported as rejected.',
      ],
      exits: [
        { code: 2, reason: 'The label, key, scope, CIDR list or expiry failed validation.' },
        { code: 3, reason: 'No such user or site; or the fingerprint is already present.' },
        { code: 4, reason: 'ssh-keygen or sshd -t failed.' },
        { code: 5, reason: 'Locked.' },
        { code: 6, reason: 'The change failed and the previous authorized_keys could not be restored.' },
      ],
      examples: [
        {
          title: 'An operator key for the whole server',
          lang: 'shell',
          code: 'ratline key add --label "Ali MacBook" --key ~/.ssh/id_ed25519.pub --scope global',
        },
        {
          title: 'A tenant key: shell plus every site that user owns',
          lang: 'shell',
          code: `ratline key add --label "Acme — Dana" \\
  --key ./dana.pub --scope user --user acme`,
        },
        {
          title: 'A contractor confined to one site, expiring in 90 days',
          lang: 'shell',
          code: `ratline key add --label "Contractor — Rae" \\
  --key ./rae.pub \\
  --scope site --user acme --site example.com \\
  --expires 90d \\
  --from 203.0.113.0/24`,
        },
        {
          title: 'A CI runner that may only rsync',
          lang: 'shell',
          code: `ratline key add --label "CI runner" \\
  --key - --scope site --user acme --site example.com \\
  --command rsync-only < ci.pub`,
        },
        {
          title: 'From a GitHub account — every fingerprint is shown before anything is written',
          lang: 'shell',
          code: 'ratline key add --label "Dana laptop" --from-github danaexample --scope user --user acme',
        },
      ],
      seeAlso: [
        { label: 'The three SSH key scopes', to: '/concepts/ssh-scopes' },
        { label: 'Give a contractor access to exactly one site', to: '/guides/contractor-access' },
        { label: 'Add a key from a new laptop', to: '/guides/new-laptop-key' },
      ],
      keywords: ['authorized_keys', 'ed25519', 'rsa', 'restrict', 'forced command', 'scope'],
    },
    {
      id: 'key-list',
      name: 'ratline key list',
      status: 'built',
      summary: 'Every key ratline knows about, filterable by scope, owner, site and age.',
      flags: [
        { name: '--scope', arg: '<s>', type: 'enum', description: 'Only global, user or site scope.' },
        { name: '--user', arg: '<u>', type: 'string', description: 'Only keys for one tenant.' },
        { name: '--site', arg: '<d>', type: 'string', description: 'Only keys for one site.' },
        {
          name: '--unused',
          arg: '<days>',
          type: 'int',
          description: 'Only keys with no recorded use in this many days.',
          note: 'Last-used data comes from scanning the auth log for accepted-publickey lines (ssh.usage_scan_enabled). A key that has never been used may be new, or may be a leftover.',
        },
        {
          name: '--expiring',
          arg: '<days>',
          type: 'int',
          description: 'Only keys whose expiry falls inside this window.',
        },
        { name: '--json', type: 'bool', default: 'false', description: 'JSON envelope instead of a table.' },
      ],
      examples: [
        {
          title: 'Quarterly review: what has nobody touched in six months?',
          lang: 'shell',
          code: 'ratline key list --unused 180',
        },
      ],
    },
    {
      id: 'key-show',
      name: 'ratline key show',
      args: '<fingerprint|label>',
      status: 'built',
      summary: 'Everything recorded about one key.',
      description: [
        'A key is addressable by SHA256 fingerprint or by its label, which is the reason labels are mandatory. Fingerprints may be given with or without the SHA256: prefix.',
      ],
      exits: [
        { code: 2, reason: 'The fingerprint is malformed.' },
        { code: 3, reason: 'No key matches, or the label is ambiguous.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline key show "Contractor — Rae"' },
        { lang: 'shell', code: 'ratline key show SHA256:x9K...' },
      ],
    },
    {
      id: 'key-remove',
      name: 'ratline key remove',
      args: '<fingerprint|label>',
      status: 'built',
      summary: 'Remove one key from one scope.',
      flags: [
        { name: '--scope', arg: '<s>', type: 'enum', description: 'Which scope to remove it from.' },
        { name: '--user', arg: '<u>', type: 'string', description: 'Which tenant.' },
        { name: '--site', arg: '<d>', type: 'string', description: 'Which site.' },
      ],
      refuses: [
        'Removing the last working global credential without --force and a typed confirmation. The rollback command is printed before anything is changed.',
      ],
      exits: [
        { code: 3, reason: 'The key is not present in that scope, or it is the last global credential.' },
        { code: 6, reason: 'The file could not be restored after a failed write.' },
      ],
      examples: [
        {
          lang: 'shell',
          code: 'ratline key remove "Contractor — Rae" --scope site --user acme --site example.com',
        },
      ],
    },
    {
      id: 'key-revoke',
      name: 'ratline key revoke',
      args: '<fingerprint|label>',
      status: 'built',
      summary: 'Remove a key from every scope on the box at once.',
      description: [
        'What you run when a laptop is lost. Unlike `key remove`, this does not ask you to remember where the key was installed — it removes it everywhere and reports each place it was found. Revoked keys are also recorded in ssh.revoked_keys (/etc/ratline/ssh/revoked_keys).',
      ],
      flags: [
        {
          name: '--everywhere',
          type: 'bool',
          required: true,
          description: 'Confirms the blast radius: every scope, every user, every site.',
        },
      ],
      exits: [
        { code: 3, reason: 'The key was not found anywhere, or it is the last global credential.' },
        { code: 6, reason: 'One or more files could not be restored.' },
      ],
      examples: [
        {
          title: 'Lost laptop',
          lang: 'shell',
          code: 'ratline key revoke "Ali MacBook" --everywhere',
        },
      ],
      seeAlso: [{ label: 'I’m locked out of SSH', to: '/guides/ssh-lockout' }],
    },
    {
      id: 'key-move',
      name: 'ratline key move',
      args: '<fingerprint>',
      status: 'built',
      summary: 'Narrow or widen an existing key’s scope in one step.',
      description: [
        'Moving a key rather than removing and re-adding it keeps its label, its recorded first-seen date and its usage history, which is what makes an audit trail worth having.',
      ],
      flags: [
        {
          name: '--to-scope',
          arg: 'global|user|site',
          type: 'enum',
          required: true,
          description: 'The scope to move it to.',
        },
        { name: '--site', arg: '<d>', type: 'string', requiredWhen: '--to-scope site', description: 'Target site.' },
        { name: '--user', arg: '<u>', type: 'string', requiredWhen: '--to-scope user or site', description: 'Target tenant.' },
      ],
      examples: [
        {
          title: 'A contractor who now only needs one site',
          lang: 'shell',
          code: 'ratline key move SHA256:x9K... --to-scope site --user acme --site example.com',
        },
      ],
    },
    {
      id: 'key-audit',
      name: 'ratline key audit',
      status: 'built',
      summary:
        'Duplicates, weak algorithms, never-used keys, expired-but-present keys, and keys added outside ratline.',
      description: [
        'The last category is the one that matters. A key someone appended to authorized_keys by hand is invisible to state and survives every other cleanup, so the audit compares the files against state and reports the difference rather than assuming ratline is the only writer.',
      ],
      exits: [{ code: 0, reason: 'Findings are output; a finding is not an error.' }],
      examples: [{ lang: 'shell', code: 'ratline key audit' }],
    },
    {
      id: 'key-test',
      name: 'ratline key test',
      args: '<fingerprint>',
      status: 'built',
      summary: 'Explain exactly what this key can reach.',
      description: [
        'Reads the rendered options back and states the result in plain language, including the honest note that a site-scoped key still runs as the owner’s UID. This is the command to run before you tell a client "yes, that contractor can only touch that one site".',
      ],
      examples: [
        { lang: 'shell', code: 'ratline key test SHA256:x9K...' },
        {
          title: 'The output',
          lang: 'text',
          code: `Key       SHA256:x9K…   "Deploy CI"   ed25519
Scope     site → example.com  (owner: alice)
Login     alice@server — forced command only, no interactive shell
Allowed   sftp, rsync, git-upload-pack, git-receive-pack
          confined to /home/alice/example.com (symlinks resolved)
Denied    shell, port forwarding, agent forwarding, X11, PTY
Source    203.0.113.0/24 only
Expires   2027-01-01 (149 days)
Last use  2026-08-02 14:11 from 203.0.113.19
Note      Runs as UID alice. Not a kernel boundary — see SECURITY.md.`,
        },
      ],
      seeAlso: [{ label: 'What site scope actually enforces', to: '/concepts/ssh-scopes#site-scope-limits' }],
    },
    {
      id: 'key-sync',
      name: 'ratline key sync',
      status: 'built',
      summary: 'Re-render every authorized_keys file from state.',
      description: [
        'The repair operation for drift. State is authoritative; the files are derived. Anything hand-added is reported by `key audit` and removed by this, so run the audit first if you are not sure what is on the box.',
      ],
      exits: [
        { code: 4, reason: 'sshd -t failed after rendering; the previous files were restored.' },
        { code: 5, reason: 'Locked.' },
      ],
      examples: [
        { lang: 'shell', code: `ratline key audit
ratline key sync --dry-run
ratline key sync` },
      ],
    },
    {
      id: 'site-deploy-key-create',
      name: 'ratline site deploy-key create',
      args: '<domain>',
      status: 'built',
      summary:
        'Create an outbound keypair so the site can pull from a private repo, and print the public half.',
      description: [
        'This is the other direction, and it is worth being precise about which is which. Every other command in this group installs an inbound key that lets something reach this server. A deploy key is an outbound credential: the private half stays on this box, owned by the site user, and the public half goes into the repository host as a read-only deploy key.',
      ],
      flags: [
        {
          name: '--type',
          arg: '<algorithm>',
          type: 'enum',
          default: 'ed25519',
          description: 'Key algorithm.',
        },
      ],
      exits: [
        { code: 3, reason: 'No such site, or a deploy key already exists — use rotate.' },
        { code: 4, reason: 'ssh-keygen failed.' },
      ],
      examples: [
        {
          lang: 'shell',
          code: `ratline site deploy-key create example.com
# add the printed public key to the repository as a read-only deploy key
ratline site deploy-key show example.com`,
        },
      ],
      seeAlso: [{ label: 'CI deploy keys in both directions', to: '/guides/ci-deploy-keys' }],
    },
    {
      id: 'site-deploy-key-show',
      name: 'ratline site deploy-key show|rotate|remove',
      args: '<domain>',
      status: 'built',
      summary: 'Print, replace or delete a site’s outbound deploy key.',
      description: [
        '`show` prints the public half only. Private key material never appears in any output, JSON or otherwise. `rotate` generates a new pair and prints the new public key; the old one stops working the moment you remove it from the repository host, so update there before you rely on the change.',
      ],
      examples: [
        { lang: 'shell', code: 'ratline site deploy-key rotate example.com' },
      ],
    },
    {
      id: 'key-prune',
      name: 'ratline key prune',
      status: 'built',
      summary: 'Remove expired keys and record key usage.',
      description: [
        'Run daily by ratline-key-prune.timer rather than by hand.',
        'Two jobs, both of which have to happen on a schedule. Expired keys are removed: OpenSSH 8.2 and later already refuse them through the expiry-time= option, but this is what takes the line out of the file, and on an older daemon it is the only mechanism.',
        'And key usage is scraped from the journal, because logs rotate. A contractor’s key last used four months ago leaves no trace by the time anyone asks — recording it as it happens is what makes `ratline key list --unused 90` mean anything.',
      ],
      flags: [],
      examples: [
        {
          title: 'What the timer runs',
          lang: 'shell',
          code: 'ratline key prune',
        },
      ],
      exits: [
        { code: 0, reason: 'Expired keys removed and usage recorded; zero of either is still success.' },
        { code: 3, reason: 'The state database could not be opened.' },
        { code: 5, reason: 'Another ratline command holds the lock.' },
      ],
      keywords: ['expiry', 'timer', 'stale', 'usage', 'journal', 'last used'],
    },
  ],
};
