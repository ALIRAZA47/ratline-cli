import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, Facts, H2, H3 } from '../../components/ui';

/**
 * The databases subject had commands and one in-depth page and no runbook, which made it
 * the thinnest subject on the site and the newest feature in the tool — a combination that
 * is exactly backwards.
 *
 * Written around the two mistakes that actually cost something here: a connection string
 * passed as an argument, and a mongod running without authentication. Both leave a server
 * that looks provisioned and is not protected.
 */
export function GuideMongoDatabase() {
  return (
    <article>
      <PageHeader
        eyebrow="Guide"
        title="Give a site a MongoDB database"
        lede="From nothing to an application reading MONGODB_URI, without the password ever reaching your scrollback or your shell history."
      />

      <div className="prose">
        <p>
          A Node site is running. It needs a database, its own user, and a credential it can
          read at startup — and nobody else’s tenant should be able to reach either. That is
          four commands, two of which are once per server.
        </p>

        <H2 id="server">1 · ratline does not install MongoDB</H2>
        <p>
          It manages what lives inside a server you point it at. A local <code>mongod</code>{' '}
          and a managed cluster are the same to it; the only difference is the connection
          string. A database server is a stateful thing with backups and a replication
          topology, and a tool that silently apt-gets one has made a decision that belongs
          to whoever owns the data — the same reason ratline configures nginx and drives
          certbot without installing either.
        </p>
        <p>So install it however your organisation installs databases, then check one thing:</p>
      </div>

      <CodeBlock lang="shell" prompt code="ratline db ping" />

      <Callout tone="danger" title="Read the authentication line before anything else">
        <p>
          A <code>mongod</code> started without <code>--auth</code> answers every command
          from anyone who can reach the port. Every user ratline creates on such a server is
          decoration: the roles are real, and nothing checks them.{' '}
          <code>db ping</code> reports whether authentication is actually enforced, which is
          why it is the first command in this guide rather than a troubleshooting step.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="connect">2 · Point ratline at it, once per server</H2>
        <p>
          The admin connection string is the root password for every database on that
          server. It goes into a file at <code>paths.mongo_uri_file</code>, mode 0600,
          root-owned — not into <code>config.yaml</code>, which is a file operators paste
          into support tickets. ratline refuses to read it at any mode another account could
          see.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`printf 'mongodb://admin:PASS@127.0.0.1:27017/?authSource=admin' \\
  | ratline db connect --stdin

# or, if it already lives in a file
ratline db connect --from-file /root/atlas.uri`}
      />

      <div className="prose">
        <p>
          <code>--stdin</code> is not a flag value on purpose. Anything in{' '}
          <code>argv</code> is world-readable through <code>/proc/PID/cmdline</code> for as
          long as the command runs, and it lands in your shell history, which outlives the
          password. The same rule governs{' '}
          <Link to="/reference/site/env">site env set</Link>.
        </p>
        <p>
          <code>db connect</code> creates the directory at 0700, writes the string at 0600,
          turns on <code>features.db_provisioning</code>, and proves the credentials work
          before committing any of it. If the server cannot be reached or rejects them,
          nothing is left behind — a stored string that does not work is indistinguishable
          from a server that is down, and you would find out on some unrelated command a
          week later.
        </p>
      </div>

      <Callout tone="note" title="Already connected, feature off">
        <p>
          If the string is stored and provisioning is off — after a{' '}
          <Link to="/reference/db/disable">db disable</Link>, say —{' '}
          <Link to="/reference/db/enable">db enable</Link> turns it back on. It checks a
          usable string exists first, because a command group that only ever refuses is
          worse than one that is plainly off.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="create">3 · Create the database and hand it straight to the site</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline db create shop --owner acme --attach shop.example.com
ratline site restart shop.example.com`}
      />

      <Terminal title="root@server">{`$ ratline db create shop --owner acme --attach shop.example.com
→ mongodb 8.0.4, replica set rs0, authentication enforced
→ created database shop
→ created collection shop.ratline
→ created user shop_app with readWrite on shop
→ wrote MONGODB_URI to /home/acme/shop.example.com/.env (0600, acme:acme)
✓ database shop created and attached to shop.example.com

  The application reads its environment at startup:
      ratline site restart shop.example.com`}</Terminal>

      <div className="prose">
        <p>Three of those lines are doing more than they look like:</p>
      </div>

      <Facts
        rows={[
          [
            '--owner acme',
            <>
              What makes cleanup possible later.{' '}
              <Link to="/reference/user/delete">ratline user delete --purge</Link> knows what
              to revoke because of this; without an owner the database outlives the account
              it was created for and nothing ever cleans it up.
            </>,
          ],
          [
            '--attach',
            <>
              The connection string goes into a 0600 <code>.env</code> owned by the tenant
              instead of onto your terminal. Prefer it: scrollback and shell history both
              outlive every rotation. Use <code>--env-key</code> if the application expects
              a different variable name.
            </>,
          ],
          [
            'the extra collection',
            <>
              MongoDB has no <code>createDatabase</code> — a database exists once something
              is written into it. Without an initial collection a new database is invisible
              to <Link to="/reference/db/list">db list</Link> until the application first
              writes, which reads as the create having silently failed.
            </>,
          ],
        ]}
      />

      <Callout tone="warn" title="The password is shown once and is never stored">
        <p>
          MongoDB keeps a hash and will not return it, so ratline could not show it again
          even if it wanted to. That is the right shape rather than a limitation: there is
          no credential store here to be stolen. A lost password is rotated, not recovered —
          see <a href="#rotate">rotating a password</a> below.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="verify">4 · Check it from both ends</H2>
        <p>
          <Link to="/reference/db/show">db show</Link> reads the collections, document count
          and index size from the server, which is the difference between <em>provisioned</em>{' '}
          and <em>in use</em>. If the count is still zero after a deploy, the application
          never connected.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline db show shop

# The site's side: the variable is there, the value is redacted
ratline site env list shop.example.com

# And what the server has, including anything ratline did not create
ratline db list --live`}
      />

      <Callout tone="note" title="What --live is for">
        <p>
          By default <code>db list</code> reads ratline’s own index. <code>--live</code> asks
          the server and marks anything it does not recognise — a database created by hand or
          by an older tool. Those are worth knowing about: nothing will revoke their users
          when the tenant is deleted.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="second-user">5 · A narrower credential for something else</H2>
        <p>
          A reporting job does not need to write. A second user on the same database is how
          you give something less access than the application has — revocable on its own,
          without touching the credential the site depends on.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline db user add reports --database shop --role read
ratline db user list --database shop --live`}
      />

      <div className="prose">
        <p>
          <Link to="/reference/db/user-grant">db user grant</Link> replaces a user’s roles
          with exactly the one named rather than adding to them. That is deliberate:
          accumulating roles quietly is how a read-only user ends up able to write.
        </p>
        <p>
          Every role ratline grants is scoped to one database. The cluster-wide ones —{' '}
          <code>root</code>, <code>readWriteAnyDatabase</code>,{' '}
          <code>userAdminAnyDatabase</code> — are deliberately absent, because granting one
          to a tenant’s application hands it every other tenant’s data.{' '}
          <Link to="/reference/db/roles">db roles</Link> prints the four that exist. If you
          genuinely need a cluster-wide role, use <code>mongosh</code> directly; ratline will
          not be the thing that made it easy.
        </p>

        <H2 id="rotate">6 · Rotating a password without causing an outage</H2>
        <p>
          The old password stops working the moment the new one is set, so the order matters
          and <code>--all-sites</code> is what makes this a rotation rather than an outage.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline db user password shop_app --all-sites
ratline site restart shop.example.com`}
      />

      <div className="prose">
        <p>
          <code>--all-sites</code> updates every site recorded as holding that credential. If
          one cannot be updated it is named loudly, because by then the password has already
          changed on the server and that site is down. The restart is still yours to do: an
          environment variable is read at startup, so a site that has the new string in its{' '}
          <code>.env</code> is still using the old one in memory.
        </p>

        <H2 id="managed">7 · A managed cluster hangs rather than refusing</H2>
        <p>
          This is the one failure worth recognising on sight. A cluster behind an IP access
          list — Atlas and most of its competitors — does not reject a connection from an
          address it does not know. It accepts the TCP connection and never answers.
        </p>
      </div>

      <Terminal title="root@server">{`$ ratline db create shop --owner acme
✗ the MongoDB server did not answer in time

  hint: raise databases.mongodb.timeout if the server is simply slow. On
  Atlas this is usually the access list: a cluster that has not allowed
  this server's address does not refuse the connection, it ignores it.

  exit 3 (precondition_failed)`}</Terminal>

      <div className="prose">
        <p>
          That timeout is <code>databases.mongodb.timeout</code>, and its entire job is to
          turn a command that never returns into an error that names the access list. Raise
          it in{' '}
          <Link to="/reference/config#cfg-databases">the databases settings</Link> if your
          cluster is genuinely slow to answer, but a hang is far more often an access list
          than a slow cluster.
        </p>

        <H2 id="decommission">8 · Taking it away again</H2>
        <H3 id="drop">Dropping a database</H3>
      </div>

      <CodeBlock lang="shell" prompt code="ratline db drop shop" />

      <div className="prose">
        <p>
          This destroys data and cannot be undone, so it names the document and collection
          count before asking and needs the database’s name typed back. A count is what makes
          somebody stop; “are you sure?” is not.
        </p>
        <p>
          Users are removed before the database, deliberately. Dropping a database does not
          remove its users, and a user left behind still authenticates and still holds a role
          on a database that springs back into existence the moment anything writes through
          it. <code>--keep-database</code> removes the users and ratline’s record but leaves
          the data — what handing a database over to somebody else’s tooling looks like.
        </p>

        <H3 id="handover">Handing the server over</H3>
      </div>

      <CodeBlock lang="shell" prompt code="ratline db disable --forget" />

      <div className="prose">
        <p>
          Nothing on the MongoDB server is touched: the databases, their users and every site
          holding a credential keep working. This only stops ratline managing them.{' '}
          <code>--forget</code> also removes the stored admin string, which is worth not doing
          otherwise — it is the one copy ratline has.
        </p>
      </div>

      <Callout tone="note" title="The same words, on the server">
        <p>
          <code>ratline explain databases</code> prints the model behind all of this — what a
          database, a user and an attachment are, and why a password is generated rather than
          chosen. It is embedded in the binary, so it works over SSH with no browser. The same
          page is <Link to="/topics/databases">here</Link>.
        </p>
      </Callout>
    </article>
  );
}
