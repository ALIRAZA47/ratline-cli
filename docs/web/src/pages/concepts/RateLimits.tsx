import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, H2, TableScroll } from '../../components/ui';
import { Inline } from '../../components/Inline';

const limits = [
  {
    setting: 'certs_per_registered_domain_per_week',
    value: '50',
    unit: 'per registered domain, per week',
    why: 'The one that hurts. It is scoped to the registrable domain (eTLD+1), so example.com, www.example.com and api.example.com all draw on the same 50.',
  },
  {
    setting: 'duplicate_certs_per_week',
    value: '5',
    unit: 'per identical SAN set, per week',
    why: 'Re-issuing the same exact name set five times in a week exhausts this. `cert renew --force` in a loop is the usual cause.',
  },
  {
    setting: 'failed_validations_per_hour',
    value: '5',
    unit: 'per hostname, per hour',
    why: 'This is why preflight reports every problem at once. Discovering three problems one attempt at a time costs three of your five.',
  },
  {
    setting: 'new_orders_per_3_hours',
    value: '300',
    unit: 'per account, per 3 hours',
    why: 'Rarely reached on one box, but it exists and it is per-account rather than per-domain.',
  },
];

export function ConceptRateLimits() {
  return (
    <article>
      <PageHeader
        eyebrow="Concepts"
        title="Rate limits — tracked, not discovered"
        lede="Every issuance attempt, successful or not, is recorded. The remaining budget is computed before acting, and an attempt that would exceed it is refused with a countdown."
      />

      <div className="prose">
        <p>
          The alternative — find out by being refused by the CA — is expensive in a way that is hard to
          appreciate until it happens. A week-long lockout on a client’s production domain, in the
          middle of a migration, because a script retried a failing issuance in a loop, is a bad
          afternoon that a locally tracked counter prevents entirely.
        </p>
        <p>
          So ratline records every attempt, computes the remaining budget per registered domain before
          acting, and refuses with exit{' '}
          <Link to="/reference/exit-codes#code-9">9 (rate_limited)</Link> and a retry-after when an
          attempt would exceed it. Nothing is sent to the CA in that case.
        </p>
      </div>

      <TableScroll>
        <table className="w-full min-w-[46rem] border-collapse text-left text-sm">
          <caption className="sr-only">Default rate-limit budgets</caption>
          <thead>
            <tr className="bg-sunken text-2xs uppercase tracking-wider text-muted">
              <th scope="col" className="px-3 py-2 font-medium">
                acme.rate_limits.*
              </th>
              <th scope="col" className="w-[4rem] px-3 py-2 font-medium">
                Default
              </th>
              <th scope="col" className="w-[14rem] px-3 py-2 font-medium">
                Scope
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                What exhausts it
              </th>
            </tr>
          </thead>
          <tbody>
            {limits.map((l) => (
              <tr key={l.setting} className="border-t border-line align-top">
                <th scope="row" className="px-3 py-2.5 text-left font-normal">
                  <code className="font-mono text-xs text-accent">{l.setting}</code>
                </th>
                <td className="px-3 py-2.5 font-mono text-sm text-strong">{l.value}</td>
                <td className="px-3 py-2.5 text-xs text-muted">{l.unit}</td>
                <td className="px-3 py-2.5 leading-relaxed">
                  <Inline text={l.why} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableScroll>

      <Callout tone="warn" title="These are configurable because they are CA policy">
        <p>
          The numbers above are the certificate authority’s published limits, mirrored locally. They
          are policy and they <em>do</em> change. If a refusal looks wrong, check the CA’s
          documentation and adjust <code>acme.rate_limits</code> — a local counter that is stricter
          than reality is annoying, and one that is looser is useless.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="registered-domain">Why the registered domain matters</H2>
        <p>
          The per-domain limit is applied to the <strong>registrable domain</strong> — eTLD+1, computed
          from the public suffix list. Every subdomain shares one budget:
        </p>
      </div>

      <CodeBlock
        lang="text"
        code={`example.com          ─┐
www.example.com       │
api.example.com       ├─ all draw on the same 50 per week
staging.example.com   │
*.example.com        ─┘

acme.example.co.uk   ─── example.co.uk is the registrable domain here,
                         because co.uk is a public suffix`}
        noCopy
      />

      <div className="prose">
        <p>
          A bare public suffix is refused before an attempt is ever made:{' '}
          <code>"com" is a public suffix rather than a domain you can own</code>, with the hint{' '}
          <code>use a name under it, for example app.com</code>. That refusal exists precisely to avoid
          burning a rate-limit attempt on something that cannot succeed.
        </p>
        <p>
          Names under a <em>private</em> suffix — <code>github.io</code> and friends — are legitimate
          but the CA rate-limits them differently. ratline can tell the difference and says so.
        </p>
      </div>

      <div className="prose">
        <H2 id="budget">Seeing the budget</H2>
        <p>
          Preflight prints the remaining budget as one of its checks, so you see it on every issuance
          rather than having to go and ask.
        </p>
      </div>

      <Terminal title="root@server">{`$ ratline cert issue api.example.com --email admin@example.com
→ preflight: site enabled, vhost found
→ preflight: A 203.0.113.10 matches this server
→ preflight: webroot token fetched, 200 with exact match
→ preflight: budget 3 of 50 certificates remaining this week for example.com
! only 3 certificates remain for example.com this week; the window resets 2026-08-09 11:04 UTC
→ certbot: certificate issued, expires 2026-11-02

$ ratline cert issue another.example.com
✗ error: this would exceed the certificates-per-registered-domain limit for example.com
         (50 issued in the last 7 days)
  hint: the oldest attempt in the window ages out in 2d 14h (2026-08-07 09:12 UTC).
        Use --staging to keep testing, or --dry-run to validate at no cost.
~ exit 9 — nothing was sent to the CA`}</Terminal>

      <div className="prose">
        <H2 id="budgeting">How to not run out</H2>
        <ul>
          <li>
            <strong>Use <code>--dry-run</code> while you are debugging.</strong> Full validation, no
            rate-limit cost. It is the same code path, so it catches the same problems.
          </li>
          <li>
            <strong>Use <code>--staging</code> for anything iterative.</strong> Staging has its own,
            far looser limits — that is the point of it. Staging certificates are marked visibly
            untrusted in <code>cert list</code>, so nobody ships one to production by accident.
          </li>
          <li>
            <strong>Put every name on one certificate.</strong> Apex plus www as SANs is one
            certificate against the budget, not two. <code>--alias</code> and <code>--san</code> exist
            for this.
          </li>
          <li>
            <strong>Do not loop on <code>--force</code>.</strong> Each forced renewal counts against
            the duplicate limit, which is only 5. This is the single fastest way to lock yourself out
            for a week.
          </li>
          <li>
            <strong>Fix preflight failures all at once.</strong> The reason it reports every problem
            together is that each failed validation costs one of five per hour, per hostname.
          </li>
          <li>
            <strong>Do not run two renewal timers.</strong> <code>ratline init</code> neutralises
            certbot’s own timer for exactly this reason. If you installed certbot separately
            afterwards, check that it did not put its timer back.
          </li>
        </ul>
      </div>

      <Callout tone="note" title="--force does not bypass the budget">
        <p>
          <code>--force</code> re-issues past a valid certificate and past a DNS mismatch. It does not
          bypass the rate-limit refusal, because that refusal is what stops you locking yourself out of
          issuance for a week — and a flag that could switch it off would be used in a retry loop
          within a month.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="alerts">Alerts</H2>
        <p>
          <code>acme.alerts.warn_days</code> defaults to 7: a certificate with fewer days than that
          left triggers a warning to <code>acme.alerts.email</code> or{' '}
          <code>acme.alerts.webhook_url</code>. Since renewal starts at 30 days, reaching 7 means three
          weeks of retries have failed — so treat that alert as an incident rather than a reminder, and
          go and read <Link to="/guides/renewal-runbook">the renewal runbook</Link>.
        </p>
      </div>
    </article>
  );
}
