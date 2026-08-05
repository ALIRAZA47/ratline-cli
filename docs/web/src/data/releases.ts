/**
 * The release history.
 *
 * Written here rather than generated from git tags, because the interesting part of a
 * release is not the list of commits — it is what changed for whoever runs this, and what
 * is still missing. A generated changelog says "fix: handle nil case"; this says which
 * command stopped lying to you.
 *
 * Kept in the same order as the GitHub releases page, newest first.
 */

export interface ReleaseChange {
  /** A short label for the change, used as the heading. */
  title: string;
  /** What changed and why it mattered. Plain sentences; no bullet soup. */
  body: string;
  /** Shown as a code block under the body. */
  code?: string;
  /**
   * `fix` marks something that was wrong and is now right, which is the category people
   * scan for when deciding whether to upgrade.
   */
  kind?: 'feature' | 'fix' | 'security';
}

export interface Release {
  version: string;
  date: string;
  /** One sentence, shown under the version heading. */
  summary: string;
  /** The upgrade command, when it differs from the usual one. */
  upgrade?: string;
  changes: ReleaseChange[];
  /** Honest about what this release does not do. */
  known?: string[];
  /** Assertions in the integration suite at this tag. */
  assertions?: number;
}

export const releases: Release[] = [
  {
    version: 'v0.4.0',
    date: '2026-08-05',
    summary:
      'MongoDB databases and users, with roles that cannot reach past their own database.',
    assertions: 236,
    changes: [
      {
        kind: 'feature',
        title: '`ratline db` provisions MongoDB',
        body:
          'Create a database, create a user whose only role is on that database, hand the connection string to a site, rotate it, re-role it, revoke it. Twelve commands: ping, create, list, show, drop, roles, and user add/list/password/grant/delete.',
        code: `ratline db create shop --owner acme --attach shop.example.com
ratline db user add reports --database shop --role read
ratline db user password shop_app --all-sites`,
      },
      {
        title: 'It provisions inside a server rather than installing one',
        body:
          'A database server is stateful, with backups and a replication topology, and a tool that silently apt-gets one has decided something belonging to whoever owns the data — the same reasoning that has ratline configure nginx and drive certbot without installing either. A local mongod and a managed cluster differ only in the connection string, which lives in a 0600 file rather than in config.yaml because it is the root password for every database on the server.',
      },
      {
        kind: 'security',
        title: 'No operator input is ever interpolated into JavaScript',
        body:
          'Every operation runs one static file, embedded in the binary, as `mongosh --nodb --quiet --file`. Values arrive through the environment. The alternative is building an --eval string, and then a username containing a quote closes it and runs whatever follows, as root, against a server holding every tenant’s data. There is no escaping to get right because there is no string to escape into. --nodb matters for the same reason: mongosh normally takes the connection string as its first argument, and /proc/PID/cmdline is world-readable.',
      },
      {
        kind: 'security',
        title: 'Every role is scoped to one database',
        body:
          'read, readWrite, dbAdmin, dbOwner. The cluster-wide roles are absent by construction — granting readWriteAnyDatabase to a tenant’s application hands it every other tenant’s data, and it would be one flag away if the list were open. The suite proves it rather than asserting it: a credential writes its own database, is refused on another, and a demotion from readWrite back to read takes the write away again.',
      },
      {
        title: 'A password is shown once, and never stored',
        body:
          'MongoDB keeps a hash and will not return it, so one cannot be displayed later even if that were wanted — which is the right shape rather than a limitation. --attach writes the URI into the site’s .env at 0600 instead of onto a terminal, because shell history and scrollback outlive every rotation. --all-sites updates every site holding a credential when it is rotated, which is the difference between a rotation and an outage; a site that could not be updated is named loudly, since by then the old password has already stopped working.',
      },
      {
        title: '`doctor` knows about the database',
        body:
          'It reports an unreachable server, an admin file at the wrong mode, a server not enforcing authentication, a database recorded here but missing there, and — the one that matters — a user a site still holds credentials for but which the server no longer has. That last case is an application failing to authenticate right now.',
      },
      {
        kind: 'fix',
        title: 'The documentation stopped claiming this did not exist',
        body:
          'Four places said database provisioning was absent or that `ratline db` was a stub, including a non-goal and a bullet on the home page. A CI check now verifies every internal link on this site resolves, after adding this section nearly shipped a link to a page that was never written.',
      },
    ],
    known: [
      'MongoDB only. Postgres, MySQL and Redis are not supported, and the shape here does not assume they will be.',
      'No whole-server restore. `restore` handles a site or a tenant; rebuilding a server still means backing up /var/lib/ratline/state.db and /etc/ratline yourself.',
      'No DNS or mail management, and no PHP runtime.',
    ],
  },
  {
    version: 'v0.3.0',
    date: '2026-08-05',
    summary: 'One-command install, and ratline’s own units now travel inside the binary.',
    upgrade: 'curl -fsSL https://ratline-cli.vercel.app/install.sh | sudo sh',
    assertions: 184,
    changes: [
      {
        kind: 'feature',
        title: 'One command on a bare server',
        body:
          'Resolves the latest release, downloads for this architecture, verifies against the release’s own SHA256SUMS, installs, and runs `ratline init` — configuration, directory layout, and the renewal and key-pruning timers started.',
        code: 'curl -fsSL https://ratline-cli.vercel.app/install.sh | sudo sh',
      },
      {
        title: 'The renewal units moved into the binary',
        body:
          'The installer could not have been piped before, for a structural reason: it installed the systemd units by copying packaging/systemd/, so it only worked with a checkout or an unpacked tarball around it. The units now live in the embedded templates and `ratline init` installs them — so a server that received nothing but the binary renews its certificates, which was not previously true of `make install` either. A unit you have edited is left alone and reported.',
      },
      {
        kind: 'fix',
        title: '`doctor` no longer tells you to delete your renewal timer',
        body:
          'It reported ratline’s own units as "a ratline unit with no matching site" and offered, as the fix, a command that removes them. Following that advice stops certificates renewing, and the first sign is an expired certificate weeks later. Survivable while only install.sh placed those units; with `init` doing it, every server would have been given that advice.',
      },
      {
        kind: 'fix',
        title: 'A failed /dev/tty probe killed the installer',
        body:
          'A failed redirection on `exec` terminates a non-interactive shell, so on any host where /dev/tty exists but cannot be opened — a container, for instance — the script died partway through, after printing a raw error beneath a question it had already asked.',
      },
      {
        kind: 'fix',
        title: 'Builds are reproducible',
        body:
          'The build stamped the wall clock into the binary, so two builds of the same commit differed and rebuilding a tag could not reproduce the published artefacts — removing the one check on a release that does not require trusting whoever uploaded it. From v0.3.0 onward, `make dist` at a tag reproduces the published files byte for byte.',
        code: 'git checkout v0.4.0 && make dist && sha256sum -c dist/SHA256SUMS',
      },
      {
        kind: 'fix',
        title: '`ratline restor --help` reports the typo',
        body:
          'cobra handles the help flag before the unknown-command check, so a misspelling printed the root help and exited 0 — leaving you reading a list of commands, wondering which one you got wrong. Without --help the same typo correctly exits 2, which is why it went unnoticed.',
      },
    ],
  },
  {
    version: 'v0.2.0',
    date: '2026-08-05',
    summary: 'Two of v0.1.0’s three known gaps closed: restore, and DNS-01.',
    assertions: 184,
    changes: [
      {
        kind: 'feature',
        title: '`ratline restore`',
        body:
          'The counterpart backup never had. The hard part is not extraction — it is everything the archive does not contain: the state row, the vhost, the unit, the tenant’s uid, the port. So restore rebuilds the row from the manifest that travelled with the files, re-renders the vhost and unit rather than restoring them, takes ownership from the account as it exists on this server, reallocates the port, and then proves the site serves before reporting success.',
        code: 'ratline restore /var/backups/ratline/app.example.com-20260105T120000Z.tar.gz',
      },
      {
        kind: 'security',
        title: 'Archives are treated as untrusted',
        body:
          'It extracts as root, and an archive may have been copied between servers or handed over by whoever is migrating in. A member with an absolute or traversing path is refused rather than sanitised, symlinks are chowned with lchown rather than followed, the manifest’s domain and owner are validated as if typed, and the slug is recomputed rather than trusted because it names the systemd unit.',
      },
      {
        kind: 'feature',
        title: 'DNS-01 through a provider certbot has no plugin for',
        body:
          '--dns-provider manual with a hook script. certbot ships plugins for around a dozen providers; for everything else — and for a company’s internal DNS — this is the only route to DNS-01 at all, and DNS-01 is the only route to a wildcard. The hook runs as root with the validation token in its environment, so it is refused unless it is an absolute path, owned by root, executable, and not writable by group or other.',
        code: `ratline cert issue '*.example.com' \\
  --dns-provider manual \\
  --dns-hook /etc/ratline/dns/publish.sh \\
  --dns-cleanup-hook /etc/ratline/dns/withdraw.sh`,
      },
      {
        kind: 'fix',
        title: 'The site manifest is readable',
        body:
          '.ratline/site.yaml was always written with a comment claiming it existed "so a site survives the loss of the state database" — and nothing read it, so it did not. It is parsed now, which is what makes restore possible and that claim true.',
      },
    ],
  },
  {
    version: 'v0.1.0',
    date: '2026-08-05',
    summary: 'The first release.',
    assertions: 137,
    changes: [
      {
        kind: 'feature',
        title: 'Tenants, sites, TLS and diagnosis',
        body:
          'A system account per tenant. An nginx vhost and a systemd unit per site, for static, Node and Python. TLS as a resource with its own lifecycle, including a preflight that checks DNS, port 80, server_name conflicts and the rate-limit budget before spending an attempt. And a diagnosis that walks the dependency chain and stops at the cause rather than listing symptoms.',
      },
      {
        title: 'Licensed under AGPL-3.0',
        body:
          'The repository was public with no licence, which means all rights reserved: nobody could legally use or contribute to it. AGPL rather than GPL for one clause — ratline is the provisioning core of a hosting panel, so the obvious way to take it without giving anything back is to wrap it in a panel and sell access, which is network use rather than distribution.',
      },
    ],
    known: [
      'backup had no restore, and DNS-01 had no automated coverage. Both closed in v0.2.0.',
      'These binaries predate reproducible builds and cannot be reproduced from the tag.',
    ],
  },
];
