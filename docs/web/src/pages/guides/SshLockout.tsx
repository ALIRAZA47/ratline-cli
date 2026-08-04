import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, H2, H3 } from '../../components/ui';

export function GuideSshLockout() {
  return (
    <article>
      <PageHeader
        eyebrow="Runbook"
        title="I’m locked out of SSH"
        lede="Console-only recovery. There is no clever remote fix — that is the nature of the failure — so this page is about the console, and about the safeguards that are meant to stop you needing it."
      />

      <Callout tone="danger" title="Read this before you need it">
        <p>
          You cannot read documentation on a server you cannot reach. Right now, while you still have access:
          confirm you know how to open your provider’s serial or VNC console, and confirm you have a root
          password or a recovery mechanism that works there. Both take two minutes today and are unavailable
          later by definition.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="triage">0 · Is it actually a lockout?</H2>
        <p>
          Thirty seconds, and it is often something else. Run these from your machine.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        filename="from your laptop"
        code={`# Is the host up at all?
ping -c 3 server.example.com

# Is sshd listening, and reachable? (A closed port is a firewall, not a key problem.)
nc -vz server.example.com 22

# What does SSH actually say? -vvv is verbose enough to be useful.
ssh -vvv -o IdentitiesOnly=yes -i ~/.ssh/id_ed25519 ali@server.example.com

# Is the website still up? If yes, the box is fine and this is SSH-specific.
curl -sS -o /dev/null -w '%{http_code}\\n' https://example.com/`}
      />

      <div className="not-prose my-6 space-y-3">
        {[
          {
            t: 'Connection refused',
            b: 'sshd is not running, or is listening on a different port. Not a key problem. Console required — but the fix is usually one systemctl command.',
          },
          {
            t: 'Connection timed out',
            b: 'A firewall or provider security group. Check the provider’s firewall rules from their dashboard first: that is fixable without a console.',
          },
          {
            t: 'Permission denied (publickey)',
            b: 'sshd is fine and your key is being rejected. The genuine lockout case. Read on.',
          },
          {
            t: 'Too many authentication failures',
            b: 'Your agent is offering a dozen keys and sshd gives up before reaching the right one. Not a lockout: retry with -o IdentitiesOnly=yes -i <the right key>.',
          },
          {
            t: 'Host key verification failed',
            b: 'Not a lockout either. The host key changed — often because the box was rebuilt. Verify why before you remove the old entry from known_hosts.',
          },
        ].map((row) => (
          <div key={row.t} className="rounded-[var(--radius-card)] border border-line bg-raised px-4 py-3">
            <p className="font-mono text-sm font-medium text-strong">{row.t}</p>
            <p className="mt-1 max-w-[var(--container-measure)] text-sm leading-relaxed text-muted">
              {row.b}
            </p>
          </div>
        ))}
      </div>

      <div className="prose">
        <H2 id="other-ways">1 · Before the console: is there another way in?</H2>
        <ul>
          <li>
            <strong>Another key.</strong> Global scope is not the only credential on the box. A user-scoped
            key gets you a shell as that tenant — not enough to run <code>ratline</code>, but enough to look
            at logs and confirm what is wrong.
          </li>
          <li>
            <strong>Another machine.</strong> The old laptop, a colleague’s key, a bastion. Try them all
            before you reach for the console.
          </li>
          <li>
            <strong>An open session.</strong> If you still have a terminal open from before the change,{' '}
            <em>do not close it</em>. sshd is reloaded rather than restarted precisely so existing sessions
            survive a bad config. That session is a working root shell and it is the whole recovery.
          </li>
        </ul>
      </div>

      <Callout tone="ok" title="If you have an open session, fix it from there right now">
        <p>
          Before doing anything else. Every provisioning change is backed up before it is applied, so the
          restore is usually one command.
        </p>
      </Callout>

      <CodeBlock
        lang="shell"
        prompt
        code={`# In the session you still have:
sshd -t                                  # does the config even parse?
ratline key list --scope global           # which credentials exist?
ratline key sync --dry-run                # what would re-rendering from state change?
ratline key sync                          # re-render every authorized_keys from state
ratline doctor`}
      />

      <div className="prose">
        <H2 id="console">2 · The console</H2>
        <p>
          Every serious provider offers a serial or VNC console that does not go through sshd. It is called
          different things — Recovery Console, Serial Console, Web Console, Launch Console, VNC — and it is
          always in the instance’s page in the dashboard. You will need a root password, or single-user mode.
        </p>
        <H3>Once you have a root shell on the console</H3>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# 1. Is sshd running and listening where you think?
systemctl status sshd
ss -lntp | grep sshd

# 2. Does the configuration parse? This is the fastest single check.
sshd -t

# 3. What did sshd say when it rejected you? The reason is always here.
journalctl -u ssh -n 100 --no-pager
grep -i -e 'sshd' -e 'authentication failure' /var/log/auth.log | tail -50

# 4. Are the permissions right? sshd silently ignores authorized_keys
#    that is group- or world-writable, or a .ssh that is not 0700.
namei -l /home/ali/.ssh/authorized_keys
stat -c '%a %U:%G %n' /home/ali /home/ali/.ssh /home/ali/.ssh/authorized_keys
# want: 750 ali:ali /home/ali
#       700 ali:ali /home/ali/.ssh
#       600 ali:ali /home/ali/.ssh/authorized_keys`}
      />

      <Callout tone="warn" title="Permissions are the answer more often than a missing key">
        <p>
          sshd refuses to use an <code>authorized_keys</code> that anyone but the owner can write, and it does
          so <em>silently</em> from the client’s point of view — you get{' '}
          <code>Permission denied (publickey)</code> with no explanation. The reason is in{' '}
          <code>/var/log/auth.log</code> on the server: <code>Authentication refused: bad ownership or modes</code>.
          A hand-run <code>chmod</code> or a restore from a backup that did not preserve modes is the usual
          cause.
        </p>
      </Callout>

      <div className="prose">
        <H3>Restore from the backup ratline made</H3>
        <p>
          Every change to <code>authorized_keys</code> and to anything under <code>/etc/ssh</code> is backed up
          before it is applied. Find the backup and put it back:
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ls -la /home/ali/.ssh/
ls -la /etc/ssh/sshd_config.d/

# Restore, fix modes, verify, reload — reload, never restart.
cp /home/ali/.ssh/authorized_keys.bak /home/ali/.ssh/authorized_keys
chown ali:ali /home/ali/.ssh/authorized_keys
chmod 600 /home/ali/.ssh/authorized_keys

sshd -t && systemctl reload ssh

# Then, once you are back in over the network, let ratline take ownership again:
ratline key audit
ratline key sync`}
      />

      <div className="prose">
        <H3>Or add a key straight from the console</H3>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# Paste a public key you hold the private half of. Modes matter.
install -d -m 700 -o ali -g ali /home/ali/.ssh
install -m 600 -o ali -g ali /dev/stdin /home/ali/.ssh/authorized_keys <<'EOF'
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI… ali@recovery
EOF

sshd -t && systemctl reload ssh

# Now get in over the network, and re-add it properly so state matches reality:
#   ratline key add --label "Ali recovery" --key ./recovery.pub --scope global
#   ratline key sync`}
      />

      <div className="prose">
        <p>
          That last step is not optional bookkeeping. A key added by hand is invisible to state, will be
          reported by <code>ratline key audit</code> as <code>outside-state</code>, and would be{' '}
          <em>removed</em> by the next <code>key sync</code> — locking you out again, from a command that was
          doing exactly what it is meant to do.
        </p>

        <H2 id="prevention">3 · Why this should not have happened</H2>
        <p>
          ratline treats <code>/etc/ssh</code> as the most dangerous thing it touches, and the safeguards are
          not advisory:
        </p>
        <ol>
          <li>Back up the config, apply the change, run <code>sshd -t</code>. On failure, restore and reload.</li>
          <li>
            <strong>Reload, never restart</strong> — existing sessions survive, which is what gives you a way
            back.
          </li>
          <li>
            After reloading, <strong>prove login still works</strong>. If verification cannot run or fails, the
            previous config is restored and the change is reported as <em>rejected</em> rather than applied.
          </li>
          <li>
            Never touch <code>PermitRootLogin</code>, <code>PasswordAuthentication</code>,{' '}
            <code>AllowUsers</code> or <code>Port</code> without an explicit flag <em>and</em> a typed
            confirmation, printing the rollback command first.
          </li>
          <li>
            Refuse to remove the last working global credential without <code>--force</code> and a typed
            confirmation.
          </li>
        </ol>
      </div>

      <Terminal title="root@server">{`$ ratline key remove "Ali MacBook" --scope global
✗ error: refusing to remove the last working global credential
  hint: add a replacement key first, verify you can log in with it, then remove this one.
        To override: --force, and you will be asked to type the label.
~ exit 3 — this refusal is the reason most people never read this page`}</Terminal>

      <div className="prose">
        <p>
          <code>ssh.verify_after_change</code> is <code>true</code> by default. The comment in{' '}
          <code>defaults.yaml</code> says it in exactly these words: turning it off on a remote server is how
          people lock themselves out. Leave it on.
        </p>

        <H3>Four habits that make this page irrelevant</H3>
        <ul>
          <li>
            <strong>Two global credentials, always.</strong> Two different machines, two different keys. The
            cost is nothing and it turns a lockout into an inconvenience.
          </li>
          <li>
            <strong>Add, verify, then remove.</strong> Never the other order. See{' '}
            <Link to="/guides/new-laptop-key">adding a key from a new laptop</Link>.
          </li>
          <li>
            <strong>Keep the session open.</strong> While making any SSH change, keep the shell you are in
            open and verify from a <em>second</em> terminal.
          </li>
          <li>
            <strong>Know your console before you need it.</strong> Test it once, on a day when nothing is
            wrong.
          </li>
        </ul>
        <p>
          See also: <Link to="/concepts/ssh-scopes#lockout">the lockout safeguards in full</Link>,{' '}
          <Link to="/reference/key#key-sync">key sync</Link>,{' '}
          <Link to="/reference/ops#doctor">doctor</Link>.
        </p>
      </div>
    </article>
  );
}
