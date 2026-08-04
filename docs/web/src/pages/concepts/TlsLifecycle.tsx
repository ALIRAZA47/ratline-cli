import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, H2, H3, TableScroll } from '../../components/ui';

const statuses: { name: string; tone: string; meaning: string }[] = [
  { name: 'valid', tone: 'ok', meaning: 'More than 21 days remaining, being served, renewal healthy.' },
  { name: 'expiring', tone: 'warn', meaning: 'Under 21 days. Yellow. Renewal should have started at 30.' },
  { name: 'critical', tone: 'danger', meaning: 'Under 7 days. Red. Something is wrong with renewal — go and look.' },
  { name: 'expired', tone: 'danger', meaning: 'Past its notAfter. Browsers are already refusing it.' },
  { name: 'degraded', tone: 'warn', meaning: 'The last renewal failed. The previous certificate is retained and doctor surfaces it.' },
  { name: 'staging', tone: 'warn', meaning: 'Issued from Let’s Encrypt staging. Visibly untrusted, so nobody ships one by accident.' },
  { name: 'self-signed', tone: 'warn', meaning: 'Generated locally. HSTS is refused on these.' },
  { name: 'orphaned', tone: 'muted', meaning: 'No site is attached. Usually residue from a deleted site.' },
  {
    name: 'unattached-mismatch',
    tone: 'danger',
    meaning: 'The certificate exists but the vhost points elsewhere — the failure mode that looks fine on disk and wrong in a browser.',
  },
];

const toneCls: Record<string, string> = {
  ok: 'bg-ok-soft text-ok',
  warn: 'bg-warn-soft text-warn',
  danger: 'bg-danger-soft text-danger',
  muted: 'bg-sunken text-muted',
};

export function ConceptTlsLifecycle() {
  return (
    <article>
      <PageHeader
        eyebrow="Concepts"
        title="The TLS resource lifecycle"
        lede="TLS is a first-class resource, not a flag on site add. A site can serve HTTP before DNS has propagated and get a real certificate later — which is the normal order of operations when a client is still moving their domain."
      />

      <div className="prose">
        <H2>Four verbs, in order</H2>
        <ol>
          <li>
            <strong>issue</strong> — preflight, then obtain from the CA. Recorded in state.
          </li>
          <li>
            <strong>attach</strong> — point a site’s vhost at it, reload, and verify it is really being
            served.
          </li>
          <li>
            <strong>renew</strong> — on a timer, under 30 days remaining, with the deploy hook
            reloading only the affected site.
          </li>
          <li>
            <strong>detach / delete / revoke</strong> — three different things, and confusing them is a
            classic mistake. See below.
          </li>
        </ol>
        <p>
          <code>cert issue</code> attaches by default. <code>--no-attach</code> separates the two,
          which is what you want when staging a cutover or when the certificate is for a name the vhost
          does not serve yet.
        </p>

        <H2 id="preflight">Preflight, before an ACME attempt is spent</H2>
        <p>
          Every one of these runs and reports <strong>every</strong> problem at once, because each
          attempt costs rate-limit budget and finding three problems one attempt at a time is how
          people exhaust a week’s worth in an afternoon.
        </p>
        <ol>
          <li>The site exists in state and its vhost is enabled.</li>
          <li>
            <strong>DNS.</strong> A/AAAA records for the domain and every SAN are resolved, following
            CNAMEs, and compared against this server’s public addresses. A mismatch is refused with the
            observed values, unless <code>--force</code>.
          </li>
          <li>
            <strong>Proxy detection.</strong> If the resolved address belongs to a known proxy range,
            HTTP-01 will fail unless the record is DNS-only. The refusal suggests{' '}
            <code>--challenge dns</code>, or an Origin certificate via <code>cert import</code>.
          </li>
          <li>
            <strong>Reachability.</strong> A random token is written to the shared webroot and fetched
            over HTTP. A 200 carrying the exact token is the only pass.
          </li>
          <li>
            <strong>Conflicts.</strong> No other vhost claims the same <code>server_name</code>.
          </li>
          <li>
            <strong>Tooling.</strong> certbot present, and the DNS plugin installed. If not, the exact
            install command is printed.
          </li>
          <li>
            <strong>Rate-limit budget</strong> — see <Link to="/concepts/rate-limits">rate limits</Link>.
          </li>
          <li>
            A wildcard request forces <code>--challenge dns</code>, and says why HTTP-01 cannot work.
          </li>
        </ol>
      </div>

      <Callout tone="ok" title="The single most useful flag on this page">
        <p>
          <code>ratline cert issue example.com --dry-run</code> runs all of the above and exercises the
          CA’s validation, at no rate-limit cost. Reach for it first, every time.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="transaction">Issue → attach is transactional</H2>
        <p>The chain, in order, with the interesting part at the end:</p>
      </div>

      <CodeBlock
        lang="text"
        code={`preflight
  → certbot                (argv only, never a shell string)
  → parse and translate the result
  → stage the vhost
  → nginx -t
  → reload
  → VERIFY FOR REAL        open a TLS connection with SNI; assert the served
                           chain matches the expected fingerprint, covers the
                           requested SANs, and validates against the system
                           root store
  → record in state

any failure  → restore the previous vhost and reload`}
        noCopy
      />

      <div className="prose">
        <p>
          A certificate that exists on disk but is not being served is a failure, not a success. That
          one decision eliminates the entire class of “certbot said it worked but the browser still
          shows the old certificate” problem, which is usually a vhost pointing at a different
          lineage or a second <code>server</code> block winning the <code>server_name</code> match.
        </p>
      </div>

      <Terminal title="root@server">{`$ ratline cert issue example.com --alias www.example.com
→ preflight: 8 checks
✗ preflight failed with 2 problems:
  · DNS: www.example.com resolves to 198.51.100.7, this server is 203.0.113.10
  · tooling: python3-certbot-dns-cloudflare is not installed
  hint: fix the A record for www, or drop the alias with --alias none.
        To install the plugin: apt-get install python3-certbot-dns-cloudflare
~ nothing was sent to the CA, so no rate-limit budget was spent`}</Terminal>

      <div className="prose">
        <H2 id="challenge-decision">HTTP-01 or DNS-01</H2>
        <p>
          The decision tree is short, and the default is right most of the time. Read it downwards and
          stop at the first line that applies.
        </p>
      </div>

      <div className="not-prose my-6 overflow-hidden rounded-[var(--radius-card)] border border-line">
        {[
          {
            q: 'Is the name a wildcard, *.example.com?',
            a: 'DNS-01. Forced — there is no single hostname to serve a token from, and Let’s Encrypt does not offer HTTP-01 for wildcards.',
            tone: 'danger',
          },
          {
            q: 'Is the A record pointed at a proxy — Cloudflare orange cloud, or similar?',
            a: 'DNS-01, or grey-cloud the record temporarily, or import the proxy’s Origin certificate. See the orange-cloud guide.',
            tone: 'warn',
          },
          {
            q: 'Is port 80 reachable from the internet?',
            a: 'If not — firewall, provider security group, an ISP blocking it — DNS-01 is the only option.',
            tone: 'warn',
          },
          {
            q: 'Do you need the certificate before DNS has moved?',
            a: 'DNS-01 against the zone you already control, or a self-signed certificate now and a real one later.',
            tone: 'note',
          },
          {
            q: 'None of the above.',
            a: 'HTTP-01. It is the default, it needs no credentials, and there is nothing to leak.',
            tone: 'ok',
          },
        ].map((row, i) => (
          <div
            key={i}
            className="flex flex-col gap-2 border-t border-line px-4 py-3 first:border-t-0 sm:flex-row sm:gap-5"
          >
            <p className="sm:w-[19rem] sm:shrink-0 text-sm font-medium text-strong">{row.q}</p>
            <p className="text-sm leading-relaxed text-muted">{row.a}</p>
          </div>
        ))}
      </div>

      <div className="prose">
        <H3>The trade-off, stated once</H3>
        <p>
          HTTP-01 needs nothing but port 80 and a webroot, and there is no credential to store. DNS-01
          needs an API token for your DNS provider, sitting on the server at{' '}
          <code>0600</code> under <code>/etc/ratline/dns</code> — a credential that can usually edit
          every record in the zone. That is a real cost, and it is why HTTP-01 is the default rather
          than DNS-01 being recommended for everything.
        </p>
        <p>
          The shared webroot is what makes HTTP-01 reliable here:{' '}
          <code>/var/www/ratline-acme</code>, one directory for every site, served{' '}
          <em>before any redirect</em> so renewal never depends on an application being up. A renewal
          that fails because the app is down is a renewal that fails exactly when you least want it to.
        </p>
      </div>

      <CodeBlock
        lang="nginx"
        filename="the shape of the ACME location, in every generated vhost"
        code={`# Served before the HTTPS redirect, so renewal works even when the app is down.
location ^~ /.well-known/acme-challenge/ {
    root /var/www/ratline-acme;
    default_type "text/plain";
    try_files $uri =404;
}

location / {
    return 301 https://$host$request_uri;
}`}
      />

      <div className="prose">
        <H2 id="statuses">Certificate statuses</H2>
        <p>
          <code>cert list</code> columns are{' '}
          <code>DOMAIN | SANS | ISSUER | KEY | EXPIRES | DAYS | STATUS | SITES | AUTO-RENEW</code>.
          Status is one of:
        </p>
      </div>

      <TableScroll>
        <table className="w-full min-w-[40rem] border-collapse text-left text-sm">
          <caption className="sr-only">Certificate statuses</caption>
          <tbody>
            {statuses.map((s) => (
              <tr key={s.name} className="border-t border-line first:border-t-0 align-top">
                <th scope="row" className="w-[13rem] px-3 py-2.5 text-left font-normal">
                  <span
                    className={`inline-block rounded px-1.5 py-0.5 font-mono text-xs ${toneCls[s.tone]}`}
                  >
                    {s.name}
                  </span>
                </th>
                <td className="px-3 py-2.5 leading-relaxed">{s.meaning}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableScroll>

      <div className="prose">
        <p>
          Certificates issued by hand outside ratline are detected and listed too. That is exactly the
          residue someone leaves behind, and a list that hid it would be worse than no list.
        </p>

        <H2 id="renewal">Renewal</H2>
        <ul>
          <li>
            A timer runs twice daily with a randomised delay —{' '}
            <code>ratline-cert-renew.timer</code>, <code>OnCalendar=*-*-* 03,15:00:00</code> with{' '}
            <code>RandomizedDelaySec=3h</code>. Without the randomisation every ratline server on the
            internet would hit the CA in the same two minutes. It is{' '}
            <code>Persistent=true</code>, so a machine that was off when a window passed runs on next
            boot rather than waiting for the following one.
          </li>
          <li>
            certbot’s own timer is neutralised at install time so the two never race. Two renewal
            timers on one box is how you get a duplicate-certificate rate-limit refusal from nowhere.
          </li>
          <li>
            Renewal is attempted under <code>acme.renew_before_days</code> (30) remaining, which leaves
            three weeks of margin for the retries.
          </li>
          <li>
            certbot invokes <code>ratline cert deploy-hook</code>, which maps the renewed lineage to
            its sites, runs <code>nginx -t</code>, and reloads <strong>only</strong> the affected site.
            Never a blanket restart.
          </li>
          <li>
            On failure: exponential backoff, the previous certificate is retained, the certificate is
            marked <code>degraded</code>, and <code>doctor</code> surfaces it. A failed renewal never
            leaves you with no certificate at all.
          </li>
        </ul>
      </div>

      <Callout tone="warn" title="detach, delete and revoke are three different things">
        <p>
          <strong>detach</strong> stops serving it; the files stay and it can be reattached.{' '}
          <strong>delete</strong> removes it from state and optionally from disk, and refuses while a
          site still uses it. <strong>revoke</strong> asks the CA to mark it invalid — that is for a
          compromised key, not for tidying up, and revoking a certificate that is still being served
          makes the site untrusted for anyone whose client checks revocation.
        </p>
      </Callout>

      <div className="prose">
        <p>
          Next: <Link to="/concepts/rate-limits">how the rate-limit budget is tracked</Link>,{' '}
          <Link to="/guides/issue-cert">issuing after DNS finally points at the box</Link>, or{' '}
          <Link to="/guides/renewal-runbook">the renewal runbook</Link>.
        </p>
      </div>
    </article>
  );
}
