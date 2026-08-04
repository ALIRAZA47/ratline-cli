import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, H2, H3, TableScroll } from '../../components/ui';

export function ConceptSshScopes() {
  return (
    <article>
      <PageHeader
        eyebrow="Concepts"
        title="The three SSH scopes"
        lede="Server administration, one tenant, or one site directory. One command group covers all three, and the defaults start from OpenSSH’s restrict rather than working towards it."
      />

      <div className="prose">
        <H2>What each scope grants</H2>
      </div>

      <TableScroll>
        <table className="w-full min-w-[46rem] border-collapse text-left text-sm">
          <caption className="sr-only">The three SSH key scopes</caption>
          <thead>
            <tr className="bg-sunken text-2xs uppercase tracking-wider text-muted">
              <th scope="col" className="w-[6rem] px-3 py-2 font-medium">
                Scope
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                Grants
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                Lands in
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                Typical holder
              </th>
            </tr>
          </thead>
          <tbody>
            <tr className="border-t border-line align-top">
              <th scope="row" className="px-3 py-2.5 text-left">
                <code className="font-mono text-xs text-accent">global</code>
              </th>
              <td className="px-3 py-2.5">
                Server administration: a shell as the admin user, and permission to run{' '}
                <code>ratline</code>.
              </td>
              <td className="px-3 py-2.5 font-mono text-xs text-muted">
                the admin user’s authorized_keys
              </td>
              <td className="px-3 py-2.5">You and your ops team.</td>
            </tr>
            <tr className="border-t border-line align-top">
              <th scope="row" className="px-3 py-2.5 text-left">
                <code className="font-mono text-xs text-accent">user</code>
              </th>
              <td className="px-3 py-2.5">
                Full access to one tenant: an interactive shell, and every site that user owns.
              </td>
              <td className="px-3 py-2.5 font-mono text-xs text-muted">
                /home/&lt;user&gt;/.ssh/authorized_keys
              </td>
              <td className="px-3 py-2.5">The client who owns those sites.</td>
            </tr>
            <tr className="border-t border-line align-top">
              <th scope="row" className="px-3 py-2.5 text-left">
                <code className="font-mono text-xs text-accent">site</code>
              </th>
              <td className="px-3 py-2.5">
                SFTP, rsync or git confined to <strong>one site directory</strong>. No interactive
                shell by default.
              </td>
              <td className="px-3 py-2.5 font-mono text-xs text-muted">
                the same file, with <code>restrict</code> + a forced command
              </td>
              <td className="px-3 py-2.5">A contractor or CI runner on one site.</td>
            </tr>
          </tbody>
        </table>
      </TableScroll>

      <div className="prose">
        <p>
          Note that <code>site</code> scope lands in the <em>same</em> file as <code>user</code>{' '}
          scope — the tenant’s <code>authorized_keys</code>. There is no separate account. What
          differs is the option string on the line, and that is exactly why the next section exists.
        </p>

        <H2>Defaults start from restrict</H2>
        <p>
          Every key, at every scope, begins with OpenSSH’s <code>restrict</code>: no port forwarding,
          no agent forwarding, no X11, no PTY, no user rc. <code>pty</code> is then re-enabled only
          for scopes that get a shell. Permissiveness is opted into, never out of — so a flag that
          was forgotten leaves a key more restricted rather than less.
        </p>
      </div>

      <CodeBlock
        lang="text"
        filename="/home/acme/.ssh/authorized_keys (shape)"
        code={`restrict,pty,label="Acme — Dana" ssh-ed25519 AAAA…  dana@laptop
restrict,command="…",from="203.0.113.0/24",expiry-time="20270101",label="Contractor — Rae" ssh-ed25519 AAAA…  rae@laptop`}
        noCopy
      />

      <Callout tone="danger" title="Options that arrive with a pasted key are stripped">
        <p>
          A key line bringing its own <code>command=</code> or <code>permitopen=</code> is an
          escalation vector: it would apply <em>its</em> options rather than yours. ratline parses out
          the algorithm, the blob and the comment, discards everything else, and applies only the
          options it derived from the flags. This is also why <code>--command</code> takes a{' '}
          <em>named preset</em> — <code>rsync-only</code>, <code>git-only</code>,{' '}
          <code>sftp-only</code> — rather than an arbitrary command string.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="site-scope-limits">What site scope actually enforces</H2>
        <p>This section is the important one, and it is deliberately blunt.</p>
      </div>

      <div className="not-prose my-6 rounded-[var(--radius-card)] border-2 border-[color-mix(in_oklab,var(--warn)_45%,transparent)] bg-warn-soft px-5 py-4">
        <p className="max-w-[var(--container-measure)] text-base leading-relaxed text-fg">
          Site scope is a <strong>blast-radius and usability boundary, not a kernel-enforced one</strong>.
          The key still authenticates as the site owner’s UID; the confinement is sshd’s forced command
          plus the <code className="font-mono text-[0.9em]">ratline-shell</code> wrapper. That reliably
          prevents accidents and stops a contractor wandering into a sibling site. It does not stop a
          determined attacker who already has code execution as that UID, and{' '}
          <code className="font-mono text-[0.9em]">--allow-shell</code> removes most of it.
        </p>
      </div>

      <div className="prose">
        <p>Unpacking that:</p>
        <ul>
          <li>
            <strong>Same UID.</strong> A site-scoped key logs in as <code>acme</code>, exactly like a
            user-scoped key. The filesystem cannot tell the two apart, because to the filesystem they
            are the same person.
          </li>
          <li>
            <strong>The confinement is in sshd and a wrapper.</strong> A forced command plus{' '}
            <code>ratline-shell</code> restricts what can be asked for. Both are userspace, both are
            configuration, and neither is a security boundary the kernel is aware of.
          </li>
          <li>
            <strong>It works for what it is for.</strong> A contractor cannot accidentally{' '}
            <code>cd ../other-client.com</code> and start editing. A CI runner with{' '}
            <code>--command rsync-only</code> cannot run anything but rsync. That is genuinely
            valuable, and it is the common case.
          </li>
          <li>
            <strong>It fails against a real attacker.</strong> Anyone who achieves code execution as
            that UID — through the application, not through SSH — has whatever that UID has, which is
            every site the tenant owns.
          </li>
        </ul>

        <H3>The answer when you need a real boundary</H3>
        <p>
          <strong>One ratline user per site.</strong> Then the boundary is a UID, which the kernel does
          enforce, and a compromise of one site cannot read another’s <code>.env</code>.{' '}
          <code>user add</code> is cheap for exactly this reason.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# Blast-radius boundary: convenient, one tenant, two sites
ratline user add acme
ratline site add example.com  --user acme --runtime node   --entry server.js
ratline site add other.com    --user acme --runtime static

# Kernel boundary: one user per site
ratline user add acme-example
ratline user add acme-other
ratline site add example.com --user acme-example --runtime node --entry server.js
ratline site add other.com   --user acme-other   --runtime static`}
      />

      <Callout tone="note" title="features.strict_isolation">
        <p>
          There is a middle option: <code>features.strict_isolation</code> adds a chroot and a bind
          mount to site-scoped keys. It is off by default because a misconfigured chroot generates
          support tickets — and even on, it hardens the SSH path, not the application path.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="key-test">Ask the tool rather than guessing</H2>
        <p>
          <code>ratline key test</code> reads the rendered options back and states the result in plain
          language, including that final honest line. Run it before you tell a client that a
          contractor can only touch one site.
        </p>
      </div>

      <CodeBlock
        lang="text"
        code={`Key       SHA256:x9K…   "Deploy CI"   ed25519
Scope     site → example.com  (owner: alice)
Login     alice@server — forced command only, no interactive shell
Allowed   sftp, rsync, git-upload-pack, git-receive-pack
          confined to /home/alice/example.com (symlinks resolved)
Denied    shell, port forwarding, agent forwarding, X11, PTY
Source    203.0.113.0/24 only
Expires   2027-01-01 (149 days)
Last use  2026-08-02 14:11 from 203.0.113.19
Note      Runs as UID alice. Not a kernel boundary — see SECURITY.md.`}
        noCopy
      />

      <div className="prose">
        <H2 id="policy">Key validation policy</H2>
        <ul>
          <li>
            Every key is validated with <code>ssh-keygen -l -f</code> before it goes near a file.
          </li>
          <li>
            <code>ssh-dss</code> is refused outright — 1024-bit DSA has no place on a new server. RSA
            under 3072 bits is refused; under 4096 it is warned about. <code>ed25519</code> is
            preferred.
          </li>
          <li>Options the submitted line already carries are stripped, as above.</li>
          <li>
            Keys with newlines, NULs or lines over 8192 bytes are refused, and the whole file is
            capped at 262144 bytes.
          </li>
          <li>
            A fingerprint already present anywhere on the box is refused unless{' '}
            <code>--allow-duplicate</code>, and the message names where it already exists. Two people
            sharing a key defeats the point of labels.
          </li>
          <li>
            <code>--from-github &lt;user&gt;</code> fetches{' '}
            <code>https://github.com/&lt;user&gt;.keys</code> with full certificate verification,
            validates each line independently, then shows every fingerprint and asks for confirmation.
            An account can carry keys its owner has forgotten about.
          </li>
        </ul>

        <H2 id="lockout">Lockout safety</H2>
        <p>
          Bricking SSH on a remote VPS has no recovery path short of the provider’s console, so every
          change to <code>/etc/ssh</code> follows the same sequence:
        </p>
        <ol>
          <li>
            Back up the config, apply the change, run <code>sshd -t</code>. On failure, restore and
            reload.
          </li>
          <li>
            <strong>Reload, never restart</strong> — existing sessions survive, so the shell you are
            typing in stays alive even if the new config is wrong.
          </li>
          <li>
            After reloading, prove login still works. If verification cannot run or fails, restore and
            report the change as <em>rejected</em> rather than applied.
          </li>
          <li>
            Never touch <code>PermitRootLogin</code>, <code>PasswordAuthentication</code>,{' '}
            <code>AllowUsers</code> or <code>Port</code> without an explicit flag <em>and</em> a typed
            confirmation, printing the rollback command first.
          </li>
          <li>
            <code>key remove</code> and <code>user delete</code> refuse to remove the last working
            global credential without <code>--force</code> and a typed confirmation.
          </li>
        </ol>
      </div>

      <Terminal title="root@server">{`$ ratline key remove "Ali MacBook" --scope global
✗ error: refusing to remove the last working global credential
  hint: add a replacement key first, verify you can log in with it, then remove this one.
        To override: --force, and you will be asked to type the label.
~ exit 3 — this is the refusal that keeps you out of the provider's console`}</Terminal>

      <div className="prose">
        <p>
          The correct order is always add, verify, then remove — see{' '}
          <Link to="/guides/new-laptop-key">adding a key from a new laptop</Link>. And read{' '}
          <Link to="/guides/ssh-lockout">the lockout runbook</Link> before you need it, because by
          definition you cannot read it over SSH when you do.
        </p>
      </div>
    </article>
  );
}
