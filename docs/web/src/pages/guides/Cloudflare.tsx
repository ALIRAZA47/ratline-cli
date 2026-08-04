import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, H2, H3 } from '../../components/ui';

export function GuideCloudflare() {
  return (
    <article>
      <PageHeader
        eyebrow="Guide"
        title="The Cloudflare orange-cloud trap"
        lede="A proxied DNS record makes HTTP-01 validation impossible and makes the site look fine while doing it. Here is why, how ratline detects it, and the three ways out."
      />

      <div className="prose">
        <H2 id="why">Why it fails</H2>
        <p>
          With the orange cloud on, the A record for your domain does not resolve to your server. It resolves
          to Cloudflare. Requests land there, Cloudflare terminates TLS with <em>its</em> certificate, and
          then makes its own connection to your origin.
        </p>
        <p>So when the CA fetches the HTTP-01 token, one of two things happens:</p>
        <ul>
          <li>
            Cloudflare answers directly from cache or its own error page, and the token is not there. The
            challenge fails.
          </li>
          <li>
            Cloudflare forwards to your origin over HTTPS and your origin has no valid certificate yet, so the
            connection fails or returns a Cloudflare 5xx page. The challenge fails.
          </li>
        </ul>
        <p>
          The trap is that the site <em>appears</em> to work throughout. Visitors get a padlock, because
          Cloudflare has a certificate for the edge. Nothing tells you the origin has no certificate at all,
          until you turn the proxy off or Cloudflare tightens its origin-verification setting.
        </p>
      </div>

      <CodeBlock
        lang="text"
        code={`With the orange cloud ON:

  browser ──TLS(Cloudflare cert)──> Cloudflare ──?──> your origin
                                        │
  Let's Encrypt ─── GET /.well-known/acme-challenge/<token> ──┘
                    lands here, not on your box → 404 → challenge fails


With the orange cloud OFF (grey):

  browser ──TLS(your cert)──> your origin
  Let's Encrypt ─── GET /.well-known/... ──> your origin → 200 → passes`}
        noCopy
      />

      <div className="prose">
        <H2 id="detection">ratline detects it before spending an attempt</H2>
        <p>
          Proxy detection is preflight check 3: if the resolved address belongs to a known proxy range,
          HTTP-01 will fail unless the record is DNS-only. The refusal names the situation and offers the
          options rather than letting you find out from the CA.
        </p>
      </div>

      <Terminal title="root@server">{`$ ratline cert issue example.com --email ops@example.com
→ preflight 1/8: site example.com exists, vhost enabled
→ preflight 2/8: DNS
    example.com  A 104.21.0.0 — does not match this server (203.0.113.10)
✗ preflight 3/8: the address 104.21.0.0 belongs to a known proxy range.
  HTTP-01 cannot succeed while the record is proxied: the challenge request never
  reaches this server.
  Choose one:
    1. Set the record to DNS-only ("grey cloud") for a few minutes, then re-run.
    2. Use DNS-01:  --challenge dns --dns-provider cloudflare
                    --dns-credentials /etc/ratline/dns/cloudflare.ini
    3. Import the proxy's Origin certificate:  ratline cert import
~ exit 3 — nothing was sent to the CA, so no rate-limit budget was spent`}</Terminal>

      <div className="prose">
        <H2 id="option-1">Option 1 · Grey-cloud it briefly</H2>
        <p>
          The simplest thing that works, and the right answer for a one-off issuance. Turn the proxy off,
          issue, turn it back on. Renewal will hit the same wall in ninety days, which is the reason this is
          not the last section on the page.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# In the Cloudflare dashboard, set the A record for example.com to DNS only.
# Then, once the change has propagated (usually seconds — the proxy toggle is fast):

dig +short example.com A @1.1.1.1     # should now be your origin address
ratline cert issue example.com --email ops@example.com --dry-run
ratline cert issue example.com --email ops@example.com

# Turn the orange cloud back on.
ratline cert show example.com`}
      />

      <Callout tone="warn" title="Then set a reminder you will not get">
        <p>
          Renewal runs on a timer twice daily, attempted under 30 days remaining. With the proxy back on, it
          will fail every time — quietly, marking the certificate <code>degraded</code> and surfacing it in{' '}
          <code>doctor</code>, while the site continues to look perfect from a browser. Run{' '}
          <code>ratline cert test-renewal</code> now, and if it fails, pick option 2 or 3 instead.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="option-2">Option 2 · DNS-01 (the durable answer)</H2>
        <p>
          DNS-01 proves control of the domain by writing a TXT record, so it does not care what the A record
          points at or whether port 80 is reachable. It keeps working with the proxy on, forever, and it also
          works for wildcards — which HTTP-01 cannot do at all.
        </p>
        <H3>Install the plugin and the credential</H3>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`apt-get install python3-certbot-dns-cloudflare

install -d -m 0700 -o root -g root /etc/ratline/dns
install -m 0600 /dev/stdin /etc/ratline/dns/cloudflare.ini <<'EOF'
dns_cloudflare_api_token = <a token with Zone:DNS:Edit on this zone only>
EOF`}
      />

      <div className="prose">
        <p>
          The credentials file must be <code>0600</code>, and it is validated before use. Scope the token to
          the one zone: a token that can edit every record in every zone, sitting on a web server, is a much
          bigger loss than the certificate it was for.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline cert issue example.com \\
  --challenge dns \\
  --dns-provider cloudflare \\
  --dns-credentials /etc/ratline/dns/cloudflare.ini \\
  --dns-propagation 60 \\
  --alias www.example.com \\
  --email ops@example.com \\
  --dry-run`}
      />

      <div className="prose">
        <p>
          <code>--dns-propagation</code> is how long to wait before asking the CA to validate. The default,{' '}
          <code>acme.dns_propagation_seconds</code>, is 60. Cloudflare is usually much faster than that; some
          providers are considerably slower, and a validation that runs before the record is visible burns
          one of your five failed validations per hour.
        </p>

        <H3>A wildcard, while you are here</H3>
        <p>
          If you are setting DNS-01 up anyway, one wildcard certificate covers every subdomain and costs one
          draw against the rate-limit budget rather than one per name.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline cert issue "*.example.com" \\
  --san example.com \\
  --challenge dns \\
  --dns-provider cloudflare \\
  --dns-credentials /etc/ratline/dns/cloudflare.ini`}
      />

      <div className="prose">
        <p>
          A wildcard request forces <code>--challenge dns</code> and says why HTTP-01 cannot work. Only a
          leading <code>*.</code> is legal — neither DNS nor the CAs support{' '}
          <code>*.*.example.com</code> or <code>a*.example.com</code>, and accepting them would fail much
          later with a worse message.
        </p>

        <H2 id="option-3">Option 3 · Import the proxy’s Origin certificate</H2>
        <p>
          Cloudflare can issue a long-lived certificate for the connection between its edge and your origin.
          It is not trusted by browsers — it does not need to be, because browsers never see it — and it can
          be valid for years, so there is no renewal to fail.
        </p>
        <p>
          This is the right answer when the proxy is permanent and you are certain <em>all</em> traffic goes
          through it.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# Generate an Origin certificate in the Cloudflare dashboard, then:
ratline cert import example.com \\
  --cert ./origin-fullchain.pem \\
  --key ./origin-privkey.pem

ratline cert attach example.com
ratline cert show example.com`}
      />

      <div className="prose">
        <p>
          Imported certificates land under <code>paths.imported_certs</code> (
          <code>/etc/ratline/certs</code>) and are listed by <code>cert list</code> with their real issuer, so
          nobody later mistakes one for a Let’s Encrypt lineage that stopped renewing.
        </p>
        <p>
          Set Cloudflare’s SSL mode to <strong>Full (strict)</strong> once this is in place. “Flexible” means
          Cloudflare talks to your origin over plain HTTP, which makes the whole exercise pointless, and
          “Full” without strict accepts any certificate including an expired one.
        </p>
      </div>

      <Callout tone="danger" title="If the proxy is ever bypassed, an Origin certificate fails">
        <p>
          It is not in any browser’s trust store. Anyone who reaches your origin directly — by IP, by an
          unproxied subdomain, or because someone grey-clouded a record — gets a certificate error. If there
          is any chance of direct traffic, use option 2 and keep a publicly trusted certificate.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="picking">Which one</H2>
        <ul>
          <li>
            <strong>One-off, proxy going away soon</strong> → option 1. Grey-cloud, issue, re-enable.
          </li>
          <li>
            <strong>Proxy staying, want a publicly trusted certificate</strong> → option 2. DNS-01. Renewal
            works untouched.
          </li>
          <li>
            <strong>Proxy staying, all traffic definitely through it, want no renewal at all</strong> → option
            3. Import the Origin certificate.
          </li>
          <li>
            <strong>Wildcard needed</strong> → option 2, always. It is the only one that can do it.
          </li>
        </ul>

        <H2 id="other-proxies">The same trap, other names</H2>
        <p>
          Nothing here is Cloudflare-specific. Any proxy that terminates TLS in front of your origin breaks
          HTTP-01 the same way: Fastly, Akamai, an AWS CloudFront distribution, a corporate WAF, a load
          balancer somebody put in front of the box without telling you. Proxy detection catches the ranges it
          knows; when it does not recognise one, the reachability check still fails and tells you the token
          came back wrong.
        </p>
        <p>
          Also worth checking: real client IPs. Behind a proxy every request appears to come from the proxy
          unless the vhost is configured to read the forwarded header, which means source-restricted SSH keys
          are unaffected (SSH does not go through the proxy) but access-log-based analysis is. That belongs in{' '}
          <code>/etc/nginx/ratline/custom/&lt;domain&gt;.conf</code>, which survives{' '}
          <code>reconcile</code>.
        </p>
        <p>
          Next: <Link to="/guides/renewal-runbook">my cert didn’t renew</Link> — the orange cloud is cause
          number two on that list.
        </p>
      </div>
    </article>
  );
}
