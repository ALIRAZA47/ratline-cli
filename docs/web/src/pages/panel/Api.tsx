import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Callout, Facts, H2, H3, TableScroll } from '../../components/ui';

const endpoints: [string, string][] = [
  ['GET /api/bootstrap', 'Whether anybody has claimed this panel. The only thing an unauthenticated caller may know.'],
  ['POST /api/auth/login', 'email, password and (once enrolled) code. Returns the account and a CSRF token.'],
  ['POST /api/auth/logout', 'Ends this session.'],
  ['GET /api/me', 'The account, its capabilities, and this session’s CSRF token.'],
  ['GET /api/actions', 'Every action this role may run, with its flags. Filter with ?group=sites.'],
  ['GET /api/actions/{id}', 'One action, as the form needs it.'],
  ['POST /api/actions/{id}/preview', 'Run it with --dry-run and return the plan.'],
  ['POST /api/actions/{id}/run', 'Run it. 202 with a job_id for anything long.'],
  ['GET /api/overview', 'The dashboard: ratline status, recent jobs and recent activity in one call.'],
  ['GET /api/sites, /api/tenants, /api/keys, /api/certs, /api/databases, /api/runtimes', 'ratline’s own JSON, passed through.'],
  ['GET /api/sites/{domain}', 'One site in full.'],
  ['GET /api/sites/{domain}/logs?lines=200', 'The tail of the site’s log, as text.'],
  ['GET /api/jobs, /api/jobs/{id}', 'The queue and one job with its transcript.'],
  ['GET /api/jobs/{id}/stream', 'Server-sent events: log and state.'],
  ['GET /api/activity', 'Who asked for what. ?failed=true for the interesting half.'],
  ['GET /api/team', 'Accounts and invitations. Super admin only.'],
  ['POST /api/team/invites', 'Create an invitation link. Super admin only.'],
];

export function PanelApi() {
  return (
    <article className="prose">
      <PageHeader
        eyebrow="The web panel"
        title="The HTTP API"
        lede="The interface has no private endpoints. Everything it does is here, in the same envelope shape the CLI prints."
      />

      <p>
        If you can already read <code>ratline --json</code>, you can read this: the same{' '}
        <code>ok</code>, the same error object with a code, a name, a message and a hint. A
        ratline failure passing through keeps its exit code the whole way rather than being
        flattened into a 500.
      </p>

      <CodeBlock
        lang="json"
        noCopy
        code={`{
  "ok": false,
  "error": {
    "code": 5,
    "name": "locked",
    "message": "another ratline invocation holds the lock",
    "hint": "retry shortly"
  }
}`}
      />

      <H2 id="status-codes">Which status you get</H2>
      <Facts
        rows={[
          ['200 with ok:false', <>ratline ran and failed. The panel did its job; the exit code, the hint and the log are in the body, which is what a client needs to show.</>],
          ['400', 'The request was wrong: a bad value, an unknown field, a missing confirmation.'],
          ['401', 'No session, or it has expired.'],
          ['403', 'A missing or wrong CSRF token, a cross-origin request, or an action above your role.'],
          ['404', <>No such action, or one you may not run. The same answer for both, so the endpoint cannot be used to map the surface above you.</>],
          ['409', <>A precondition failed, or ratline is locked. Not a 5xx: the server is fine, and monitoring should not page on it.</>],
          ['429', 'Too many failed sign-ins.'],
        ]}
      />

      <H2 id="auth">Authenticating</H2>
      <p>
        A session cookie and a CSRF header. There is no API token, deliberately: a
        long-lived bearer credential for a root-equivalent surface is a thing to store
        somewhere, and the tool that already has one is SSH.
      </p>

      <CodeBlock
        lang="bash"
        prompt
        code={`# sign in, keeping the cookie
CSRF=$(curl -s -c jar -X POST https://panel.example.com/api/auth/login \\
  -H 'Content-Type: application/json' \\
  -d '{"email":"you@example.com","password":"…","code":"123456"}' \\
  | jq -r .data.csrf)

# then every state-changing call carries both
curl -s -b jar -X POST https://panel.example.com/api/actions/site.deploy/run \\
  -H 'Content-Type: application/json' -H "X-Ratline-CSRF: $CSRF" \\
  -d '{"args":["api.example.com"]}'`}
      />

      <Callout tone="note" title="For automation, use the CLI">
        Anything you would script against this API, you can run as{' '}
        <code>ratline … --json</code> over SSH with a scoped key — which is one fewer
        credential, one fewer network service, and{' '}
        <Link to="/guides/ci-deploy-keys">already documented</Link>. The API is here because
        the interface uses it, and because sometimes a script is already inside the network.
      </Callout>

      <H2 id="endpoints">Endpoints</H2>
      <TableScroll>
        <table>
          <thead>
            <tr>
              <th>Endpoint</th>
              <th>What it is</th>
            </tr>
          </thead>
          <tbody>
            {endpoints.map(([e, what]) => (
              <tr key={e}>
                <td>
                  <code>{e}</code>
                </td>
                <td>{what}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableScroll>

      <H2 id="running">Running an action</H2>
      <p>
        An action id is its verb with dots for spaces: <code>site.deploy</code>,{' '}
        <code>db.user.add</code>. The body has four fields, all optional:
      </p>
      <Facts
        rows={[
          ['args', 'The positional arguments, in order.'],
          ['flags', <>A map of flag name to value. A bool is a bool; a repeatable flag is an array. Unknown flags are refused, because the panel knows which ones the installed binary has.</>],
          ['secret / secret_key', <>The value that reaches ratline on stdin, and the name that goes with it where stdin is an assignment. Never in argv.</>],
          ['confirm', <>The target&rsquo;s name, typed back, for anything irreversible.</>],
        ]}
      />

      <H3 id="jobs">Long-running actions</H3>
      <p>
        A deploy, an issuance or an install returns <code>202</code> and a{' '}
        <code>job_id</code>. Poll <code>/api/jobs/&#123;id&#125;</code> or follow the
        transcript:
      </p>
      <CodeBlock
        lang="bash"
        prompt
        code={`curl -N -b jar https://panel.example.com/api/jobs/$JOB/stream`}
      />
      <CodeBlock
        lang="text"
        noCopy
        code={`event: log
data: installing dependencies as acme

event: log
data: health check: 200 in 41ms

event: state
data: done`}
      />

      <H2 id="rehearse">Rehearse first</H2>
      <p>
        <code>/preview</code> is the same call with <code>--dry-run</code>. It writes
        nothing at any layer, and it is the cheapest way to find out whether a request is
        shaped the way you think — the response includes the exact argv the panel would run.
      </p>
    </article>
  );
}
