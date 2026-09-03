import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, Facts, H2, H3 } from '../../components/ui';

export function PanelInstall() {
  return (
    <article className="prose">
      <PageHeader
        eyebrow="The web panel"
        title="Installing it"
        lede="One command, onto a server that is already running ratline. It creates the first super admin itself, so there is never a moment in which the panel is answering and unclaimed."
      />

      <Callout tone="note" title="It is meant to go onto a server already in use">
        Nothing about an existing install is special. The panel writes its own
        configuration, its own database and its own systemd unit; it does not touch{' '}
        <code>/etc/ratline/config.yaml</code>, it never writes to{' '}
        <code>state.db</code>, and every site and tenant on the box is exactly where it
        was. The integration suite asserts that by installing the panel onto a server it
        has already provisioned and comparing before and after.
      </Callout>

      <H2 id="prerequisites">What has to be there first</H2>
      <Facts
        rows={[
          ['ratline', <>Installed and working. The panel drives it and refuses to start without it — check with <code>ratline version</code>.</>],
          ['nginx', <>Only needed to put the panel on a domain. It runs on a port without it.</>],
          ['certbot', <>Only needed for a certificate. Same.</>],
        ]}
      />

      <H2 id="install">Install</H2>
      <Terminal>{`$ curl -fsSL https://ratline.alirazakhan.me/panel.sh | sudo sh
→ Architecture: amd64
→ Driving ratline v0.14.1
→ Resolving the latest release
→ Downloading ratline-panel-linux-amd64
→ Verifying checksums
→ Installing the binary
Email address for the panel's first super admin: you@example.com
Wrote /etc/ratline/panel.yaml
Panel database at /var/lib/ratline/panel.db (schema 1)
Installed /etc/systemd/system/ratline-panel.service

The panel is running.

  Sign in as   you@example.com
  Password     k7fm-3q9x-2vtb-npd4-h6ws

That password is shown once and is not stored anywhere in the clear.`}</Terminal>

      <p>
        Twenty characters from an alphabet with no <code>0</code>/<code>O</code> or{' '}
        <code>1</code>/<code>l</code>/<code>I</code> in it, because it exists to be read
        off one screen and typed into another. It is not meant to be remembered — change
        it once you are in, or set a new one with{' '}
        <code>ratline-panel account password</code>.
      </p>

      <p>
        Every artefact is checked against the release&rsquo;s own <code>SHA256SUMS</code>{' '}
        before anything is installed, and a missing checksum file is a refusal — the same
        rule ratline&rsquo;s own installer follows, for the same reason. From a checkout:
      </p>

      <CodeBlock lang="bash" prompt code={`make install-panel`} />

      <H3 id="unattended">From a provisioning script</H3>
      <p>
        With no terminal there is nobody to ask, so the address is a flag. Supply the
        password on stdin and nothing is printed; leave it out and one is generated and
        printed once, which is what you want when a human is reading the output.
      </p>
      <CodeBlock
        lang="bash"
        prompt
        code={`printf '%s' "$PANEL_PASSWORD" | ratline-panel install \\
    --admin-email ops@example.com --admin-password-stdin

# or, piped from curl
curl -fsSL https://ratline.alirazakhan.me/panel.sh \\
  | sudo PANEL_ADMIN_EMAIL=ops@example.com sh`}
      />
      <p>
        The password is never a flag. <code>/proc/PID/cmdline</code> is world-readable, so
        one on a command line is one every account on the machine can read while the
        command runs — and it is in the shell history afterwards.
      </p>

      <Callout tone="note" title="Safe to run twice">
        An existing configuration is kept, an existing database is reused, and an existing
        account is left alone rather than reset. That matters because re-running the
        installer is the natural way to fix a mistake in it.
      </Callout>

      <p>Or from the Debian package, which installs the binary and stops:</p>

      <CodeBlock
        lang="bash"
        prompt
        code={`sudo dpkg -i ratline-panel_0.15.0_amd64.deb
sudo ratline-panel install`}
      />

      <Callout tone="note" title="The package does not start it">
        A package that brought up a root-equivalent web service at install time would give
        the server to whoever reached the port first, on a machine nobody was watching.{' '}
        <code>ratline-panel install</code> is the step a person runs, and is then in a
        position to claim it.
      </Callout>

      <H2 id="reach">Reach it</H2>

      <p>
        The panel is on <code>127.0.0.1:8420</code>, which is not reachable from anywhere
        else. Until it has a domain, that means a tunnel from your own machine:
      </p>

      <CodeBlock
        lang="bash"
        prompt
        code={`ssh -L 8420:127.0.0.1:8420 your-server
# then open http://localhost:8420`}
      />

      <p>
        Sign in with the address and the password the installer printed. From then on
        people arrive by <Link to="/panel/team">invitation</Link>, and the sign-up page
        does not exist.
      </p>

      <H3 id="no-admin">Leaving it unclaimed on purpose</H3>
      <p>
        <code>--no-admin</code> installs without an account, which puts the panel back in
        the state where whoever opens it first becomes its super admin. That is only
        sensible if you are about to open it yourself, and{' '}
        <code>ratline-panel doctor</code> reports it as a problem until somebody does.
      </p>

      <H2 id="verify">Check it</H2>
      <Terminal>{`$ ratline-panel doctor
ok    config          /etc/ratline/panel.yaml
ok    ratline         v0.14.1, 122 commands
ok    policy          every command is classified
ok    database        /var/lib/ratline/panel.db, schema 1
ok    accounts        1 accounts, 1 super admins
ok    interface       the built interface is in this binary
ok    service         /etc/systemd/system/ratline-panel.service
ok    exposure        loopback only; reach it through an SSH tunnel
ok    second factor   not required, but the panel is not publicly reachable`}</Terminal>

      <p>
        It exits 0 when everything is fine and 3 when something is not, so a monitoring
        system can branch on it. Every check runs and every problem is reported together,
        rather than stopping at the first.
      </p>

      <H2 id="upgrading">Upgrading it</H2>
      <p>
        The same command. The installer replaces the binary and re-runs{' '}
        <code>ratline-panel install</code>, which keeps the configuration, reuses the
        database and leaves the accounts alone — so an upgrade is not a re-claim.
      </p>
      <CodeBlock
        lang="bash"
        prompt
        code={`curl -fsSL https://ratline.alirazakhan.me/panel.sh | sudo sh
systemctl restart ratline-panel`}
      />
      <p>
        The panel and ratline are versioned together but installed apart, and the panel
        reads the installed binary&rsquo;s command surface at runtime — so a ratline
        upgraded ahead of the panel keeps working, with any command the panel has no
        policy for treated as super-admin only. <code>ratline-panel doctor</code> says so
        when that happens.
      </p>

      <H2 id="removing">Removing it</H2>
      <CodeBlock lang="bash" prompt code={`ratline-panel uninstall`} />
      <p>
        Stops the service, removes the unit and the vhost, and touches nothing ratline
        manages — no tenant, no site, no certificate, and no environment variable set
        through it. The accounts database survives unless you pass <code>--purge</code>.
      </p>

      <H2 id="next">Next</H2>
      <p>
        <Link to="/panel/domain">Put it on a domain</Link>, then{' '}
        <Link to="/panel/security">read what signing in actually grants</Link> before you
        invite anybody.
      </p>
    </article>
  );
}
