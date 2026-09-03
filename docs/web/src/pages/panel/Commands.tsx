import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Callout, H2, TableScroll } from '../../components/ui';

const groups: { title: string; blurb: string; rows: [string, string][] }[] = [
  {
    title: 'Running it',
    blurb: 'What the systemd unit starts, and what set it up.',
    rows: [
      ['ratline-panel serve', 'Run the panel. This is the unit’s ExecStart; you rarely type it.'],
      ['ratline-panel install', 'Configuration, database, first super admin, unit, start. --admin-email names the account; --admin-password-stdin supplies its password instead of generating one; --no-admin skips it. --domain and --email put it on a domain in the same run. Safe to run twice.'],
      ['ratline-panel uninstall', 'Stop it and remove the unit and the vhost. --purge also deletes the accounts.'],
      ['ratline-panel doctor', 'Every check, all the problems together. Exit 3 when something is wrong.'],
      ['ratline-panel version', 'The version, the commit and the build date.'],
    ],
  },
  {
    title: 'The domain',
    blurb: 'nginx and the certificate.',
    rows: [
      ['ratline-panel domain set <domain>', 'Write the vhost, obtain a certificate, rewrite with TLS. --staging spends no rate limit; --no-tls stops at HTTP.'],
      ['ratline-panel domain show', 'Where the panel is answering, and what proxies to it.'],
      ['ratline-panel domain clear', 'Remove the vhost. The certificate is left alone.'],
      ['ratline-panel nginx reload', 'Check and reload. certbot’s deploy hook calls this after a renewal.'],
    ],
  },
  {
    title: 'Accounts',
    blurb: 'The recovery path — for when the panel has locked you out of itself.',
    rows: [
      ['ratline-panel account list', 'Who has access, their role, whether they have a second factor.'],
      ['ratline-panel account create <email>', 'Create one. The password is read from a terminal without echo, or from stdin — never a flag.'],
      ['ratline-panel account role <email> <role>', 'superadmin or admin. Refuses to remove the last active super admin.'],
      ['ratline-panel account password <email>', 'Set a password and end every session for that account.'],
      ['ratline-panel account disable <email>', 'Disable it and cut its sessions. --enable reverses it.'],
      ['ratline-panel account totp-reset <email>', 'Remove a second factor so a new device can be enrolled. The password still applies.'],
      ['ratline-panel account delete <email>', 'Remove their access to the panel. Nothing on the server changes.'],
    ],
  },
  {
    title: 'Configuration',
    blurb: 'panel.yaml.',
    rows: [
      ['ratline-panel config show', 'The values in force, not the file — an absent key shows the default it fell back to.'],
      ['ratline-panel config path', 'Where it is reading from.'],
      ['ratline-panel config validate', 'Check it and say what is wrong.'],
      ['ratline-panel config reference', 'Every setting with the reasoning attached. Redirect it into panel.yaml to start from a file that explains itself.'],
    ],
  },
];

export function PanelCommands() {
  return (
    <article className="prose">
      <PageHeader
        eyebrow="The web panel"
        title="ratline-panel commands"
        lede="A small surface, on purpose. The panel’s job is to run ratline’s commands; its own are about installing it, putting it on a domain, and getting back in."
      />

      {groups.map((g) => (
        <section key={g.title}>
          <H2 id={g.title.toLowerCase().replace(/\s+/g, '-')}>{g.title}</H2>
          <p>{g.blurb}</p>
          <TableScroll>
            <table>
              <thead>
                <tr>
                  <th>Command</th>
                  <th>What it does</th>
                </tr>
              </thead>
              <tbody>
                {g.rows.map(([cmd, what]) => (
                  <tr key={cmd}>
                    <td>
                      <code>{cmd}</code>
                    </td>
                    <td>{what}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableScroll>
        </section>
      ))}

      <H2 id="globals">Global flags</H2>
      <p>
        The same names as ratline&rsquo;s, doing the same things:{' '}
        <code>--config</code>, <code>--json</code>, <code>--verbose</code>,{' '}
        <code>--quiet</code>, <code>--dry-run</code>, <code>--yes</code>.
      </p>

      <Callout tone="note" title="The same exit codes">
        <Link to="/reference/exit-codes">ratline&rsquo;s exit-code contract</Link> applies
        here too — 0 success, 2 usage, 3 precondition, 4 an external command failed, and so
        on. A script driving both branches on one set of numbers.
      </Callout>

      <H2 id="secrets">Passwords are never flags</H2>
      <p>
        <code>/proc/PID/cmdline</code> is world-readable, so a password on a command line is
        a password every account on the machine can read while the command runs — and it is
        in your shell history afterwards. On a terminal it is prompted for without echo;
        otherwise it is read from stdin, which is what a provisioning script should do:
      </p>
      <CodeBlock
        lang="bash"
        prompt
        code={`printf '%s' "$PANEL_PASSWORD" | ratline-panel account create ops@example.com --role admin`}
      />
    </article>
  );
}
