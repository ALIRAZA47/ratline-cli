import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, H2, H3, TableScroll } from '../../components/ui';

const survives = [
  {
    property: 'The resource ceiling is still kernel-enforced',
    how: 'systemd owns the cgroup and a cgroup contains every descendant, so MemoryMax, CPUQuota and TasksMax cover PM2 and all of its workers. PM2’s own max_memory_restart is deliberately not set — it would fire first and mask the limit that actually holds.',
  },
  {
    property: 'There is no shared daemon',
    how: 'PM2_HOME is inside the site directory, so each site has its own daemon, its own socket and its own process list. Nothing outlives the site it was supervising, and nothing leaks between tenants.',
  },
  {
    property: 'Nothing is orphaned on stop',
    how: 'ExecStop runs pm2 kill, which stops the daemon as well as the workers. systemctl stop leaves no process behind.',
  },
  {
    property: 'The configuration is data, not code',
    how: 'ratline generates ecosystem.config.json, not the more common ecosystem.config.js. A JavaScript config is a program PM2 evaluates as the tenant, and a settings file has no business being executable.',
  },
];

export function GuideNodePm2() {
  return (
    <article>
      <PageHeader
        eyebrow="Guide"
        title="Node sites and PM2"
        lede="PM2 in cluster mode supervises a node site by default, because it is the only way the site can reload without dropping a request. Here is the trade that buys, what it costs, and how to turn it off."
      />

      <div className="prose">
        <p>
          A node site is one systemd unit running as the tenant behind a Unix socket. Between systemd
          and your server sits PM2 in cluster mode.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline runtime install node 22 --with-pm2
ratline site add app.example.com --user acme --runtime node --entry server.js --node 22`}
      />

      <div className="prose">
        <H2 id="why">Why PM2 is the default</H2>
        <p>
          Because it is the only way <code>ratline site reload</code> can mean anything here.{' '}
          <code>pm2 reload</code> starts a replacement worker, waits for it to come up, and{' '}
          <em>only then</em> retires the old one — so a deploy drops no requests. There is no signal a
          plain node process handles that way.
        </p>
        <p>
          Without PM2, <code>reload</code> refuses rather than pretending. Claiming a graceful reload
          while dropping in-flight requests would be worse than saying no:
        </p>
      </div>

      <Terminal title="root@server">{`$ ratline site reload app.example.com
✗ a Node site running without PM2 cannot reload gracefully
  hint: switch it to PM2, which reloads with no dropped requests:
          ratline site runtime app.example.com --daemon pm2
        or accept a restart: ratline site restart app.example.com
        the trade-off is in: ratline explain node
~ exit 3. Nothing was restarted, so nothing was dropped either.`}</Terminal>

      <div className="prose">
        <H2 id="cost">What the extra layer costs</H2>
        <p>
          Stated plainly, because it is a real trade: systemd now supervises PM2, and PM2 supervises
          your application. Two layers where there was one. These are the properties that survive it,
          and <em>how</em> — a claim about isolation is worth nothing without the mechanism:
        </p>
      </div>

      <TableScroll>
        <table className="w-full min-w-[44rem] border-collapse text-left text-sm">
          <caption className="sr-only">What PM2 supervision does not cost, and why</caption>
          <thead>
            <tr className="bg-sunken text-2xs uppercase tracking-wider text-muted">
              <th scope="col" className="w-[17rem] px-3 py-2 font-medium">
                Still true
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                Because
              </th>
            </tr>
          </thead>
          <tbody>
            {survives.map((row) => (
              <tr key={row.property} className="border-t border-line align-top">
                <th scope="row" className="px-3 py-2.5 text-left font-medium text-strong">
                  {row.property}
                </th>
                <td className="px-3 py-2.5 leading-relaxed">{row.how}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableScroll>

      <Callout tone="warn" title="What genuinely changes: systemd stops counting restarts">
        <p>
          PM2 does the restarting, so systemd’s own <code>NRestarts</code> stays at{' '}
          <strong>zero</strong> on a PM2 site — even while the application crash-loops.{' '}
          <code>ratline site status</code>, <code>ratline status</code> and{' '}
          <code>ratline doctor</code> all read PM2’s counter instead and label it as PM2’s. Reading{' '}
          <code>systemctl show -p NRestarts</code> directly on such a site will tell you everything is
          fine while it is not.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="logs">Where the logs went</H2>
        <p>
          PM2 captures its workers’ stdout into <code>logs/app.log</code>, so the journal holds PM2’s
          own messages and not your application’s. <code>ratline site logs</code> knows this and reads
          the file; <code>--journal</code> is there for questions about the <em>unit</em>, such as a
          failed start or an OOM kill.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site logs app.example.com              # the application, from logs/app.log
ratline site logs app.example.com --follow
ratline site logs app.example.com --journal    # the unit: failed starts, OOM kills
ratline site logs app.example.com --error      # nginx, not the app`}
      />

      <div className="prose">
        <H2 id="cluster">Cluster mode needs a JavaScript entry point</H2>
        <p>
          Cluster mode is node’s own <code>cluster</code> module, so it can only fan out a{' '}
          <code>.js</code>, <code>.mjs</code> or <code>.cjs</code> file. A <code>--start-command</code>{' '}
          that runs a package manager or a binary falls back to fork mode with{' '}
          <code>interpreter: none</code>, and ratline says so when it writes the configuration — in fork
          mode a reload is a restart.
        </p>
        <p>
          So prefer <code>--entry</code> pointing at the file that calls <code>listen()</code>. A
          package manager between systemd and your server also breaks signal delivery and restart
          counting, which is a second reason for the same advice.
        </p>

        <H2 id="instances">Instances are cluster workers, not units</H2>
        <p>
          <code>--instances</code> sets PM2’s cluster worker count. All the workers share the one
          listening socket, inside one cgroup and under one memory ceiling — that is what cluster mode
          is for.
        </p>
      </div>

      <CodeBlock lang="shell" prompt code={`ratline site scale app.example.com --instances 4`} />

      <Callout tone="note" title="Which is why it is refused where nothing can fan out">
        <p>
          A node site running <code>--daemon direct</code> is a single process, and a python site
          scales with gunicorn workers. Asking either for four instances is refused, naming the flag
          that does work, rather than being accepted and quietly ignored — which is how an operator
          comes to believe a site is running four workers when it is running one.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="socket">Telling your server where to listen</H2>
        <p>
          There is no standard for handing a socket path to a node server, so three variables are set
          to the same value and you read whichever your framework understands:
        </p>
      </div>

      <CodeBlock
        lang="text"
        code={`PORT=/run/ratline/acme-app_example_com/app.sock
RATLINE_SOCKET=/run/ratline/acme-app_example_com/app.sock
SOCKET_PATH=/run/ratline/acme-app_example_com/app.sock`}
      />

      <div className="prose">
        <p>
          <code>PORT</code> holding a path is deliberate: <code>server.listen(process.env.PORT)</code>{' '}
          accepts a path in that same argument, so most applications need no change at all. For{' '}
          <code>--listen port</code>, <code>PORT</code> is a number and <code>HOST</code> is{' '}
          <code>127.0.0.1</code>.
        </p>
      </div>

      <CodeBlock
        lang="js"
        filename="server.js"
        code={`import http from 'node:http';

// PORT is a socket path by default and a number with --listen port. Node's
// listen() takes either in the same argument, so this one line covers both.
http.createServer(app).listen(process.env.PORT, () => {
  // Only needed if you want pm2 reload to wait for readiness rather than
  // treating the process as ready as soon as it is listening.
  if (process.send) process.send('ready');
});`}
      />

      <div className="prose">
        <H3>wait_ready is off unless you opt in</H3>
        <p>
          PM2 can be told to hold a reload until the new worker sends{' '}
          <code>process.send('ready')</code>. ratline leaves that <em>off</em>, because with it on and
          an application that never signals, every reload would stall for the listen timeout and then
          be reported as a failure. A reload that waits for a signal nobody sends is worse than one
          that does not wait.
        </p>

        <H2 id="config">The generated configuration</H2>
        <p>
          Written to <code>&lt;site&gt;/.ratline/ecosystem.config.json</code> and regenerated whenever
          the site changes. You do not edit it; <code>ratline site scale</code> and{' '}
          <code>ratline site runtime</code> do.
        </p>
      </div>

      <CodeBlock
        lang="json"
        filename="/home/acme/app.example.com/.ratline/ecosystem.config.json"
        code={`{
  "apps": [
    {
      "name": "acme-app_example_com",
      "script": "/home/acme/app.example.com/app/server.js",
      "cwd": "/home/acme/app.example.com/app",
      "instances": 4,
      "exec_mode": "cluster",
      "env": {
        "NODE_ENV": "production",
        "PATH": "/opt/ratline/runtimes/node/22/bin:/usr/local/bin:/usr/bin:/bin",
        "PORT": "/run/ratline/acme-app_example_com/app.sock",
        "RATLINE_SOCKET": "/run/ratline/acme-app_example_com/app.sock",
        "SOCKET_PATH": "/run/ratline/acme-app_example_com/app.sock"
      },
      "out_file": "/home/acme/app.example.com/logs/app.log",
      "error_file": "/home/acme/app.example.com/logs/app.log",
      "merge_logs": true,
      "time": true,
      "wait_ready": false,
      "listen_timeout": 10000,
      "kill_timeout": 5000,
      "max_restarts": 10,
      "min_uptime": "5s",
      "restart_delay": 1000,
      "autorestart": true
    }
  ]
}`}
      />

      <div className="prose">
        <H2 id="unit">And the unit around it</H2>
        <p>
          A PM2 site’s unit differs from every other in three lines, all of which follow from PM2
          daemonising: <code>Type=forking</code>, a <code>PIDFile</code> so systemd follows the right
          process after the fork, and an <code>ExecStop</code> that kills the daemon. The full unit is
          on <Link to="/concepts/supervision">the supervision page</Link>.
        </p>
      </div>

      <CodeBlock
        lang="systemd"
        filename="/etc/systemd/system/ratline-acme-app_example_com.service (the PM2-specific lines)"
        code={`Type=forking
PIDFile=/home/acme/app.example.com/.pm2/pm2.pid
Environment=PM2_HOME=/home/acme/app.example.com/.pm2

ExecStart=/opt/ratline/runtimes/node/22/bin/pm2 start /home/acme/app.example.com/.ratline/ecosystem.config.json
ExecReload=/opt/ratline/runtimes/node/22/bin/pm2 reload /home/acme/app.example.com/.ratline/ecosystem.config.json --update-env
ExecStop=/opt/ratline/runtimes/node/22/bin/pm2 kill

ReadWritePaths=/home/acme/app.example.com/.pm2`}
      />

      <div className="prose">
        <p>
          <code>--update-env</code> on the reload is not optional: without it the replacement workers
          would inherit the old environment, which would make{' '}
          <code>ratline site env set</code> followed by <code>ratline site reload</code> a silent
          no-op.
        </p>

        <H2 id="off">Turning it off</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# At creation:
ratline site add app.example.com --user acme --runtime node --entry server.js --daemon direct

# On an existing site:
ratline site runtime app.example.com --daemon direct`}
      />

      <div className="prose">
        <p>
          <code>direct</code> runs node straight under systemd. One fewer moving part, systemd sees the
          application itself, and <code>reload</code> becomes a restart. It is the better choice for a
          single-process application that is never reloaded in place.
        </p>
        <p>Server-wide, for every node site created afterwards:</p>
      </div>

      <CodeBlock
        lang="yaml"
        filename="/etc/ratline/config.yaml"
        code={`runtimes:
  node_process_manager: direct`}
      />

      <Callout tone="ok" title="Switching stops the old supervisor first">
        <p>
          Only the PM2 unit carries <code>ExecStop=pm2 kill</code>. So{' '}
          <code>site runtime --daemon</code> stops the site using the unit that is{' '}
          <em>still on disk</em>, and re-renders afterwards. Re-rendering first would leave the PM2
          daemon and its workers alive until the kill timeout — still holding the socket the
          replacement is about to bind.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="install">Installing PM2</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline runtime install node 22 --with-pm2
ratline runtime install node 22 --pm2-version 5.4.2   # pinned`}
      />

      <div className="prose">
        <p>
          Per Node version, into the root-owned runtime prefix at{' '}
          <code>/opt/ratline/runtimes/node/&lt;version&gt;/bin/pm2</code>. Per version because a PM2
          resolved against Node 18 is not the one a Node 22 site should run, and because one shared
          install would mean <code>runtime default</code> silently changing the supervisor binary
          underneath every existing site. Root-owned because a supervisor binary a tenant could modify
          is a way to run arbitrary code from inside a service unit.
        </p>
        <p>
          A site whose Node version has no PM2 refuses to start, and the refusal offers both ways
          forward rather than leaving you stuck:
        </p>
      </div>

      <Terminal title="root@server">{`$ ratline site add app.example.com --user acme --runtime node --entry server.js
✗ PM2 is not installed for this Node version
  hint: install it: ratline runtime install node 22 --with-pm2
        or run this site without PM2: ratline site runtime app.example.com --daemon direct
~ exit 3. Nothing was created.`}</Terminal>

      <div className="prose">
        <H2 id="jit">One hardening directive is off, deliberately</H2>
        <p>
          <code>MemoryDenyWriteExecute</code> refuses writable-executable memory pages, which is a
          good default and incompatible with a JIT. V8 needs them, so it is relaxed for every node
          site — and the generated unit records the fact in a comment rather than leaving the next
          reader to wonder:
        </p>
      </div>

      <CodeBlock
        lang="systemd"
        code={`# MemoryDenyWriteExecute=true — relaxed for this site
...
#
# Relaxed for this site: MemoryDenyWriteExecute`}
      />

      <div className="prose">
        <p>
          See also: <Link to="/concepts/supervision">process supervision</Link>,{' '}
          <Link to="/guides/nextjs">Next.js standalone</Link>,{' '}
          <Link to="/guides/debug-502">debugging a 502</Link>,{' '}
          <Link to="/reference/site#site-runtime">
            <code>ratline site runtime</code>
          </Link>
          .
        </p>
      </div>
    </article>
  );
}
