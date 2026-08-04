import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, Facts, H2, H3 } from '../../components/ui';

const steps = [
  {
    n: 1,
    title: 'Validate all inputs',
    body: 'Before anything on the system is touched. The validators are pure functions with no system access, so a bad username costs nothing and changes nothing.',
  },
  {
    n: 2,
    title: 'Check preconditions',
    body: 'User exists, domain not already configured, port free, runtime installed, disk space available, entry point present. All of them, reported together.',
  },
  {
    n: 3,
    title: 'Write to temporary files, then rename',
    body: 'In the same directory as the target, so the rename is atomic and a config file is never half-written. A reader either sees the old file or the new one.',
  },
  {
    n: 4,
    title: 'Verify before activating',
    body: 'nginx -t before the reload; systemd-analyze verify before daemon-reload. On failure, restore the previous config and return non-zero with the raw error — not a summary of it.',
  },
  {
    n: 5,
    title: 'Reload, not restart',
    body: 'systemctl reload for nginx. A restart drops in-flight connections for every site on the box to fix one of them.',
  },
  {
    n: 6,
    title: 'Unwind the rollback stack on error',
    body: 'Every created file, user, directory, symlink, unit, venv and port allocation registers an undo action. On error they unwind in reverse, and the output reports exactly what was rolled back and what could not be.',
  },
  {
    n: 7,
    title: 'Keep the previous release addressable',
    body: 'site deploy reverts to it if the post-deploy health check fails. The rollback is not a re-clone — it is a pointer move.',
  },
  {
    n: 8,
    title: 'Be fully idempotent',
    body: 'Re-running site add with identical parameters exits 0 with "already configured". With different parameters it errors and names the specific update command, rather than silently reconfiguring a live site.',
  },
];

export function ConceptTransactions() {
  return (
    <article>
      <PageHeader
        eyebrow="Concepts"
        title="Staged, verified, committed"
        lede="Every mutating operation is staged, verified, then committed — and every failure unwinds. This is the property that makes it safe to run a provisioning tool against a box with other people’s sites on it."
      />

      <div className="not-prose my-7 space-y-2.5">
        {steps.map((s) => (
          <div
            key={s.n}
            className="flex gap-4 rounded-[var(--radius-card)] border border-line bg-raised px-4 py-3"
          >
            <span className="mt-0.5 inline-flex size-6 shrink-0 items-center justify-center rounded-full bg-accent-soft font-mono text-xs font-semibold text-accent">
              {s.n}
            </span>
            <div>
              <p className="font-medium text-strong">{s.title}</p>
              <p className="mt-1 max-w-[var(--container-measure)] text-sm leading-relaxed text-muted">
                {s.body}
              </p>
            </div>
          </div>
        ))}
      </div>

      <div className="prose">
        <H2 id="rollback">The rollback stack</H2>
        <p>
          Not a concept, an actual stack. Each operation that creates something pushes the action that
          undoes it. On failure the stack is popped in reverse — so a venv is removed before the
          directory that contains it, and a port allocation is released after the unit that used it is
          gone.
        </p>
      </div>

      <Terminal title="root@server">{`$ ratline site add api.example.com --user acme --runtime python --app-module app.main:app
→ validating inputs
→ preconditions: user acme exists, api.example.com unconfigured, python 3.12 installed
→ created /home/acme/api.example.com                         [undo registered]
→ created venv                                               [undo registered]
→ pip install -r requirements.txt (as acme)
→ wrote /etc/systemd/system/ratline-acme-api_example_com.service   [undo registered]
→ systemd-analyze verify passed
→ wrote /etc/nginx/sites-available/api.example.com.conf      [undo registered]
→ nginx -t passed
→ started ratline-acme-api_example_com.service
→ waiting for health on /run/ratline/acme-api_example_com/app.sock
✗ health check failed after 30s
→ rolling back
→ stopped and removed the unit, daemon-reload
→ removed the nginx config; nothing was symlinked into sites-enabled
→ removed the venv and /home/acme/api.example.com
→ released port allocation (none held)
✗ site add failed: the app did not become healthy within 30s. systemd reports
  ratline-acme-api_example_com.service exited 3; the last log line was
  "ModuleNotFoundError: No module named 'app'". Nothing was enabled in nginx.
  hint: check --app-module against your project layout, then re-run with --dry-run to preview
~ exit 7. The box is exactly as it was before the command ran.`}</Terminal>

      <div className="prose">
        <p>
          Two details in that transcript matter. The nginx config was written but{' '}
          <em>never symlinked</em>, so no traffic could reach a broken site even during the attempt.
          And the last twenty lines of <code>journalctl -u &lt;unit&gt;</code> are surfaced
          automatically on any failed start — the operator never has to go and find them.
        </p>

        <H3 id="rollback-failed">When the rollback itself fails</H3>
        <p>
          That is exit <Link to="/reference/exit-codes#code-6">6, rollback_failed</Link>, and it is the
          only exit code that means the system is in a partial state. The output names exactly what was
          rolled back and what could not be. It is a separate code precisely so that automation can
          page a human instead of retrying.
        </p>
      </div>

      <Callout tone="danger" title="Exit 6 is a stop-and-read, not a retry">
        <p>
          Run <code>ratline doctor</code> first — it is read-only and it will tell you what drifted.
          Only then consider <code>ratline reconcile --fix</code>, and run it with{' '}
          <code>--dry-run</code> before you run it for real.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="lock">The lock</H2>
        <p>
          An exclusive <code>flock</code> is held for the duration of any mutating command. A second
          invocation waits up to <code>defaults.lock_timeout</code> (30s), then fails fast with exit{' '}
          <Link to="/reference/exit-codes#code-5">5</Link> and names the holder.
        </p>
      </div>

      <Facts
        rows={[
          ['lock file', <code key="a">/run/ratline.lock</code>],
          ['wait', <>up to <code>defaults.lock_timeout</code>, default <code>30s</code></>],
          ['on timeout', <>exit <code>5</code>, naming the command that holds it</>],
          ['read-only commands', 'take no lock — list, show, doctor and version never block'],
          ['--dry-run', 'takes no lock either, so it is safe to run alongside a real operation'],
        ]}
      />

      <div className="prose">
        <p>
          Naming the holder matters more than it sounds. “Resource busy” tells you to try again;{' '}
          <code>locked: ratline cert renew (pid 4182) holds the lock</code> tells you that the renewal
          timer fired and you should wait ninety seconds rather than go looking for a stale lock file.
        </p>

        <H2 id="idempotency">Idempotency, and why it is not "just re-run it"</H2>
        <p>
          Re-running <code>site add</code> with the <em>same</em> parameters exits 0 with “already
          configured”. Re-running it with <em>different</em> parameters is an error that names the right
          command:
        </p>
      </div>

      <Terminal title="root@server">{`$ ratline site add api.example.com --user acme --runtime python --app-module app.main:app
→ already configured; nothing to do
~ exit 0 — safe in a provisioning script that runs on every deploy

$ ratline site add api.example.com --user acme --runtime python --app-module app.main:app --workers 8
✗ error: api.example.com is already configured with 4 workers
  hint: use 'ratline site scale api.example.com --workers 8' to change it
~ exit 3 — because silently reconfiguring a live site is not a thing a provisioning tool should do`}</Terminal>

      <div className="prose">
        <p>
          That distinction is the difference between a command that is safe in a loop and a command
          that quietly changes production because someone edited a variable in a CI file.
        </p>

        <H2 id="drift">Drift, and the two commands for it</H2>
        <p>
          State is authoritative; every config is derived from it. That makes re-rendering safe in
          principle — and it also means re-rendering overwrites anything edited by hand. So the two
          concerns are separate commands:
        </p>
        <ul>
          <li>
            <strong><code>ratline doctor</code></strong> reports. Read-only. Catches failed units, dead
            sockets, orphaned configs, permission anomalies, ports allocated but unused, degraded
            certificates, and state-vs-filesystem drift.
          </li>
          <li>
            <strong><code>ratline reconcile</code></strong> acts, but only with <code>--fix</code>.
            Without it, it reports the differences.
          </li>
        </ul>
        <p>
          The supported place for hand-written additions is{' '}
          <code>/etc/nginx/ratline/custom/&lt;domain&gt;.conf</code>. It is included by the generated
          vhost and never regenerated, so it survives <code>reconcile</code>. Anything you put directly
          in the generated vhost will not.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline doctor                    # read-only; start here
ratline reconcile                 # report the differences
ratline reconcile --fix --dry-run # see exactly what would be rewritten
ratline reconcile --fix`}
      />

      <Callout tone="note" title="Why umask 027 for every provisioning write">
        <p>
          So that anything created without an explicit mode still lands at <code>0640</code> for files
          and <code>0750</code> for directories. A forgotten <code>chmod</code> then produces something
          too restrictive rather than something world-readable — the failure mode you want is a
          permission error, not a leak.
        </p>
      </Callout>
    </article>
  );
}
