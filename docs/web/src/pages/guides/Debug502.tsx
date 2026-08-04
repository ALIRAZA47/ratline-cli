import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { RequestPath } from '../../components/diagrams/RequestPath';
import { Callout, H2 } from '../../components/ui';

export function GuideDebug502() {
  return (
    <article>
      <PageHeader
        eyebrow="Runbook"
        title="Debugging a 502"
        lede="A 502 means nginx could not get a usable answer from the application. There are six places that can go wrong, and they are worth checking in this order."
      />

      <div className="prose">
        <p>
          Only <code>node</code> and <code>python</code> sites can 502 — a <code>static</code> site has
          nothing to proxy to. And a deploy that <em>would</em> have returned 502 fails with exit{' '}
          <Link to="/reference/exit-codes#code-7">7</Link> instead of succeeding, so if you are seeing a 502
          in production it appeared <em>after</em> a successful deploy: a crash, a resource limit, or
          something changed by hand.
        </p>
      </div>

      <RequestPath />

      <div className="prose">
        <H2 id="triage">0 · Two commands first</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site show api.example.com
ratline site logs api.example.com --error --lines 50`}
      />

      <div className="prose">
        <p>
          The nginx error log names the failure mode explicitly, and it is worth learning to read the three
          shapes:
        </p>
        <ul>
          <li>
            <code>connect() to unix:/run/… failed (2: No such file or directory)</code> → the socket does not
            exist. The app is not running. Cause 1.
          </li>
          <li>
            <code>connect() to unix:/run/… failed (13: Permission denied)</code> → the socket exists and nginx
            cannot open it. Cause 3.
          </li>
          <li>
            <code>upstream prematurely closed connection</code> or{' '}
            <code>upstream timed out (110: Connection timed out)</code> → the app accepted the connection and
            then died or took too long. Causes 4, 5 or 6.
          </li>
        </ul>
      </div>

      <div className="not-prose my-7 space-y-4">
        {[
          {
            n: 1,
            t: 'The application is not running',
            b: 'Overwhelmingly the most common. The unit crashed, was stopped by hand, or never came back after a reboot. Restart=always retries with RestartSec (3s) — so a unit that is still down has failed repeatedly, and the journal has the reason.',
            cmd: `ratline site status api.example.com
ratline site logs api.example.com --app --lines 50
systemctl status ratline-acme-api_example_com.service
journalctl -u ratline-acme-api_example_com.service -n 50 --no-pager

# Then, once you know why:
ratline site restart api.example.com`,
            note: 'ratline surfaces the last 20 lines of the journal automatically on a failed start, so you often have the answer before you run any of this.',
          },
          {
            n: 2,
            t: 'The app is running but not listening on the socket nginx expects',
            b: 'The unit is active, nginx says "No such file or directory". The app bound something else — a TCP port because PORT was not set, or a socket path it chose itself.',
            cmd: `ls -la /run/ratline/acme-api_example_com/
ss -lxp | grep ratline
ratline site env list api.example.com

# The socket path the unit provisions:
#   /run/ratline/<slug>/app.sock
# Point the app at exactly that.`,
            note: '/run is a tmpfs. The directory is created by systemd’s RuntimeDirectory= with the right owner and mode, and removed when the unit stops — a directory made by hand would not survive a reboot.',
          },
          {
            n: 3,
            t: 'nginx cannot open the socket',
            b: 'The socket exists and nginx gets EACCES. The mode should be 0660 <user>:www-data. Either the mode is wrong, or www-data is not in the right group, or nginx has not restarted since the group changed.',
            cmd: `stat -c '%a %U:%G %n' /run/ratline/acme-api_example_com/app.sock
# want: 660 acme:www-data

getent group acme        # www-data should be a member
id www-data              # and the running nginx must know it

# Supplementary groups are resolved at process start, so a group added
# while nginx was running is invisible to the workers:
systemctl reload nginx || systemctl restart nginx`,
            note: 'That last point catches people out: adding www-data to a group by hand needs a reload — and sometimes a restart — before the workers see it.',
          },
          {
            n: 4,
            t: 'The app starts, accepts a connection, then dies',
            b: 'nginx logs "upstream prematurely closed connection". Usually a missing environment variable, a database it cannot reach, or an import that fails only on the first real request.',
            cmd: `ratline site logs api.example.com --app --follow
# and in another terminal, make one request:
curl -sSi https://api.example.com/ -o /dev/null

ratline site env list api.example.com
ratline site env list api.example.com --reveal   # if you suspect a value, not a name`,
            note: 'Values are masked by default. A variable that is present but empty looks identical to one that is set, until you --reveal it.',
          },
          {
            n: 5,
            t: 'It is being killed by a resource limit',
            b: 'The app works, then stops under load. MemoryMax is 512M by default and it is per-process — four Gunicorn workers each holding 200 MB exceeds it. MemoryHigh at 87.5% means the kernel reclaims before it kills, so you may see slowness first.',
            cmd: `systemctl show ratline-acme-api_example_com.service \\
  -p MemoryMax -p MemoryHigh -p MemoryCurrent -p TasksMax -p CPUQuotaPerSecUSec
journalctl -u ratline-acme-api_example_com.service | grep -i -e oom -e killed

# Raise the ceiling, or use fewer workers:
ratline site scale api.example.com --memory-max 1G
ratline site scale api.example.com --workers 2`,
            note: 'cgroup limits are advisory unless the controllers are enabled and delegated. On a stock Ubuntu with cgroup v2 they are enforced; on an unusual host, check before you trust the number.',
          },
          {
            n: 6,
            t: 'A systemd hardening directive is blocking something',
            b: 'The app works when run by hand as the site user and fails under the unit. ProtectSystem=strict, ProtectHome=tmpfs, ProtectKernelTunables and SystemCallFilter are all candidates. ratline reports which directive rather than dropping hardening silently.',
            cmd: `journalctl -u ratline-acme-api_example_com.service | grep -i -e denied -e 'read-only' -e ENOSYS

# ProtectHome=tmpfs replaces every home with an empty tmpfs and binds
# only this site's directory back in, so a path under another home is ENOENT.
systemd-analyze security ratline-acme-api_example_com.service`,
            note: 'The fix is a bind path or --relax <directive>, explicitly. Not turning the hardening off wholesale.',
          },
        ].map((row) => (
          <section key={row.n} className="rounded-[var(--radius-card)] border border-line bg-raised">
            <div className="flex gap-3 border-b border-line px-4 py-3">
              <span className="mt-0.5 inline-flex size-6 shrink-0 items-center justify-center rounded-full bg-accent-soft font-mono text-xs font-semibold text-accent">
                {row.n}
              </span>
              <div>
                <h3 className="font-medium text-strong">{row.t}</h3>
                <p className="mt-1 max-w-[var(--container-measure)] text-sm leading-relaxed text-muted">
                  {row.b}
                </p>
              </div>
            </div>
            <div className="px-4 py-2">
              <CodeBlock code={row.cmd} lang="shell" />
              {row.note && (
                <p className="mb-3 max-w-[var(--container-measure)] border-l-2 border-line-strong pl-3 text-xs leading-relaxed text-muted">
                  {row.note}
                </p>
              )}
            </div>
          </section>
        ))}
      </div>

      <div className="prose">
        <H2 id="not-502">Related, but not a 502</H2>
        <ul>
          <li>
            <strong>504 Gateway Timeout</strong> — the app answered too slowly.{' '}
            <code>defaults.proxy_read_timeout</code> is 60s. For a genuinely long endpoint, raise it for that
            location only, in{' '}
            <code>/etc/nginx/ratline/custom/&lt;domain&gt;.conf</code>, rather than globally.
          </li>
          <li>
            <strong>413 Request Entity Too Large</strong> — <code>defaults.client_max_body_size</code> is 20M,
            and the configuration file calls this out as the most common cause of a mystery 413.
          </li>
          <li>
            <strong>403 Forbidden on a static file</strong> — a permission problem, not a proxy problem.{' '}
            <code>namei -l</code> the whole path; nginx needs to traverse every directory, which it does via
            the user’s group.
          </li>
          <li>
            <strong>404 for a client-side route</strong> — the SPA fallback. Add <code>--spa</code> so nginx
            renders <code>try_files $uri $uri/ /index.html</code>.
          </li>
          <li>
            <strong>503</strong> — probably deliberate. <code>ratline user disable</code> stops a tenant’s
            services and serves 503 rather than a connection refused or a stale page. Check{' '}
            <code>ratline user show</code>.
          </li>
        </ul>

        <H2 id="reproduce">Reproducing it without the browser</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# Straight at the socket, bypassing nginx entirely. If this works, the problem
# is between nginx and the socket; if it does not, the problem is the app.
curl -sS --unix-socket /run/ratline/acme-api_example_com/app.sock http://localhost/ -i

# Through nginx, bypassing DNS.
curl -sSi --resolve api.example.com:443:127.0.0.1 https://api.example.com/

# Watch both logs while you do it.
ratline site logs api.example.com --error --follow
ratline site logs api.example.com --app --follow`}
      />

      <Callout tone="ok" title="That first command splits the problem in half">
        <p>
          A working <code>curl --unix-socket</code> proves the application is healthy and narrows everything to
          nginx, permissions or the socket path — causes 2 and 3. A failing one narrows it to the application
          — causes 1, 4, 5 and 6. It is the single most useful command on this page.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="after-deploy">If it appeared right after a deploy</H2>
        <p>
          Then something is unusual, because <code>site deploy</code> health-checks with a real HTTP request
          and reverts to the previous release if it fails. A 502 that shows up minutes later is a crash under
          real traffic rather than a broken build — check cause 5 first, then cause 4.
        </p>
      </div>

      <Terminal title="root@server">{`$ ratline site deploy api.example.com --pull --install --restart
→ git fetch && checkout main (as acme)
→ pip install -r requirements.txt (as acme)
→ restarting ratline-acme-api_example_com.service
→ waiting for health on /run/ratline/acme-api_example_com/app.sock
→ healthy in 2.1s (HTTP 200)

~ Twenty minutes later, under real traffic:

$ ratline site logs api.example.com --error --lines 5
2026/08/04 15:02:11 [error] 918#918: *4471 upstream prematurely closed connection
  while reading response header from upstream, client: 203.0.113.19,
  upstream: "http://unix:/run/ratline/acme-api_example_com/app.sock:/reports"

$ journalctl -u ratline-acme-api_example_com.service | grep -i oom
Aug 04 15:02:11 server systemd[1]: ratline-acme-api_example_com.service:
  A process of this unit has been killed by the OOM killer.

$ ratline site scale api.example.com --memory-max 1G --workers 2
→ re-rendered the unit, systemd-analyze verify passed
→ restarted; healthy in 1.8s`}</Terminal>

      <div className="prose">
        <H2 id="last-resort">When nothing above explains it</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline doctor                     # drift, failed units, dead sockets, permission anomalies
ratline reconcile                  # what does the rendered config differ from state?
ratline reconcile --fix --dry-run
nginx -t
ratline version                    # everything a bug report needs, in one paste`}
      />

      <div className="prose">
        <p>
          <code>doctor</code> is read-only and catches the two things that are hardest to spot by hand:
          state-vs-filesystem drift, which is the vhost or unit someone edited, and permission anomalies —
          the home that became 0755, the <code>.env</code> that became 0644, the socket whose group changed.
        </p>
        <p>
          See also: <Link to="/concepts/supervision">process supervision</Link>,{' '}
          <Link to="/concepts/filesystem">the permission layout</Link>,{' '}
          <Link to="/reference/exit-codes#code-7">exit code 7</Link>.
        </p>
      </div>
    </article>
  );
}
