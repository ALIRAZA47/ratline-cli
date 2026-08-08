import { Link } from 'react-router-dom';
import type { Command } from '../data/types';
import { CodeBlock, CodePanel, CodePanelLabel, PanelCopy } from './CodeBlock';
import { FlagTable } from './FlagTable';
import { ExitChip, StatusBadge } from './ui';
import { Inline } from './Inline';
import { exitCodes } from '../data/globals';
import { anchoredFlags } from '../lib/flags';
import { RefGroupHeading } from './Reference';

const codeName = new Map(exitCodes.map((e) => [e.code, e.name]));

/**
 * One command, in full: what it is, what it takes, what it refuses, how it exits.
 *
 * This is a whole page rather than one section of a stack. It used to be the latter, and
 * the group pages it stacked into ran to fourteen commands — long enough that nobody read
 * them and nobody could link to a single command either.
 *
 * The order is the order of the questions: what does it do, how do I call it, what can I
 * pass, what will it refuse, how do I branch on the result, show me. The synopsis is a
 * panel with its own copy button rather than a line of prose, because it is the one thing
 * on the page that goes straight into a terminal.
 */
export function CommandEntry({ command }: { command: Command }) {
  const { groups } = anchoredFlags(command);
  const flagCount = groups.reduce((n, g) => n + g.flags.length, 0);
  const invocation = [command.name, command.args].filter(Boolean).join(' ');

  return (
    <section id={command.id} aria-labelledby={`${command.id}-heading`} className="scroll-mt-24">
      <div className="not-prose">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <h1
            id={`${command.id}-heading`}
            className="font-mono text-2xl font-semibold tracking-tight text-strong"
          >
            {command.name}
          </h1>
          <StatusBadge status={command.status} />
        </div>

        <p className="mt-3 max-w-[var(--content-w)] text-lg leading-relaxed text-muted">
          <Inline text={command.summary} />
        </p>
      </div>

      <CodePanel
        label={<CodePanelLabel mono={false}>synopsis</CodePanelLabel>}
        right={<PanelCopy text={invocation} what="the invocation" />}
      >
        <div className="scroll-thin-dark overflow-x-auto">
          <pre className="px-4 py-3 text-xs leading-[1.75] md:text-[0.8125rem]">
            <code className="whitespace-pre">
              <span className="tok-cmd">ratline</span>
              <span className="text-panel-fg">{command.name.replace(/^ratline/, '')}</span>
              {command.args && <span className="tok-punct"> {command.args}</span>}
            </code>
          </pre>
        </div>
      </CodePanel>

      {command.description && (
        <div className="prose mt-6">
          {command.description.map((p, i) => (
            <p key={i}>
              <Inline text={p} />
            </p>
          ))}
        </div>
      )}

      {groups.map((g) => (
        <FlagTable key={g.title} flags={g.flags} caption={g.title} note={g.note} />
      ))}

      {flagCount === 0 && (
        <p className="not-prose mt-5 text-sm text-muted">
          No flags beyond the{' '}
          <Link to="/reference/global-flags" className="font-medium text-accent">
            global ones
          </Link>
          .
        </p>
      )}

      {command.refuses && (
        <>
          <RefGroupHeading title="What it refuses, and why" id="refuses" />
          <ul className="not-prose mt-3 max-w-[var(--content-w)] space-y-2.5 text-[0.9375rem] leading-relaxed">
            {command.refuses.map((r, i) => (
              <li key={i} className="flex gap-2.5">
                <span
                  aria-hidden="true"
                  className="mt-[0.5em] size-1.5 shrink-0 rounded-full bg-danger"
                />
                <span>
                  <Inline text={r} />
                </span>
              </li>
            ))}
          </ul>
        </>
      )}

      {command.exits && command.exits.length > 0 && (
        <>
          <RefGroupHeading title="Exit codes" id="exit-codes" />
          <ul className="not-prose mt-3 max-w-[var(--content-w)] space-y-2 text-[0.9375rem]">
            {command.exits.map((e) => (
              <li key={e.code} className="flex items-start gap-2.5">
                <ExitChip code={e.code} />
                <span className="pt-0.5 leading-relaxed">
                  <span className="font-mono text-xs text-muted">{codeName.get(e.code)}</span>
                  {' — '}
                  <Inline text={e.reason} />
                </span>
              </li>
            ))}
          </ul>
        </>
      )}

      {command.examples && command.examples.length > 0 && (
        <>
          <RefGroupHeading title="Examples" id="examples" />
          {command.examples.map((ex, i) => (
            <div key={i}>
              {ex.title && (
                <p className="not-prose mt-4 max-w-[var(--content-w)] text-[0.9375rem] leading-relaxed text-muted">
                  <Inline text={ex.title} />
                </p>
              )}
              <CodeBlock code={ex.code} lang={ex.lang} prompt={ex.lang === 'shell'} />
            </div>
          ))}
        </>
      )}

      {command.seeAlso && (
        <p className="not-prose mt-8 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted">
          <span className="label">See also</span>
          {command.seeAlso.map((s, i) => (
            <span key={s.to} className="flex items-center gap-2">
              {i > 0 && (
                <span aria-hidden="true" className="text-faint">
                  ·
                </span>
              )}
              <Link to={s.to} className="font-medium text-accent">
                {s.label}
              </Link>
            </span>
          ))}
        </p>
      )}
    </section>
  );
}
