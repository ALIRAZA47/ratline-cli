import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, Facts, H2, H3 } from '../../components/ui';

export function GuideContractorAccess() {
  return (
    <article>
      <PageHeader
        eyebrow="Guide"
        title="Give a contractor access to exactly one site"
        lede="Site-scoped SSH, expiring on a date you choose, restricted to their office network — and an honest account of what that boundary is and is not."
      />

      <Facts
        rows={[
          ['scope', <code key="a">site</code>],
          ['grants', 'SFTP, rsync and git, confined to one site directory. No interactive shell.'],
          ['lands in', <code key="b">/home/&lt;user&gt;/.ssh/authorized_keys</code>],
          ['boundary', 'blast-radius, not kernel-enforced. Read the caveat below before you promise anything.'],
        ]}
      />

      <div className="prose">
        <H2 id="ask">1 · Ask for a public key, not a password</H2>
        <p>
          Send them this. Note that it is the <code>.pub</code> file — if they send you something that
          begins <code>-----BEGIN OPENSSH PRIVATE KEY-----</code>, tell them to generate a new one,
          because the one they just emailed is compromised.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        filename="what the contractor runs"
        code={`ssh-keygen -t ed25519 -C "rae@contractor" -f ~/.ssh/acme_example
cat ~/.ssh/acme_example.pub`}
      />

      <Callout tone="note" title="ed25519, and why RSA-2048 will be refused">
        <p>
          <code>ssh-dss</code> is refused outright. RSA under 3072 bits is refused; under 4096 it is
          warned about. A key generated in 2015 with the then-default 2048 bits will not be accepted, and
          the answer is a new key rather than a configuration change.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="add">2 · Add it, scoped and bounded</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline key add \\
  --label "Contractor — Rae (redesign)" \\
  --key ./rae-acme_example.pub \\
  --scope site \\
  --user acme \\
  --site example.com \\
  --expires 2026-10-31 \\
  --from 203.0.113.0/24`}
      />

      <div className="prose">
        <p>Every one of those flags is doing work:</p>
        <ul>
          <li>
            <strong><code>--label</code></strong> is required, and it should say who and why. In eighteen
            months, “Contractor — Rae (redesign)” is the difference between confidently removing a key and
            leaving it forever. At most 64 characters, printable, no double quote or backslash — the label
            is rendered inside a <code>label="…"</code> comment and the validator refuses to produce an
            ambiguous file.
          </li>
          <li>
            <strong><code>--scope site</code></strong> applies <code>restrict</code> plus a forced command,
            confined to <code>/home/acme/example.com</code> with symlinks resolved. No interactive shell:{' '}
            <code>ssh.site_scope_sftp_only</code> is <code>true</code> by default.
          </li>
          <li>
            <strong><code>--expires</code></strong> renders <code>expiry-time="…"</code>. A bare date means
            valid through the end of that day, in UTC — which is what you meant when you typed it, and what
            OpenSSH compares against. A daily timer removes keys past their expiry whether or not the sshd
            on the box supports the option (<code>ssh.prune_expired</code>).
          </li>
          <li>
            <strong><code>--from</code></strong> renders <code>from="203.0.113.0/24"</code>. Bare addresses
            are widened to <code>/32</code> and every entry is canonicalised, so you cannot accidentally
            write a prefix that does not mean what it looks like.
          </li>
        </ul>
      </div>

      <div className="prose">
        <H2 id="verify">3 · Verify before you promise anything</H2>
        <p>
          <code>ratline key test</code> reads the rendered options back and states the result in plain
          language. Run it, and read the last line.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline key test "Contractor — Rae (redesign)"`}
      />

      <CodeBlock
        lang="text"
        code={`Key       SHA256:x9K…   "Contractor — Rae (redesign)"   ed25519
Scope     site → example.com  (owner: acme)
Login     acme@server — forced command only, no interactive shell
Allowed   sftp, rsync, git-upload-pack, git-receive-pack
          confined to /home/acme/example.com (symlinks resolved)
Denied    shell, port forwarding, agent forwarding, X11, PTY
Source    203.0.113.0/24 only
Expires   2026-10-31 (88 days)
Last use  never
Note      Runs as UID acme. Not a kernel boundary — see SECURITY.md.`}
        noCopy
      />

      <div className="prose">
        <H2 id="what-it-is">What this boundary actually is</H2>
      </div>

      <div className="not-prose my-5 rounded-[var(--radius-card)] border-2 border-[color-mix(in_oklab,var(--warn)_45%,transparent)] bg-warn-soft px-5 py-4">
        <p className="max-w-[var(--container-measure)] text-base leading-relaxed">
          Site scope is a <strong>blast-radius and usability boundary, not a kernel-enforced one</strong>.
          The key still authenticates as the site owner’s UID; the confinement is sshd’s forced command plus
          the <code className="font-mono text-[0.9em]">ratline-shell</code> wrapper. That reliably prevents
          accidents and stops a contractor wandering into a sibling site. It does not stop a determined
          attacker who already has code execution as that UID, and{' '}
          <code className="font-mono text-[0.9em]">--allow-shell</code> removes most of it.
        </p>
      </div>

      <div className="prose">
        <p>In practice, that means it is right for:</p>
        <ul>
          <li>A designer who should be updating one site and not the other three.</li>
          <li>An agency you have a contract with, where the risk is a mistake rather than malice.</li>
          <li>A CI runner that should only be able to rsync into one directory.</li>
        </ul>
        <p>And it is not right for:</p>
        <ul>
          <li>Someone you do not trust at all.</li>
          <li>
            A situation where a compromise of that one site must not reach the tenant’s other sites.
          </li>
        </ul>

        <H3>If you need a real boundary</H3>
        <p>
          <strong>One ratline user per site.</strong> Then the boundary is a UID, which the kernel does
          enforce, and a compromise of one site cannot read another’s <code>.env</code>.{' '}
          <code>user add</code> is cheap for exactly this reason.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# The contractor's site gets its own tenant, so the key is user-scoped
# to an account that owns nothing else.
ratline user add acme-redesign --comment "Acme — redesign, contractor-facing"
ratline site add redesign.example.com --user acme-redesign --runtime static

ratline key add --label "Contractor — Rae" --key ./rae.pub \\
  --scope user --user acme-redesign --expires 2026-10-31`}
      />

      <div className="prose">
        <H2 id="how-they-work">4 · Tell them how to connect</H2>
        <p>
          They get SFTP, rsync and git. Not a shell — <code>ssh acme@server</code> will be refused by the
          forced command, and that is not a misconfiguration to fix.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        filename="what the contractor runs"
        code={`# SFTP
sftp -i ~/.ssh/acme_example acme@server.example.com

# rsync a built site up
rsync -avz --delete -e "ssh -i ~/.ssh/acme_example" \\
  ./dist/ acme@server.example.com:example.com/public/

# git, if a repository lives on the box
git clone acme@server.example.com:example.com/repo.git`}
      />

      <Callout tone="warn" title="A narrower key: rsync and nothing else">
        <p>
          If all they need to do is push a built site, <code>--command rsync-only</code> is tighter than
          full SFTP. The presets map to real programs in <code>ssh.command_presets</code> —{' '}
          <code>sftp-only → internal-sftp</code>, <code>rsync-only → rsync</code>,{' '}
          <code>git-only → git</code> — and only a named preset is accepted. An arbitrary command string is
          not, because that is exactly the escalation vector option stripping exists to close.
        </p>
      </Callout>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline key add --label "Contractor — Rae (deploy only)" \\
  --key ./rae.pub --scope site --user acme --site example.com \\
  --command rsync-only --expires 90d --from 203.0.113.0/24`}
      />

      <div className="prose">
        <H2 id="end">5 · When the engagement ends</H2>
        <p>
          The expiry does this for you, which is the reason to set one. If you need it gone sooner, or you
          want it gone from everywhere at once:
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# From this one place
ratline key remove "Contractor — Rae (redesign)" --scope site --user acme --site example.com

# From everywhere on the box, reporting each place it was found
ratline key revoke "Contractor — Rae (redesign)" --everywhere

# And then check nothing else is lingering
ratline key list --unused 90
ratline key audit`}
      />

      <Terminal title="root@server">{`$ ratline key audit
! 3 findings
! duplicate     SHA256:aB1… present for both acme and beta — one key, two tenants
!               "Ali MacBook" (global), "Ali laptop" (user acme)
! expired       SHA256:cD2… "Contractor — Jun 2025" expired 2025-09-30, still present
!               the prune timer removes these daily; this one predates it
! outside-state SHA256:eF3… in /home/beta/.ssh/authorized_keys, not in state
!               added by hand. 'ratline key sync' would remove it.
→ nothing was changed; this command only reports`}</Terminal>

      <div className="prose">
        <p>
          The third finding is the one that matters. A key someone appended by hand is invisible to state
          and survives every other cleanup, so the audit compares the files against state rather than
          assuming ratline is the only writer.
        </p>
        <p>
          See also: <Link to="/concepts/ssh-scopes">the three SSH scopes</Link>,{' '}
          <Link to="/guides/ci-deploy-keys">CI deploy keys in both directions</Link>.
        </p>
      </div>
    </article>
  );
}
