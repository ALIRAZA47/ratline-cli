import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, Facts, H2, H3 } from '../../components/ui';

export function GuideAstro() {
  return (
    <article>
      <PageHeader
        eyebrow="Guide"
        title="Publish an Astro static build"
        lede="The simplest thing ratline does, and the most reliable. No unit, no socket, nothing running — which means nothing to restart and nothing to return 502."
      />

      <Facts
        rows={[
          ['runtime', <code key="a">static</code>],
          ['processes', 'none'],
          ['serves', <code key="b">/home/&lt;user&gt;/&lt;domain&gt;/dist</code>],
          ['restart semantics', <>none needed — <code>site start</code> and friends are no-ops</>],
        ]}
      />

      <div className="prose">
        <p>
          Everything on this page applies equally to a Vite build, an Eleventy build, a Hugo build or a
          directory of hand-written HTML. The runtime is about what happens at request time, not about
          which generator produced the files.
        </p>

        <H2 id="create">Create it</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub

ratline site add example.com \\
  --user acme \\
  --runtime static \\
  --repo https://github.com/acme/site.git \\
  --branch main \\
  --install-command "npm ci" \\
  --build-command "npm run build" \\
  --build-output dist \\
  --alias www.example.com \\
  --www-redirect apex \\
  --dry-run`}
      />

      <div className="prose">
        <p>
          <code>--build-output dist</code> is what nginx serves. Astro writes to <code>dist/</code> by
          default; Vite too. <code>--root</code> is the alternative when there is no build at all and the
          files are simply committed — the default is <code>public</code>.
        </p>
      </div>

      <Callout tone="note" title="--www-redirect apex, and what it costs">
        <p>
          <code>apex</code> makes <code>example.com</code> canonical and redirects{' '}
          <code>www.example.com</code> to it. <code>www</code> does the reverse. Whichever you choose, the
          alias still has to be a SAN on the certificate — nginx has to terminate TLS for{' '}
          <code>www</code> before it can issue the redirect, so a name that is only ever redirected still
          needs to be covered.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="spa">SPA routing, and when you actually need it</H2>
        <p>
          <code>--spa</code> renders <code>try_files $uri $uri/ /index.html</code>, so an unknown path
          falls through to the app shell and the client-side router takes over.
        </p>
        <p>
          A default Astro build does <strong>not</strong> want this. Astro pre-renders a real{' '}
          <code>index.html</code> for every route, so the files already exist and a genuine 404 should
          return 404. Turning on <code>--spa</code> would make every typo return 200 with your homepage —
          which is bad for users and worse for search engines.
        </p>
        <p>Use it when the built output is a single HTML file plus a bundle:</p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# A React or Vue SPA with client-side routing
ratline site add app.example.com --user acme --runtime static \\
  --build-command "npm run build" --build-output dist --spa

# A pre-rendered Astro site: leave --spa off so 404s are real 404s
ratline site add example.com --user acme --runtime static \\
  --build-command "npm run build" --build-output dist`}
      />

      <div className="prose">
        <H2 id="deploy">Deploying a change</H2>
        <p>
          There is no restart, because there is nothing running. The deploy is: pull, install, build. The
          moment the build finishes, nginx is serving the new files — no reload required, because nginx
          resolves the path per request.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site deploy example.com --pull --install --build`}
      />

      <Terminal title="root@server">{`$ ratline site deploy example.com --pull --install --build
→ git fetch && checkout main (as acme)
→ npm ci (as acme)
→ npm run build (as acme)
→ static runtime: no unit to restart
→ dist/ is 4.1 MB across 218 files
→ done in 22s`}</Terminal>

      <div className="prose">
        <H2 id="permissions">Why the files are readable and the home is not</H2>
        <p>
          <code>/home/acme</code> stays <code>0750</code>. nginx reads <code>dist/</code> because{' '}
          <code>www-data</code> is a member of the <code>acme</code> group — not because anything is
          world-readable.
        </p>
        <p>
          The alternative, <code>chmod 755</code> on the home, would work and would also make every file
          in that home traversable by every other tenant on the box. The group route grants exactly one
          daemon exactly the access it needs.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`namei -l /home/acme/example.com/dist/index.html
# drwxr-xr-x root root  /
# drwxr-xr-x root root  home
# drwxr-x--- acme acme  acme            ← 0750; nginx enters via the group
# drwxr-x--- acme acme  example.com
# drwxr-x--- acme acme  dist
# -rw-r----- acme acme  index.html`}
      />

      <div className="prose">
        <H3 id="caching">Caching</H3>
        <p>
          Content-hashed assets get <code>nginx.asset_max_age</code> — 31536000 seconds, a year. That is
          only safe <em>because</em> the filename changes when the content does. Astro hashes everything
          under <code>_astro/</code>, which is also why the subdirectory validator permits a leading
          underscore: <code>_astro</code>, <code>_next</code> and <code>_assets</code> are all real
          build-output directories. A leading <em>dot</em> is still refused, since nginx denies dotfiles.
        </p>
        <p>
          <code>index.html</code> and other unhashed entry documents are not given a year. If they were,
          a deploy would be invisible to anyone who had already visited.
        </p>

        <H2 id="tls">TLS for both names</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# Aliases become the default SAN list, so this covers both names
ratline cert issue example.com --email ops@example.com --dry-run
ratline cert issue example.com --email ops@example.com

ratline cert show example.com`}
      />

      <div className="prose">
        <p>
          Both names on one certificate is one draw against the{' '}
          <Link to="/concepts/rate-limits">rate-limit budget</Link>, not two.
        </p>

        <H2 id="adding-a-name">Adding a name later</H2>
        <p>
          Adding an alias changes the vhost, not the certificate. The new name is served but not covered
          by TLS until you re-issue.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site alias add example.com blog.example.com
ratline cert issue example.com --force    # picks the site's aliases up as SANs`}
      />

      <Callout tone="warn" title="--force here costs a duplicate">
        <p>
          Re-issuing for a name set that overlaps an existing certificate counts against{' '}
          <code>duplicate_certs_per_week</code>, which is only 5. Batch your alias changes rather than
          re-issuing once per name.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="custom">Adding something ratline does not model</H2>
        <p>
          Redirects for old URLs, a custom <code>robots.txt</code>, a security header you want on this
          site only. Put it in{' '}
          <code>/etc/nginx/ratline/custom/&lt;domain&gt;.conf</code>: it is included by the generated
          vhost and never regenerated, so it survives <code>ratline reconcile</code>. Anything added
          directly to the generated vhost will not.
        </p>
      </div>

      <CodeBlock
        lang="nginx"
        filename="/etc/nginx/ratline/custom/example.com.conf"
        code={`# Old URLs from the previous site, permanently moved.
location = /old-pricing { return 301 /pricing; }
location = /blog.php    { return 301 /blog/; }

# A 404 page that is a real 404 rather than a redirect.
error_page 404 /404.html;`}
      />

      <div className="prose">
        <p>
          Then verify and reload through ratline rather than by hand, so the change goes through{' '}
          <code>nginx -t</code> before it goes live:
        </p>
      </div>

      <CodeBlock lang="shell" prompt code={`ratline doctor
ratline site reload example.com`} />

      <div className="prose">
        <p>
          See also: <Link to="/guides/nextjs">a Next.js standalone build</Link> when the site does need a
          server, and <Link to="/guides/contractor-access">giving a contractor access</Link> when someone
          else has to update the content.
        </p>
      </div>
    </article>
  );
}
