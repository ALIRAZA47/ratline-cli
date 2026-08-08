import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, H2, H3 } from '../../components/ui';

export function GuideCiDeployKeys() {
  return (
    <article>
      <PageHeader
        eyebrow="Guide"
        title="CI deploy keys, in both directions"
        lede="Two different credentials that people routinely confuse. One lets CI reach the server; the other lets the server reach a private repository. They point opposite ways and neither substitutes for the other."
      />

      <div className="not-prose my-7 grid gap-4 md:grid-cols-2">
        <div className="rounded-[var(--radius-card)] border border-line bg-raised px-4 py-4">
          <p className="font-mono text-2xs uppercase tracking-wider text-faint">Direction A · inbound</p>
          <h2 className="mt-1.5 text-base font-semibold text-strong">CI → server</h2>
          <p className="mt-2 text-sm leading-relaxed text-muted">
            The CI runner holds a private key. Its public half is installed on the server as a{' '}
            <strong>site-scoped</strong> key, so the runner can rsync a build in or trigger a deploy — and
            nothing else.
          </p>
          <p className="mt-3 font-mono text-xs text-accent">ratline key add --scope site</p>
        </div>
        <div className="rounded-[var(--radius-card)] border border-line bg-raised px-4 py-4">
          <p className="font-mono text-2xs uppercase tracking-wider text-faint">Direction B · outbound</p>
          <h2 className="mt-1.5 text-base font-semibold text-strong">server → repository</h2>
          <p className="mt-2 text-sm leading-relaxed text-muted">
            The <em>server</em> holds a private key, generated on the box and owned by the site user. Its
            public half goes into the repository host as a read-only deploy key, so{' '}
            <code>site deploy --pull</code> can clone a private repo.
          </p>
          <p className="mt-3 font-mono text-xs text-accent">ratline site deploy-key create</p>
        </div>
      </div>

      <Callout tone="warn" title="They are not interchangeable">
        <p>
          A GitHub “deploy key” and a ratline site-scoped SSH key are both called deploy keys by somebody,
          and they do opposite things. If <code>site deploy --pull</code> fails with{' '}
          <code>Permission denied (publickey)</code>, you need direction B. If your CI job cannot reach the
          server at all, you need direction A. Adding the wrong one produces a key that works perfectly and
          solves nothing.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="inbound">Direction A · letting CI reach the server</H2>
        <H3>1 · Generate a keypair for the runner</H3>
        <p>
          One key per pipeline, not one key shared by four. A fingerprint already present anywhere on the
          box is refused unless <code>--allow-duplicate</code>, and the message names where it already
          exists — which is the tool telling you that sharing keys defeats the point of labels.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        filename="on your machine, once"
        code={`ssh-keygen -t ed25519 -N "" -C "ci@example.com-deploy" -f ./ci_example_com
# ci_example_com      → the CI secret
# ci_example_com.pub  → goes on the server`}
      />

      <div className="prose">
        <H3>2 · Install the public half, as narrowly as the job allows</H3>
        <p>
          If the job only pushes files, <code>--command rsync-only</code> is much tighter than SFTP: the key
          can invoke rsync and nothing else.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline key add \\
  --label "CI runner — example.com" \\
  --key ./ci_example_com.pub \\
  --scope site \\
  --user acme \\
  --site example.com \\
  --command rsync-only \\
  --from 203.0.113.0/24 \\
  --expires 365d`}
      />

      <div className="prose">
        <p>
          <code>--from</code> is worth the effort even for a hosted runner with a wide address range: it
          turns a stolen key into a stolen key that only works from one network. And{' '}
          <code>--expires</code> means a pipeline that is decommissioned and forgotten stops being a
          credential a year later, whether or not anyone remembers to clean it up.
        </p>

        <H3>3 · Verify, then wire it into the pipeline</H3>
      </div>

      {/* One panel: the command, and under a hairline, what it prints. As two blocks the
          reader had to infer that the second was the first one's output, which is the only
          reason it is here. */}
      <CodeBlock
        lang="shell"
        prompt
        code={`ratline key test "CI runner — example.com"`}
        output={`Key       SHA256:cD2…   "CI runner — example.com"   ed25519
Scope     site → example.com  (owner: acme)
Login     acme@server — forced command only, no interactive shell
Allowed   rsync
          confined to /home/acme/example.com (symlinks resolved)
Denied    shell, sftp, git, port forwarding, agent forwarding, X11, PTY
Source    203.0.113.0/24 only
Expires   2027-08-04 (365 days)
Last use  never
Note      Runs as UID acme. Not a kernel boundary — see SECURITY.md.`}
      />

      <CodeBlock
        lang="text"
        filename=".github/workflows/deploy.yml (the relevant step)"
        code={`- name: Publish the build
  env:
    SSH_KEY: \${{ secrets.RATLINE_DEPLOY_KEY }}
    HOST: server.example.com
  run: |
    install -m 600 /dev/stdin ~/.ssh/deploy <<< "$SSH_KEY"
    ssh-keyscan -H "$HOST" >> ~/.ssh/known_hosts
    rsync -avz --delete -e "ssh -i ~/.ssh/deploy" ./dist/ "acme@$HOST:example.com/public/"`}
      />

      <Callout tone="note" title="ssh-keyscan, and its caveat">
        <p>
          Accepting the host key on first connection is trust-on-first-use, and in CI there is no human to
          make that decision. It is fine for most threat models and it is not nothing: pin the host key in
          your CI secrets instead if the difference matters to you.
        </p>
      </Callout>

      <div className="prose">
        <H3 id="trigger">If CI needs to run a deploy rather than push files</H3>
        <p>
          A site-scoped key cannot run <code>ratline</code> — that is <code>global</code> scope, which
          grants server administration. Handing a CI runner a global key means handing it the whole box, and
          that trade is almost never worth it for a deploy.
        </p>
        <p>
          Have CI push, and let the server decide when to build. Either the push itself triggers the build,
          or a scheduled <code>site deploy --pull</code> picks the change up:
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# CI pushes to a branch; the server pulls and builds on its own schedule
ratline site deploy app.example.com --pull --install --build --restart`}
      />

      <div className="prose">
        <H2 id="outbound">Direction B · letting the server reach a private repository</H2>
        <p>
          <code>site add --repo</code> and <code>site deploy --pull</code> both need to authenticate to the
          repository host when the repo is private. The credential for that is generated{' '}
          <em>on the server</em>: the private half stays there, owned by the site user, and never appears in
          any output.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site deploy-key create example.com --type ed25519`}
      />

      <Terminal title="root@server">{`$ ratline site deploy-key create example.com --type ed25519
→ generated ed25519 keypair for example.com (owner: acme)
→ private key written 0600 acme:acme — it will never be printed
→ public key:

ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI… ratline-deploy-example.com

→ add this to your repository host as a READ-ONLY deploy key.
  GitHub:  Settings → Deploy keys → Add deploy key (leave write access off)
  GitLab:  Settings → Repository → Deploy keys`}</Terminal>

      <div className="prose">
        <p>
          Read-only, always. A deploy key with write access turns a compromise of one site into a compromise
          of the source of truth for that site, and there is no deploy that needs to push.
        </p>
        <H3>Then use an ssh:// URL</H3>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site add example.com --user acme --runtime static \\
  --repo git@github.com:acme/private-site.git \\
  --branch main \\
  --build-command "npm run build" --build-output dist`}
      />

      <div className="prose">
        <p>
          <code>https://</code> and <code>ssh://</code> (or scp-style{' '}
          <code>user@host:path</code>) are accepted. Refused on purpose, each with its own reason:{' '}
          <code>ext::</code> runs arbitrary commands, <code>file::</code> local clones are not supported,{' '}
          <code>git://</code> is neither authenticated nor encrypted, and plain <code>http://</code> is not
          encrypted.
        </p>

        <H3 id="rotate">Rotating and removing</H3>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site deploy-key show example.com     # public half only, ever
ratline site deploy-key rotate example.com   # new pair, prints the new public key
ratline site deploy-key remove example.com`}
      />

      <Callout tone="warn" title="Update the repository host before you rely on a rotation">
        <p>
          <code>rotate</code> generates a new pair immediately, and the old public key stops working the
          moment you delete it at the repository host — not before. So the safe order is: rotate, add the
          new public key upstream, verify a pull works, then delete the old one upstream. The same
          add-verify-remove shape as{' '}
          <Link to="/guides/new-laptop-key">rotating your own key</Link>, for the same reason.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="both">Both directions, in one place</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# B: the server can clone the private repo
ratline site deploy-key create app.example.com
# ...paste the printed key into the repository host as read-only...

ratline site add app.example.com --user acme --runtime node \\
  --repo git@github.com:acme/web.git --branch main \\
  --entry .next/standalone/server.js --node 22 \\
  --install-command "npm ci" --build-command "./bin/build"

# A: CI can push a prebuilt artefact into the same site
ratline key add --label "CI runner — app.example.com" \\
  --key ./ci.pub --scope site --user acme --site app.example.com \\
  --command rsync-only --from 203.0.113.0/24 --expires 365d

# And the routine review that keeps this from accumulating
ratline key list --unused 180
ratline key list --expiring 30
ratline key audit`}
      />

      <div className="prose">
        <p>
          See also: <Link to="/concepts/ssh-scopes">the three SSH scopes</Link> — in particular{' '}
          <Link to="/concepts/ssh-scopes#site-scope-limits">what site scope does not enforce</Link>, which
          applies just as much to a CI runner as to a person.
        </p>
      </div>
    </article>
  );
}
