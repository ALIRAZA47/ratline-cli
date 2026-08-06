import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, H2, TableScroll } from '../../components/ui';

export function ConceptInteractive() {
  return (
    <article>
      <PageHeader
        eyebrow="Concepts"
        title="Interactive mode"
        lede="A wizard that is a flag collector, never a second implementation — and which never appears in a place where nobody can answer it."
      />

      <div className="prose">
        <H2 id="triggers">When it appears</H2>
      </div>

      <TableScroll>
        <table className="w-full min-w-[42rem] border-collapse text-left text-sm">
          <caption className="sr-only">Interactive mode trigger rules</caption>
          <thead>
            <tr className="bg-sunken text-2xs uppercase tracking-wider text-muted">
              <th scope="col" className="w-[18rem] px-3 py-2 font-medium">
                Situation
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                What happens
              </th>
            </tr>
          </thead>
          <tbody>
            {[
              [
                <code key="a" className="font-mono text-xs">ratline</code>,
                'With no arguments, on a TTY: the main menu, with a live server summary — users, sites by runtime, failed units, certificates expiring soon.',
              ],
              [
                <code key="b" className="font-mono text-xs">ratline &lt;command&gt; -i</code>,
                'Asks which options to set, then runs it. Works on every command: the list comes from the command’s own flags. Four commands — user add, site add, cert issue, key add — have richer wizards that also suggest a runtime or fetch a key, and -i runs those instead.',
              ],
              [
                'Missing required flags, on a TTY',
                'It asks for them, one question each, and writes the answers into the flagset — so what runs is exactly what would have run had the flags been typed. Without a terminal it is unchanged: exit 2, naming every missing flag at once.',
              ],
              [
                <span key="c">
                  Not a TTY, or <code className="font-mono text-xs">--no-input</code>, or{' '}
                  <code className="font-mono text-xs">--yes</code>
                </span>,
                'Never prompts, under any circumstance. Fails with exit 2 naming every missing flag. A prompt in a CI pipeline is a hung build.',
              ],
              [
                <code key="d" className="font-mono text-xs">ratline init</code>,
                'The first-run server setup wizard — the one command that is interactive by nature.',
              ],
            ].map(([k, v], i) => (
              <tr key={i} className="border-t border-line align-top">
                <th scope="row" className="px-3 py-2.5 text-left font-normal">
                  {k}
                </th>
                <td className="px-3 py-2.5 leading-relaxed">{v}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableScroll>

      <Callout tone="danger" title="A prompt in a CI pipeline is a hung build">
        <p>
          This is the rule that everything else follows from. <code>--no-input</code> is{' '}
          <em>implied</em> when stdout is not a TTY, and a prompt needs a terminal on both ends — stdin
          to read and stderr to draw on. If either is missing, a command that needed input exits{' '}
          <Link to="/reference/exit-codes#code-10">10</Link> immediately rather than waiting forever for
          an answer that cannot come.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="echo">It always echoes the equivalent command</H2>
        <p>
          The wizard is a flag collector, not a second implementation: both paths call the same internal
          APIs, so there is no behaviour that only the wizard can produce and none it can skip. And it
          finishes with a summary panel of every resolved value plus the exact non-interactive
          invocation that reproduces it, with <code>[Run] [Copy] [Edit a field] [Cancel]</code>.
        </p>
        <p>
          That is how operators graduate from the wizard to scripting it. The first site is a
          conversation; the tenth is a line in a provisioning script that came out of the first one.
        </p>
      </div>

      <Terminal title="root@server">{`$ ratline site add api.example.com -i
~ ratline · new site
~
~   domain          api.example.com
~   owner           acme                        (5 sites)
~   runtime         python
~   app module      app.main:app
~   python          3.12                        (installed)
~   interface       ASGI                        (detected: FastAPI)
~   server          gunicorn + UvicornWorker
~   workers         5                           ((2 x 2 cores) + 1)
~   listen          unix socket
~   static          /static → staticfiles
~   TLS             letsencrypt                 (A record already points here)
~
~ Equivalent command:
~
~   ratline site add api.example.com --user acme --runtime python \\
~     --app-module app.main:app --python 3.12 --asgi --workers 5 \\
~     --static-url /static --static-dir staticfiles --ssl letsencrypt
~
~   [Run]  [Copy]  [Edit a field]  [Cancel]`}</Terminal>

      <div className="prose">
        <H2 id="missing-flags">The helpful failure</H2>
        <p>
          On a terminal, missing required flags produce an offer rather than a wall of usage text. In
          automation the same situation produces a precise, greppable error naming{' '}
          <em>every</em> missing flag at once — not the first one, which would mean four runs to
          discover four omissions.
        </p>
      </div>

      <Terminal title="on a terminal">{`$ ratline site add example.com
! Missing --user and --runtime. Run with -i for a guided setup, or see 'ratline site add --help'.`}</Terminal>

      <Terminal title="in CI, stdout is a pipe">{`$ ratline site add example.com | tee provision.log
✗ error: missing required flags: --user, --runtime
  see:  ratline site add --help
~ exit 2 — immediately, because --no-input is implied when stdout is not a TTY`}</Terminal>

      <div className="prose">
        <H2 id="destructive">Destructive confirmations</H2>
        <p>
          Never a bare <code>y/N</code>. A destructive operation shows a precise inventory of what will
          be deleted — paths, unit, certificate, port, state rows, home directory size — and requires the
          operator to type the domain or the username exactly.
        </p>
      </div>

      <Terminal title="root@server">{`$ ratline site delete api.example.com --purge
! This will permanently delete:
!
!   directory   /home/acme/api.example.com          412 MB
!   unit        ratline-acme-api_example_com.service (running)
!   vhost       /etc/nginx/sites-available/api.example.com.conf  + symlink
!   certificate api.example.com (valid, expires 2026-11-02)
!   socket      /run/ratline/acme-api_example_com/app.sock
!   logrotate   /etc/logrotate.d/ratline-api.example.com
!   state       1 site row, 3 env rows, 2 key grants
!
! Nothing is backed up unless you pass --backup.
!
Type "api.example.com" to confirm: api.exampel.com
✗ error: confirmation did not match "api.example.com"; nothing was changed
~ exit 2`}</Terminal>

      <div className="prose">
        <p>
          A mistyped confirmation is a successful outcome for this design. The whole point is that the
          only way to delete a site is to demonstrate you know which site you are deleting.
        </p>

        <H2 id="degradation">Colour and narrow terminals</H2>
        <ul>
          <li>
            <code>NO_COLOR</code> is respected, and so is <code>logging.color: never</code>.
          </li>
          <li>
            The interface degrades to plain line-based prompts when <code>TERM=dumb</code> or the
            terminal is narrower than 60 columns. A form that assumes 100 columns is unusable in a
            provider’s web console, which is exactly where you end up when something has gone wrong.
          </li>
          <li>
            <code>--json</code> disables colour unconditionally, because a JSON document with ANSI escape
            sequences in it is nobody’s idea of machine-readable.
          </li>
        </ul>

        <H2 id="refused">The refused combinations</H2>
        <p>
          Four flag pairs are usage errors rather than one silently winning — see{' '}
          <Link to="/reference/global-flags#refused">global flags</Link>. Two of them are about this
          page:
        </p>
      </div>

      <CodeBlock
        lang="text"
        code={`--json        --interactive   → --json exists for automation, which cannot answer prompts
--interactive --no-input      → one asks, the other forbids asking
--interactive --yes           → --yes suppresses every prompt, including the wizard's
--quiet       --verbose       → an operator who typed both has one of them wrong`}
        noCopy
      />

      <div className="prose">
        <p>
          Rather than picking a winner, ratline refuses. An operator who typed both has one of them
          wrong, and guessing which would be worse than saying so.
        </p>
      </div>
    </article>
  );
}
