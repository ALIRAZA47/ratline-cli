import type { ReactNode } from 'react';

/**
 * The reference row: one flag, one exit code, one configuration setting.
 *
 * This is the shape an API reference has, and for the same reason. A four-column table put
 * the name, the type, the default and the description in competition for a column width
 * that had to suit all four, so the descriptions — the part with the actual information —
 * were squeezed into whatever was left, and the whole thing had to be re-laid-out or
 * abandoned as the column narrowed. Here the identifier and its metadata are one line of
 * monospace and the description gets the full measure underneath, which is legible at any
 * width and needs no second rendering for narrow screens.
 *
 * That last point is not cosmetic. The table version rendered a second, stacked copy for
 * narrow columns and chose between them by measuring, because putting the anchor ids on a
 * `display: none` element breaks deep links — a link to a hidden element does not scroll.
 * One row, always in the DOM, has no such problem.
 */

export type PillTone = 'required' | 'neutral' | 'ok' | 'danger';

const PILL: Record<PillTone, string> = {
  required:
    'border-[color-mix(in_oklab,var(--warn)_35%,transparent)] bg-warn-soft text-warn',
  danger:
    'border-[color-mix(in_oklab,var(--danger)_35%,transparent)] bg-danger-soft text-danger',
  ok: 'border-[color-mix(in_oklab,var(--ok)_35%,transparent)] bg-ok-soft text-ok',
  neutral: 'border-line-strong bg-sunken text-muted',
};

export function Pill({ tone = 'neutral', children }: { tone?: PillTone; children: ReactNode }) {
  return (
    <span
      className={`shrink-0 rounded border px-1.5 py-px text-2xs font-medium ${PILL[tone]}`}
    >
      {children}
    </span>
  );
}

/** A list of reference rows: hairline-separated, one framed block. */
export function RefList({
  label,
  children,
  className = '',
}: {
  label?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <dl
      aria-label={label}
      className={`not-prose my-4 divide-y divide-[var(--border)] overflow-hidden rounded-[var(--radius-card)] border border-line bg-raised ${className}`}
    >
      {children}
    </dl>
  );
}

/**
 * The heading above a list — a flag group's title, and the note that explains what the
 * group is for.
 *
 * A level two, not a level three: on a command page these are the page's own sections, and
 * the page's only other heading is its h1. Styling it as a small tracked label does not
 * change what it is, and skipping a level to get the look would break the outline for
 * anyone navigating by heading.
 */
export function RefGroupHeading({
  title,
  children,
  id,
}: {
  title: string;
  children?: ReactNode;
  id?: string;
}) {
  return (
    <div className="not-prose mt-9 mb-1">
      <h2 id={id} className="label text-muted">
        {title}
      </h2>
      {children && (
        <p className="mt-1.5 max-w-[var(--content-w)] text-sm leading-relaxed text-muted">
          {children}
        </p>
      )}
    </div>
  );
}

export function RefRow({
  anchor,
  name,
  /** A value placeholder after the name: `<domain>`, `<static|node|python>`. */
  arg,
  /** The type, set beside the name the way an API reference sets it. */
  type,
  /** Extra key/value metadata on the same line: `default`, `raised by`. */
  meta,
  pills,
  /** Rendered instead of `name` when the identifier needs its own markup. */
  lead,
  /** A field nested under the row above it: `error.hint` under `error`. */
  indent,
  children,
}: {
  anchor: string;
  name?: string;
  arg?: string;
  type?: ReactNode;
  meta?: [string, ReactNode][];
  pills?: ReactNode;
  lead?: ReactNode;
  indent?: boolean;
  children: ReactNode;
}) {
  return (
    <div
      id={anchor}
      className={[
        'group scroll-mt-[calc(var(--header-h)+1.75rem)] py-3.5 pr-4 target:bg-accent-soft',
        indent ? 'ml-4 border-l border-line pl-4' : 'pl-4',
      ].join(' ')}
    >
      <dt className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1.5">
        {lead ?? (
          <a
            href={`#${anchor}`}
            className="font-mono text-[0.8125rem] font-semibold whitespace-nowrap text-strong no-underline hover:text-accent"
          >
            {name}
          </a>
        )}
        {arg && <span className="font-mono text-[0.8125rem] break-words text-muted">{arg}</span>}
        {type && <span className="font-mono text-2xs text-faint">{type}</span>}
        {meta?.map(([k, v]) => (
          <span key={k} className="font-mono text-2xs text-faint">
            {k} <span className="text-muted">{v}</span>
          </span>
        ))}
        {pills}
        {/* The anchor, revealed on hover. "Which flag was it that…" is the single most
            common thing anybody needs to send a colleague.

            Out of the tab order and hidden from assistive technology on purpose: the
            identifier itself is already a link to the same place, so leaving this one in
            would double every stop in a twenty-flag list to reach the same href twice. */}
        <a
          href={`#${anchor}`}
          aria-hidden="true"
          tabIndex={-1}
          className="ml-auto shrink-0 font-mono text-xs text-faint no-underline opacity-0 transition-opacity hover:text-accent group-hover:opacity-100"
        >
          #
        </a>
      </dt>
      <dd className="mt-1.5 max-w-[var(--content-w)] text-[0.9375rem] leading-relaxed text-fg">
        {children}
      </dd>
    </div>
  );
}

/** A second paragraph under a row: the trap, the reason, the refusal. */
export function RefNote({ children }: { children: ReactNode }) {
  return (
    <span className="mt-2 block border-l-2 border-line-strong pl-3 text-sm leading-relaxed text-muted">
      {children}
    </span>
  );
}
