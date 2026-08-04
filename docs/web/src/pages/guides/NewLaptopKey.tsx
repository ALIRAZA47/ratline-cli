import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, H2, H3 } from '../../components/ui';

export function GuideNewLaptopKey() {
  return (
    <article>
      <PageHeader
        eyebrow="Guide"
        title="Add a key from a new laptop"
        lede="Add, verify, then remove. In that order, without exception — the last step is the one that locks people out when it happens first."
      />

      <Callout tone="danger" title="The order is the whole guide">
        <p>
          <strong>Add the new key. Prove it works in a second session. Only then remove the old one.</strong>{' '}
          Bricking SSH on a remote VPS has no recovery path short of the provider’s console. Keep the shell
          you are currently in open until the very end, because an open session survives a reload and is
          your way back if something is wrong.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="generate">1 · On the new laptop</H2>
      </div>

      <CodeBlock
        lang="shell"
        filename="new laptop"
        code={`ssh-keygen -t ed25519 -C "ali@mbp-2026"
cat ~/.ssh/id_ed25519.pub

# Note the fingerprint — you will compare it on the server.
ssh-keygen -lf ~/.ssh/id_ed25519.pub
# 256 SHA256:x9K… ali@mbp-2026 (ED25519)`}
      />

      <div className="prose">
        <H2 id="add">2 · From the old machine, add it</H2>
        <p>
          Do this from the session you already have, not from the new laptop — you cannot yet log in from
          the new laptop, which is the point of the exercise.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# Global scope: server administration, and permission to run ratline
ratline key add \\
  --label "Ali MacBook Pro 2026" \\
  --key ./ali-mbp-2026.pub \\
  --scope global`}
      />

      <Terminal title="root@server">{`$ ratline key add --label "Ali MacBook Pro 2026" --key ./ali-mbp-2026.pub --scope global
→ ssh-keygen -l: 256 SHA256:x9K… ed25519 — accepted
→ options: restrict,pty  (pty re-enabled: this scope gets a shell)
→ backed up /home/ali/.ssh/authorized_keys
→ wrote 3 keys atomically
→ sshd -t passed
→ reloaded sshd (existing sessions retained)
→ verified: publickey authentication succeeds for ali
→ recorded in state
! this is now the 3rd global credential. Remove the ones you no longer use:
!   ratline key list --scope global --unused 90`}</Terminal>

      <div className="prose">
        <p>
          Notice <em>reloaded, existing sessions retained</em> and{' '}
          <em>verified: publickey authentication succeeds</em>. Both are deliberate. sshd is reloaded rather
          than restarted so the session you are typing in survives, and login is proven to still work before
          the change is reported as applied. If verification cannot run or fails, the previous file is
          restored and the change is reported as <em>rejected</em>.
        </p>

        <H3>If you can only reach the box from the new laptop</H3>
        <p>
          Then you cannot add the key with ratline, because you cannot log in. Use the key you have on the
          old machine, or an existing key on any machine, or the provider’s console. This is the situation{' '}
          <Link to="/guides/ssh-lockout">the lockout runbook</Link> is for — go and read it now rather than
          later.
        </p>

        <H2 id="verify">3 · Verify from the new laptop, in a new session</H2>
        <p>Leave the old session open. Open a second terminal.</p>
      </div>

      <CodeBlock
        lang="shell"
        filename="new laptop, second terminal"
        code={`ssh -i ~/.ssh/id_ed25519 ali@server.example.com

# Confirm the server sees the key you think it does
sudo ratline key test SHA256:x9K…

# And that ratline itself works — global scope grants that
sudo ratline version`}
      />

      <div className="prose">
        <p>
          Do not skip <code>ratline version</code>. Global scope grants a shell{' '}
          <em>and</em> permission to run <code>ratline</code>; if you can log in but cannot run the tool,
          you have half of what you need and removing the old key would leave you unable to fix it.
        </p>

        <H2 id="remove">4 · Only now, remove the old key</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline key list --scope global
ratline key remove "Ali MacBook 2022" --scope global`}
      />

      <Terminal title="root@server">{`$ ratline key list --scope global
LABEL                    FINGERPRINT      ALGO      ADDED       LAST USE
Ali MacBook Pro 2026     SHA256:x9K…      ed25519   2026-08-04  2026-08-04 14:22
Ali MacBook 2022         SHA256:aB1…      ed25519   2022-03-11  2026-07-29 09:04
CI runner (deploy)       SHA256:cD2…      ed25519   2024-01-08  2026-08-04 03:00

$ ratline key remove "Ali MacBook 2022" --scope global
! removing a global credential. 2 will remain: "Ali MacBook Pro 2026", "CI runner (deploy)"
→ backed up /home/ali/.ssh/authorized_keys
→ wrote 2 keys atomically
→ sshd -t passed
→ reloaded sshd
→ verified: publickey authentication succeeds for ali
→ removed from state`}</Terminal>

      <Callout tone="ok" title="The refusal that saves you">
        <p>
          <code>key remove</code> and <code>user delete</code> refuse to remove the last working global
          credential without <code>--force</code> and a typed confirmation — and print the rollback command
          before anything changes. If you have done the steps out of order, this is what stops you.
        </p>
      </Callout>

      <Terminal title="root@server">{`$ ratline key remove "Ali MacBook 2022" --scope global
✗ error: refusing to remove the last working global credential
  hint: add a replacement key first, verify you can log in with it, then remove this one.
        To override: --force, and you will be asked to type the label.
~ exit 3`}</Terminal>

      <div className="prose">
        <H2 id="lost">If the old laptop is lost rather than replaced</H2>
        <p>
          Different problem, different command. You are not tidying up — you are revoking a credential that
          may be in someone else’s hands, and you do not want to have to remember every place it was
          installed.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# 1. Add the new key first, from any machine that still has access
ratline key add --label "Ali MacBook Pro 2026" --key ./new.pub --scope global

# 2. Verify it, in a second session

# 3. Remove the lost key from every scope on the box, at once
ratline key revoke "Ali MacBook 2022" --everywhere

# 4. Check what it could reach and whether it was used after the loss
ratline key list --scope global
ratline key audit`}
      />

      <div className="prose">
        <p>
          <code>key revoke --everywhere</code> reports each place the key was found, and records it in{' '}
          <code>ssh.revoked_keys</code> (<code>/etc/ratline/ssh/revoked_keys</code>) so a re-add of the same
          fingerprint is visible rather than silent.
        </p>
        <p>
          Last-used data comes from scanning the auth log for accepted-publickey lines (
          <code>ssh.usage_scan_enabled</code>). If the lost key shows a use <em>after</em> the time you lost
          it, treat that as an incident and not a bookkeeping exercise.
        </p>

        <H2 id="from-github">A shortcut, with a caveat</H2>
        <p>
          <code>--from-github &lt;user&gt;</code> fetches{' '}
          <code>https://github.com/&lt;user&gt;.keys</code> with full certificate verification, validates
          each line independently, then shows every fingerprint and asks for confirmation.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline key add --label "Ali (from GitHub)" --from-github aliexample --scope global`}
      />

      <Terminal title="root@server">{`$ ratline key add --label "Ali (from GitHub)" --from-github aliexample --scope global
→ fetched https://github.com/aliexample.keys (certificate verified), 3 keys
→ SHA256:x9K…  ed25519   ali@mbp-2026
→ SHA256:aB1…  ed25519   ali@mbp-2022
! SHA256:zZ9…  rsa-2048  ali@old-desktop — refused: RSA under 3072 bits
! 2 of 3 keys would be added, both with the same label and the same scope.
Continue? [y/N]:`}</Terminal>

      <div className="prose">
        <p>
          That confirmation exists because an account often carries keys its owner has forgotten about —
          a decade-old desktop, a CI integration, a key from a previous employer’s machine. Adding all of
          them because one of them was the one you wanted is not what you meant.
        </p>
        <p>
          See also: <Link to="/concepts/ssh-scopes#lockout">the lockout safeguards</Link> and{' '}
          <Link to="/guides/ssh-lockout">what to do when you are already locked out</Link>.
        </p>
      </div>
    </article>
  );
}
