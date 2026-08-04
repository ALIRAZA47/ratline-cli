import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, H2, H3 } from '../../components/ui';

export function GuideInheritedServer() {
  return (
    <article>
      <PageHeader
        eyebrow="Guide"
        title="A server you did not set up"
        lede="You have root on a box someone else provisioned, or one you have not touched in a month. Four commands establish what is on it, whether any of it is broken, and why it was built that way — without reading a single configuration file."
      />

      <div className="prose">
        <p>
          The order matters, because each one answers a question the next one assumes. Nothing here
          changes anything: all four are read-only and none takes the lock.
        </p>

        <H2 id="status">1 · What is on here</H2>
      </div>

      <CodeBlock lang="shell" prompt code={`ratline status`} />

      <Terminal title="root@web-1">{`web-1.example.net — ratline 1.0.0
Ubuntu 24.04.1 LTS, up 41d 6h

3 tenants, 5 sites, 7 SSH keys, 4 certificates

    DOMAIN                OWNER   RUNTIME  STATE     TLS                 NOTE
    www.example.com       acme    static   serving   https
    app.example.com       acme    node     running   https               4 workers
  ! api.example.com       acme    python   failed    https
    blog.example.org      beta    static   serving   https (expiring)
  ! stage.example.org     beta    node     running   http                2 of 4 workers online

Certificates needing attention:
  blog.example.org                         expiring, 6 days left

2 problems found. See them with 'ratline doctor'.`}</Terminal>

      <div className="prose">
        <p>
          That is the whole server on one screen: every tenant, every site, what state each one is
          actually in, and a count of anything worth acting on. The <code>!</code> column marks a row
          that needs attention — a failed unit, a disabled site, workers that died and did not come
          back, a certificate that is not simply valid.
        </p>
      </div>

      <Callout tone="note" title="status and doctor answer different questions">
        <p>
          <code>doctor</code> prints what is <em>wrong</em>, so on a healthy server it prints nothing —
          correct, and no help at all in working out what the server <em>is</em>.{' '}
          <code>status</code> always prints the inventory and marks the problems. doctor is what a cron
          job runs; status is what a human runs first.
        </p>
        <p>
          The problem count in <code>status</code> comes from <code>doctor</code> itself rather than a
          second implementation, so the two can never disagree about whether this server is healthy.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="doctor">2 · What is wrong with it</H2>
      </div>

      <CodeBlock lang="shell" prompt code={`ratline doctor`} />

      <div className="prose">
        <p>
          Every check ratline knows how to run, each finding carrying the command that fixes it: the
          nginx configuration, failed services, dead sockets, socket permissions, certificate expiry,
          orphaned configuration, drift between state and the filesystem, permission anomalies,
          allocated but unused ports, and the SSH key audit.
        </p>
        <p>
          Two of those are the ones that are hardest to spot by hand and easiest to have gone wrong on
          a server you did not build: <strong>drift</strong>, which is the vhost or unit somebody edited
          directly, and <strong>permission anomalies</strong> — the home that became <code>0755</code>,
          the <code>.env</code> that became <code>0644</code>.
        </p>

        <H2 id="troubleshoot">3 · Why that one site is broken</H2>
        <p>
          <code>doctor</code> sweeps the server and hands you a list. When a specific site is down, a
          list is the wrong shape — you want a cause.
        </p>
      </div>

      <CodeBlock lang="shell" prompt code={`ratline site troubleshoot api.example.com`} />

      <Terminal title="root@web-1">{`api.example.com

  ok    enabled
  ok    nginx configuration  —  /etc/nginx/sites-available/api.example.com.conf
  ok    nginx accepts the configuration
  ok    site directory  —  /home/acme/api.example.com
  ok    systemd unit  —  active, pid 41822
  --    pm2 workers  —  this site runs node directly under systemd
  FAIL  socket permissions  —  the socket is mode 0640; nginx needs 0660 to connect,
        so every request is a 502
  --    the application answers  —  not checked: an earlier step has to pass first

Likely cause: the socket is mode 0640; nginx needs 0660 to connect, so every
              request is a 502
Try:          ratline site restart api.example.com; the full story is in
              'ratline explain sockets'`}</Terminal>

      <div className="prose">
        <p>
          It walks the path a request actually travels — nginx, then the directories, then the unit,
          then the process manager, then the socket, then the application, then nginx end to end, then
          TLS, then DNS — and stops at the first failure. Checking in that order means the first
          failure <em>is</em> the cause; the steps after it are marked not-checked rather than reported
          as separate problems you would then have to rule out.
        </p>

        <H3>Two of the checks cannot be done any other way</H3>
        <p>
          It makes a real HTTP request straight to the application, bypassing nginx entirely. That one
          request splits the problem in half: an answer means the application is healthy and everything
          is between nginx and the socket; no answer means it is the application. Then it makes the
          request a visitor would — over the loopback, with the site’s <code>Host</code> header — which
          is the same path minus the network.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site troubleshoot api.example.com --json | jq -r '.data.likely_cause'
ratline site troubleshoot api.example.com --json | jq '.data.steps[] | select(.verdict=="failed")'`}
      />

      <div className="prose">
        <H2 id="explain">4 · Why it was built this way</H2>
        <p>
          The one thing the commands above cannot tell you is the reasoning. That is what{' '}
          <code>explain</code> is for, and the reason it is built into the binary rather than living
          only on this site: you are on someone else’s server over SSH, with no browser.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline explain              # the twelve topics
ratline explain sockets      # the 502 you just found, in full
ratline explain node | less
ratline explain 502          # a near-miss is corrected, not refused`}
      />

      <div className="prose">
        <p>
          The pages are the same markdown this site renders, embedded at build time — so the binary and
          the website cannot give you different answers, and neither needs the network.
        </p>

        <H2 id="then">Then, before you change anything</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site show api.example.com     # every setting for one site
ratline reconcile                    # what on disk disagrees with state?
ratline backup                       # before the first mutation, every time`}
      />

      <div className="prose">
        <p>
          <code>reconcile</code> reports and changes nothing until asked; <code>--fix</code>{' '}
          regenerates configuration from state, and leaves alone anything without a{' '}
          <code># managed-by: ratline</code> header — so a file a human wrote survives.
        </p>
        <p>
          And every mutating command takes <code>--dry-run</code>, which prints the files, their
          contents and every command that would run. On a server you did not build, that is the
          cheapest way to find out what a change would actually do.
        </p>
      </div>

      <Callout tone="ok" title="Shell completion makes the rest of it typeable">
        <p>
          Completion here is dynamic: it offers the domains, tenants, certificates and key fingerprints
          that exist on <em>this</em> server. A fingerprint is the one identifier nobody types from
          memory, so it is the difference between <code>ratline key remove</code> being usable and
          being preceded by <code>ratline key list</code> every single time.
        </p>
        <p>
          It needs no privileges, never blocks for more than two seconds, and returns nothing rather
          than an error when it cannot read state.
        </p>
      </Callout>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline completion bash | sudo tee /etc/bash_completion.d/ratline
ratline completion zsh  | sudo tee /usr/share/zsh/site-functions/_ratline`}
      />

      <div className="prose">
        <p>
          See also: <Link to="/guides/debug-502">debugging a 502</Link>,{' '}
          <Link to="/concepts/transactions">the transaction model</Link>,{' '}
          <Link to="/reference/ops#status">
            <code>ratline status</code>
          </Link>
          ,{' '}
          <Link to="/reference/site#site-troubleshoot">
            <code>ratline site troubleshoot</code>
          </Link>
          .
        </p>
      </div>
    </article>
  );
}
