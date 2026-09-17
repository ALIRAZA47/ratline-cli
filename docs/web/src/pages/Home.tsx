import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { CodeBlock } from '../components/CodeBlock';
import { Terminal } from '../components/Terminal';
import { RequestPath } from '../components/diagrams/RequestPath';
import { Callout, CardLink, H2, StatusBadge } from '../components/ui';
import { commandGroups } from '../data/nav';
import { commandsOf, subjects, type Subject } from '../data/subjects';

/**
 * Where a subject's own reading starts.
 *
 * Subjects do not have a page of their own — the sidebar is the subject spine — so the
 * front door has to send somebody somewhere real. Its command index if it has commands,
 * the first concept behind it otherwise. Derived rather than declared, because a second
 * hand-written list of six paths is a second thing to forget to update.
 */
function entryPoint(subject: Subject): string {
  return commandsOf(subject)[0]?.path ?? subject.concepts[0] ?? subject.guides[0] ?? '/reference';
}

export function Home() {
  const total = commandGroups.reduce((n, g) => n + g.commands.length, 0);
  const built = commandGroups.reduce(
    (n, g) => n + g.commands.filter((c) => c.status === 'built').length,
    0,
  );

  return (
    /* The front door takes the wider canvas: a hero, a diagram and a grid of cards is not
       sustained reading, and at the reading measure the whole page reads as a column of
       leftovers. The prose inside it still sets to the measure. */
    <article data-width="wide">
      <header className="mb-14">
        <p className="not-prose label text-faint">The manual</p>
        <h1 className="mt-4 max-w-[19ch] text-3xl tracking-tight text-strong sm:text-4xl md:text-5xl">
          Put a website on a server you own, without becoming a sysadmin.
        </h1>
        <p className="lede mt-6 max-w-[42rem]">
          <code className="rounded border border-line bg-code px-1.5 py-0.5 font-mono text-[0.78em] text-strong">
            ratline
          </code>{' '}
          is one static binary that runs as root on a bare Ubuntu or Debian box and provisions
          isolated system users, nginx, systemd, certificates and databases for the apps you host.
          No containers, no agent, no account with anyone.
        </p>

        <div className="not-prose mt-8 flex flex-wrap items-center gap-3">
          <Link
            to="/quickstart"
            className="rounded-md bg-accent px-4 py-2.5 text-sm font-medium text-accent-fg no-underline shadow-[var(--shadow-card)] transition-colors hover:bg-accent-hover"
          >
            Put a site up in ten minutes
          </Link>
          <Link
            to="/reference"
            className="rounded-md border border-line-strong bg-raised px-4 py-2.5 text-sm font-medium text-fg no-underline shadow-[var(--shadow-card)] transition-colors hover:border-accent hover:bg-hover hover:text-accent"
          >
            Read the command reference
          </Link>
        </div>
      </header>

      <section className="not-prose mb-14">
        <p className="label mb-2.5 text-faint">On a fresh server</p>
        <CodeBlock
          lang="shell"
          code={`curl -fsSL https://ratline.alirazakhan.me/install.sh \\
  | sudo WITH_PANEL=1 PANEL_ADMIN_EMAIL=you@example.com sh`}
        />
        <p className="mt-2.5 max-w-[var(--content-w)] text-xs leading-relaxed text-muted">
          The installer checksums everything it downloads and refuses rather than warning.{' '}
          <code className="font-mono">WITH_PANEL</code> is optional — it brings{' '}
          <Link to="/panel" className="text-accent underline underline-offset-2">
            the browser interface
          </Link>{' '}
          along; leave it out and the server gets the CLI and nothing listening.
        </p>
      </section>

      {/* Three doors, because the three states of mind arriving here want different
          things: somebody evaluating wants one honest page, somebody with a job to do
          wants the commands in order, and somebody at 2am wants one flag. Sending all
          three into the same prose is what a wiki does. */}
      <section className="not-prose mb-14">
        <h2 className="mb-5 text-2xl text-strong">Which of these are you?</h2>
        <div className="grid gap-3 md:grid-cols-3">
          <DoorCard
            to="/concepts/model"
            eyebrow="Looking"
            title="Is this the right tool?"
          >
            What it does, what it deliberately refuses to do, and where it stops being the
            answer. The object model first, then the security model.
          </DoorCard>
          <DoorCard to="/quickstart" eyebrow="Doing" title="I have a thing to put up">
            A Node app, a Python API, a built static site, a database behind one of them. Each
            is a task with the commands in order.
          </DoorCard>
          <DoorCard to="/guides/debug-502" eyebrow="Stuck" title="Something is broken">
            A 502, a renewal that will not go through, a key that stopped working. The runbooks
            start from the symptom rather than the subsystem.
          </DoorCard>
        </div>
      </section>

      {built === total ? (
        <Callout tone="ok" title="Everything on this site is implemented">
          <p>
            All {total} documented commands are built and tested. Every one carries a{' '}
            <StatusBadge status="built" size="xs" /> badge, and nothing here describes behaviour that
            does not exist — the command pages are generated from the same surface the binary
            implements, and the concept pages are the same markdown the binary itself prints with{' '}
            <code>ratline explain</code>.
          </p>
          <p>
            Two deliberate limits, named rather than implied: <code>ratline db</code> provisions
            MongoDB and nothing else, behind the <code>features.db_provisioning</code> flag because
            it needs an admin connection string; and the only runtimes are static, node and python.
            There is no PHP, Go or Ruby — the runtime layer is an interface, so each would be a new
            file rather than a rewrite.
          </p>
        </Callout>
      ) : (
        <Callout tone="warn" title="Under construction, and the docs say so">
          <p>
            {built} of {total} documented commands are implemented today; the rest are specified and
            being built in order. Every command on this site carries a{' '}
            <StatusBadge status="built" size="xs" /> or <StatusBadge status="planned" size="xs" />{' '}
            badge, and nothing here describes behaviour that has not been specified.
          </p>
        </Callout>
      )}

      <div className="prose">
        <H2>The shape of it</H2>
        <p>
          A tenant is a system user: its own group, its own home at <code>0750</code>, a locked
          password, its own SSH keys, no sudo. A site belongs to one tenant and lives inside that
          tenant’s home. For <code>static</code> sites nginx serves the files and nothing runs; for
          the others the application runs under its own systemd unit, as that user, behind a Unix
          socket only nginx and the owner can open.
        </p>
        <p>
          Every command is staged, verified with the real tool, then committed — nginx configuration
          is checked with <code>nginx -t</code> before it is moved into place, a unit with{' '}
          <code>systemd-analyze verify</code>, a sudo rule with <code>visudo</code>. A command that
          fails halfway puts back what it changed and leaves the server serving.
        </p>
        <p>
          Everything answers <code>--json</code> and exits with{' '}
          <Link to="/reference/exit-codes">a code automation can branch on</Link>, and{' '}
          <code>--dry-run</code> means the same thing everywhere: it writes nothing, at any layer.
        </p>
      </div>

      <RequestPath />

      <div className="prose">
        <H2 id="shape">What a failure looks like</H2>
        <p>
          Invocation is always <code>ratline &lt;group&gt; &lt;verb&gt; [args]</code>. Errors state
          what failed, why, and the next action — with the last twenty lines of the journal already
          included when a unit failed to start.
        </p>
      </div>

      <Terminal title="root@server">{`$ ratline site add api.example.com --user acme --runtime python --app-module app.main:app
→ validating inputs
→ creating /home/acme/api.example.com
→ building virtualenv python 3.12
! no configuration file; using built-in defaults path=/etc/ratline/config.yaml fix="run 'ratline init'"
→ writing ratline-acme-api_example_com.service
→ nginx -t passed
→ waiting for health on /run/ratline/acme-api_example_com/app.sock
✗ site add failed: the app did not become healthy within 30s. systemd reports
  ratline-acme-api_example_com.service exited 3; the last log line was
  "ModuleNotFoundError: No module named 'app'". Nothing was enabled in nginx.
  hint: check --app-module against your project layout, then re-run with --dry-run to preview
~ exit code 7 — health_check_failed`}</Terminal>

      <div className="prose">
        <p>
          Note the last line of the failure: <em>nothing was enabled in nginx</em>. A deploy that
          would have returned 502 is a failure, not a success, and the rollback stack unwinds
          everything the attempt created.
        </p>
      </div>

      <div className="prose">
        <H2>What it does not do</H2>
        <p>Saying this plainly is cheaper than everybody discovering it individually.</p>
        <ul>
          <li>
            <strong>No containers.</strong> Isolation is Unix users plus systemd sandboxing on a
            shared kernel. That is defence in depth, not virtualization — see the{' '}
            <Link to="/concepts/security">security model</Link> for exactly where it stops.
          </li>
          <li>
            <strong>No web UI in this binary.</strong> It is a CLI with a <code>--json</code>{' '}
            envelope, designed to sit under one — and <Link to="/panel">ratline-panel</Link> is that
            one, a separate binary and a separate service. The installer will put it on beside
            ratline if you ask for it, and leaves it off if you do not — a server that did not
            request a web service should not find one listening. It reimplements nothing: every
            action it offers runs this binary and reads the envelope.
          </li>
          <li>
            <strong>No multi-server orchestration.</strong> One box. <code>ratline export</code>{' '}
            exists so you can move to another one.
          </li>
          <li>
            <strong>No shell strings, anywhere.</strong> Every external invocation is an argv slice,
            and there is no shell in the binary registry at all — which makes it structural rather
            than a convention.
          </li>
          <li>
            <strong>Three database engines, not every database.</strong> <code>ratline db</code>{' '}
            provisions databases and least-privilege users on MongoDB, MySQL/MariaDB and
            Redis/Valkey — pick one with <code>--engine</code> — behind{' '}
            <code>features.db_provisioning</code>, because it needs an admin connection string.
            Postgres is not supported.
          </li>
        </ul>
      </div>

      <div className="prose">
        <H2>Everything is scriptable</H2>
        <p>
          Every command takes <code>--json</code> and emits exactly one object on stdout, with logs
          on stderr. Exit codes are a{' '}
          <Link to="/reference/exit-codes">documented contract</Link> — automation branches on them,
          so they are declared once and never inferred from error text.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site list --runtime python --json | jq -r '.data.sites[].domain'

# 9 means "would exceed a CA rate limit", so back off rather than retry
ratline cert issue example.com --json > result.json || case $? in
  9) echo "rate limited; see the retry-after in result.json" ;;
  8) echo "challenge failed; DNS or the webroot" ;;
esac`}
      />

      {/* The subject spine, which is also the sidebar's. One structure, stated once, so
          the front door and the navigation cannot come to disagree about what this site
          is made of. */}
      <div className="prose">
        <H2>By subject</H2>
        <p>
          A subject owns everything about itself — its commands, the concepts behind them, the
          in-depth topics, the runbooks, and the settings that change how it behaves.
        </p>
      </div>

      <div className="not-prose mt-5 grid gap-3 sm:grid-cols-2">
        {subjects.map((s) => (
          <CardLink key={s.id} to={entryPoint(s)} title={s.title}>
            {s.blurb}
          </CardLink>
        ))}
      </div>
    </article>
  );
}

/** One of the three doors: a category of reader, and where that reader should go. */
function DoorCard({
  to,
  eyebrow,
  title,
  children,
}: {
  to: string;
  eyebrow: string;
  title: string;
  children: ReactNode;
}) {
  return (
    <Link
      to={to}
      className="group flex flex-col gap-2 rounded-[var(--radius-card)] border border-line bg-raised px-5 py-5 no-underline shadow-[var(--shadow-card)] transition-colors hover:border-accent hover:bg-hover"
    >
      <span className="label text-faint">{eyebrow}</span>
      <span className="font-serif text-xl leading-tight text-strong group-hover:text-accent">
        {title}
      </span>
      <span className="text-sm leading-relaxed text-muted">{children}</span>
    </Link>
  );
}
