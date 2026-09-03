import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Callout, CardLink, Facts, H2, H3 } from '../../components/ui';

const layers = [
  {
    n: 1,
    title: 'The browser talks to the panel',
    body: 'A session cookie, a CSRF token held in the page’s memory, and a strict Content-Security-Policy with no inline scripts or styles. Nothing else reaches the server from a page.',
  },
  {
    n: 2,
    title: 'The panel builds an argv slice',
    body: 'Element by element, from typed values checked against the flags the installed binary declares. No string is split into a command line, there is no shell anywhere, and a value that fails validation never reaches execve.',
  },
  {
    n: 3,
    title: 'ratline does the work',
    body: 'The same binary, the same global lock, the same staged-verified-committed discipline and the same rollback stack. A mutation made in a browser is the mutation that would have run over SSH.',
  },
  {
    n: 4,
    title: 'Two records, of two different things',
    body: 'ratline writes its own audit entry for the command. The panel writes who asked for it — which ratline cannot know, because every invocation reaches it as root.',
  },
];

export function PanelOverview() {
  return (
    <article className="prose">
      <PageHeader
        eyebrow="The web panel"
        title="ratline-panel"
        lede={
          <>
            A web interface for ratline: a separate binary, a separate service and a
            separate install. It reimplements nothing — every action runs{' '}
            <code>ratline &lt;verb&gt; --json</code> and reads the envelope.
          </>
        }
      />

      <p>
        Ploi, RunCloud and moss.sh all solve the same problem: somebody has a server and
        would rather not do everything over SSH. What they build is a control plane with
        its own idea of what a site is, which then has to be kept in step with the machine.
        This is the other shape. The command line is the product; the panel is a caller of
        it.
      </p>

      <p>
        That is not a slogan about architecture — it is what makes the panel safe to give
        somebody. A deploy started in a browser is staged, verified, committed and rolled
        back by the same code that would have run had you typed it, takes the same global
        lock, and fails with the same exit code and the same hint.
      </p>

      <CodeBlock
        lang="text"
        noCopy
        filename="how a button becomes a change"
        code={`browser ──HTTP──▶ ratline-panel ──argv──▶ ratline
                        │                    │
                        │                    ├─▶ nginx
                   panel.db             state.db  systemd
                  (who asked)        (what exists) certbot`}
      />

      <H2 id="how">What happens when you press a button</H2>

      <div className="not-prose my-7 space-y-2.5">
        {layers.map((s) => (
          <div
            key={s.n}
            className="flex gap-4 rounded-[var(--radius-card)] border border-line bg-raised px-4 py-3"
          >
            <span className="mt-0.5 inline-flex size-6 shrink-0 items-center justify-center rounded-full bg-accent-soft font-mono text-xs font-semibold text-accent">
              {s.n}
            </span>
            <div>
              <p className="font-medium text-strong">{s.title}</p>
              <p className="mt-1 max-w-[var(--container-measure)] text-sm leading-relaxed text-muted">
                {s.body}
              </p>
            </div>
          </div>
        ))}
      </div>

      <H2 id="two-databases">Two databases</H2>

      <p>
        The panel has its own, and never writes to ratline&rsquo;s. That is a boundary
        rather than a preference: <code>state.db</code> is ratline&rsquo;s index of what
        exists on this server and it has exactly one writer, so there is one answer to
        &ldquo;what is on this machine&rdquo; and it is the one ratline gives. Anything the
        panel wants to know about sites, tenants, certificates or databases, it asks the
        CLI for.
      </p>

      <Facts
        rows={[
          [
            '/var/lib/ratline/state.db',
            <>ratline&rsquo;s. Sites, tenants, keys, certificates, deployments. The panel reads it only through <code>ratline … --json</code>.</>,
          ],
          [
            '/var/lib/ratline/panel.db',
            <>The panel&rsquo;s. Accounts, sessions, invitations, jobs and who asked for what. 0600 root:root — the one file that, read, lets somebody become an administrator of this server.</>,
          ],
        ]}
      />

      <H2 id="what-it-adds">What it adds that the command line does not</H2>

      <H3 id="dry-run">A rehearsal you can read before you commit</H3>
      <p>
        Every mutating action has a dry run beside the real one. ratline implements{' '}
        <code>--dry-run</code> at the Runner, so nothing is written at any layer, and the
        plan you read is produced by the same code path that would have done the work. It
        is the feature a hand-written web provisioner cannot have, and here it was free.
      </p>
      <Callout tone="note" title="One caveat, and it is ratline’s own">
        A command that composes other commands cannot rehearse itself by running them with{' '}
        <code>--dry-run</code>: each step preconditions on the previous one having really
        happened. Those resolve a plan and print it instead, which is what the panel shows.
      </Callout>

      <H3 id="jobs">A transcript that outlives the tab</H3>
      <p>
        A deploy, an issuance or a runtime build is a job with a stored log, streamed to the
        browser over server-sent events. Closing the tab does not stop it, and the
        transcript is still there tomorrow. Jobs run one at a time, because ratline takes a
        global lock for every mutation — queueing turns a lock contention failure into a
        position in a line you can watch.
      </p>

      <H3 id="forms">Forms that cannot offer a flag the binary does not have</H3>
      <p>
        They are generated from <code>ratline schema</code>, which the binary produces by
        walking its own command tree. So a form offers exactly the flags the installed
        ratline takes, with the types and required-ness it declares — and a ratline upgrade
        that adds a flag adds a field without anybody editing the panel.
      </p>

      <H2 id="not">What it deliberately does not have</H2>
      <p>
        No terminal, no file browser, no editor. If you need those you need SSH, and the
        panel not offering them is precisely what lets you give it to somebody who should
        not have them.
      </p>

      <div className="not-prose my-8 grid gap-3 sm:grid-cols-2">
        <CardLink
          to="/panel/install"
          title="Install it"
          >
            One command onto a server already running ratline. It creates the first super
            admin itself.
          </CardLink>
        <CardLink
          to="/panel/domain"
          title="Put it on a domain"
          >
            An nginx vhost and a certificate, staged and rolled back like everything else.
          </CardLink>
        <CardLink
          to="/panel/team"
          title="Super admins and admins"
          >
            Two roles, what separates them, and how invitations work.
          </CardLink>
        <CardLink
          to="/panel/security"
          title="The security model"
          >
            What signing in actually grants, and the four settings that matter.
          </CardLink>
        <CardLink
          to="/panel/commands"
          title="ratline-panel commands"
          >
            Install, domain, account recovery and doctor.
          </CardLink>
        <CardLink
          to="/panel/api"
          title="The HTTP API"
          >
            The same envelope as the CLI, for anything you would rather script.
          </CardLink>
      </div>

      <p>
        The same page in a terminal:{' '}
        <code>ratline explain panel</code> — or{' '}
        <Link to="/topics/panel">read it here</Link>.
      </p>
    </article>
  );
}
