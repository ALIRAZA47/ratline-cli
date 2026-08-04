import { Link } from 'react-router-dom';
import type { Command } from '../data/types';
import { CodeBlock } from './CodeBlock';
import { FlagTable } from './FlagTable';
import { CopyButton, ExitChip, StatusBadge } from './ui';
import { Inline } from './Inline';
import { exitCodes } from '../data/globals';
import { anchoredFlags } from '../lib/flags';

const codeName = new Map(exitCodes.map((e) => [e.code, e.name]));

export function CommandEntry({ command }: { command: Command }) {
  const { groups } = anchoredFlags(command);
  const flagCount = groups.reduce((n, g) => n + g.flags.length, 0);
  const invocation = [command.name, command.args].filter(Boolean).join(' ');

  return (
    <section
      id={command.id}
      aria-labelledby={`${command.id}-heading`}
      className="scroll-mt-24 border-t border-line py-10 first:border-t-0 first:pt-2"
    >
      <div className="not-prose">
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-2">
          <h2 id={`${command.id}-heading`} className="text-xl font-semibold text-strong">
            <a href={`#${command.id}`} className="no-underline">
              <span className="font-mono">{command.name}</span>
            </a>
          </h2>
          <StatusBadge status={command.status} />
          <span className="ml-auto">
            <CopyButton text={invocation} />
          </span>
        </div>

        <p className="mt-2 max-w-[var(--container-measure)] text-base leading-relaxed text-muted">
          <Inline text={command.summary} />
        </p>

        <div className="scroll-thin mt-3 overflow-x-auto rounded-[var(--radius-card)] border border-line bg-code px-3.5 py-2.5">
          <code className="whitespace-pre font-mono text-xs md:text-sm">
            <span className="tok-cmd">ratline</span>
            <span>{command.name.replace(/^ratline/, '')}</span>
            {command.args && <span className="text-muted"> {command.args}</span>}
          </code>
        </div>
      </div>

      {command.description && (
        <div className="prose mt-5">
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
        <p className="not-prose mt-4 text-sm text-muted">
          No flags beyond the{' '}
          <Link to="/reference/global-flags" className="text-accent underline">
            global ones
          </Link>
          .
        </p>
      )}

      {command.refuses && (
        <div className="not-prose mt-6">
          <h3 className="mb-2 flex items-center gap-1.5 font-mono text-xs font-semibold uppercase tracking-wider text-muted">
            <span aria-hidden="true" className="text-danger">
              ✗
            </span>
            What it refuses, and why
          </h3>
          <ul className="max-w-[var(--container-measure)] space-y-2 text-sm leading-relaxed">
            {command.refuses.map((r, i) => (
              <li key={i} className="flex gap-2.5">
                <span aria-hidden="true" className="mt-[0.45em] size-1 shrink-0 rounded-full bg-danger" />
                <span>
                  <Inline text={r} />
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {command.exits && command.exits.length > 0 && (
        <div className="not-prose mt-6">
          <h3 className="mb-2 font-mono text-xs font-semibold uppercase tracking-wider text-muted">
            Exit codes
          </h3>
          <ul className="max-w-[42rem] space-y-1.5 text-sm">
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
        </div>
      )}

      {command.examples && command.examples.length > 0 && (
        <div className="mt-6">
          <h3 className="not-prose mb-2 font-mono text-xs font-semibold uppercase tracking-wider text-muted">
            Examples
          </h3>
          {command.examples.map((ex, i) => (
            <div key={i} className="mb-4">
              {ex.title && (
                <p className="not-prose mb-1 max-w-[var(--container-measure)] text-sm text-muted">
                  <Inline text={ex.title} />
                </p>
              )}
              <CodeBlock code={ex.code} lang={ex.lang} prompt={ex.lang === 'shell'} />
            </div>
          ))}
        </div>
      )}

      {command.seeAlso && (
        <p className="not-prose mt-5 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted">
          <span className="font-mono text-xs uppercase tracking-wider">See also</span>
          {command.seeAlso.map((s, i) => (
            <span key={s.to} className="flex items-center gap-2">
              {i > 0 && <span aria-hidden="true" className="text-faint">·</span>}
              <Link to={s.to} className="text-accent underline">
                {s.label}
              </Link>
            </span>
          ))}
        </p>
      )}
    </section>
  );
}
