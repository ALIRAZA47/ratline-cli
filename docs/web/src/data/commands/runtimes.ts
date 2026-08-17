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
  ],
};
