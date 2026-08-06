import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, Facts, H2 } from '../../components/ui';

/**
 * The access model here is the interesting part, not the YAML.
 *
 * A deploy needs root — systemd, nginx — and handing CI a root key to get it is how a
 * compromised action becomes a compromised server. ratline already has the primitive for
 * this and its own source says so: the sudo escape hatch was written for "a client whose
 * CI restarts their own service". One pinned command, validated by visudo, and a key that
 * can reach nothing else.
 */
export function GuideContinuousDeployment() {
  return (
    <article>
      <PageHeader
        eyebrow="Guide"
        title="Deploy from GitHub Actions"
        lede="A workflow that pushes code and deploys it, with a key that can run exactly one command as root and nothing else."
      />

      <Facts
        rows={[
          ['the runner gets', 'an SSH key scoped to one tenant'],
          ['it may run as root', <>exactly <code>ratline site deploy &lt;domain&gt; …</code>, argument list pinned</>],
          ['it may not run', 'anything else — not another ratline command, not a shell'],
          ['you create the site', 'by hand, once; CI only ever deploys to it'],
        ]}
      />

      <div className="prose">
        <H2 id="why">Why not just give CI a root key</H2>
        <p>
          Because a workflow is a program written by whoever can open a pull request, and a
          root key on the runner turns any mistake in that program into a compromised
          server. What CI actually needs is narrow: put files in one directory, then run one
          command. That is what this sets up.
        </p>

        <H2 id="server">1 · On the server, once</H2>
        <p>
          Create the site however you normally would — see{' '}
          <Link to="/guides/deploy-node">the Node guide</Link> or{' '}
          <Link to="/guides/deploy-python">the Python guide</Link>. Then give CI its key and
          its one command.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# A key for the runner. Generate it on your laptop, keep the private half in
# GitHub's secrets, and never let it touch the server's disk.
ssh-keygen -t ed25519 -f ./ci -N '' -C "github actions"

ratline key add --scope user --user acme --label "github actions" \\
  --key "$(cat ./ci.pub)"

# The one thing it may do as root. The full argument list is pinned: this cannot
# become a different command, and --json is part of it because the workflow reads
# the result.
ratline config set users.allow_sudo true
ratline user sudo grant acme \\
  --command '/usr/local/bin/ratline site deploy app.example.com --install --build --restart --json'`}
      />

      <Callout tone="warn" title="allow_sudo is a real decision">
        <p>
          A tenant with any sudo can, in principle, reach every other tenant's files —
          ratline says so when you grant it. The grant here is one pinned command, validated
          with <code>visudo</code> before it is installed, and{' '}
          <Link to="/reference/user/sudo-grant">revocable</Link> in one step. If you would rather
          not, the alternative is a self-hosted runner on the server itself, or a webhook
          you write.
        </p>
      </Callout>

      <div className="prose">
        <p>Check what sudo itself thinks the key can do, rather than trusting the grant:</p>
      </div>

      <Terminal title="root@server">{`$ sudo -l -U acme
User acme may run the following commands on server:
    (root) NOPASSWD: /usr/local/bin/ratline site deploy app.example.com --install --build --restart --json

$ sudo -u acme sudo -n /bin/bash -c id
sudo: a password is required`}</Terminal>

      <div className="prose">
        <H2 id="secrets">2 · In the repository</H2>
        <p>Three secrets, under an environment so a deploy can require a reviewer:</p>
      </div>

      <Facts
        rows={[
          ['RATLINE_SSH_KEY', <>the private half of <code>./ci</code></>],
          [
            'RATLINE_HOST_KEY',
            <>
              the server's host key: <code>ssh-keyscan server.example.com</code>. Pinned,
              not trust-on-first-use — without it the first connection accepts whatever
              answers, which is the whole attack.
            </>,
          ],
          ['environment', <>a GitHub environment named <code>production</code></>],
        ]}
      />

      <div className="prose">
        <H2 id="node">3 · The workflow — Node</H2>
        <p>
          Typecheck on the runner, then copy and deploy. Failing before anything reaches the
          server is the point of doing it in that order.
        </p>
      </div>

      <CodeBlock
        lang="yaml"
        code={`name: deploy
on:
  push:
    branches: [main]
  workflow_dispatch:

concurrency:
  # Two rsyncs into one directory would have ratline build a mixture of both.
  group: deploy-\${{ github.ref }}
  cancel-in-progress: false

env:
  RATLINE_DOMAIN: app.example.com
  RATLINE_TENANT: acme
  RATLINE_HOST: server.example.com

jobs:
  deploy:
    runs-on: ubuntu-latest
    environment: production
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-node@v5
        with: { node-version: 24, cache: npm }
      - run: npm ci
      - run: npm run typecheck --if-present
      - run: npm run lint --if-present

      - name: Authorise this runner
        run: |
          set -euo pipefail
          install -d -m 0700 ~/.ssh
          printf '%s\\n' "\${{ secrets.RATLINE_SSH_KEY }}" > ~/.ssh/deploy
          chmod 0600 ~/.ssh/deploy
          printf '%s\\n' "\${{ secrets.RATLINE_HOST_KEY }}" > ~/.ssh/known_hosts

      - name: Copy the source
        run: |
          rsync -az --delete \\
            --exclude .git --exclude node_modules --exclude .next --exclude '.env*' \\
            -e "ssh -i ~/.ssh/deploy -o IdentitiesOnly=yes" \\
            ./ "\${RATLINE_TENANT}@\${RATLINE_HOST}:\${RATLINE_DOMAIN}/app/"

      - name: Deploy
        run: |
          set -euo pipefail
          ssh -i ~/.ssh/deploy -o IdentitiesOnly=yes "\${RATLINE_TENANT}@\${RATLINE_HOST}" \\
            "sudo /usr/local/bin/ratline site deploy \${RATLINE_DOMAIN} --install --build --restart --json" \\
            | tee deploy.json
          jq -e '.ok == true' deploy.json > /dev/null

      - name: What is serving
        if: always()
        run: jq -r '.data | "health: \\(.health // "n/a")"' deploy.json || true`}
      />

      <Callout tone="note" title="Read the envelope, not the exit code alone">
        <p>
          <code>--json</code> wraps everything in{' '}
          <code>{'{ok, command, version, data}'}</code>, and{' '}
          <code>jq -e '.ok == true'</code> is what turns a deploy that reported a problem
          into a red build. See <Link to="/reference/json">the JSON envelope</Link> and{' '}
          <Link to="/reference/exit-codes">the exit codes</Link>, which are a contract you
          can branch on.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="python">4 · The workflow — Python</H2>
        <p>The same shape; only the checks and the deploy flags differ.</p>
      </div>

      <CodeBlock
        lang="yaml"
        code={`      - uses: actions/setup-python@v5
        with: { python-version: '3.12' }
      - run: pip install -r requirements.txt
      - run: python -m compileall -q .

      # ... copy step identical ...

      - name: Deploy
        run: |
          set -euo pipefail
          ssh -i ~/.ssh/deploy -o IdentitiesOnly=yes "\${RATLINE_TENANT}@\${RATLINE_HOST}" \\
            "sudo /usr/local/bin/ratline site deploy \${RATLINE_DOMAIN} --install --restart --json" \\
            | tee deploy.json
          jq -e '.ok == true' deploy.json > /dev/null

# Django wants the migration and static steps in the grant too:
#   ratline site deploy api.example.com --install --migrate --collectstatic --restart --json`}
      />

      <div className="prose">
        <H2 id="static">5 · The workflow — a static build</H2>
        <p>
          Nothing to install on the server and nothing to restart: build on the runner and
          copy the output into the document root. No sudo grant is needed at all, which
          makes this the safest of the three.
        </p>
      </div>

      <CodeBlock
        lang="yaml"
        code={`      - run: npm ci && npm run build

      - name: Publish
        run: |
          rsync -az --delete \\
            -e "ssh -i ~/.ssh/deploy -o IdentitiesOnly=yes" \\
            ./dist/ "\${RATLINE_TENANT}@\${RATLINE_HOST}:\${RATLINE_DOMAIN}/public/"`}
      />

      <Callout tone="note" title="A site-scoped key is enough here">
        <p>
          <code>ratline key add --scope site --site app.example.com</code> gives a key whose
          forced command permits sftp, rsync and git and nothing else, and which cannot
          leave that one site's directory. Use it wherever the deploy is only a file copy —
          see <Link to="/concepts/ssh-scopes">the three scopes</Link> for what it does and
          does not enforce.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="rollback">6 · When a deploy fails</H2>
        <p>
          It does not need handling in the workflow. A deploy step that fails leaves the
          previous version serving; one that starts but never answers its health check is
          reverted, and the command exits non-zero with the reason. The build goes red and
          the site stays up.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site deploy app.example.com --rollback   # by hand, to the previous release
ratline site logs app.example.com
ratline site troubleshoot app.example.com`}
      />
    </article>
  );
}
