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
  { d: 'UMask=0027', why: 'Anything the app writes lands at 0640/0750 rather than world-readable.' },
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
          unit is <code>ratline-&lt;slug&gt;.service</code>, and with{' '}
          <code>--instances &gt; 1</code> it becomes a template unit —{' '}
          <code>ratline-&lt;slug&gt;@1.service</code> — with an nginx upstream pool across the instance
          sockets.
        </p>
        <p>
          A <code>ratline.target</code> lets an operator{' '}
          <code>systemctl stop ratline.target</code> to stop every managed site at once, which is what
          you want before a kernel upgrade or a disk operation.
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
          The command surface states the mechanism but not which verb carries the flag, so this page
          does not claim one.
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
        filename="/etc/systemd/system/ratline-acme-app_example_com.service (shape)"
        code={`[Unit]
Description=ratline site app.example.com (acme)
After=network-online.target
PartOf=ratline.target

[Service]
Type=simple
User=acme
Group=acme
WorkingDirectory=/home/acme/app.example.com/app
EnvironmentFile=/home/acme/app.example.com/.env
RuntimeDirectory=ratline/acme-app_example_com
RuntimeDirectoryMode=0750
UMask=0027

ExecStart=/opt/ratline/runtimes/node/22/bin/node server.js

Restart=always
RestartSec=3s
TimeoutStopSec=30s

MemoryMax=512M
MemoryHigh=448M
CPUQuota=100%
TasksMax=256
LimitNOFILE=8192

NoNewPrivileges=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectSystem=strict
ProtectHome=tmpfs
BindPaths=/home/acme/app.example.com
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictNamespaces=yes
RestrictSUIDSGID=yes
LockPersonality=yes
SystemCallFilter=@system-service

[Install]
WantedBy=multi-user.target`}
      />

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

        <H2 id="scaling">Workers and instances are not the same thing</H2>
        <ul>
          <li>
            <strong><code>--workers</code></strong> is Gunicorn’s worker count, inside one unit. Default{' '}
            <code>(2 × cores) + 1</code>, capped at <code>defaults.worker_cap</code> (8).
          </li>
          <li>
            <strong><code>--instances</code></strong> is a count of <em>units</em>, via a systemd
            template and an nginx upstream pool. Used for Node, where the process is single-threaded and
            the way to use more cores is more processes.
          </li>
        </ul>
        <p>
          Both are changed with <code>ratline site scale</code>, which re-renders the unit, verifies it
          and reloads — rather than being edited by hand, which <code>doctor</code> would then report as
          drift.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site scale api.example.com --workers 4 --memory-max 1G
ratline site scale app.example.com --instances 2 --cpu-quota 150%
ratline site reload api.example.com   # gunicorn replaces workers without dropping the socket`}
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
