import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, H2, H3, TableScroll } from '../../components/ui';

const directives = [
  { d: 'User / Group', why: 'The site owner. This is the boundary that actually matters.' },
  { d: 'WorkingDirectory', why: 'The application directory, so relative paths in the app resolve.' },
  { d: 'EnvironmentFile', why: 'Reads .env as root, before privileges are dropped — which is why the file can be 0600.' },
  { d: 'RuntimeDirectory', why: 'Creates /run/ratline/<slug>/ with the right owner and mode, and removes it on stop. /run is a tmpfs, so a hand-made directory would not survive a reboot.' },
  {
    d: 'UMask',
    why: '0027 normally, so anything the app writes lands at 0640/0750 rather than world-readable — but 0007 on a socket site, because connect(2) needs write permission on the socket inode and at 0027 the socket lands 0640 and nginx gets EACCES.',
  },
  { d: 'Restart=always', why: 'With RestartSec from defaults.restart_sec (3s).' },
  { d: 'MemoryMax / MemoryHigh', why: 'MemoryHigh is 0.875 of MemoryMax, so the kernel starts reclaiming before it starts killing.' },
  { d: 'CPUQuota / TasksMax / LimitNOFILE', why: 'Defaults 100%, 256 and 8192. Over 100% CPUQuota means more than one core.' },
  { d: 'NoNewPrivileges', why: 'No setuid escalation from inside the service, ever.' },
  { d: 'PrivateTmp', why: 'A private /tmp, so two tenants cannot collide or snoop on temp files.' },
  { d: 'PrivateDevices', why: 'No raw device access.' },
  { d: 'ProtectSystem=strict', why: 'The whole filesystem read-only except what is explicitly allowed.' },
  {
    d: 'ProtectHome=tmpfs + BindPaths',
    why: 'Every home is replaced by an empty tmpfs, and only this site’s directory is bound back in. A compromised app cannot even see that other tenants exist.',
  },
  { d: 'ProtectKernelTunables / ProtectKernelModules / ProtectControlGroups', why: 'No /proc/sys writes, no module loading, no cgroup edits.' },
  { d: 'RestrictNamespaces', why: 'No namespace creation, which closes a common container-escape-shaped hole.' },
  { d: 'RestrictSUIDSGID', why: 'The service cannot create setuid files.' },
  { d: 'LockPersonality', why: 'No personality(2) changes.' },
  { d: 'SystemCallFilter=@system-service', why: 'A seccomp allowlist for the syscalls a service legitimately needs.' },
  { d: 'SystemCallArchitectures=native', why: 'No 32-bit syscall entry point, which is a way around a filter aimed at the native ABI.' },
  { d: 'RestrictAddressFamilies', why: 'AF_UNIX, AF_INET and AF_INET6 only. No packet sockets, no netlink.' },
  {
    d: 'MemoryDenyWriteExecute',
    why: 'Refuses writable-executable pages. On by default, and relaxed for every node site because V8’s JIT requires them — the generated unit records that in a comment rather than leaving the next reader to wonder.',
  },
  { d: 'ReadWritePaths', why: 'The exceptions to ProtectSystem=strict: the site’s logs and tmp, plus PM2’s own directory on a PM2 site.' },
];

export function ConceptSupervision() {
  return (
    <article>
      <PageHeader
        eyebrow="Concepts"
        title="Process supervision"
        lede="One systemd unit per dynamic site, with hardening verified at install time. If a directive breaks the application, ratline reports which one — it does not silently drop it."
      />

      <div className="prose">
        <p>
          A <code>static</code> site has no unit. For <code>node</code> and <code>python</code> sites the
          unit is <code>ratline-&lt;slug&gt;.service</code>, and there is exactly one of them per site
          binding exactly one socket. Concurrency lives <em>inside</em> the unit — PM2 cluster workers or
          gunicorn workers, sharing that one listening handle — so there is never an nginx upstream pool
          to balance across.
        </p>
        <p>
          A <code>ratline.target</code> lets an operator{' '}
          <code>systemctl stop ratline.target</code> to stop every managed site at once, which is what
          you want before a kernel upgrade or a disk operation.
        </p>

        <H2 id="type">Type=exec, and Type=forking under PM2</H2>
        <p>
          Neither gunicorn nor a plain node server implements <code>sd_notify</code>, so{' '}
          <code>Type=notify</code> would hang until the start timeout and then be reported as a failure.{' '}
          <code>Type=exec</code> it is — and ratline proves the service is genuinely up by making an HTTP
          request through its socket afterwards, which is a stronger check than a readiness ping anyway.
        </p>
        <p>
          A node site supervised by PM2 is <code>Type=forking</code> instead, because PM2 daemonises. It
          carries a <code>PIDFile</code> so systemd follows the right process after the fork, and an{' '}
          <code>ExecStop</code> that runs <code>pm2 kill</code> so the daemon does not outlive the unit.
          The cgroup still contains every worker, which is what keeps <code>MemoryMax</code> enforceable
          across the extra layer — <Link to="/guides/node">the node guide</Link> has that trade in full.
        </p>
      </div>

      <div className="prose">
        <H2 id="directives">The hardening directives</H2>
      </div>

      <TableScroll>
        <table className="w-full min-w-[44rem] border-collapse text-left text-sm">
          <caption className="sr-only">systemd directives and their purpose</caption>
          <thead>
            <tr className="bg-sunken text-2xs uppercase tracking-wider text-muted">
              <th scope="col" className="w-[19rem] px-3 py-2 font-medium">
                Directive
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                Why
              </th>
            </tr>
          </thead>
          <tbody>
            {directives.map((row) => (
              <tr key={row.d} className="border-t border-line align-top">
                <th scope="row" className="px-3 py-2.5 text-left font-normal">
                  <code className="font-mono text-xs text-accent">{row.d}</code>
                </th>
                <td className="px-3 py-2.5 leading-relaxed">{row.why}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableScroll>

      <Callout tone="note" title="ProtectHome=tmpfs is the one that surprises people">
        <p>
          It replaces <em>every</em> home directory with an empty tmpfs inside the service’s mount
          namespace, and then <code>BindPaths</code> mounts this site’s directory back in. So the
          application sees its own files and an otherwise empty <code>/home</code>. If your app reads
          something from another path under <code>/home</code> — a shared cache, a sibling checkout — it
          will get ENOENT, and the fix is a bind path rather than turning the directive off.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="relax">When a directive breaks the app</H2>
        <p>
          The tempting behaviour is to notice a service failing and quietly drop the hardening until it
          starts. That produces a fleet where nobody knows what is protected. Instead, ratline reports{' '}
          <strong>which</strong> directive to relax and offers <code>--relax &lt;directive&gt;</code>,
          so the decision is explicit and recorded.
        </p>
        <p>
          The flag is on <code>site add</code> at creation and on <code>site runtime</code> afterwards,
          and the generated unit records which directives are off in a comment — so the next person to
          read it knows without having to diff against a default.
        </p>
      </div>

      <Terminal title="root@server">{`$ ratline site start api.example.com
✗ site start failed: the app did not become healthy within 30s.
  ratline-acme-api_example_com.service exited 1; the last log line was
  "PermissionError: [Errno 13] Permission denied: '/proc/sys/vm/overcommit_memory'".
  That path is blocked by ProtectKernelTunables=yes.
  hint: this application writes a kernel tunable. If that is genuinely required,
        re-run with --relax ProtectKernelTunables. Otherwise, configure the setting
        on the host instead — it is a system-wide knob and a per-site service is the
        wrong place to set it.
~ exit 7. The hardening is intact and the operator makes the call.`}</Terminal>

      <div className="prose">
        <H2 id="health">Health is a real request</H2>
        <p>
          After <code>start</code>, <code>restart</code> or <code>deploy</code>, ratline polls the
          socket or port with a real HTTP request until it answers or{' '}
          <code>defaults.health_timeout</code> (30s) elapses.
        </p>
        <p>
          This is not a process check. A process that started and then failed to bind, or bound and then
          returned 500 to everything, passes a process check and fails a request. A “successful” deploy
          that returns 502 is a bug, which is why{' '}
          <Link to="/reference/exit-codes#code-7">exit 7</Link> exists as its own code rather than being
          folded into 4.
        </p>
        <p>
          It matters more under PM2, not less. PM2 will report a worker as started and then restart it
          ten times without systemd’s counter moving, so the request is the only thing that distinguishes
          “running” from “working”.
        </p>

        <H3>And the logs come to you</H3>
        <p>
          On any failed start, the last 20 lines of{' '}
          <code>journalctl -u &lt;unit&gt;</code> are surfaced automatically. The operator never has to
          go and find them, which matters because the interesting line is almost always the last one
          before the exit.
        </p>

        <H2 id="execstart">ExecStart uses absolute paths, always</H2>
      </div>

      <CodeBlock
        lang="systemd"
        filename="/etc/systemd/system/ratline-acme-app_example_com.service"
        tag="real output"
        code={`# managed-by: ratline
# site: app.example.com
# generated: 2026-08-04T17:23:27Z
[Unit]
Description=ratline site app.example.com (node) owned by acme
Documentation=man:ratline(8)
After=network-online.target
Wants=network-online.target
PartOf=ratline.target

[Service]
Type=forking
PIDFile=/home/acme/app.example.com/.pm2/pm2.pid
User=acme
Group=acme
WorkingDirectory=/home/acme/app.example.com/app
EnvironmentFile=-/home/acme/app.example.com/.env
Environment=PM2_HOME=/home/acme/app.example.com/.pm2
Environment=NODE_ENV=production
Environment=PM2_DISCRETE_MODE=true
Environment=TMPDIR=/home/acme/app.example.com/tmp
RuntimeDirectory=ratline/acme-app_example_com
RuntimeDirectoryMode=0750
UMask=0007
ExecStart=/opt/ratline/runtimes/node/22/bin/pm2 start /home/acme/app.example.com/.ratline/ecosystem.config.json
ExecStartPost=+/bin/sh -c 'for i in $(seq 1 100); do if [ -S /run/ratline/acme-app_example_com/app.sock ]; then chmod 0660 /run/ratline/acme-app_example_com/app.sock; exit 0; fi; sleep 0.1; done; exit 0'
ExecReload=/opt/ratline/runtimes/node/22/bin/pm2 reload /home/acme/app.example.com/.ratline/ecosystem.config.json --update-env
ExecStop=/opt/ratline/runtimes/node/22/bin/pm2 kill
Restart=always
RestartSec=3s
StartLimitBurst=5
StartLimitIntervalSec=60
KillSignal=SIGTERM
KillMode=mixed
TimeoutStopSec=30s
SyslogIdentifier=ratline-acme-app_example_com

MemoryMax=512M
MemoryHigh=448M
MemoryAccounting=true
CPUQuota=100%
CPUAccounting=true
TasksMax=256
LimitNOFILE=8192
OOMPolicy=continue

NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=tmpfs
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectKernelLogs=true
ProtectControlGroups=true
ProtectClock=true
ProtectHostname=true
RestrictNamespaces=true
RestrictSUIDSGID=true
RestrictRealtime=true
LockPersonality=true
# MemoryDenyWriteExecute=true — relaxed for this site
SystemCallFilter=@system-service
SystemCallArchitectures=native
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
BindPaths=/home/acme/app.example.com
ReadWritePaths=/home/acme/app.example.com/logs /home/acme/app.example.com/tmp
ReadWritePaths=/home/acme/app.example.com/.pm2

#
# Relaxed for this site: MemoryDenyWriteExecute

[Install]
WantedBy=multi-user.target`}
      />

      <div className="prose">
        <p>
          That is a real render, with the explanatory comments ratline writes into the file stripped for
          length. Three lines are worth pointing at.
        </p>
        <p>
          <code>UMask=0007</code> rather than the <code>0027</code> used everywhere else, because{' '}
          <code>connect(2)</code> on a Unix socket needs <em>write</em> permission on the socket inode. At{' '}
          <code>0027</code> the socket lands <code>0640</code>, nginx gets <code>EACCES</code>, and every
          request is a 502 with an empty application log —{' '}
          <Link to="/guides/debug-502">cause 3</Link>. Files the application creates are group-writable as
          a result, but the group is the tenant’s own.
        </p>
        <p>
          <code>ExecStartPost</code> runs as root — that is what the <code>+</code> means — waits for the
          socket to appear, and chmods it. Belt and braces for a framework that chmods the socket itself
          after binding, and it always exits 0 so it can never be the reason a start fails.
        </p>
        <p>
          <code>EnvironmentFile=-</code> with the dash: a site with no <code>.env</code> yet must still
          start. Without it, a missing file is a start failure.
        </p>
      </div>

      <div className="prose">
        <p>
          <code>ExecStart</code> invokes the managed runtime by absolute path. nvm, pyenv, shell
          profiles and login shells are never involved, which has three consequences worth naming: a
          tenant editing <code>.bashrc</code> cannot break their own service; two sites can sit on
          different major versions without arguing; and there is no <code>PATH</code> lookup to
          hijack.
        </p>
        <p>
          <code>--start-command</code> is resolved to an argv slice. Anything needing a shell is
          refused — see <Link to="/reference/validation#command-strings">the command-string rules</Link>{' '}
          for the exact list and the reason.
        </p>

        <H2 id="scaling">Workers and instances are both inside the unit</H2>
        <p>
          Neither one adds a unit. A site is one unit binding one socket, and both flags set how many
          processes inside it share that socket:
        </p>
        <ul>
          <li>
            <strong><code>--workers</code></strong> is Gunicorn’s worker count. Default{' '}
            <code>(2 × cores) + 1</code>, capped at <code>defaults.worker_cap</code> (8). The master holds
            the socket and re-forks to the new count on <code>SIGHUP</code>, so a worker change is a
            reload and drops nothing.
          </li>
          <li>
            <strong><code>--instances</code></strong> is PM2’s cluster worker count on a node site. Node’s
            <code> cluster</code> module shares one listening handle across the workers, which is what
            makes <code>pm2 reload</code> able to cut over a worker at a time.
          </li>
        </ul>
        <p>
          So <code>--instances</code> is refused where nothing can act on it: a node site running{' '}
          <code>--daemon direct</code> is a single process, and a python site scales with{' '}
          <code>--workers</code>. Each refusal names the flag that does work, rather than accepting the
          value and quietly ignoring it.
        </p>
        <p>
          Both are changed with <code>ratline site scale</code>, which re-renders the unit, verifies it
          and restarts or reloads — rather than being edited by hand, which <code>doctor</code> would then
          report as drift.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site scale api.example.com --workers 4          # gunicorn: a reload, nothing dropped
ratline site scale api.example.com --memory-max 1G     # the cgroup changes, so this restarts
ratline site scale app.example.com --instances 4       # pm2 cluster workers

ratline site reload api.example.com   # gunicorn SIGHUP: workers cycle, the socket stays open
ratline site reload app.example.com   # pm2 reload: a replacement worker, then the old one retires`}
      />

      <Callout tone="warn" title="cgroup limits are advisory unless configured">
        <p>
          <code>MemoryMax</code> and <code>CPUQuota</code> need the relevant cgroup controllers to be
          enabled and delegated. On a stock Ubuntu with cgroup v2 they are, but on an unusual host or
          inside somebody else’s container they may not be — and a limit that is not enforced is a limit
          you are relying on for nothing. This is stated in the{' '}
          <Link to="/concepts/security">security model</Link> as one of the honest limits of the
          isolation.
        </p>
      </Callout>
    </article>
  );
}
