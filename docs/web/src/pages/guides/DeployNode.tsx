import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, Facts, H2, H3 } from '../../components/ui';

/**
 * Written by deploying a real Next.js application to a real server, start to finish, and
 * writing down what happened — including five things that did not work and are now fixed.
 *
 * The existing Next.js guide described the shape correctly and had never been run: its
 * `--entry .next/standalone/server.js` was refused by ratline's own path validator. That is
 * the argument for this page existing in the form it does. Every command here was executed
 * in the order it appears.
 */
export function GuideDeployNode() {
  return (
    <article>
      <PageHeader
        eyebrow="Guide"
        title="Deploy a Node app, start to finish"
        lede="From a bare server to a Next.js application serving over HTTPS with its own MongoDB database — every command in the order you run it, and what each one is actually doing."
      />

      <Facts
        rows={[
          ['worked example', 'Next.js with output: "standalone"'],
          ['gets you', 'a tenant, a site, a database, TLS, and a repeatable deploy'],
          ['assumes', <>a fresh Ubuntu or Debian server, and <code>ratline init</code> already run</>],
          ['time', 'about ten minutes, most of it npm'],
        ]}
      />

      <div className="prose">
        <H2 id="runtime">1 · Install the Node you want to run</H2>
        <p>
          Managed runtimes live under <code>/opt/ratline/runtimes</code> and are invoked by
          absolute path from the unit. nvm and shell profiles are never involved, because
          systemd does not read them — a unit that depended on one would work when you
          tested it by hand and fail on the next boot.
        </p>
      </div>

      <CodeBlock lang="shell" prompt code={`ratline runtime install node 24 --with-pm2
ratline runtime list`} />

      <Callout tone="note" title="PM2 is per Node version, on purpose">
        <p>
          A PM2 resolved against Node 18 is not the one a Node 24 site should run under, and
          one shared install would mean changing the default silently changed the supervisor
          under every existing site. <code>--with-pm2</code> is what gives a site a graceful
          reload; <code>--daemon direct</code> runs node straight under systemd instead, one
          fewer moving part for an app that never needs one.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="tenant">2 · Create the tenant</H2>
        <p>
          One system account per tenant: its own group, a locked password, a 0750 home, no
          sudo. Everything that tenant owns lives under that home, and nginx reads their
          <code> public/</code> by being in their group rather than by the world being able
          to read it.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub

# or paste the key itself, which is what you will actually do over SSH
ratline user add acme --ssh-key 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA… you@laptop'`}
      />

      <div className="prose">
        <H2 id="build-script">3 · Write a build script</H2>
        <p>
          ratline runs one argv and never a shell line — no <code>&amp;&amp;</code>, no
          pipes, no redirection — so anything with more than one step lives in a script in
          your repository. Next.js needs three steps, because a standalone build leaves its
          static assets behind and the server expects them beside it.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        code={`#!/bin/sh
# bin/build — committed to the repository, chmod +x
set -eu
npm run build
mkdir -p .next/standalone/.next
cp -r .next/static .next/standalone/.next/static
[ -d public ] && cp -r public .next/standalone/public`}
      />

      <Callout tone="warn" title="Without the copy you get a running site with no CSS">
        <p>
          <code>next build</code> with <code>output: "standalone"</code> writes a
          self-contained <code>server.js</code> and then leaves <code>.next/static</code>{' '}
          and <code>public/</code> where they were. The server starts, answers, and serves
          pages with no stylesheets and no images — a failure that looks like a CSS problem
          and is a copy problem.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="create">4 · Create the site</H2>
        <p>
          You can create a site before its code exists — a private repository the server
          cannot clone, a build produced by CI, an rsync from your laptop. It is configured
          and left stopped, and the next step brings it up.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site add app.example.com \\
  --user acme \\
  --runtime node \\
  --node 24 \\
  --package-manager npm \\
  --install-command "npm install" \\
  --build-command "./bin/build" \\
  --entry .next/standalone/server.js \\
  --public public \\
  --listen port \\
  --ssl none`}
      />

      <Terminal title="root@server">{`! configured, but not started: there is no code in the application directory yet
    dir=/home/acme/app.example.com/app
    next="deploy your code, then 'ratline site deploy app.example.com --install --build --restart'"
✓ site created domain=app.example.com runtime=node owner=acme

owner    acme
runtime  node
root     /home/acme/app.example.com
unit     ratline-acme-app_example_com.service
listen   127.0.0.1:20000`}</Terminal>

      <Facts
        rows={[
          [
            '--listen port',
            <>
              Next.js standalone binds <code>HOSTNAME:PORT</code> and cannot listen on a
              Unix socket. Sites that can should stay on the default socket — see{' '}
              <Link to="/topics/sockets">sockets</Link> — but this one cannot, and a socket
              site whose app only speaks TCP fails with a 502 that looks like a crash.
            </>,
          ],
          [
            '--public public',
            <>
              nginx serves that directory itself, so requests for images never reach node.
              It is a path under the site directory, not a URL.
            </>,
          ],
          [
            '--ssl none',
            <>
              Because DNS does not point here yet. Ask for the certificate in step 8, once
              it does — a failed ACME attempt costs one of five per hostname per hour.
            </>,
          ],
        ]}
      />

      <div className="prose">
        <H2 id="code">5 · Get the code onto the server</H2>
        <p>
          Either let ratline clone it, or push it yourself. <code>--repo</code> on{' '}
          <code>site add</code> is the shortest path when the server can reach the
          repository; rsync is what you use for a private repo or a build made elsewhere.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# from your laptop
rsync -az --delete \\
  --exclude node_modules --exclude .next --exclude .git --exclude '.env*' \\
  ./ root@server:/home/acme/app.example.com/app/

# on the server, because the files must belong to the tenant
chown -R acme:acme /home/acme/app.example.com/app`}
      />

      <Callout tone="danger" title="Never rsync your .env">
        <p>
          The server's environment is set with <code>site env set</code>, which writes a
          0600 file owned by the tenant outside any document root. Copying your local{' '}
          <code>.env</code> puts your development secrets on a production box and usually
          overwrites the database credential ratline just created.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="database">6 · Give it a database</H2>
        <p>
          One command creates the database, creates a user whose only role is on that
          database, and writes the connection string into the site's <code>.env</code>{' '}
          instead of printing it — so the password never reaches your scrollback.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline db create acmeshop --owner acme --attach app.example.com`}
      />

      <div className="prose">
        <p>
          If the server has no MongoDB connection yet, <code>ratline db connect</code> asks
          for one first — see <Link to="/guides/mongodb">the database guide</Link>.
        </p>

        <H2 id="env">7 · Set the rest of the environment</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# not secret: say it outright
ratline site env set app.example.com NODE_ENV=production HOSTNAME=127.0.0.1

# secret: name it, and paste the value at the prompt — not echoed, not in argv,
# not in your shell history
ratline site env set app.example.com AUTH_SECRET

ratline site env list app.example.com`}
      />

      <Callout tone="note" title="Do not set PORT">
        <p>
          ratline allocates one from <code>20000–29999</code> and the unit passes it in. A{' '}
          <code>PORT</code> in <code>.env</code> fights the allocation, and the site answers
          on a port nginx is not proxying to.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="deploy">8 · Deploy</H2>
        <p>
          Install, build, restart, and wait for a real HTTP response before reporting
          success. The build gets the site's environment, the same variables the service
          runs with — Next.js evaluates route modules while collecting page data, so a build
          without them fails on code that would have run perfectly.
        </p>
      </div>

      <CodeBlock lang="shell" prompt code={`ratline site deploy app.example.com --install --build --restart`} />

      <Terminal title="root@server">{`→ deploy step step=install domain=app.example.com
→ installing dependencies package_manager=npm dir=/home/acme/app.example.com/app
→ building command=./bin/build
→ the application is healthy domain=app.example.com check="HTTP 200 in 535ms"
✓ Deployed app.example.com
  steps   install, build, restart
  health  HTTP 200 in 535ms`}</Terminal>

      <Callout tone="note" title="Dev dependencies are installed when there is a build">
        <p>
          Tailwind, TypeScript, PostCSS and Vite live in <code>devDependencies</code>, so a
          site with a build command gets them. A site without one is installed
          production-only, because then they are dead weight on a server. Nothing is pruned
          afterwards: a wrong prune fails at request time rather than at deploy time.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="tls">9 · TLS, once DNS points here</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`dig +short app.example.com A @1.1.1.1     # check first; a failed attempt costs a retry
ratline cert issue app.example.com --email ops@example.com`}
      />

      <div className="prose">
        <p>
          Renewal is automatic from then on, and{' '}
          <Link to="/guides/renewal-runbook">the renewal runbook</Link> is worth reading
          before you need it. <code>--hsts</code> only once a trusted certificate is
          attached: HSTS on a self-signed one is a site nobody can reach for a year.
        </p>

        <H2 id="after">10 · Afterwards</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site show app.example.com          # everything about it on one screen
ratline site logs app.example.com --follow
ratline site deploy app.example.com --install --build --restart   # every change
ratline site scale app.example.com --instances 4 --memory-max 1G`}
      />

      <div className="prose">
        <p>
          A deploy that fails leaves the previous version serving. A deploy that starts but
          never answers is reverted, and the error says so — the health check is what
          decides, not whether the process is still alive.
        </p>

        <H2 id="wrong">When it does not come up</H2>
        <H3 id="wrong-502">502 from nginx</H3>
        <p>
          The application is not listening where nginx expects.{' '}
          <Link to="/reference/site/troubleshoot">site troubleshoot</Link> checks the whole
          chain in dependency order and names the first thing that is wrong;{' '}
          <Link to="/guides/debug-502">debugging a 502</Link> is the long version.
        </p>
        <H3 id="wrong-logs">The unit fails immediately</H3>
        <p>
          The application's own output goes to <code>logs/app.log</code> in the site
          directory, not to the journal — the journal has systemd's view, which will tell
          you the process exited and not why.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site troubleshoot app.example.com
ratline site logs app.example.com          # the application's own output
ratline site logs app.example.com --journal  # systemd's view of the unit`}
      />
    </article>
  );
}
