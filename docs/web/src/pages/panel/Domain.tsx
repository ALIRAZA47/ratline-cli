import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, Facts, H2 } from '../../components/ui';

export function PanelDomain() {
  return (
    <article className="prose">
      <PageHeader
        eyebrow="The web panel"
        title="Putting it on a domain"
        lede="An nginx vhost that proxies to the panel, and a certificate for the name — staged, verified and rolled back exactly as ratline’s own are."
      />

      <Callout tone="warn" title="Point DNS here first">
        An ACME attempt against a name that does not resolve to this server spends one of
        five validations per hour and cannot succeed. Check the record before you run this,
        or use <code>--staging</code>, which proves the plumbing and costs no budget.
      </Callout>

      <Terminal>{`$ ratline-panel domain set panel.example.com --email you@example.com
→ writing the nginx vhost
→ nginx -t: configuration file /etc/nginx/nginx.conf test is successful
→ certbot: obtaining a certificate for panel.example.com
→ rewriting the vhost with TLS
→ reloading nginx
the panel is on its domain  url=https://panel.example.com
Restarting the panel so it knows its own name…`}</Terminal>

      <H2 id="order">Why it writes the vhost twice</H2>
      <p>
        HTTP first, always, then the certificate, then TLS. The order is not incidental:
        the ACME challenge is answered over port 80 out of a vhost that must exist before
        certbot runs, and a TLS vhost naming a certificate file that is not there yet fails{' '}
        <code>nginx -t</code> — so the reload never happens and the challenge is never
        served.
      </p>

      <H2 id="not-a-site">Why the panel is not a ratline site</H2>
      <p>
        It would be convenient to register it as one and reuse the vhost renderer. It is
        not one: it has no tenant, no home directory and no systemd unit running as a site
        owner, so making the model say otherwise would be a lie the model would then have
        to keep — in <code>site list</code>, in <code>reconcile</code>, in{' '}
        <code>user delete</code>&rsquo;s cascade.
      </p>
      <p>
        So the panel writes its own vhost, with the same discipline: render to a temporary
        file, check it with the real tool, rename atomically, push an undo step, reload. A{' '}
        <code>domain set</code> that fails halfway leaves nginx serving exactly what it was
        serving before. The file carries a{' '}
        <code># managed-by: ratline-panel</code> header and the panel refuses to overwrite
        one that does not.
      </p>

      <H2 id="renewal">Renewal</H2>
      <p>
        certbot&rsquo;s own timer renews it, and the deploy hook stored with the lineage
        calls <code>ratline-panel nginx reload</code>. That last part is the one people
        forget: a renewal that does not reload nginx changes a file on disk and leaves the
        old certificate being served until it expires.
      </p>

      <H2 id="what-it-writes">What it writes</H2>
      <Facts
        rows={[
          ['/etc/nginx/sites-available/ratline-panel.conf', 'The vhost. Regenerated whenever the domain or the certificate changes; do not edit it.'],
          ['/etc/nginx/sites-enabled/ratline-panel.conf', 'A symlink to the above.'],
          ['/var/www/ratline-acme', <>The ACME webroot — ratline&rsquo;s own, shared on purpose so there is one place to look when a challenge fails.</>],
          ['/etc/ratline/panel.yaml', <>Gains <code>listen.domain</code>, which is how the running panel knows its own name.</>],
        ]}
      />

      <H2 id="terminating-elsewhere">TLS terminated somewhere else</H2>
      <p>
        Behind Cloudflare, a load balancer or another proxy, use{' '}
        <code>--no-tls</code> to write the HTTP vhost and stop. Two settings matter then:
      </p>
      <Facts
        rows={[
          [
            'listen.trust_proxy',
            <>Leave it true only while something on this host sets <code>X-Forwarded-For</code>. A panel reachable directly must set it false, or every client can claim any address and the per-address rate limit counts them separately.</>,
          ],
          [
            'session.secure_cookie',
            <><code>auto</code> marks the cookie Secure when the request arrived over HTTPS, which needs <code>X-Forwarded-Proto</code> to be trusted. Set it to <code>always</code> if you are certain, never leave it <code>never</code> on a public name.</>,
          ],
        ]}
      />

      <H2 id="off">Taking it off a domain</H2>
      <CodeBlock lang="bash" prompt code={`ratline-panel domain clear`} />
      <p>
        The vhost goes; the certificate stays. Deleting a lineage because the panel moved
        would spend a rate limit to get it back, and <code>certbot delete</code> is the
        tool for actually wanting it gone.
      </p>

      <p>
        Next: <Link to="/panel/security">what signing in grants</Link>.
      </p>
    </article>
  );
}
