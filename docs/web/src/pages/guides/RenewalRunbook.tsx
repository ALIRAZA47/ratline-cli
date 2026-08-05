import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, Facts, H2, H3 } from '../../components/ui';

export function GuideRenewalRunbook() {
  return (
    <article>
      <PageHeader
        eyebrow="Runbook"
        title="My cert didn’t renew"
        lede="Ordered by how often each cause is the answer, not by how interesting it is. Work down the list and stop when you find it."
      />

      <Callout tone="note" title="First: you probably have more time than you think">
        <p>
          Renewal is attempted under <code>acme.renew_before_days</code> (30) remaining, twice daily with a
          randomised delay. If you are reading this because of a 7-day warning, three weeks of retries have
          already failed — so treat it as an incident. But the certificate is still valid, the site is still
          up, and you are not fixing this under a browser-error deadline.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="triage">0 · Triage, in three commands</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline cert list --expiring 30
ratline cert show example.com
ratline doctor`}
      />

      <Terminal title="root@server">{`$ ratline cert list --expiring 30
DOMAIN           SANS  ISSUER          KEY    EXPIRES     DAYS  STATUS    SITES  AUTO-RENEW
example.com      2     Let's Encrypt   ecdsa  2026-08-11     7  degraded  1      on
old.example.com  1     Let's Encrypt   ecdsa  2026-08-19    15  expiring  0      on

$ ratline cert show example.com
domain            example.com
sans              example.com, www.example.com
issuer            Let's Encrypt R11
status            degraded
last renewal      2026-08-04 03:14 — FAILED (attempt 22)
last error        acme: challenge failed for www.example.com:
                  404 fetching http://www.example.com/.well-known/acme-challenge/<token>
next attempt      2026-08-04 19:00 (backoff)
auto-renew        enabled`}</Terminal>

      <div className="prose">
        <p>
          <code>degraded</code> means the last renewal failed and the previous certificate was retained — a
          failed renewal never leaves you with no certificate at all. And <code>last error</code> is usually
          the answer: read it before you start looking at anything else. In the transcript above it is{' '}
          <code>www</code>, not the apex, which narrows it immediately.
        </p>
        <p>
          The other row is worth noticing too: <code>old.example.com</code> has <code>0</code> sites. An{' '}
          <code>orphaned</code> certificate still tries to renew and still consumes budget from the same
          registered domain. Clean it up.
        </p>

        <H2 id="reproduce">1 · Reproduce it for free</H2>
        <p>
          Do not renew for real yet. <code>--dry-run</code> exercises the same path at no rate-limit cost, and{' '}
          <code>test-renewal</code> exercises the timer, the webroot, the deploy hook and the reload.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline cert renew example.com --dry-run
ratline cert test-renewal`}
      />

      <div className="prose">
        <H2 id="causes">The causes, in order of how often they are it</H2>
      </div>

      <div className="not-prose my-6 space-y-4">
        {[
          {
            n: 1,
            t: 'A redirect is swallowing /.well-known/acme-challenge/',
            b: 'Somebody added an HTTPS redirect, or a catch-all rewrite, or a maintenance page — and it matches the challenge path before the ACME location does. The generated vhost serves the shared webroot before any redirect precisely to avoid this, so if it is happening, something was added by hand.',
            check: `curl -sSi http://example.com/.well-known/acme-challenge/probe
# Want: 404 from nginx (the token does not exist yet — correct)
# Bad:  301/302 to https://, or a 200 with your homepage in it

grep -rn "well-known\\|return 301\\|rewrite" /etc/nginx/sites-enabled/example.com.conf \\
  /etc/nginx/ratline/custom/example.com.conf`,
          },
          {
            n: 2,
            t: 'A proxy went in front of the box',
            b: 'Cloudflare’s orange cloud, or any proxy that terminates TLS. HTTP-01 stops being possible and the site keeps looking perfect, because the edge has its own certificate. Preflight detects known proxy ranges.',
            check: `dig +short example.com A @1.1.1.1
ratline version | grep -i ipv4
# If the A record is not this server, see the orange-cloud guide.`,
          },
          {
            n: 3,
            t: 'DNS moved, or a SAN was left behind',
            b: 'Every name on the certificate is validated, not just the primary. A www CNAME still pointing at an old host fails the whole renewal, which is exactly what the transcript above shows.',
            check: `for n in example.com www.example.com; do
  printf '%s -> %s\\n' "$n" "$(dig +short "$n" A @1.1.1.1 | tr '\\n' ' ')"
done`,
          },
          {
            n: 4,
            t: 'Port 80 is closed',
            b: 'A provider firewall rule, a security group, a UFW change, or an ISP blocking it. HTTPS keeps working, so nothing looks wrong until renewal — which needs port 80 specifically.',
            check: `ss -lntp | grep ':80'
# and from OUTSIDE the box, which is the only test that counts:
#   curl -sS -m 10 -o /dev/null -w '%{http_code}\\n' http://example.com/`,
          },
          {
            n: 5,
            t: 'Two renewal timers are racing',
            b: 'ratline init neutralises certbot’s own timer for exactly this reason. If certbot was installed or upgraded separately afterwards, the package may have put it back — and two timers renewing the same lineage produces duplicate-certificate refusals from nowhere.',
            check: `systemctl list-timers --all | grep -i -e certbot -e ratline
systemctl is-enabled certbot.timer 2>/dev/null   # want: disabled or masked`,
          },
          {
            n: 6,
            t: 'Rate limit already exhausted',
            b: 'Often a consequence of one of the above: twenty-two failed attempts is five failed validations per hour, several times over. Exit 9, with a countdown, and nothing sent to the CA.',
            check: `ratline cert renew example.com --dry-run
# The refusal names the window and the reset time.`,
          },
          {
            n: 7,
            t: 'The webroot is gone, or unreadable',
            b: 'One shared HTTP-01 webroot for every site, served before any redirect so renewal never depends on an application being up. If it was deleted, or its permissions changed, every renewal on the box fails at once — which is the tell.',
            check: `ls -ld /var/www/ratline-acme
namei -l /var/www/ratline-acme
# nginx (www-data) must be able to traverse and read it.`,
          },
          {
            n: 8,
            t: 'The deploy hook is not reloading the right site',
            b: 'Renewal succeeded, the files on disk are new, and the browser still shows the old certificate. certbot invokes ratline cert deploy-hook, which maps the renewed lineage to its sites, runs nginx -t and reloads only the affected site. If the mapping drifted, the new certificate exists and is not served — which cert list reports as unattached-mismatch.',
            check: `ratline cert list | grep -i mismatch
ratline cert show example.com
openssl s_client -connect example.com:443 -servername example.com </dev/null 2>/dev/null \\
  | openssl x509 -noout -dates`,
          },
          {
            n: 9,
            t: 'A private CA, issued with a flag instead of a setting',
            b: 'Only for step-ca, an internal issuer, or anything else that is not Let’s Encrypt. certbot verifies the ACME directory against certifi’s bundled roots rather than the system trust store, so a private root installed with update-ca-certificates is not consulted. cert issue --acme-ca-bundle covers one issuance; renewal runs from a timer with no command line and reads acme.ca_bundle. Set only the flag and the certificate issues perfectly and never renews — the failure is a TLS error inside certbot, which reads as a network problem. doctor checks this directly.',
            check: `# Which CA this lineage really renews from — certbot's own record, not config:
grep '^server' /etc/letsencrypt/renewal/example.com.conf

# If that is not Let's Encrypt, this must be set:
grep ca_bundle /etc/ratline/config.yaml

# doctor checks both together, before the first renewal has had a chance to fail:
ratline doctor                 # reports it as a problem against the certificate
ratline doctor server          # the same check by name: acme-trust`,
          },
        ].map((row) => (
          <section key={row.n} className="rounded-[var(--radius-card)] border border-line bg-raised">
            <div className="flex gap-3 border-b border-line px-4 py-3">
              <span className="mt-0.5 inline-flex size-6 shrink-0 items-center justify-center rounded-full bg-accent-soft font-mono text-xs font-semibold text-accent">
                {row.n}
              </span>
              <div>
                <h3 className="font-medium text-strong">{row.t}</h3>
                <p className="mt-1 max-w-[var(--container-measure)] text-sm leading-relaxed text-muted">
                  {row.b}
                </p>
              </div>
            </div>
            <div className="px-4 py-2">
              <CodeBlock code={row.check} lang="shell" />
            </div>
          </section>
        ))}
      </div>

      <div className="prose">
        <H2 id="fix">Once you have found it</H2>
        <p>
          Fix the cause, confirm with a dry run, then renew for real. Do not renew first and hope.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline cert renew example.com --dry-run    # free; proves the fix
ratline cert renew example.com
ratline cert show example.com               # status should be back to valid
ratline cert test-renewal                   # and the whole path still works`}
      />

      <Callout tone="danger" title="Do not loop on --force">
        <p>
          Each forced renewal counts against <code>duplicate_certs_per_week</code>, which is only 5. Three
          hopeful <code>--force</code> attempts while you are still guessing at the cause is most of your
          week’s budget, and then you cannot renew even after you fix it. Use{' '}
          <code>--dry-run</code> to iterate and <code>--staging</code> if you need real certificates to test
          against.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="emergency">If it is already expired</H2>
        <p>
          Restore service first, diagnose second. A self-signed certificate produces a browser warning; no
          certificate produces a connection failure, and a warning users can click through beats a site that
          does not load.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline cert selfsign example.com --days 30
ratline cert attach example.com
# The site is reachable again, with a warning. Now work the list above.`}
      />

      <div className="prose">
        <p>
          If the rate limit is what is blocking you, <code>--staging</code> gets you a real (untrusted)
          certificate from a far looser quota so you can verify the whole path works before spending
          production budget:
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline cert issue example.com --staging --force
# Prove the chain is served, then re-issue against production when the window resets.`}
      />

      <div className="prose">
        <H2 id="prevent">Preventing the next one</H2>
      </div>

      <Facts
        rows={[
          [
            'test-renewal',
            <>
              Run <code>ratline cert test-renewal</code> after any nginx change, any DNS change, and any
              proxy change. It is free and it catches all three.
            </>,
          ],
          [
            'alerts',
            <>
              Set <code>acme.alerts.email</code> or <code>acme.alerts.webhook_url</code>. Raise{' '}
              <code>acme.alerts.warn_days</code> from 7 to 14 — at 7 days you have already lost three weeks
              of retries.
            </>,
          ],
          [
            'orphans',
            <>
              <code>ratline cert list --orphaned</code> monthly. They still renew, and they still draw on the
              same registered-domain budget.
            </>,
          ],
          [
            'custom config',
            <>
              Put hand-written nginx in{' '}
              <code>/etc/nginx/ratline/custom/&lt;domain&gt;.conf</code>. Edits to the generated vhost are
              lost on <code>reconcile</code>, and a redirect added there is cause number one on this page.
            </>,
          ],
          [
            'doctor',
            <>
              <code>ratline doctor</code> surfaces degraded certificates, so it belongs in whatever you
              already look at weekly.
            </>,
          ],
        ]}
      />

      <div className="prose">
        <H3>The one-line version</H3>
        <p>
          Nine times out of ten it is something in front of the box — a redirect, a proxy, or a firewall —
          rather than anything about the certificate. Check that <code>curl</code> against{' '}
          <code>/.well-known/acme-challenge/probe</code> from <em>outside</em> the server before you check
          anything else.
        </p>
        <p>
          See also: <Link to="/guides/cloudflare">the orange-cloud trap</Link>,{' '}
          <Link to="/concepts/rate-limits">how the budget is tracked</Link>,{' '}
          <Link to="/concepts/tls-lifecycle#renewal">the renewal design</Link>.
        </p>
      </div>
    </article>
  );
}
