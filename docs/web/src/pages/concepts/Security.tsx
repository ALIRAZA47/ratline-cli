import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, H2, H3 } from '../../components/ui';

export function ConceptSecurity() {
  return (
    <article>
      <PageHeader
        eyebrow="Concepts"
        title="Security model"
        lede="What is structurally prevented, what is defence in depth, and where the isolation stops. The last part is the part worth reading."
      />

      <div className="prose">
        <H2 id="no-shell">Never build shell strings</H2>
        <p>
          Every external invocation is an argv slice. <strong>There is no shell in the binary registry
          at all</strong>, which makes this structural rather than a convention — a future contributor
          cannot accidentally reintroduce shell execution, because there is nothing to reintroduce it
          with.
        </p>
        <p>
          <code>--start-command</code>, <code>--build-command</code> and{' '}
          <code>--install-command</code> are parsed by a shell-words parser that <em>refuses</em>{' '}
          <code>;</code>, <code>&amp;&amp;</code>, <code>|</code>, backticks, <code>$(</code>,
          redirections and newlines. A genuine pipeline goes in a script in the repository.
        </p>
      </div>

      <Terminal title="root@server">{`$ ratline site add example.com --user acme --runtime node \\
    --start-command "npm run build && node server.js"
✗ error: command contains "&&" (command chaining) at position 15, which needs a shell
  hint: put the pipeline in a script inside your repository and reference that script instead,
        for example --start-command "./bin/start"
~ exit 2

$ ratline site add example.com --user acme --runtime node --start-command "sh -c 'node server.js'"
✗ error: "sh" may not be the command's program
  hint: ratline executes commands directly rather than through a shell;
        reference the real program, or a script in your repository
~ exit 2 — sh, bash, env, exec, sudo, xargs and friends exist to reinterpret their
~ arguments, which would defeat argv-only execution`}</Terminal>

      <div className="prose">
        <H3>Binaries, PATH and the child environment</H3>
        <p>
          Absolute paths for all external binaries, resolved at startup. <code>PATH</code> is never
          inherited for lookups, and children get a scrubbed environment. That closes the whole family
          of “a tenant put a malicious <code>nginx</code> earlier in <code>PATH</code>” problems, and it
          is also why <code>ratline version</code> can report the OpenSSH and nginx versions truthfully:
          it looked them up itself.
        </p>

        <H2 id="privileges">Privileges</H2>
        <ul>
          <li>
            Refuses to run unless EUID is 0 — except <code>version</code>, <code>man</code> and{' '}
            <code>completion</code>.
          </li>
          <li>
            Refuses to run if its own binary is group- or world-writable. A writable binary that runs as
            root is a root escalation waiting for a cron job.
          </li>
          <li>
            <code>npm install</code> and <code>pip install</code> run <strong>as the site user</strong>,
            never as root. A postinstall script is arbitrary code from the internet, and running it as
            root because it is convenient is the single most common way a provisioning tool becomes the
            vulnerability.
          </li>
          <li>
            Created users get no sudo. <code>users.allow_sudo</code> only permits the escape hatch to
            exist; each grant is still validated with <code>visudo -c</code>.
          </li>
        </ul>
      </div>

      <Terminal title="ali@server">{`$ ratline site list
✗ error: ratline must run as root; the current effective UID is 501
  hint: re-run it with sudo
~ exit 3 — real output`}</Terminal>

      <div className="prose">
        <H2 id="inputs">Inputs are treated as hostile</H2>
        <p>
          Usernames, domains and paths routinely arrive from a web form by way of an automation layer, so
          each is validated before anything is touched. The full rules are on the{' '}
          <Link to="/reference/validation">validation page</Link>; the short version:
        </p>
        <ul>
          <li>
            Username: <code>^[a-z_][a-z0-9_-]{'{0,31}'}$</code>, no reserved names, no{' '}
            <code>/etc/passwd</code> or <code>/etc/group</code> collision.
          </li>
          <li>
            Domain: per-label <code>^[a-z0-9]([a-z0-9-]{'{0,61}'}[a-z0-9])?$</code>, ≤253 characters, ≥2
            labels, a valid public suffix, IDN converted to punycode before use.
          </li>
          <li>
            App module: <code>^[A-Za-z_][A-Za-z0-9_.]*:[A-Za-z_][A-Za-z0-9_]*$</code> — this string lands
            on a command line and inside a unit file.
          </li>
          <li>
            Paths: containment is checked <em>after symlink resolution</em>, so a link planted in a
            tenant’s own home cannot redirect a document root.
          </li>
        </ul>

        <H2 id="secrets">Secrets</H2>
        <ul>
          <li>
            Never in argv, where they would appear in the process list and in the audit log.{' '}
            <code>env set</code> and <code>user password set</code> support <code>--stdin</code>.
          </li>
          <li>
            Redacted in logs, in errors and in <code>env list</code> unless <code>--reveal</code>.
          </li>
          <li>Private key material never appears in any output, JSON included.</li>
          <li>
            <code>.env</code> is <code>0600</code>, outside anything nginx serves, and nginx denies
            dotfiles as well.
          </li>
          <li>
            DNS provider credentials must be <code>0600</code> and are validated before use — that token
            can usually rewrite every record in the zone.
          </li>
        </ul>
      </div>

      <div className="prose">
        <H2 id="limits">Where the isolation stops</H2>
        <p>
          Stated plainly, because a security model whose limits are implied is a security model people
          get wrong.
        </p>
      </div>

      <div className="not-prose my-6 space-y-3">
        {[
          {
            t: 'A shared kernel',
            b: 'This is not virtualization. Every site runs on one kernel. A kernel vulnerability is a boundary failure for the whole box, and there is no second layer behind it.',
          },
          {
            t: 'systemd sandboxing is defence in depth, not a container',
            b: 'ProtectSystem=strict, ProtectHome=tmpfs, RestrictNamespaces and SystemCallFilter raise the cost of an escape considerably. They are not a security boundary of the kind a hypervisor provides, and they should not be described as one.',
          },
          {
            t: 'Tenants can see process names',
            b: '/proc is not hidden between users. One tenant can see that another tenant’s gunicorn is running, and how many workers it has. Not the contents of its memory or its environment — but the existence and the command line, yes.',
          },
          {
            t: 'cgroup limits are advisory unless configured',
            b: 'MemoryMax and CPUQuota need the controllers enabled and delegated. On a stock Ubuntu with cgroup v2 they are. On an unusual host they may not be, and a limit that is not enforced is one you are relying on for nothing.',
          },
          {
            t: 'Site-scoped SSH keys are a blast-radius boundary, not a kernel one',
            b: 'The key still authenticates as the site owner’s UID; the confinement is sshd’s forced command plus the ratline-shell wrapper. It reliably prevents accidents. It does not stop a determined attacker who already has code execution as that UID.',
          },
        ].map((row) => (
          <div
            key={row.t}
            className="rounded-[var(--radius-card)] border border-[color-mix(in_oklab,var(--warn)_30%,transparent)] bg-warn-soft px-4 py-3"
          >
            <p className="flex items-start gap-2 font-medium text-strong">
              <span aria-hidden="true" className="mt-0.5 font-mono text-warn">
                !
              </span>
              {row.t}
            </p>
            <p className="mt-1 max-w-[var(--container-measure)] pl-5 text-sm leading-relaxed text-fg">
              {row.b}
            </p>
          </div>
        ))}
      </div>

      <div className="prose">
        <H3>What to do about it</H3>
        <p>
          <strong>One ratline user per site</strong> wherever the tenants do not trust each other. Then
          the boundary is a UID, which the kernel does enforce, and a compromise of one site cannot read
          another’s <code>.env</code>. <code>user add</code> is cheap for exactly this reason — see{' '}
          <Link to="/concepts/ssh-scopes#site-scope-limits">the SSH scopes page</Link>.
        </p>
        <p>
          If you need a boundary stronger than a UID on a shared kernel, you need separate machines. That
          is not a failing of this tool; it is the shape of the problem, and the honest thing to do is say
          so rather than imply otherwise.
        </p>

        <H2 id="lockout">The lockout safeguards</H2>
        <p>
          Bricking SSH on a remote VPS has no recovery path short of the provider’s console, so changes
          under <code>/etc/ssh</code> are treated as the most dangerous thing the tool does:
        </p>
        <ol>
          <li>Back up, apply, <code>sshd -t</code>. On failure, restore and reload.</li>
          <li>
            <strong>Reload, never restart</strong> — existing sessions survive, so the shell you are
            typing in stays alive even if the new config is wrong.
          </li>
          <li>
            Prove login still works afterwards. If verification cannot run or fails, restore and report
            the change as <em>rejected</em>.
          </li>
          <li>
            Never touch <code>PermitRootLogin</code>, <code>PasswordAuthentication</code>,{' '}
            <code>AllowUsers</code> or <code>Port</code> without an explicit flag <em>and</em> a typed
            confirmation, printing the rollback command first.
          </li>
          <li>
            Refuse to remove the last working global credential without <code>--force</code> and a typed
            confirmation.
          </li>
        </ol>
        <p>
          <code>ssh.verify_after_change</code> is <code>true</code> by default. Turning it off on a remote
          server is how people lock themselves out — the configuration comment says so in those words.
        </p>

        <H2 id="destructive">Destructive operations</H2>
        <p>
          A destructive command prints a precise inventory of what will be deleted — paths, unit,
          certificate, port, state rows, home directory size — and requires you to type the domain or the
          username. Never a bare <code>y/N</code>: the thing being deleted is somebody’s site, and a
          reflex keystroke should not be enough.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# Without a terminal, a confirmation is exit 10 rather than a hung process
ratline site delete old.example.com --purge
# error: this operation cannot be confirmed without a terminal
#   hint: pass --yes to proceed

# In automation, --yes is explicit and gets logged as such
ratline site delete old.example.com --purge --backup /var/backups/ratline --yes
# ! destructive operation confirmed by --yes target=old.example.com`}
      />

      <Callout tone="note" title="The audit trail records the person, not just the account">
        <p>
          Every invocation is recorded with the command, its argv, the UID, and the{' '}
          <code>SUDO_USER</code> behind it — so <code>sudo ratline user delete acme</code> records who
          actually ran it rather than “root”. Failures are recorded too, because a failed destructive
          command is exactly what you will want to find later.
        </p>
      </Callout>
    </article>
  );
}
