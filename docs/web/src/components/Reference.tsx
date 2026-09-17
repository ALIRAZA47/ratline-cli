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
    <div className="not-prose mt-10 mb-1.5">
      {/* Set as a heading rather than as a micro-label. "What it refuses" and "Exit
          codes" are the questions a reader is actually scanning for on a command page,
          and eleven-pixel uppercase tracking renders them as captions on the thing above
          instead of as the sections they are. */}
      <h2 id={id} className="font-sans text-[0.9375rem] font-semibold text-strong">
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
    /* Two columns where there is room for two: the identifier and its metadata on the
       left, the meaning beside it. That is what makes a twenty-flag list scannable — the
       eye runs down one rail of names rather than down alternating name, prose, name,
       prose. Below `sm` the two stack, and it is the same element either way, so the
       anchor id is never on something `display: none` and a deep link always scrolls. */
    <div
      id={anchor}
      className={[
        'group grid scroll-mt-[calc(var(--header-h)+1.75rem)] gap-x-6 gap-y-1.5 py-3.5 pr-4',
        'sm:grid-cols-[minmax(0,12.5rem)_minmax(0,1fr)] target:bg-accent-soft',
        indent ? 'ml-4 border-l border-line pl-4' : 'pl-4',
      ].join(' ')}
    >
      <dt className="self-start">
        <span className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
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
          {pills}
        </span>
        {/* The default always on its own line, never flowed in after the type. Wrapping
            it only when it happened not to fit gave the column a ragged second line on
            some rows and not others, which is exactly the thing that stops a list of
            twenty flags reading as one rail. */}
        {meta && meta.length > 0 && (
          <span className="mt-1 block font-mono text-2xs text-faint">
            {meta.map(([k, v], i) => (
              <span key={k}>
                {i > 0 && ' · '}
                {k} <span className="text-muted">{v}</span>
              </span>
            ))}
          </span>
        )}
      </dt>
      <dd className="min-w-0 text-[0.9375rem] leading-relaxed text-fg">
        {children}
        {/* The anchor, revealed on hover. "Which flag was it that…" is the single most
            common thing anybody needs to send a colleague.

            Out of the tab order and hidden from assistive technology on purpose: the
            identifier itself is already a link to the same place, so leaving this one in
            would double every stop in a twenty-flag list to reach the same href twice. */}
        <a
          href={`#${anchor}`}
          aria-hidden="true"
          tabIndex={-1}
          className="ml-2 inline-block align-baseline font-mono text-xs text-faint no-underline opacity-0 transition-opacity hover:text-accent group-hover:opacity-100"
        >
          #
        </a>
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
