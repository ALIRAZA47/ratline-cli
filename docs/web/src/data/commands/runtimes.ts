import type { CommandGroup } from '../types';

export const runtimes: CommandGroup = {
  id: 'runtime',
  title: 'Runtimes',
  path: '/reference/runtime',
  blurb: 'Managed Node, Bun and Python versions, under /opt/ratline/runtimes.',
  intro: [
    'Runtimes are installed once, under paths.runtimes_dir (/opt/ratline/runtimes), and referenced by absolute path from every unit that uses them: /opt/ratline/runtimes/node/22/bin/node, /opt/ratline/runtimes/bun/1.2/bin/bun, /opt/ratline/runtimes/python/3.12/.',
    'That absolute path is the point. nvm, pyenv, `bun upgrade`, shell profiles and login shells are never involved in starting a site, so a tenant editing their .bashrc — or upgrading their own bun — cannot change the interpreter their service executes, and two sites can sit on different major versions without arguing.',
    'A site can only be created on a version that is already installed. `site add` refuses a missing runtime as a precondition failure rather than installing several hundred megabytes as a side effect.',
  ],
  commands: [
    {
      id: 'runtime-list',
      name: 'ratline runtime list',
      status: 'built',
      summary: 'Installed Node, Bun and Python versions, and which sites use each.',
      description: [
        'The "which sites use each" column is what makes this safe to act on. It is the difference between removing an unused runtime and taking three sites down.',
      ],
      examples: [{ lang: 'shell', code: 'ratline runtime list' }],
    },
    {
      id: 'runtime-install-node',
      name: 'ratline runtime install node',
      args: '<version>',
      status: 'built',
      summary: 'Install a managed Node version.',
      description: [
        'Downloaded from runtimes.node_mirror (https://nodejs.org/dist) and unpacked under /opt/ratline/runtimes/node/<version>/. The install is bounded by runtimes.install_timeout (30m).',
      ],
      flags: [],
      exits: [
        { code: 2, reason: 'The version string failed validation. A major version (22) or a full version (22.11.0) is accepted.' },
        { code: 4, reason: 'The download or the unpack failed.' },
        { code: 5, reason: 'Locked.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline runtime install node 22' },
        { lang: 'shell', code: 'ratline runtime install node 22.11.0' },
      ],
    },
    {
      id: 'runtime-install-bun',
      name: 'ratline runtime install bun',
      args: '<version>',
      status: 'built',
      summary: 'Install a managed Bun version.',
      description: [
        'Downloaded from runtimes.bun_mirror (https://github.com/oven-sh/bun/releases) as a release asset, verified against the SHASUMS256.txt published beside it, and unpacked under /opt/ratline/runtimes/bun/<version>/. The zip is extracted in-process rather than through unzip, which a minimal Ubuntu does not ship — and which would mean the archive’s own filenames reaching the filesystem.',
        'Deliberately not the `curl … | bash` installer: that puts the binary in ~/.bun where `bun upgrade` can rewrite it, owned by whoever ran it, findable only through a shell profile. All three are things a systemd unit cannot rely on.',
        'A partial version (1 or 1.2) resolves to the newest matching release from the same host’s release feed. That feed only carries recent releases, so an older line has to be named in full rather than silently resolving to whatever is newest.',
      ],
      flags: [
        {
          name: '--baseline',
          type: 'bool',
          default: 'detected from /proc/cpuinfo',
          description: 'Install the build for x86-64 CPUs without AVX2.',
          note: 'Bun’s default x86-64 build requires AVX2, which a good number of older VPS hosts do not expose. Getting it wrong is not a graceful failure — the process dies on an illegal instruction with no message of its own — so the CPU is read rather than assumed, and the post-install version check names this flag if it dies anyway.',
        },
      ],
      exits: [
        { code: 2, reason: 'The version string failed validation, or --baseline was passed on arm64.' },
        { code: 3, reason: 'No recent release matches a partial version, or the installed binary does not run.' },
        { code: 4, reason: 'The download failed, or the archive did not match its published checksum. Nothing is installed either way.' },
      ],
      examples: [
        { lang: 'shell', code: 'ratline runtime install bun 1.2' },
        { lang: 'shell', code: 'ratline runtime install bun 1.2.21 --baseline' },
      ],
    },
    {
      id: 'runtime-install-python',
      name: 'ratline runtime install python',
      args: '<version>',
      status: 'built',
      summary: 'Install a managed Python version.',
      description: [
        'Accepts 3.x or 3.x.y. Python 2 is not supported and the validator says so rather than failing later.',
      ],
      exits: [
        { code: 2, reason: 'The version string failed validation.' },
        { code: 4, reason: 'The build or the download failed.' },
      ],
      examples: [{ lang: 'shell', code: 'ratline runtime install python 3.12' }],
    },
    {
      id: 'runtime-default',
      name: 'ratline runtime default',
      args: '<node|bun|python> <version>',
      status: 'built',
      summary: 'Set the version new sites get when they do not ask for one.',
      description: [
        'Writes runtimes.node_default, runtimes.bun_default or runtimes.python_default. All three are empty until `ratline runtime install` or `ratline init` has run, and while a default is empty `site add` requires the version to be named explicitly. The first version installed of a kind becomes its default, since an operator who installs exactly one means that one.',
        'Changing the default does not move existing sites. `ratline site runtime <domain> --node 22` does that, one site at a time, with a health check.',
      ],
      examples: [
        { lang: 'shell', code: `ratline runtime default node 22
ratline runtime default bun 1.2
ratline runtime default python 3.12` },
      ],
      seeAlso: [{ label: 'site runtime', to: '/reference/site/runtime' }],
    },
    {
      id: 'pm2',
      name: 'ratline pm2',
      args: '[<domain>|<user>] -- <pm2 arguments>',
      status: 'built',
      summary: 'Run pm2 against a tenant’s shared PM2 daemon, as the tenant.',
      description: [
        'Every tenant’s PM2-supervised sites share one PM2 daemon per Node version, run by systemd as that tenant (ratline-pm2@<user>.<node>.service, with PM2_HOME at /home/<user>/.ratline/pm2/<node>). This runs pm2 against it as the tenant, with the daemon’s own PM2_HOME and node. A bare `pm2` as root talks to root’s ~/.pm2 instead — and, finding no daemon there, starts an empty one, which is the worst possible answer to “what is running”.',
        'With a domain, the daemon that site runs in; and a verb that takes an application name (logs, describe, restart, reload, reset, flush) is given the site’s own when none follows. With a user, that tenant’s daemon — `--node` picks one when they have sites on more than one Node version. With neither, a listing verb (list, status, jlist, prettylist) runs against every daemon on the server in turn.',
        'The daemon is read from its unit file, which is root’s and carries ratline’s header, so nothing about which user or which pm2 to run is taken from the tenant’s tree. A daemon that is not running is not asked at all, because every pm2 command that cannot reach a daemon starts one — outside the unit’s cgroup, holding its socket.',
        'Nothing is interpreted by a shell; everything after -- is pm2’s argv. It takes no lock, because `pm2 logs` can run for hours and holding the lock would stop every other ratline command on the box for as long as somebody watched.',
      ],
      flags: [
        {
          name: '--node',
          arg: '<version>',
          type: 'version',
          description: 'With a user: the daemon for this Node version. Refused with a domain, whose daemon follows its Node version, and without a target.',
        },
      ],
      refuses: [
        'kill — it stops the tenant’s daemon behind systemd’s back. Restart its unit instead, `ratline systemctl -- restart ratline-pm2@<user>.<node>.service`, which brings every site in it back.',
        'delete and stop — the site’s unit would still say active. Use `ratline site disable` or `ratline site delete`.',
        'start — an application started here is not a site, and is gone on the daemon’s next restart. Use `ratline site add` or `ratline site worker add`.',
        'scale — the site’s configuration would disagree with what is running. Use `ratline site scale <domain> --instances N`.',
        'save, dump, resurrect, cleardump — systemd brings the applications back from each site’s unit; a dump is a second, stale copy.',
        'startup and unstartup — the daemon is already a systemd unit; a second boot unit would start a second daemon.',
        'update — upgrade PM2 with `ratline runtime install node <version> --with-pm2`, then restart the daemon.',
        'install, uninstall, set and the other module verbs — a PM2 module is code run inside the daemon of every site of the tenant, and not something ratline manages.',
        'link and plus — PM2 Plus sends the daemon’s process data to a third party.',
        'deploy, serve, ecosystem, init, attach — each has a ratline equivalent, which the refusal names.',
        'A site still on its own per-site daemon from an earlier release — `ratline reconcile --fix` then `ratline site restart <domain>` moves it into its tenant’s.',
      ],
      exits: [
        { code: 2, reason: 'No pm2 arguments, a verb that is refused or not passed through, a verb that needs one daemon given several, or --node in the wrong place.' },
        { code: 3, reason: 'No PM2 daemon to ask: the site is not supervised by PM2 or still runs its own, the user has none (or none for that Node version), or the daemon is not running.' },
        { code: 4, reason: 'pm2 itself failed against a daemon.' },
      ],
      examples: [
        {
          lang: 'shell',
          code: `ratline pm2 -- list                                # every tenant's daemon in turn
ratline pm2 app.example.com -- logs --lines 200    # that site's application
ratline pm2 app.example.com -- describe
ratline pm2 acme -- monit                          # the tenant's daemon
ratline pm2 acme --node 18 -- list                 # one of several`,
        },
      ],
      seeAlso: [
        { label: 'Node sites and PM2', to: '/guides/node' },
        { label: 'site runtime', to: '/reference/site/runtime' },
        { label: 'ratline systemctl', to: '/reference/ops/systemctl' },
      ],
      keywords: ['pm2', 'pm2_home', 'pm2 list', 'pm2 logs', 'pm2 monit', 'daemon', 'ratline-pm2', 'tenant daemon'],
    },
  ],
};
