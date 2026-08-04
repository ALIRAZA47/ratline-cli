import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, H2, H3 } from '../../components/ui';
import { Inline } from '../../components/Inline';

export function GuideIssueCert() {
  return (
    <article>
      <PageHeader
        eyebrow="Guide"
        title="Issue a certificate after DNS finally points at the box"
        lede="The normal order of operations: build the site first, get the certificate when the domain arrives. This is what the whole TLS-as-a-resource design exists for."
      />

      <div className="prose">
        <p>
          A client is moving a domain from their old host. You need the site built, tested and ready before
          the DNS change, and you cannot get a Let’s Encrypt certificate until the change has happened. So
          the two are separate steps, and <code>site add</code> knows it: a certificate failure never fails
          the site creation.
        </p>

        <H2 id="before">1 · Build the site before DNS moves</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site add example.com --user acme --runtime static \\
  --build-command "npm run build" --build-output dist \\
  --alias www.example.com \\
  --ssl selfsigned`}
      />

      <div className="prose">
        <p>
          <code>--ssl selfsigned</code> is explicit here, but it is also what the default would do. The
          default is <em>Let’s Encrypt if the domain already resolves to this server, otherwise self-signed
          with a printed note</em> — so leaving the flag off gets the same result, and tells you why.
        </p>
        <p>
          Now you can test over HTTPS with a certificate warning, or over HTTP by pointing your own machine
          at the box:
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# Test the vhost without touching DNS
curl -sS --resolve example.com:443:203.0.113.10 -k https://example.com/ | head -20

# Or add a line to /etc/hosts on your laptop while you work
# 203.0.113.10  example.com www.example.com`}
      />

      <Callout tone="note" title="Self-signed and staging certificates are marked, not hidden">
        <p>
          Both show up with their own status in <code>cert list</code>, and ratline refuses to enable HSTS on
          either. A self-signed certificate plus HSTS is a site nobody can reach and nobody can un-break for
          a year.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="dns">2 · Make the DNS change, then check what the world sees</H2>
        <p>
          Preflight will do this for you, but doing it yourself first is faster than a refused attempt —
          and it costs nothing.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# What the authoritative servers say, bypassing every cache
dig +short example.com A @1.1.1.1
dig +short www.example.com A @1.1.1.1

# And what this server thinks its own addresses are
ratline version`}
      />

      <div className="prose">
        <p>
          Watch out for <code>www</code>. An apex record that has moved and a <code>www</code> CNAME still
          pointing at the old host is the single most common reason an otherwise fine issuance is refused,
          because every SAN is checked, not just the primary name.
        </p>

        <H3>The TTL you set last week is the wait you have now</H3>
        <p>
          If the old records had a 24-hour TTL, resolvers will keep answering with the old address for up to
          24 hours regardless of what the authoritative servers now say. Lower the TTL <em>before</em> a
          migration, not during one.
        </p>

        <H2 id="dry-run">3 · Dry run, always</H2>
        <p>
          Full validation, no rate-limit cost, same code path. There is no reason to skip it.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline cert issue example.com --email ops@example.com --dry-run`}
      />

      <Terminal title="root@server">{`$ ratline cert issue example.com --email ops@example.com --dry-run
→ dry run: no rate-limit budget will be spent
→ preflight 1/8: site example.com exists, vhost enabled
→ preflight 2/8: DNS
    example.com      A 203.0.113.10  → matches this server
    www.example.com  A 203.0.113.10  → matches this server
→ preflight 3/8: no proxy range detected for either address
→ preflight 4/8: reachability — wrote token to /var/www/ratline-acme, fetched 200 with exact match
→ preflight 5/8: no other vhost claims example.com or www.example.com
→ preflight 6/8: certbot 2.9.0 present
→ preflight 7/8: budget 50 of 50 certificates remaining this week for example.com
→ preflight 8/8: not a wildcard; http-01 is appropriate
→ certbot --dry-run: validation succeeded for example.com, www.example.com
→ nothing was issued. Re-run without --dry-run.`}</Terminal>

      <div className="prose">
        <p>
          Preflight reports <strong>every</strong> problem at once rather than one per attempt — because each
          failed validation costs one of five per hostname per hour, and discovering three problems one
          attempt at a time is how people exhaust that in ten minutes.
        </p>

        <H2 id="issue">4 · Issue it</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline cert issue example.com --email ops@example.com`}
      />

      <Terminal title="root@server">{`$ ratline cert issue example.com --email ops@example.com
→ preflight: 8 checks passed
→ certbot: order created, http-01 challenge for 2 names
→ certbot: certificate issued — ecdsa P-256, expires 2026-11-02
→ staged /etc/nginx/sites-available/example.com.conf
→ nginx -t passed
→ reloaded nginx
→ verifying over TLS with SNI…
    served chain fingerprint matches the issued certificate
    covers example.com, www.example.com
    validates against the system root store
→ recorded in state; auto-renew enabled
→ done. The self-signed certificate has been replaced.`}</Terminal>

      <div className="prose">
        <p>
          The verification step is what separates this from running certbot by hand. A certificate that
          exists on disk but is not being served is a failure, not a success — and that one decision
          eliminates the entire “certbot said it worked but the browser shows the old certificate” class of
          problem, which is usually a vhost pointing at a different lineage or a second{' '}
          <code>server</code> block winning the <code>server_name</code> match.
        </p>

        <H2 id="confirm">5 · Confirm, independently</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline cert show example.com
ratline cert list --expiring 90

# And from outside the box, which is the only check that really counts
openssl s_client -connect example.com:443 -servername example.com </dev/null 2>/dev/null \\
  | openssl x509 -noout -subject -dates -ext subjectAltName`}
      />

      <div className="prose">
        <H2 id="refusals">When preflight refuses</H2>
      </div>

      <div className="not-prose my-6 space-y-3">
        {[
          {
            t: 'DNS mismatch',
            b: 'The refusal prints the observed values against this server’s addresses. Usually a www CNAME left behind, or a TTL that has not expired. Wait, or fix the record. --force overrides it, but only do that when you know why they differ — for example, on a NAT’d host where server.public_ipv4 should have been set in configuration instead.',
          },
          {
            t: 'Proxy range detected',
            b: 'HTTP-01 cannot work through a proxy that terminates TLS itself. Three ways out, all in the orange-cloud guide: grey-cloud the record temporarily, switch to DNS-01, or import the proxy’s Origin certificate.',
          },
          {
            t: 'Reachability failed',
            b: 'A token was written to /var/www/ratline-acme and fetched over HTTP, and something other than a 200 with the exact token came back. Check that port 80 is open at the provider’s firewall, and that nothing in front is redirecting /.well-known.',
          },
          {
            t: 'server_name conflict',
            b: 'Another vhost claims the same name. nginx resolves this by picking one, which means half your requests go somewhere unexpected. Find it with `ratline doctor`, or `grep -r server_name /etc/nginx/sites-enabled/`.',
          },
          {
            t: 'Tooling missing',
            b: 'certbot or the DNS plugin is not installed, and the exact install command is printed. Nothing is guessed at.',
          },
          {
            t: 'Rate limit',
            b: 'Exit 9 with a countdown, and nothing was sent to the CA. Use --staging while you debug — it has its own, far looser limits.',
          },
        ].map((row) => (
          <div key={row.t} className="rounded-[var(--radius-card)] border border-line bg-raised px-4 py-3">
            <p className="font-medium text-strong">{row.t}</p>
            <p className="mt-1 max-w-[var(--container-measure)] text-sm leading-relaxed text-muted">
              <Inline text={row.b} />
            </p>
          </div>
        ))}
      </div>

      <div className="prose">
        <H2 id="cutover">A zero-surprise cutover</H2>
        <p>
          When downtime is unacceptable, separate issuance from attachment. Get the certificate while the
          old host is still serving, then attach at a moment you choose.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# While DNS still points at the old host: DNS-01 needs nothing to be reachable here
ratline cert issue example.com \\
  --challenge dns \\
  --dns-provider cloudflare \\
  --dns-credentials /etc/ratline/dns/cloudflare.ini \\
  --no-attach

# ...make the DNS change...

# Attach and verify, at a time of your choosing
ratline cert attach example.com
ratline cert show example.com`}
      />

      <Callout tone="warn" title="DNS-01 costs you a credential on the box">
        <p>
          The API token lives at <code>0600</code> under <code>/etc/ratline/dns</code> and can usually edit
          every record in the zone. That is a real cost, and it is why HTTP-01 is the default rather than
          DNS-01 being recommended for everything. Use a scoped token if your provider offers one.
        </p>
      </Callout>

      <div className="prose">
        <p>
          Next: <Link to="/concepts/tls-lifecycle">the full TLS lifecycle</Link>,{' '}
          <Link to="/guides/cloudflare">the orange-cloud trap</Link>, or{' '}
          <Link to="/guides/renewal-runbook">what to do in ninety days if renewal fails</Link>.
        </p>
      </div>
    </article>
  );
}
