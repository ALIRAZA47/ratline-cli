import type { CommandGroup } from '../types';

export const configuration: CommandGroup = {
  id: 'config',
  title: 'Configuration commands',
  path: '/reference/configure',
  blurb: 'Read and change /etc/ratline/config.yaml from the CLI, without breaking it.',
  intro: [
    'The configuration is read on every invocation, so there is nothing to reload after a change. Editing the file by hand is still perfectly fine — these commands exist because some settings are easy to get subtly wrong, and because a file that no longer loads breaks every other command, with the failure arriving on the next unrelated one.',
    'Every change is validated before it is committed. If the result would not load, the previous file is left exactly as it was and the error names the setting.',
    'Comments are preserved. The shipped configuration is documentation — every setting carries an explanation of what it does and why the default is what it is — so the editor works on the text rather than re-encoding the struct. Re-encoding is what `ratline init` used to do, and it flattened the reference on the very first run.',
    'For what each setting means and what its default is, see the settings reference at /reference/config. This page is about changing them.',
    'An unknown setting is refused rather than written. A typo like paths.systemdir would otherwise sit in the file being silently ignored, and the misconfiguration would surface as ratline writing units somewhere nobody looks — so it is caught, and the nearest real setting is suggested.',
  ],
  commands: [
    {
      id: 'config-show',
      name: 'ratline config show',
      args: '[prefix]',
      status: 'built',
      summary: 'Every setting in effect, and where its value came from.',
      description: [
        '--changed shows only what differs from the shipped defaults, which is usually the actual question. A prefix narrows it to one section.',
        'Values that carry tokens — the alert webhook — are redacted, because this output ends up in support tickets.',
      ],
      flags: [
        {
          name: '--changed',
          type: 'bool',
          default: 'false',
          description: 'Only settings that differ from the shipped defaults.',
        },
      ],
      examples: [
        { lang: 'shell', code: 'ratline config show --changed' },
        { lang: 'shell', code: 'ratline config show databases' },
      ],
    },
    {
      id: 'config-get',
      name: 'ratline config get',
      args: '<setting>',
      status: 'built',
      summary: 'One setting’s value, and whether it comes from the file or the default.',
      description: [
        'That distinction is the useful part: a setting absent from the file behaves as the default today, and would change if the default ever did.',
      ],
      examples: [{ lang: 'shell', code: 'ratline config get acme.renew_before_days' }],
    },
    {
      id: 'config-set',
      name: 'ratline config set',
      args: '<setting> <value>',
      status: 'built',
      summary: 'Change one setting, preserving the file’s comments.',
      description: [
        'Edits in place, then validates. A change that would produce a file that does not load is refused and the previous one is kept.',
        'A yes-or-no setting accepts what people actually type — yes, on, enabled — and lands in the file as the true or false a YAML parser reads back. Written as the string "yes" it would be silently ignored.',
      ],
      refuses: [
        'A setting that does not exist, with the nearest real one suggested. A missing underscore — paths.systemdir for paths.systemd_dir — is the commonest typo and is caught.',
        'A value that would fail validation, such as an address that is not an email or a mode that is not octal. The file is left unchanged.',
        'A file whose indentation ratline cannot edit safely. A wrong edit to a configuration file is worse than no edit.',
      ],
      examples: [
        {
          lang: 'shell',
          code: `ratline config set acme.email ops@example.com
ratline config set features.db_provisioning true
ratline config set defaults.memory_max 1G`,
        },
      ],
    },
    {
      id: 'config-unset',
      name: 'ratline config unset',
      args: '<setting>',
      status: 'built',
      summary: 'Remove a setting, so its built-in default applies again.',
      description: [
        'Deletes the line rather than blanking it. Those are different: an absent setting takes the built-in default, and an empty one is an explicit empty value — which for a path or an address is usually not what anybody wants.',
        'The setting’s own comment goes with it, since leaving it orphaned above an unrelated key would describe the wrong thing.',
      ],
      examples: [{ lang: 'shell', code: 'ratline config unset defaults.memory_max' }],
    },
    {
      id: 'config-edit',
      name: 'ratline config edit',
      status: 'built',
      summary: 'Open it in $EDITOR, and refuse to save it broken.',
      description: [
        'Opens a copy. On exit the copy is validated and only replaces the real file if it loads, so a typo cannot leave every other command broken.',
        'That last part is the whole reason to go through ratline rather than opening the file directly.',
      ],
      examples: [{ lang: 'shell', code: 'EDITOR=vi ratline config edit' }],
    },
    {
      id: 'config-validate',
      name: 'ratline config validate',
      args: '[path]',
      status: 'built',
      summary: 'Check a file without applying it.',
      description: [
        'Reports every problem at once rather than the first. Useful before copying a configuration onto a server, and in CI.',
      ],
      examples: [{ lang: 'shell', code: 'ratline config validate ./staging-config.yaml' }],
    },
    {
      id: 'config-reference',
      name: 'ratline config reference',
      status: 'built',
      summary: 'The shipped configuration, with every setting explained.',
      description: [
        'What `ratline init` writes on a fresh server. Print it to recover a block you deleted, or to read the explanation of a setting on a machine with no browser.',
      ],
      examples: [
        { lang: 'shell', code: 'ratline config reference | grep -A 4 renew_before_days' },
      ],
    },
    {
      id: 'config-path',
      name: 'ratline config path',
      status: 'built',
      summary: 'Which file is being read.',
      description: [
        'Worth checking when a change appears not to have taken: --config and the built-in default can disagree about which file is in play.',
      ],
      examples: [{ lang: 'shell', code: 'ratline config path' }],
      seeAlso: [{ label: 'Every setting and its default', to: '/reference/config' }],
    },
  ],
};
