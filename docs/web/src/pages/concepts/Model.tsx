import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { RequestPath } from '../../components/diagrams/RequestPath';
import { Callout, Facts, H2, H3, TableScroll } from '../../components/ui';

export function ConceptModel() {
  return (
    <article>
      <PageHeader
        eyebrow="Concepts"
        title="Users, sites and runtimes"
        lede="Three objects and one rule about where things live. Read this before the reference and most of the reference will need no explanation."
      />

      <div className="prose">
        <H2>The object model</H2>
        <p>
          A <strong>user</strong> is a tenant sandbox: a system account with its own group, its own
          home at <code>0750</code>, a locked password, its own SSH keys and no sudo. A{' '}
          <strong>site</strong> belongs to exactly one user and lives inside that user’s home. A{' '}
          <strong>runtime</strong> is a managed Node or Python version under{' '}
          <code>/opt/ratline/runtimes</code>, referenced by absolute path.
        </p>
        <p>
          A <strong>certificate</strong> is deliberately <em>not</em> a property of a site. It is its
          own resource with its own lifecycle, because a site frequently exists and serves HTTP
          before its domain has finished moving — see{' '}
          <Link to="/concepts/tls-lifecycle">the TLS lifecycle</Link>.
        </p>
      </div>

      <Facts
        rows={[
          ['user', <>A Linux account. Cheap on purpose — it is the unit of real isolation.</>],
          ['site', <>An nginx vhost plus a directory, plus a systemd unit for node and python.</>],
          ['runtime', <>An interpreter under <code>/opt/ratline/runtimes/&lt;kind&gt;/&lt;version&gt;</code>.</>],
          ['certificate', <>A TLS lineage, attachable to and detachable from a site.</>],
          ['key', <>An inbound SSH credential with a scope. Or, for deploy keys, an outbound one.</>],
        ]}
      />

      <div className="prose">
        <H2>The three runtimes</H2>
      </div>

      <TableScroll>
        <table className="w-full min-w-[44rem] border-collapse text-left text-sm">
          <caption className="sr-only">What each runtime provisions</caption>
          <thead>
            <tr className="bg-sunken text-2xs uppercase tracking-wider text-muted">
              <th scope="col" className="w-[7rem] px-3 py-2 font-medium">
                Runtime
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                nginx does
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                A process runs
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                Required flag
              </th>
            </tr>
          </thead>
          <tbody>
            <tr className="border-t border-line align-top">
              <th scope="row" className="px-3 py-2.5 text-left">
                <code className="font-mono text-xs text-accent">static</code>
              </th>
              <td className="px-3 py-2.5">Serves files from the document root.</td>
              <td className="px-3 py-2.5 text-muted">
                No. No unit, no socket, nothing to restart.
              </td>
              <td className="px-3 py-2.5 font-mono text-xs text-muted">—</td>
            </tr>
            <tr className="border-t border-line align-top">
              <th scope="row" className="px-3 py-2.5 text-left">
                <code className="font-mono text-xs text-accent">node</code>
              </th>
              <td className="px-3 py-2.5">
                Reverse-proxies to a Unix socket, or an allocated port. Serves{' '}
                <code>--public</code> directly, bypassing the app.
              </td>
              <td className="px-3 py-2.5">
                The managed Node binary, by absolute path, under{' '}
                <code className="font-mono text-xs">ratline-&lt;slug&gt;.service</code>.
              </td>
              <td className="px-3 py-2.5 font-mono text-xs text-muted">
                --entry <em>or</em> --start-command
              </td>
            </tr>
            <tr className="border-t border-line align-top">
              <th scope="row" className="px-3 py-2.5 text-left">
                <code className="font-mono text-xs text-accent">python</code>
              </th>
              <td className="px-3 py-2.5">
                Reverse-proxies to a Unix socket. Serves <code>--static-url</code> from{' '}
                <code>--static-dir</code> directly.
              </td>
              <td className="px-3 py-2.5">
                Gunicorn, or Gunicorn with a Uvicorn worker for ASGI, in a per-site virtualenv.
              </td>
              <td className="px-3 py-2.5 font-mono text-xs text-muted">--app-module</td>
            </tr>
          </tbody>
        </table>
      </TableScroll>

      <div className="prose">
        <H2 id="request-path">The request path</H2>
        <p>
          Worth internalising, because it is where nearly every failure lives. A 502 is always one of
          the arrows below being broken.
        </p>
      </div>

      <RequestPath />

      <div className="prose">
        <p>
          Two things about that diagram deserve stating outright. First, the socket’s mode is the
          access control: <code>0660 &lt;user&gt;:www-data</code> means nginx can connect and nobody
          else can, without a port for another tenant to find. Second, nginx reads{' '}
          <code>public/</code> because <code>www-data</code> is a member of the site user’s group —
          not because the directory is world-readable. The home stays <code>0750</code>, so one
          tenant cannot read another tenant’s files by walking <code>/home</code>.
        </p>

        <H3>Why nginx joins the group instead of the home being 0755</H3>
        <p>
          The obvious way to let nginx read a tenant’s files is <code>chmod 755</code> on the home.
          That works, and it also makes every file in that home readable by every account on the
          box — including the other tenants. The group route grants exactly one daemon exactly the
          access it needs, and leaves the rest of the shell users looking at a directory they cannot
          enter.
        </p>
      </div>

      <div className="prose">
        <H2 id="slug">The slug</H2>
        <p>
          One identifier is shared by a site’s systemd unit, its runtime directory and its socket
          path. It is <code>&lt;user&gt;-&lt;domain&gt;</code> with dots replaced by underscores,
          lowercased, and truncated with a digest suffix if too long.
        </p>
      </div>

      <CodeBlock
        lang="text"
        code={`alice + example.com
  → slug     alice-example_com
  → unit     ratline-alice-example_com.service
  → socket   /run/ratline/alice-example_com/app.sock
  → logs     /home/alice/example.com/logs/{app,access,error}.log`}
        noCopy
      />

      <div className="prose">
        <p>
          Dots become underscores because unit names <em>do</em> accept dots — but a name carrying two
          separators with different meanings is much harder to scan in a <code>systemctl</code>{' '}
          listing.
        </p>
      </div>

      <Callout tone="note" title="The 64-character cap is a kernel limit, not a preference">
        <p>
          <code>sockaddr_un.sun_path</code> is 108 bytes on Linux and the fixed part of{' '}
          <code>/run/ratline/&lt;slug&gt;/app.sock</code> is 22 characters, so a slug over about 85
          characters produces a socket the application cannot bind — surfacing as an opaque “invalid
          argument” at start time. 64 leaves room for multi-instance suffixes like{' '}
          <code>app-1.sock</code>. Truncating alone would risk two long domains colliding on one unit
          name, so an over-long slug gets an 8-character SHA-256 suffix and is collision-checked
          against existing units.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="containment">The one rule about paths</H2>
        <p>
          A document root is <em>always</em> under the owning user’s home. Any path that escapes it
          after cleaning <em>and symlink resolution</em> is refused. That second clause is the whole
          point: a lexical check is trivially defeated by a symlink planted inside a tenant’s own
          home, so containment resolves links first and compares real paths.
        </p>
        <p>
          The candidate does not have to exist yet. The deepest existing ancestor is resolved and the
          remainder appended, which is what lets the check run before the directory tree has been
          created.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# refused: escapes the home
ratline site add example.com --user acme --runtime static --root ../../var/www

# refused: resolves outside the home even though it reads as a subdirectory
ln -s /etc/nginx /home/acme/example.com/public
ratline site add example.com --user acme --runtime static --root public
# error: public resolves to /etc/nginx, which is outside /home/acme/example.com
#   hint: symlinks are followed before this check; a path may not escape the owner's home directory`}
      />

      <div className="prose">
        <H2>Where to go from here</H2>
        <ul>
          <li>
            <Link to="/concepts/filesystem">Filesystem and permissions</Link> — every path, its mode
            and its owner.
          </li>
          <li>
            <Link to="/concepts/supervision">Process supervision</Link> — the unit and its hardening.
          </li>
          <li>
            <Link to="/reference/site">The site command reference</Link> — every flag for all three
            runtimes.
          </li>
        </ul>
      </div>
    </article>
  );
}
