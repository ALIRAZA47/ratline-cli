import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, Facts, H2, H3 } from '../../components/ui';

export function GuideNextJs() {
  return (
    <article>
      <PageHeader
        eyebrow="Guide"
        title="A Next.js standalone build behind nginx"
        lede="output: 'standalone' produces a self-contained server.js, which is exactly the shape --entry wants. The interesting parts are the build memory, the static bypass, and the fact that the managed Node binary is invoked by absolute path."
      />

      <Callout tone="note" title="Starting from nothing?">
        <p>
          <Link to="/guides/deploy-node">Deploy a Node app, start to finish</Link> walks the whole thing —
          runtime, tenant, code, database, environment, TLS — in the order you run it.
          This page is the detail behind the parts specific to this framework.
        </p>
      </Callout>

      <Facts
        rows={[
          ['runtime', <code key="a">node</code>],
          ['entry', <code key="b">--entry .next/standalone/server.js</code>],
          ['listens on', <>an allocated TCP port — see <a href="#socket">socket or port</a></>],
          ['nginx serves', <><code>public/</code> directly, bypassing the app</>],
        ]}
      />

      <div className="prose">
        <H2 id="standalone">Configure the standalone output</H2>
        <p>
          Without this, <code>next build</code> leaves you needing the whole{' '}
          <code>node_modules</code> tree and the <code>next</code> CLI at runtime.{' '}
          <code>standalone</code> traces exactly the files the server needs and emits a{' '}
          <code>server.js</code> that runs on its own.
        </p>
      </div>

      <CodeBlock
        lang="text"
        filename="next.config.mjs"
        code={`/** @type {import('next').NextConfig} */
export default {
  output: 'standalone',
};`}
      />

      <div className="prose">
        <p>
          The build writes to <code>.next/standalone/</code>, and Next deliberately does{' '}
          <em>not</em> copy <code>public/</code> or <code>.next/static/</code> into it — it assumes
          something in front will serve those. That is precisely the arrangement here.
        </p>

        <H2 id="build">A build script, because pipelines are refused</H2>
        <p>
          Assembling a standalone build takes more than one command, and{' '}
          <code>--build-command</code> is parsed into an argv slice with <code>&amp;&amp;</code>,{' '}
          <code>|</code> and <code>;</code> refused. So the multi-step part goes in a script in the
          repository, which is where it belongs anyway — it is versioned with the code that needs it.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        filename="bin/build (chmod +x, committed to the repo)"
        code={`#!/usr/bin/env bash
set -euo pipefail

npm run build

# Next does not copy these into standalone/ on purpose; something in front serves them.
cp -r public .next/standalone/public
mkdir -p .next/standalone/.next
cp -r .next/static .next/standalone/.next/static`}
      />

      <Callout tone="danger" title="Why the pipeline is refused rather than passed to a shell">
        <p>
          There is no shell in the binary registry at all, so a command containing{' '}
          <code>&amp;&amp;</code> would either be passed as a literal argument (confusing) or, if it were
          ever handed to a shell, become command injection (dangerous). The error names the operator, its
          meaning and its position:{' '}
          <code>command contains "&amp;&amp;" (command chaining) at position 15, which needs a shell</code>.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="create">Create the site</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline runtime install node 22
ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub

ratline site add app.example.com \\
  --user acme \\
  --runtime node \\
  --repo https://github.com/acme/web.git \\
  --branch main \\
  --node 22 \\
  --package-manager npm \\
  --install-command "npm ci" \\
  --build-command "./bin/build" \\
  --entry .next/standalone/server.js \\
  --public public \\
  --listen port \\
  --dry-run`}
      />

      <div className="prose">
        <p>
          Two things worth noticing. <code>--install-command "npm ci"</code> rather than{' '}
          <code>npm ci --omit=dev</code>: a Next build needs the dev dependencies, so pruning them before
          the build breaks it. And <code>--entry</code> takes a path with a directory part — that is
          allowed, and the directory is validated as a subdirectory, so <code>../../etc/main.js</code>{' '}
          would be refused.
        </p>
      </div>

      <Callout tone="warn" title="Next builds are memory-hungry">
        <p>
          <code>defaults.memory_max</code> is <code>512M</code>. A Next build with source maps on a
          medium-sized app will exceed that and get OOM-killed, which surfaces as a build that fails with
          no useful message. Raise the ceiling before you debug anything else:
        </p>
      </Callout>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site scale app.example.com --memory-max 2G
# or, for every site on the box, set defaults.memory_max in /etc/ratline/config.yaml`}
      />

      <div className="prose">
        <H2 id="execstart">What the unit actually runs</H2>
      </div>

      <CodeBlock
        lang="systemd"
        filename="/etc/systemd/system/ratline-acme-app_example_com.service (the relevant lines)"
        code={`User=acme
Group=acme
WorkingDirectory=/home/acme/app.example.com/app
EnvironmentFile=/home/acme/app.example.com/.env
RuntimeDirectory=ratline/acme-app_example_com

ExecStart=/opt/ratline/runtimes/node/22/bin/node .next/standalone/server.js`}
      />

      <div className="prose">
        <p>
          Absolute path to the managed binary. nvm, <code>.nvmrc</code>, shell profiles and login shells
          are never involved — so a tenant editing their <code>.bashrc</code> cannot break their own
          service, and two sites can sit on Node 20 and Node 22 without arguing.
        </p>

        <H3 id="socket">Socket or port</H3>
        <p>
          Next’s own standalone server reads <code>HOSTNAME</code> and <code>PORT</code> and binds a TCP
          listener. It does not bind a Unix socket, so for an unmodified{' '}
          <code>.next/standalone/server.js</code> the honest choice is <code>--listen port</code>:
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site add app.example.com --user acme --runtime node \\
  --entry .next/standalone/server.js --node 22 --listen port

ratline site env set app.example.com NODE_ENV=production HOSTNAME=127.0.0.1`}
      />

      <div className="prose">
        <p>
          A port is allocated from <code>20000–29999</code> and nginx proxies to{' '}
          <code>127.0.0.1:&lt;port&gt;</code>. <code>PORT</code> is set by the unit from that allocation,
          so do not set it yourself — a value in <code>.env</code> would fight the one ratline assigned
          and produce a site nginx cannot reach.
        </p>
        <p>
          If you want the socket instead, write a few lines of your own entry point that creates the Next
          request handler and hands it to <code>server.listen(socketPath)</code>, then point{' '}
          <code>--entry</code> at that file. That is a change to your application, not a ratline setting,
          which is why this guide does not pretend it is a flag.
        </p>
      </div>

      <Callout tone="warn" title="A port is weaker than a socket, and worth knowing about">
        <p>
          There is a port on <code>localhost</code> that any account on the box can connect to, whereas
          the socket’s <code>0660 &lt;user&gt;:www-data</code> mode <em>is</em> the access control. It is
          the right trade here — a wrapper you have to maintain is worse — but it is a trade.{' '}
          <code>ratline doctor</code> reports ports allocated but unused, so the range does not fill with
          leftovers.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="static">The static bypass</H2>
        <p>
          <code>--public public</code> makes nginx serve that directory directly. Requests for{' '}
          <code>/_next/static/*</code> and everything in <code>public/</code> never reach Node, which
          matters more than it sounds for a Next app: a page with forty hashed asset requests would
          otherwise occupy the event loop forty times over.
        </p>
        <p>
          <code>_next</code> is why the subdirectory validator permits a leading underscore.{' '}
          <code>_next</code> and <code>_assets</code> are real build-output directories; a leading{' '}
          <em>dot</em> is still refused, because nginx is configured to deny dotfiles.
        </p>

        <H2 id="deploy">Deploying a change</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site deploy app.example.com --pull --install --build --restart`}
      />

      <Terminal title="root@server">{`$ ratline site deploy app.example.com --pull --install --build --restart
→ git fetch && checkout main (as acme)
→ npm ci (as acme)
→ ./bin/build (as acme)
→ restarting ratline-acme-app_example_com.service
→ waiting for health on /run/ratline/acme-app_example_com/app.sock
✗ health check failed after 30s; the last log line was
  "Error: Cannot find module '/home/acme/app.example.com/app/.next/standalone/server.js'"
→ reverting to the previous release
→ previous release is serving again, healthy in 1.2s
  hint: the build did not produce the entry point. Run the build alone and check
        that output: 'standalone' is set in next.config.mjs.
~ exit 7 — and the site is still up, on the previous release`}</Terminal>

      <div className="prose">
        <p>
          That is the property worth paying for: the previous release stays addressable for the duration
          of the deploy, and a failed health check reverts to it. A failed deploy is an inconvenience
          rather than an outage.
        </p>

        <H2 id="instances">More than one process</H2>
        <p>
          Node is single-threaded, so the way to use more cores is more processes.{' '}
          <code>--instances 2</code> sets PM2’s cluster worker count: two workers sharing the one
          listening socket, inside the one unit and under the one memory ceiling. Not two units, and no
          nginx upstream pool — node’s <code>cluster</code> module shares the listening handle, which is
          also what lets <code>pm2 reload</code> cut over one worker at a time.
        </p>
        <p>
          Which means the ceiling is a <em>total</em>. Two workers each holding 400 MB exceed a 512 MB{' '}
          <code>MemoryMax</code>, because the limit covers the whole cgroup rather than each process —
          so raise the memory when you raise the worker count.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site scale app.example.com --instances 2 --cpu-quota 200% --memory-max 1G
ratline site status app.example.com`}
      />

      <Callout tone="warn" title="Two instances means two of everything">
        <p>
          Two Node heaps, two sets of in-memory caches, and no shared state between them. Anything you
          were keeping in a module-level variable — a session store, a rate-limit counter, a warmed cache
          — now exists twice and disagrees with itself. If the app is not already stateless, fix that
          before scaling out.
        </p>
      </Callout>

      <div className="prose">
        <p>
          See also: <Link to="/guides/astro">an Astro static build</Link> — if the site does not need a
          server at runtime, the <code>static</code> runtime is simpler in every respect.
        </p>
      </div>
    </article>
  );
}
