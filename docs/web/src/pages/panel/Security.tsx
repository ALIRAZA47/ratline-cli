import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Callout, Facts, H2, H3 } from '../../components/ui';

export function PanelSecurity() {
  return (
    <article className="prose">
      <PageHeader
        eyebrow="The web panel"
        title="The security model"
        lede="Signing in to the panel is equivalent to root on the machine. Everything below follows from saying that plainly rather than working around it."
      />

      <Callout tone="danger" title="There is no version of this where it is not true">
        The panel runs as root because its job is to invoke a tool that creates system
        accounts and writes into <code>/etc</code>. No arrangement of privileges changes
        what an authenticated user can then ask for. Treat a panel account exactly as you
        would treat a root SSH key.
      </Callout>

      <H2 id="what-that-buys">What that buys, and what it costs</H2>
      <p>
        The upside of being honest about it is that everything else can be designed around
        the real threat. The panel is not trying to be a sandbox — it is trying to make the
        front door hard, the blast radius legible, and the record complete.
      </p>

      <H2 id="no-window">There is no unclaimed window</H2>
      <p>
        The installer creates the first super admin and prints a generated password once.
        That is the difference between this and every panel whose first screen invites you
        to create an account: there is no state in which the software is answering and
        anybody who reaches it becomes its administrator. No default password either — the
        other way that state is usually reached.
      </p>
      <p>
        <code>--no-admin</code> puts you back in it deliberately, and{' '}
        <code>ratline-panel doctor</code> reports it as a problem until somebody signs up.
      </p>

      <H2 id="front-door">The front door</H2>

      <Facts
        rows={[
          [
            'Passwords',
            <>argon2id with RFC 9106&rsquo;s server parameters — 64 MiB, three passes, four lanes — and the parameters are stored in the hash rather than assumed, so raising them later verifies old hashes with their own.</>,
          ],
          [
            'Length, not punctuation',
            <>Twelve characters minimum and a check against the handful of passwords a scanner tries first. Composition rules reliably produce <code>Password1!</code>, which is why NIST stopped recommending them.</>,
          ],
          [
            'Rate limiting',
            <>Counted per account <em>and</em> per source address, independently. Per account alone lets one password be sprayed across every address; per address alone lets a distributed attempt through.</>,
          ],
          [
            'Timing',
            <>A wrong address and a wrong password take the same time and produce the same sentence. A response that is fast for an unknown address enumerates the accounts as surely as a different message would.</>,
          ],
          [
            'Second factor',
            <>TOTP, RFC 6238. Enrolment is two steps — the secret is stored inert until a code proves the authenticator has it — so nobody locks themselves out of a panel by scanning a code that did not save.</>,
          ],
        ]}
      />

      <H2 id="sessions">Sessions</H2>
      <p>
        A cookie, HttpOnly and SameSite=Strict, whose token is stored only as a hash. Two
        clocks run on it: an idle timeout that ends a quiet session, and an absolute
        lifetime it can never exceed — refreshing the first never pushes the second past
        its original ceiling. Sliding a fixed expiry forward on every request is the common
        version of this, and it means a browser that polls has an unbounded session.
      </p>
      <p>
        Every state-changing request carries a token in a header, held in the page&rsquo;s
        memory rather than in a cookie a script could read, and the request&rsquo;s Origin
        is checked as well. <code>GET</code> is exempt because a server-sent event stream
        cannot carry a header — which is safe only because no <code>GET</code> here changes
        anything, and that is a property to keep rather than a coincidence.
      </p>

      <H2 id="secrets">Secrets</H2>
      <p>
        A value in argv is a value in <code>/proc/PID/cmdline</code>, which every account on
        the server can read for as long as the command runs. So the panel never puts one
        there. A password, a connection string or an environment value is sent as its own
        field, validated, and written to ratline&rsquo;s standard input; the argv that is
        recorded in the activity log contains <code>--stdin</code> and nothing else.
      </p>
      <p>
        <code>site env set</code> is the interesting case: its stdin is{' '}
        <code>NAME=value</code> lines, so the panel asks for the name separately, validates
        both halves, and composes the line itself — because typing it as a positional
        assignment would work perfectly and put the value in the process table.
      </p>

      <H2 id="injection">Why there is no command injection to find</H2>
      <p>
        Not because the escaping is careful — because there is nothing to escape. Every
        invocation is an argv slice built element by element from typed values; no string is
        ever split into a command line, and there is no shell in the picture at any layer.
        Three details do the work:
      </p>
      <Facts
        rows={[
          ['Flags are one element', <>Emitted as <code>--name=value</code>. The two-element form lets a value beginning with a dash be read as the next flag; the joined form cannot be, whatever it contains.</>],
          ['Positionals come after --', <>Without it, a &ldquo;domain&rdquo; of <code>--config=/tmp/mine.yaml</code> would hand ratline a different configuration file to run against.</>],
          ['The globals are the panel’s', <>A request cannot set <code>--config</code>, <code>--dry-run</code>, <code>--yes</code> or <code>--json</code>. Each of those changes what the command means.</>],
        ]}
      />
      <p>
        Values are then checked for control characters and length before they can become
        argv, and ratline validates them again where they enter a manager — this is the
        first of two checks, not the only one.
      </p>

      <H2 id="destructive">Irreversible operations</H2>
      <p>
        Anything that cannot be undone by running another command needs the target&rsquo;s
        name typed back, and the check is on the server rather than in the form. A y/N is
        too easy to hit by reflex when the thing being deleted is somebody&rsquo;s site —
        which is the same reasoning behind ratline&rsquo;s own{' '}
        <Link to="/concepts/security">typed confirmations</Link>.
      </p>

      <H2 id="harden">The four settings that matter</H2>

      <H3 id="bind">Keep it on the loopback</H3>
      <p>
        <code>listen.address: 127.0.0.1</code>, with nginx in front. What faces the internet
        is then nginx, with a certificate, a real TLS configuration and logs — rather than a
        Go server somebody has to remember to keep patched.
      </p>

      <H3 id="totp">Require a second factor</H3>
      <CodeBlock lang="yaml" filename="/etc/ratline/panel.yaml" code={`security:
  require_totp: true`} />
      <p>
        Off by default, because a panel nobody can sign in to is broken rather than secure.
        On is the right answer the moment it is reachable from the internet, and{' '}
        <code>ratline-panel doctor</code> says so if it is not.
      </p>

      <H3 id="allow-from">Narrow who can reach it</H3>
      <CodeBlock lang="yaml" filename="/etc/ratline/panel.yaml" code={`security:
  allow_from:
    - 203.0.113.0/24
    - 2001:db8::/32`} />
      <p>
        Requests from outside these blocks are refused before their body is read. A second
        lock for a panel used only from one place.
      </p>

      <H3 id="roles">Give people the admin role</H3>
      <p>
        Super admin is for the people who invite others and run the irreversible
        operations. Most work does not need it, and an admin account that is phished cannot{' '}
        <code>db drop</code>, <code>user sudo grant</code> or <code>cert revoke</code>.
      </p>

      <H2 id="record">The record</H2>
      <p>
        Two logs, and you need both. ratline writes its own audit entry for every command —
        but each one reaches it as root, so it cannot know who asked. The panel&rsquo;s
        activity log carries the person, the address and the exact argv. Read together they
        are the whole story; either alone is half of one.
      </p>

      <H2 id="uninstall">Taking it away</H2>
      <p>
        <code>ratline-panel uninstall</code> stops the service, removes the unit and the
        vhost, and touches nothing ratline manages. The accounts database survives unless
        you pass <code>--purge</code>, because reinstalling without it means claiming the
        panel from scratch.
      </p>
    </article>
  );
}
