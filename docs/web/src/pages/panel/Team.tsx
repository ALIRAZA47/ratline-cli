import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, H2, H3, TableScroll } from '../../components/ui';

const superAdminOnly = [
  ['user delete', 'Cascades to their sites; --purge takes the home directory.'],
  ['user sudo grant', 'The one escape hatch out of the tenant sandbox.'],
  ['user password set', 'Sets a login password on a system account.'],
  ['site delete', 'Takes a tenant’s application off the internet.'],
  ['site env import', 'Writes a whole environment file in one go.'],
  ['key prune', 'A sweep: it can remove many keys in one run.'],
  ['key sync', 'Rewrites every authorized_keys file on the box.'],
  ['cert revoke', 'Announced to the CA and cannot be taken back.'],
  ['cert delete', 'Removes a lineage.'],
  ['cert import', 'Installs a certificate and key from files.'],
  ['cert account register', 'Registers the ACME account for this server.'],
  ['db install', 'Installs a database server on the host.'],
  ['db connect', 'Stores the admin connection string.'],
  ['db drop', 'Deletes a database and its contents.'],
  ['db user delete', 'Removes a database user.'],
  ['db restore', 'Writes over whatever is in the database now.'],
  ['db access allow / revoke', 'Adds and removes firewall rules for the database port.'],
  ['runtime default', 'Changes what every new site gets.'],
  ['config set / unset', 'Changes how every operation behaves.'],
  ['init', 'Rewrites the configuration and the directory layout.'],
  ['reconcile', 'Rebuilds state from a scan of the system.'],
  ['update', 'Replaces the ratline binary the panel is driving.'],
  ['restore / import', 'Recovery operations with a blast radius the size of the machine.'],
];

export function PanelTeam() {
  return (
    <article className="prose">
      <PageHeader
        eyebrow="The web panel"
        title="Super admins and admins"
        lede="Two roles. One runs the server; the other also decides who else can."
      />

      <p>
        A third read-only role is the obvious next thing to want, and it is not there
        because &ldquo;read-only&rdquo; is not a property of the panel — it is a property of
        each command, and the catalogue already carries it. Adding a role before anybody has
        said what it should <em>not</em> be able to do produces a role nobody can describe.
      </p>

      <H2 id="admin">Admin</H2>
      <p>
        Runs the server day to day. Sites, deploys, certificates, SSH keys, databases,
        environment variables, runtimes, logs, diagnostics. Everything Ploi calls
        &ldquo;managing a server&rdquo;. This is the role most people should have.
      </p>

      <H2 id="superadmin">Super admin</H2>
      <p>
        Everything an admin can do, plus two things they cannot: change who else has
        access, and run the operations that cannot be undone by running another command.
      </p>

      <TableScroll>
        <table>
          <thead>
            <tr>
              <th>Command</th>
              <th>Why it is up here</th>
            </tr>
          </thead>
          <tbody>
            {superAdminOnly.map(([verb, why]) => (
              <tr key={verb}>
                <td>
                  <code>ratline {verb}</code>
                </td>
                <td>{why}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableScroll>

      <Callout tone="note" title="The default is the safe direction">
        A command the panel has no policy for falls to super admin if it mutates and to
        admin if it does not. So a ratline release that adds a command makes it appear
        locked down rather than wide open, and the panel&rsquo;s test suite lists anything
        unclassified so somebody notices.
      </Callout>

      <H2 id="enforcement">Where the split is enforced</H2>
      <p>
        On the server, per request. An admin&rsquo;s browser is never sent the super-admin
        operations at all — they are absent from the catalogue it receives, not hidden in
        it. Asking for one directly returns &ldquo;no such action&rdquo;, the same answer as
        for a command that does not exist, because telling somebody an action exists but is
        not theirs maps out the surface above them.
      </p>

      <H2 id="invitations">Invitations</H2>
      <p>
        A super admin creates a link. It is shown once, works once, and expires — 72 hours
        by default. The role comes from the invitation and never from the request body, so
        somebody holding an admin link cannot make themselves a super admin with it.
      </p>

      <Callout tone="note" title="The panel does not send email, deliberately">
        Sending it would mean the panel owned an SMTP configuration, a queue, a bounce
        problem and a new way for an invitation to leak through somebody&rsquo;s mail logs.
        You get the link and choose a channel you trust.
      </Callout>

      <H3 id="stored">What is stored</H3>
      <p>
        The hash of the token, not the token. The invitations table is therefore not a set
        of working links for anybody who reads it — and neither is the sessions table, for
        the same reason.
      </p>

      <H2 id="removing">Removing somebody</H2>
      <p>
        Disabling an account ends its sessions immediately rather than waiting for them to
        expire. Deleting one removes their access to the panel and <em>nothing else</em>:
        the tenants, sites and keys ratline holds are the server&rsquo;s, not the
        administrator&rsquo;s, and making &ldquo;remove a departing colleague&rdquo; the most
        dangerous button in the product would be a design mistake.
      </p>

      <H2 id="last-super-admin">The last super admin</H2>
      <p>
        The panel refuses to demote, disable or delete the last active super admin, in the
        browser and on the command line alike — the check is in the store, so both paths
        get the same answer. It also refuses to let you change your own role, because a
        super admin who demotes themselves while alone has locked the door and posted the
        key through it.
      </p>

      <H2 id="recovery">When it goes wrong anyway</H2>
      <p>
        A lost phone, a forgotten password, a panel with nobody able to sign in. Every
        recovery path is a command, run over SSH by whoever has root — which is not a new
        privilege, because root can read the panel&rsquo;s database in any case.
      </p>

      <Terminal>{`$ ratline-panel account list
EMAIL              ROLE        2FA  STATE     LAST SIGN-IN
dana@example.com   superadmin  yes  active    2026-08-28 09:14
sam@example.com    admin       no   active    2026-08-27 16:40

$ ratline-panel account role sam@example.com superadmin
sam@example.com is now superadmin.

$ ratline-panel account totp-reset dana@example.com
The second factor for dana@example.com has been removed.`}</Terminal>

      <CodeBlock
        lang="bash"
        prompt
        code={`# reads the password from a terminal without echo, or from stdin
ratline-panel account password dana@example.com
printf '%s' "$PASSWORD" | ratline-panel account password dana@example.com`}
      />

      <p>
        A password change ends every session for that account, which is also how you evict
        one you do not recognise. See{' '}
        <Link to="/panel/commands">the full command list</Link>.
      </p>
    </article>
  );
}
