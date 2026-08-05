import type { CommandGroup } from '../types';

export const runtimes: CommandGroup = {
  id: 'runtime',
  title: 'Runtimes',
  path: '/reference/runtime',
  blurb: 'Managed Node and Python versions, under /opt/ratline/runtimes.',
  intro: [
    'Runtimes are installed once, under paths.runtimes_dir (/opt/ratline/runtimes), and referenced by absolute path from every unit that uses them: /opt/ratline/runtimes/node/22/bin/node, /opt/ratline/runtimes/python/3.12/.',
    'That absolute path is the point. nvm, pyenv, shell profiles and login shells are never involved in starting a site, so a tenant editing their .bashrc cannot break their own service, and two sites can sit on different major versions without arguing.',
    'A site can only be created on a version that is already installed. `site add` refuses a missing runtime as a precondition failure rather than installing several hundred megabytes as a side effect.',
  ],
  commands: [
    {
      id: 'runtime-list',
      name: 'ratline runtime list',
      status: 'built',
      summary: 'Installed Node and Python versions, and which sites use each.',
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
      args: '<node|python> <version>',
      status: 'built',
      summary: 'Set the version new sites get when they do not ask for one.',
      description: [
        'Writes runtimes.node_default or runtimes.python_default. Both are empty until `ratline runtime install` or `ratline init` has run, and while a default is empty `site add` requires the version to be named explicitly.',
        'Changing the default does not move existing sites. `ratline site runtime <domain> --node 22` does that, one site at a time, with a health check.',
      ],
      examples: [
        { lang: 'shell', code: `ratline runtime default node 22
ratline runtime default python 3.12` },
      ],
      seeAlso: [{ label: 'site runtime', to: '/reference/site/runtime' }],
    },
  ],
};
