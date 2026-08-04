import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { FilesystemTree } from '../../components/diagrams/FilesystemTree';
import { Callout, H2, H3 } from '../../components/ui';

export function ConceptFilesystem() {
  return (
    <article>
      <PageHeader
        eyebrow="Concepts"
        title="Filesystem and permissions"
        lede="Every path ratline owns, with its mode and its owner — and the reasoning behind the three or four choices that are not obvious."
      />

      <div className="prose">
        <H2 id="layout">The layout</H2>
        <p>
          Three modes carry meaning and are colour-coded below: <code>.env</code> at{' '}
          <code>0600</code>, which nginx can never reach; <code>public/</code> at <code>0750</code>,
          which nginx reads through the group; and the logs at <code>0640 &lt;user&gt;:adm</code>,
          which an operator reads without being root.
        </p>
      </div>

      <FilesystemTree />

      <div className="prose">
        <H2 id="rules">The four rules the implementation enforces</H2>
        <ol>
          <li>
            A document root is <strong>always</strong> under the owning user’s home. Any path that
            escapes it after cleaning <em>and symlink resolution</em> is refused.
          </li>
          <li>
            nginx needs read access to <code>public/</code> only, granted by adding{' '}
            <code>www-data</code> to the site user’s group — never by loosening world permissions. The
            home stays <code>0750</code>.
          </li>
          <li>
            <code>.env</code> is <code>0600</code>, owned by the site user, loaded by systemd’s{' '}
            <code>EnvironmentFile=</code> (read as root before privileges are dropped), and never inside
            a directory nginx can serve. nginx additionally denies dotfiles.
          </li>
          <li>
            <code>umask 027</code> for all provisioning writes.
          </li>
        </ol>

        <H2 id="why-0750">Why the home stays 0750</H2>
        <p>
          The obvious way to let nginx serve a tenant’s files is <code>chmod 755 /home/acme</code>.
          That works. It also makes everything in that home traversable by every account on the box —
          every other tenant included. A shared-kernel box with several clients on it is precisely
          where that matters.
        </p>
        <p>
          Adding <code>www-data</code> to the <code>acme</code> group grants exactly one daemon exactly
          the access it needs. Other tenants get <code>Permission denied</code> at the directory, which
          is what they should get.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# What ratline arranges
getent group acme
# acme:x:1001:www-data

namei -l /home/acme/example.com/public/index.html
# drwxr-xr-x root root  /
# drwxr-xr-x root root  home
# drwxr-x--- acme acme  acme          ← 0750: nginx enters via the group
# drwxr-x--- acme acme  example.com
# drwxr-x--- acme acme  public
# -rw-r----- acme acme  index.html`}
      />

      <Callout tone="warn" title="Adding www-data to a group needs an nginx reload to take effect">
        <p>
          Supplementary groups are resolved when a process starts. If you add <code>www-data</code> to a
          group by hand, the running worker processes will not see it. ratline handles this as part of
          its transaction; if you are debugging a permission-denied that looks impossible, this is
          usually why. See <Link to="/guides/debug-502">debugging a 502</Link>.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="env">Why .env can be 0600 and still work</H2>
        <p>
          Because systemd reads <code>EnvironmentFile=</code> <em>as root</em>, before it drops
          privileges to <code>User=</code>. The application process never needs to open the file; it
          receives the variables in its environment. So the file can be readable only by its owner and
          the app still starts.
        </p>
        <p>Two consequences worth knowing:</p>
        <ul>
          <li>
            The file lives at the <em>site root</em>, not inside <code>public/</code> or{' '}
            <code>app/</code>. Even if nginx’s dotfile denial were removed, there is no URL path that
            maps to it.
          </li>
          <li>
            systemd’s parser cannot represent a multi-line value, so ratline refuses one rather than
            writing a file that silently truncates. For a PEM key or a JSON service account, put the
            payload in a file inside the site directory and point a variable at its path.
          </li>
        </ul>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# refused, with the reason
ratline site env set api.example.com PRIVATE_KEY="$(cat key.pem)"
# error: the value contains a newline
#   hint: systemd's EnvironmentFile cannot represent multi-line values;
#         store the payload in a file inside the site directory and point a variable at it

# the shape that works
install -m 0600 -o acme -g acme key.pem /home/acme/api.example.com/key.pem
ratline site env set api.example.com PRIVATE_KEY_PATH=/home/acme/api.example.com/key.pem`}
      />

      <div className="prose">
        <H2 id="logs">Why logs are 0640 user:adm</H2>
        <p>
          The group is <code>adm</code>, not the tenant’s group. That means an operator who is in{' '}
          <code>adm</code> can read every site’s logs without being root, and the tenant — who owns the
          file — can read their own. What the tenant cannot do is read <em>another</em> tenant’s logs,
          because they are not in <code>adm</code>.
        </p>
        <p>
          <code>logrotate</code> gets a per-site config at{' '}
          <code>/etc/logrotate.d/ratline-&lt;domain&gt;</code>, so one noisy site cannot fill the disk
          and take every other site down with it.
        </p>

        <H2 id="socket">The socket, and why it is preferred over a port</H2>
        <p>
          <code>/run/ratline/&lt;slug&gt;/app.sock</code> at <code>0660 &lt;user&gt;:www-data</code>.
          The mode <em>is</em> the access control: nginx can connect because it is in{' '}
          <code>www-data</code>, the owner can connect, and nothing else can. There is no port for
          another tenant to find by scanning <code>localhost</code>, and no port to collide.
        </p>
        <p>
          A TCP port is still available — <code>--listen port</code> allocates from{' '}
          <code>20000–29999</code> — for the applications that genuinely cannot bind a Unix socket. It
          is the exception, and <code>doctor</code> reports ports allocated but unused so the range does
          not silently fill up with leftovers.
        </p>

        <H3 id="runtime-dir">RuntimeDirectory, not mkdir</H3>
        <p>
          The socket’s parent directory is created by systemd’s <code>RuntimeDirectory=</code> with the
          right owner and mode, and removed when the unit stops. <code>/run</code> is a tmpfs, so a
          reboot clears it — a directory created by hand at install time would not survive one, and the
          service would fail to start with an error about a path that used to exist.
        </p>

        <H2 id="custom">The one directory that is yours</H2>
        <p>
          <code>/etc/nginx/ratline/custom/&lt;domain&gt;.conf</code>. It is included by the generated
          vhost and <strong>never regenerated</strong>, so hand-written additions survive{' '}
          <code>ratline reconcile</code>. Anything you add directly to the generated vhost will be lost
          the next time state is re-rendered — which is exactly the drift{' '}
          <code>doctor</code> reports.
        </p>
      </div>

      <CodeBlock
        lang="nginx"
        filename="/etc/nginx/ratline/custom/example.com.conf"
        code={`# Survives reconcile. Put anything ratline does not model here.
location = /robots.txt {
    access_log off;
    add_header Content-Type text/plain;
    return 200 "User-agent: *\\nDisallow: /admin\\n";
}

# A long-running export endpoint that needs more than proxy_read_timeout 60s.
location /export/ {
    proxy_pass         http://ratline_upstream;
    proxy_read_timeout 600s;
}`}
      />

      <div className="prose">
        <H2 id="state">State, and the audit log</H2>
        <p>
          <code>/var/lib/ratline/state.db</code> is SQLite at <code>0600 root:root</code>. It is the
          authority — every config is derived from it, which is what makes{' '}
          <code>reconcile</code> and <code>key sync</code> possible at all.
        </p>
        <p>
          <code>/var/log/ratline/audit.log</code> records every invocation: the command, its argv, the
          UID and the <code>SUDO_USER</code> behind it, whether it was a dry run, the result, the exit
          code and the duration. It is written on failure as well as success — a failed mutation is
          exactly what you will later want to find. Losing the audit log does not stop an operator
          managing the server: it is downgraded to a debug line rather than an error, because a tool
          that refuses to work because it cannot write a log is worse than one that works and says so.
        </p>
      </div>

      <Callout tone="note" title="Secrets are redacted, in logs and in argv">
        <p>
          Secrets never go in argv, where they would be visible in the process list and recorded in the
          audit log — which is why <code>env set</code> and <code>user password set</code> support{' '}
          <code>--stdin</code>. Values are redacted in logs, in errors, and in{' '}
          <code>env list</code> unless <code>--reveal</code> is given.
        </p>
      </Callout>
    </article>
  );
}
