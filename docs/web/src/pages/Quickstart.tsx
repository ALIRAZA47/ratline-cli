import { Link } from 'react-router-dom';
import { CodeBlock } from '../components/CodeBlock';
import { Terminal } from '../components/Terminal';
import { Callout, Facts, H2, H3, Tabs } from '../components/ui';
import { PageHeader } from '../components/PageHeader';

export function Quickstart() {
  return (
    <article>
      <PageHeader
        eyebrow="Start here"
        title="60-second quickstart"
        lede="From a bare Ubuntu box to a working HTTPS site: install, initialise, a runtime, a tenant, a site, a certificate. Seven steps, and the site part changes with the runtime."
      />

      <Callout tone="warn" title="Most of this is not implemented yet">
        <p>
          <code>version</code>, <code>man</code> and <code>completion</code> work today. Everything
          from <code>ratline init</code> onwards is specified and being built in order, so treat the
          sequence below as the intended shape rather than something you can run on a server this
          afternoon. The invocations themselves are taken from the command surface and will not
          change shape.
        </p>
      </Callout>

      <div className="prose">
        <H2>0 · Install it</H2>
        <p>
          Three ways in, in order of how little work they are. All three put the same two binaries in
          the same places: <code>ratline</code> at <code>/usr/local/bin/ratline</code>, and the
          forced-command wrapper at <code>/usr/local/lib/ratline/ratline-shell</code> — which is{' '}
          <code>paths.shell_wrapper</code>, and which is root-owned and not group-writable because it
          runs on every site-scoped SSH connection.
        </p>
      </div>

      <Tabs
        label="Installation method"
        tabs={[
          {
            id: 'pkg',
            label: '.deb / .rpm',
            content: (
              <div>
                <p className="not-prose mb-3 max-w-[var(--container-measure)] text-sm leading-relaxed text-muted">
                  Built from <code>packaging/nfpm.yaml</code>. nginx and certbot are{' '}
                  <em>Recommends</em>, not <em>Depends</em>: ratline is useful for inspecting a server
                  before either is installed, and <code>doctor</code> says what is missing.
                </p>
                <CodeBlock
                  lang="shell"
                  prompt
                  code={`VERSION=1.0.0 ARCH=amd64 nfpm package --config packaging/nfpm.yaml --packager deb
sudo apt-get install ./ratline_1.0.0_amd64.deb`}
                />
              </div>
            ),
          },
          {
            id: 'script',
            label: 'install.sh',
            content: (
              <div>
                <p className="not-prose mb-3 max-w-[var(--container-measure)] text-sm leading-relaxed text-muted">
                  POSIX sh, root required, and it does nothing surprising: every package it would
                  install is named first and confirmed, and it never edits a configuration file it did
                  not create. It needs the binaries built first.
                </p>
                <CodeBlock
                  lang="shell"
                  prompt
                  code={`make dist
sudo sh packaging/install.sh

# PREFIX defaults to /usr/local; ASSUME_YES=1 answers every prompt
sudo PREFIX=/opt/local ASSUME_YES=1 sh packaging/install.sh`}
                />
              </div>
            ),
          },
          {
            id: 'source',
            label: 'From source',
            content: (
              <div>
                <p className="not-prose mb-3 max-w-[var(--container-measure)] text-sm leading-relaxed text-muted">
                  The <code>0755 root:root</code> is not decoration — see the note below.
                </p>
                <CodeBlock
                  lang="shell"
                  prompt
                  code={`git clone https://github.com/ALIRAZA47/ratline-cli.git
cd ratline-cli
go build -o ratline ./cmd/ratline
sudo install -m 0755 -o root -g root ratline /usr/local/bin/ratline`}
                />
              </div>
            ),
          },
        ]}
      />

      <div className="prose">
        <H3>What the installer actually does</H3>
        <ul>
          <li>
            Creates every directory with the mode ratline expects: <code>/etc/ratline</code>{' '}
            <code>0755</code>, but <code>ssh/</code>, <code>dns/</code> and <code>certs/</code> under
            it at <code>0700</code> because they hold credentials;{' '}
            <code>/var/lib/ratline</code> and <code>/var/log/ratline</code> at <code>0750</code>;{' '}
            <code>/var/backups/ratline</code> at <code>0700</code>;{' '}
            <code>/var/www/ratline-acme/.well-known/acme-challenge</code> and{' '}
            <code>/etc/nginx/ratline/custom</code> at <code>0755</code>.
          </li>
          <li>
            Checks for nginx and certbot and offers to install them, then continues without them if
            you decline — <code>doctor</code> keeps reminding you.
          </li>
          <li>
            Seeds <code>/etc/ratline/config.yaml</code> only if one is not already there. An existing
            file is kept.
          </li>
          <li>
            Installs and enables two timers: <code>ratline-cert-renew.timer</code> (
            <code>OnCalendar=*-*-* 03,15:00:00</code> with <code>RandomizedDelaySec=3h</code>) and{' '}
            <code>ratline-key-prune.timer</code> (daily, <code>RandomizedDelaySec=1h</code>). Both are{' '}
            <code>Persistent=true</code>, so a machine that was off when a window passed runs on next
            boot rather than waiting.
          </li>
          <li>
            <strong>Disables <code>certbot.timer</code> if it is enabled.</strong> Two renewal timers
            would race, each reloading nginx from under the other. ratline’s runs the deploy hook that
            reloads only the sites whose certificates actually changed, so certbot’s is the one that
            goes.
          </li>
          <li>
            Installs bash and zsh completions and the man pages, using{' '}
            <code>ratline completion</code> and <code>ratline man</code> — the two commands that are
            already built.
          </li>
          <li>
            <strong>Does not touch your firewall.</strong> It prints what needs to be reachable: 22 for
            SSH, 443 for HTTPS, and 80 — the ACME challenge is served there, and renewal fails without
            it.
          </li>
        </ul>
      </div>

      <Callout tone="warn" title="Why the binary must be 0755 root:root">
        <p>
          ratline refuses to run if its own binary is group- or world-writable, because a writable
          binary that runs as root is a root escalation waiting for a cron job. It also refuses to run
          at all unless EUID is 0 — except for <code>version</code>, <code>man</code> and{' '}
          <code>completion</code>.
        </p>
      </Callout>

      <div className="prose">
        <H3>Confirm what the host actually has</H3>
        <p>
          <code>ratline version</code> is deliberately more than a version string. Almost every report
          that starts “it does not work” is answered by one of these lines.
        </p>
      </div>

      <Terminal title="ali@server">{`$ ratline version
ratline dev commit=none built=unknown darwin/arm64 go1.26.5
os               darwin
config           /etc/ratline/config.yaml (not present; using built-in defaults)
nginx            not installed
certbot          not installed
openssh          10.2p1
systemd          not installed
node runtimes    none installed
python runtimes  none installed
~ real output, on a machine with none of it installed. Every line here is a thing
~ that breaks a provisioning run, which is why they are printed together.`}</Terminal>

      <div className="prose">
        <H2>1 · Initialise the server</H2>
        <p>
          <code>ratline init</code> is the first-run wizard. It writes{' '}
          <code>/etc/ratline/config.yaml</code>, chooses the admin account that will hold global-scope
          SSH keys, and records that you have accepted the CA’s terms —{' '}
          <code>acme.tos_agreed</code> is <code>false</code> until you do, and ratline does not accept
          them on your behalf.
        </p>
        <p>
          If you installed from a package or with <code>install.sh</code>, the webroot and the timers
          are already in place; <code>init</code> fills in the things only you can answer.
        </p>
      </div>

      <CodeBlock lang="shell" prompt code={`sudo ratline init`} />

      <Callout tone="note" title="Until init has run">
        <p>
          Mutating commands warn — <code className="font-mono">! no configuration file; using
          built-in defaults</code> — and carry on with the built-in defaults. Nothing is broken, but{' '}
          <code>runtimes.node_default</code>, <code>runtimes.python_default</code>,{' '}
          <code>acme.email</code> and <code>acme.tos_agreed</code> are all empty or false, so you
          will have to name versions explicitly and TLS issuance will refuse.
        </p>
      </Callout>

      <div className="prose">
        <H2>2 · Install a runtime</H2>
        <p>
          Runtimes are installed once and referenced by absolute path from every unit that uses them.
          Skip this for a static site.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline runtime install node 22
ratline runtime install python 3.12
ratline runtime default node 22
ratline runtime default python 3.12`}
      />

      <div className="prose">
        <H2>3 · Create the tenant</H2>
        <p>
          One user per client, or one user per site where you want real separation.{' '}
          <code>user add</code> is cheap for exactly that reason.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub --comment "Acme Ltd"`}
      />

      <div className="prose">
        <p>
          That creates the account and its group, locks the password, makes{' '}
          <code>/home/acme</code> at <code>0750</code> with <code>.ssh/</code> at <code>0700</code>,
          installs the key, and grants no sudo.
        </p>
      </div>

      <div className="prose">
        <H2 id="add-the-site">4 · Add the site</H2>
        <p>Pick the runtime that matches what you are deploying.</p>
      </div>

      <Tabs
        label="Runtime"
        tabs={[
          {
            id: 'static',
            label: 'static',
            content: (
              <div>
                <p className="not-prose mb-3 max-w-[var(--container-measure)] text-sm leading-relaxed text-muted">
                  nginx serves files straight from the document root. No unit, no socket, nothing
                  running — which also means nothing to restart and nothing to 502.
                </p>
                <CodeBlock
                  lang="shell"
                  prompt
                  code={`ratline site add example.com \\
  --user acme \\
  --runtime static \\
  --repo https://github.com/acme/site.git \\
  --build-command "npm ci" \\
  --build-output dist \\
  --alias www.example.com \\
  --www-redirect apex`}
                />
                <Facts
                  rows={[
                    ['serves', <code key="a">/home/acme/example.com/dist</code>],
                    ['unit', 'none'],
                    [
                      'SPA routes',
                      <>
                        add <code>--spa</code> for{' '}
                        <code>try_files $uri $uri/ /index.html</code>
                      </>,
                    ],
                    [
                      'full guide',
                      <Link key="g" to="/guides/astro">
                        Publish an Astro static build
                      </Link>,
                    ],
                  ]}
                />
              </div>
            ),
          },
          {
            id: 'node',
            label: 'node',
            content: (
              <div>
                <p className="not-prose mb-3 max-w-[var(--container-measure)] text-sm leading-relaxed text-muted">
                  nginx reverse-proxies to a Unix socket. <code>ExecStart</code> invokes the managed
                  Node binary by absolute path, so nvm and shell profiles are never involved.
                </p>
                <CodeBlock
                  lang="shell"
                  prompt
                  code={`ratline site add app.example.com \\
  --user acme \\
  --runtime node \\
  --entry server.js \\
  --node 22 \\
  --install-command "npm ci --omit=dev" \\
  --build-command "npm run build" \\
  --public public`}
                />
                <Facts
                  rows={[
                    [
                      'unit',
                      <code key="a">ratline-acme-app_example_com.service</code>,
                    ],
                    [
                      'socket',
                      <code key="b">/run/ratline/acme-app_example_com/app.sock</code>,
                    ],
                    [
                      'ExecStart',
                      <code key="c">/opt/ratline/runtimes/node/22/bin/node server.js</code>,
                    ],
                    [
                      'full guide',
                      <Link key="g" to="/guides/nextjs">
                        A Next.js standalone build behind nginx
                      </Link>,
                    ],
                  ]}
                />
              </div>
            ),
          },
          {
            id: 'python',
            label: 'python',
            content: (
              <div>
                <p className="not-prose mb-3 max-w-[var(--container-measure)] text-sm leading-relaxed text-muted">
                  Gunicorn, or Gunicorn with a Uvicorn worker for ASGI, in a per-site virtualenv
                  behind a Unix socket. FastAPI and Starlette are detected as ASGI automatically.
                </p>
                <CodeBlock
                  lang="shell"
                  prompt
                  code={`ratline site add api.example.com \\
  --user acme \\
  --runtime python \\
  --app-module app.main:app \\
  --python 3.12 \\
  --asgi \\
  --workers 4 \\
  --static-url /static --static-dir staticfiles`}
                />
                <Facts
                  rows={[
                    ['venv', <code key="a">/home/acme/api.example.com/venv</code>],
                    ['workers', '(2 × cores) + 1, capped at 8, unless --workers says otherwise'],
                    [
                      'app module',
                      <>
                        must match{' '}
                        <code>^[A-Za-z_][A-Za-z0-9_.]*:[A-Za-z_][A-Za-z0-9_]*$</code>
                      </>,
                    ],
                    [
                      'full guide',
                      <Link key="g" to="/guides/fastapi">
                        Deploy a FastAPI app behind Gunicorn and Uvicorn
                      </Link>,
                    ],
                  ]}
                />
              </div>
            ),
          },
        ]}
      />

      <Callout tone="note" title="Run it with --dry-run first">
        <p>
          <code>--dry-run</code> prints every file, command and permission change without making
          any of them. Reads still run, so the preview reflects the real system rather than a guess
          about it.
        </p>
      </Callout>

      <div className="prose">
        <H2>5 · Get a real certificate</H2>
        <p>
          <code>site add --ssl</code> is convenience only, and its default is honest: Let’s Encrypt
          if the domain already resolves to this server, self-signed with a printed note otherwise. A
          certificate failure never fails the site creation, because creating the site before DNS has
          moved is the normal order of operations.
        </p>
        <p>Once DNS points here:</p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# validate everything first — this costs no rate-limit budget
ratline cert issue example.com --email admin@example.com --dry-run

# then for real
ratline cert issue example.com --email admin@example.com --alias www.example.com`}
      />

      <Terminal title="root@server">{`$ ratline cert issue example.com --email admin@example.com
→ preflight: site enabled, vhost found
→ preflight: A 203.0.113.10 matches this server
→ preflight: webroot token fetched, 200 with exact match
→ preflight: budget 47 of 50 certificates remaining this week for example.com
→ certbot: certificate issued, expires 2026-11-02
→ staging vhost, nginx -t passed, reloaded
→ verified: served chain matches, covers example.com, validates against the system root store
→ recorded in state`}</Terminal>

      <div className="prose">
        <p>
          The verification step is the one that matters. A certificate on disk that is not being
          served is a failure, not a success — so ratline opens a real TLS connection with SNI and
          checks the served chain before it records anything.
        </p>

        <H3>If preflight refuses</H3>
        <p>
          It reports <em>every</em> problem at once rather than one per attempt, because each attempt
          costs rate-limit budget. The usual answers:
        </p>
        <ul>
          <li>
            DNS does not point here → wait, or <Link to="/guides/issue-cert">check what it sees</Link>.
          </li>
          <li>
            The address belongs to a known proxy range → HTTP-01 cannot work; see{' '}
            <Link to="/guides/cloudflare">the orange-cloud trap</Link>.
          </li>
          <li>
            Rate limit exhausted → exit 9, with a countdown. Use <code>--staging</code> while you
            debug.
          </li>
        </ul>
      </div>

      <div className="prose">
        <H2>6 · Confirm, then walk away</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site show example.com
ratline cert list --expiring 30
ratline doctor`}
      />

      <div className="prose">
        <p>
          Renewal runs on a timer twice daily with a randomised delay, attempted under 30 days
          remaining, and the deploy hook reloads only the affected site. Run{' '}
          <code>ratline cert test-renewal</code> once now rather than finding out in eighty-nine
          days.
        </p>
      </div>
    </article>
  );
}
