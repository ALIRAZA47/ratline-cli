import { Link } from 'react-router-dom';
import { CodeBlock } from '../components/CodeBlock';
import { Terminal } from '../components/Terminal';
import { RequestPath } from '../components/diagrams/RequestPath';
import { Callout, CardLink, H2, StatusBadge } from '../components/ui';
import { commandGroups } from '../data/nav';

export function Home() {
  const total = commandGroups.reduce((n, g) => n + g.commands.length, 0);
  const built = commandGroups.reduce(
    (n, g) => n + g.commands.filter((c) => c.status === 'built').length,
    0,
  );

  return (
    <article>
      <header className="mb-10">
        <p className="not-prose font-mono text-xs uppercase tracking-[0.18em] text-muted">
          Server-side CLI · Go · Ubuntu
        </p>
        <h1 className="mt-3 max-w-[38rem] text-4xl font-semibold tracking-tight text-strong md:text-5xl">
          One bare VPS, many isolated sites.
        </h1>
        <p className="mt-5 max-w-[36rem] text-lg leading-relaxed text-muted">
          <code className="rounded border border-line bg-code px-1.5 py-0.5 font-mono text-[0.9em] text-strong">
            ratline
          </code>{' '}
          provisions isolated system users and their web apps — static, node or python — on a single
          Ubuntu server. Each site gets an nginx vhost, a TLS certificate, its own systemd unit, its
          own logs, and SSH keys scoped to exactly what their holder should reach.
        </p>
        <p className="mt-4 max-w-[36rem] text-base leading-relaxed text-fg">
          It is the provisioning core of Ploi, RunCloud or Dokku — minus the web UI, and minus
          containers.
        </p>

        <div className="not-prose mt-7 flex flex-wrap items-center gap-3">
          <Link
            to="/quickstart"
            className="rounded-md bg-accent px-4 py-2 text-sm font-medium text-accent-fg no-underline transition-colors hover:bg-accent-hover"
          >
            60-second quickstart
          </Link>
          <Link
            to="/reference"
            className="rounded-md border border-line bg-raised px-4 py-2 text-sm font-medium text-fg no-underline transition-colors hover:border-line-strong hover:bg-hover"
          >
            Command reference
          </Link>
        </div>
      </header>

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
            MongoDB and nothing else, behind the <code>features.db_provisioning</code> flag because it
            needs an admin connection string; and the only runtimes are static, node and python. There
            is no PHP, Go or Ruby — the runtime layer is an interface, so each would be a new file
            rather than a rewrite.
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
        <H2>What it actually does</H2>
        <p>
          A tenant is a system user: its own group, its own home at <code>0750</code>, a locked
          password, its own SSH keys, no sudo. A site belongs to one tenant and lives inside that
          tenant’s home. For <code>static</code> sites nginx serves the files and nothing runs. For{' '}
          <code>node</code> and <code>python</code> sites the application runs under its own systemd
          unit, as that user, behind a Unix socket that only nginx and the owner can open.
        </p>
        <p>
          The interesting part is not any single command — it is that every mutation is staged,
          verified and committed as a unit, that a health check is a real HTTP request rather than a
          process check, and that a certificate is not considered issued until a TLS connection
          proves it is actually being served.
        </p>
      </div>

      <RequestPath />

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
            <strong>No web UI.</strong> It is a CLI with a <code>--json</code> envelope, designed to
            sit under one.
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
            <strong>Databases are MongoDB only.</strong>{' '}
            <code>ratline db</code> provisions databases and least-privilege users, behind{' '}
            <code>features.db_provisioning</code> because it needs an admin connection
            string. Nothing else — Postgres, MySQL, Redis — is supported.
          </li>
        </ul>
      </div>

      <div className="prose">
        <H2 id="shape">The shape of a command</H2>
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

      <div className="prose">
        <H2>Where to go next</H2>
      </div>

      <div className="not-prose mt-4 grid gap-3 sm:grid-cols-2">
        <CardLink to="/quickstart" title="60-second quickstart">
          Install, one user, one site, working HTTPS — with the exact commands for static, node and
          python.
        </CardLink>
        <CardLink to="/concepts/model" title="The object model">
          Users, sites, runtimes and the request path. Read this before the reference.
        </CardLink>
        <CardLink to="/concepts/ssh-scopes" title="The three SSH scopes">
          Including an honest section on what site scope does <em>not</em> enforce.
        </CardLink>
        <CardLink to="/concepts/tls-lifecycle" title="TLS lifecycle">
          Issue, attach, renew — and how to choose between HTTP-01 and DNS-01.
        </CardLink>
        <CardLink to="/guides/debug-502" title="Debugging a 502">
          Six causes, in the order that finds them fastest.
        </CardLink>
        <CardLink to="/reference/config" title="Configuration reference">
          Every setting, its default, and the reason the default is what it is.
        </CardLink>
      </div>
    </article>
  );
}
