import { Link } from 'react-router-dom';
import { CodeBlock } from '../components/CodeBlock';
import { FlagTable } from '../components/FlagTable';
import { PageHeader } from '../components/PageHeader';
import { Terminal } from '../components/Terminal';
import { Callout, H2, TableScroll } from '../components/ui';
import { globalFlags, refusedFlagPairs } from '../data/globals';
import { flatAnchoredFlags } from '../lib/flags';

/** The global flags reuse the command machinery so their anchors — and the search
 *  index entries that point at them — are produced exactly like any other flag. */
const globals = flatAnchoredFlags({
  id: 'global',
  name: 'Global flag',
  status: 'built',
  summary: '',
  flags: globalFlags,
});

export function GlobalFlags() {
  return (
    <article>
      <PageHeader
        eyebrow="Reference"
        title="Global flags"
        lede="Eight flags every command accepts, and the four combinations that are refused rather than resolved."
      />

      <FlagTable flags={globals} caption="Global" />

      <div className="prose">
        <H2 id="refused">Refused combinations</H2>
        <p>
          Each of these is a usage error — exit <Link to="/reference/exit-codes#code-2">2</Link> —
          rather than one flag silently winning. An operator who typed both has one of them wrong,
          and guessing which would be worse than saying so.
        </p>
      </div>

      <TableScroll>
        <table className="w-full min-w-[38rem] border-collapse text-left text-sm">
          <caption className="sr-only">
            Flag combinations that are refused as usage errors, and the message each produces
          </caption>
          <thead>
            <tr className="bg-sunken text-2xs uppercase tracking-wider text-muted">
              <th scope="col" className="w-[16rem] px-3 py-2 font-medium">
                Combination
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                Message, and the hint where there is one
              </th>
            </tr>
          </thead>
          <tbody>
            {refusedFlagPairs.map((p) => (
              <tr key={p.pair} className="border-t border-line align-top">
                <th scope="row" className="px-3 py-2.5 text-left font-normal">
                  <code className="font-mono text-xs text-danger">{p.pair}</code>
                </th>
                <td className="px-3 py-2.5">
                  <code className="font-mono text-xs">{p.message}</code>
                  {p.hint && (
                    <span className="mt-1 block border-l-2 border-line-strong pl-2.5 text-xs text-muted">
                      hint: {p.hint}
                    </span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableScroll>

      <Terminal title="ali@laptop">{`$ ratline version --quiet --verbose
✗ error: --quiet and --verbose contradict each other
  see:  ratline version --help
~ exit 2

$ ratline version --json --interactive
{
  "ok": false,
  "command": "ratline version",
  "version": "dev",
  "error": {
    "code": 2,
    "name": "usage",
    "message": "--json and --interactive contradict each other",
    "hint": "--json exists for automation, which cannot answer prompts"
  }
}
~ exit 2 — and note the error came back inside the envelope, because --json was asked for`}</Terminal>

      <div className="prose">
        <H2 id="implied">What is implied, and when</H2>
        <ul>
          <li>
            <code>--no-input</code> is implied when stdout is not a TTY. This is the rule that keeps
            a prompt from hanging a CI pipeline forever.
          </li>
          <li>
            <code>--yes</code> also implies <code>--no-input</code>, since a flag that answers every
            prompt has nothing left to prompt for.
          </li>
          <li>
            <code>--json</code> turns colour off unconditionally. So do <code>NO_COLOR</code>,{' '}
            <code>TERM=dumb</code> and <code>logging.color: never</code>.
          </li>
          <li>
            A prompt needs a terminal on <em>both</em> ends — stdin to read and stderr to draw on. If
            either is missing, a command that needed input exits{' '}
            <Link to="/reference/exit-codes#code-10">10</Link> rather than blocking.
          </li>
        </ul>

        <H2 id="dry-run">--dry-run in practice</H2>
        <p>
          It prints every mutation — files written, commands executed, permissions changed — and makes
          none of them. Reads still happen, so the preview reflects the real system rather than a
          guess about it: an existing user, a taken port and a missing entry point all show up.
        </p>
        <p>
          No lock is taken under <code>--dry-run</code>, so it is safe to run while another
          invocation is working.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site add example.com --user acme --runtime static --dry-run
ratline cert issue example.com --dry-run     # also skips the rate-limit cost
ratline reconcile --fix --dry-run`}
      />

      <div className="prose">
        <H2 id="config-path">Configuration path precedence</H2>
        <ol>
          <li>
            <code>--config &lt;path&gt;</code>
          </li>
          <li>
            <code>RATLINE_CONFIG</code>
          </li>
          <li>
            <code>/etc/ratline/config.yaml</code>
          </li>
        </ol>
        <p>
          A missing file is not an error. The built-in defaults are used, and mutating commands warn:{' '}
          <code>! no configuration file; using built-in defaults path=… fix="run 'ratline init'"</code>
          . Losing the configuration file must not stop an operator managing the server — and neither
          does losing the audit log, which is downgraded to a debug line rather than a failure.
        </p>
      </div>

      <Callout tone="note" title="Colour and narrow terminals">
        <p>
          <code>NO_COLOR</code> is respected. The interface degrades to plain line-based prompts when{' '}
          <code>TERM=dumb</code> or the terminal is narrower than 60 columns — the same threshold that
          turns the wizard’s form into sequential questions.
        </p>
      </Callout>
    </article>
  );
}
