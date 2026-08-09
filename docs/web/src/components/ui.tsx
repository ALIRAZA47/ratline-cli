import { Link } from 'react-router-dom';
import type { ReactNode } from 'react';
import { isValidElement, useId, useState } from 'react';
import type { Status } from '../data/types';
import { slugify } from '../lib/slug';

/* ---------------------------------------------------------------- StatusBadge */

/**
 * Build status, as marked in the command surface. The CLI is under active
 * construction and the docs must not claim more than exists, so every command
 * carries one of these.
 */
export function StatusBadge({ status, size = 'sm' }: { status: Status; size?: 'sm' | 'xs' }) {
  const built = status === 'built';
  return (
    <span
      className={[
        'inline-flex shrink-0 items-center gap-1.5 rounded-full border font-medium',
        size === 'xs' ? 'px-1.5 py-px text-2xs' : 'px-2 py-0.5 text-2xs',
        built
          ? 'border-[color-mix(in_oklab,var(--ok)_35%,transparent)] bg-ok-soft text-ok'
          : 'border-line-strong bg-sunken text-muted',
      ].join(' ')}
      title={
        built
          ? 'Implemented today.'
          : 'Specified, not yet implemented.'
      }
    >
      <span aria-hidden="true" className={`size-1.5 rounded-full ${built ? 'bg-ok' : 'bg-faint'}`} />
      {built ? 'built' : 'planned'}
    </span>
  );
}

/* -------------------------------------------------------------------- Callout */

type Tone = 'note' | 'warn' | 'danger' | 'ok';

const TONE: Record<
  Tone,
  { rule: string; bg: string; fg: string; label: string; icon: ReactNode }
> = {
  note: {
    rule: 'border-l-[var(--info)]',
    bg: 'bg-info-soft',
    fg: 'text-info',
    label: 'Note',
    icon: (
      <svg width="13" height="13" viewBox="0 0 13 13" aria-hidden="true" fill="none">
        <circle cx="6.5" cy="6.5" r="5.4" stroke="currentColor" strokeWidth="1.3" />
        <path d="M6.5 5.6v4M6.5 3.4v.9" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      </svg>
    ),
  },
  ok: {
    rule: 'border-l-[var(--ok)]',
    bg: 'bg-ok-soft',
    fg: 'text-ok',
    label: 'Good',
    icon: (
      <svg width="13" height="13" viewBox="0 0 13 13" aria-hidden="true" fill="none">
        <circle cx="6.5" cy="6.5" r="5.4" stroke="currentColor" strokeWidth="1.3" />
        <path
          d="M4.2 6.7l1.7 1.7 3.1-3.6"
          stroke="currentColor"
          strokeWidth="1.4"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    ),
  },
  warn: {
    rule: 'border-l-[var(--warn)]',
    bg: 'bg-warn-soft',
    fg: 'text-warn',
    label: 'Careful',
    icon: (
      <svg width="13" height="13" viewBox="0 0 13 13" aria-hidden="true" fill="none">
        <path
          d="M6.5 1.4l5.1 9.2H1.4z"
          stroke="currentColor"
          strokeWidth="1.3"
          strokeLinejoin="round"
        />
        <path d="M6.5 5v2.5M6.5 8.9v.7" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      </svg>
    ),
  },
  danger: {
    rule: 'border-l-[var(--danger)]',
    bg: 'bg-danger-soft',
    fg: 'text-danger',
    label: 'Refused',
    icon: (
      <svg width="13" height="13" viewBox="0 0 13 13" aria-hidden="true" fill="none">
        <circle cx="6.5" cy="6.5" r="5.4" stroke="currentColor" strokeWidth="1.3" />
        <path d="M4.5 4.5l4 4M8.5 4.5l-4 4" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      </svg>
    ),
  },
};

/**
 * A note, a warning, a refusal.
 *
 * A 3px rule in the semantic hue carries the meaning; the tint is faint enough that a page
 * with three of them still reads as a page rather than as a set of boxes. Colour is never
 * the only signal — the glyph and the label say the same thing.
 */
export function Callout({
  tone = 'note',
  title,
  children,
}: {
  tone?: Tone;
  title?: string;
  children: ReactNode;
}) {
  const t = TONE[tone];
  return (
    <aside
      className={`not-prose my-6 max-w-[var(--content-w)] rounded-r-[var(--radius-card)] border-y border-r border-line border-l-[3px] ${t.rule} ${t.bg} px-4 py-3`}
    >
      <p className={`mb-1.5 flex items-center gap-2 text-xs font-semibold ${t.fg}`}>
        <span aria-hidden="true" className="shrink-0">
          {t.icon}
        </span>
        {title ?? t.label}
      </p>
      <div className="text-[0.9375rem] leading-relaxed text-fg [&_a]:font-medium [&_a]:text-accent [&_a]:underline [&_a]:decoration-[color-mix(in_oklab,var(--accent)_35%,transparent)] [&_a]:underline-offset-2 [&_code]:rounded [&_code]:border [&_code]:border-line [&_code]:bg-[color-mix(in_oklab,var(--bg-raised)_70%,transparent)] [&_code]:px-1 [&_code]:py-px [&_code]:text-[0.85em] [&_p+p]:mt-2.5">
        {children}
      </div>
    </aside>
  );
}

/* ----------------------------------------------------------------------- Tabs */

/** Content in several forms. For code specifically, prefer `CodeTabs`. */
export function Tabs({
  tabs,
  label,
}: {
  tabs: { id: string; label: string; content: ReactNode }[];
  label: string;
}) {
  const [active, setActive] = useState(tabs[0]?.id);
  const base = useId();

  const move = (delta: number) => {
    const i = tabs.findIndex((x) => x.id === active);
    const next = tabs[(i + delta + tabs.length) % tabs.length];
    setActive(next.id);
    document.getElementById(`${base}-tab-${next.id}`)?.focus();
  };

  return (
    <div className="not-prose my-6">
      <div role="tablist" aria-label={label} className="flex flex-wrap gap-0.5 border-b border-line">
        {tabs.map((t) => {
          const selected = t.id === active;
          return (
            <button
              key={t.id}
              role="tab"
              id={`${base}-tab-${t.id}`}
              aria-selected={selected}
              aria-controls={`${base}-panel-${t.id}`}
              tabIndex={selected ? 0 : -1}
              onClick={() => setActive(t.id)}
              onKeyDown={(e) => {
                if (e.key === 'ArrowRight') {
                  e.preventDefault();
                  move(1);
                }
                if (e.key === 'ArrowLeft') {
                  e.preventDefault();
                  move(-1);
                }
              }}
              className={[
                '-mb-px border-b-2 px-3 py-2 text-sm transition-colors',
                selected
                  ? 'border-accent font-semibold text-accent'
                  : 'border-transparent font-medium text-muted hover:text-strong',
              ].join(' ')}
            >
              {t.label}
            </button>
          );
        })}
      </div>
      {tabs.map((t) => (
        <div
          key={t.id}
          role="tabpanel"
          id={`${base}-panel-${t.id}`}
          aria-labelledby={`${base}-tab-${t.id}`}
          hidden={t.id !== active}
          className="pt-3"
        >
          {t.id === active && t.content}
        </div>
      ))}
    </div>
  );
}

/* ------------------------------------------------------------- Anchor heading */

/** Flatten a heading's children to text so an anchor can be derived from it. */
function textOf(node: ReactNode): string {
  if (node === null || node === undefined || typeof node === 'boolean') return '';
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(textOf).join('');
  if (isValidElement<{ children?: ReactNode }>(node)) return textOf(node.props.children);
  return '';
}

export function H2({ children, id }: { children: ReactNode; id?: string }) {
  const anchor = id ?? slugify(textOf(children));
  return (
    <h2 id={anchor} className="group">
      <a href={`#${anchor}`} className="heading-anchor">
        {children}
        <span
          aria-hidden="true"
          className="ml-2 align-middle font-mono text-sm text-faint opacity-0 transition-opacity group-hover:opacity-100"
        >
          #
        </span>
      </a>
    </h2>
  );
}

export function H3({ children, id }: { children: ReactNode; id?: string }) {
  const anchor = id ?? slugify(textOf(children));
  return (
    <h3 id={anchor} className="group">
      <a href={`#${anchor}`} className="heading-anchor">
        {children}
        <span
          aria-hidden="true"
          className="ml-2 align-middle font-mono text-xs text-faint opacity-0 transition-opacity group-hover:opacity-100"
        >
          #
        </span>
      </a>
    </h3>
  );
}

/* ---------------------------------------------------------------- Definitions */

/** A key/value grid, for "at a glance" panels. */
export function Facts({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className="not-prose my-6 grid max-w-[var(--content-w)] gap-x-6 gap-y-2.5 rounded-[var(--radius-card)] border border-line bg-sunken px-4 py-3.5 text-sm sm:grid-cols-[max-content_1fr]">
      {rows.map(([k, v], i) => (
        <div key={i} className="contents">
          <dt className="font-mono text-xs text-muted sm:pt-1">{k}</dt>
          <dd className="text-fg [&_a]:font-medium [&_a]:text-accent [&_a]:underline [&_a]:underline-offset-2 [&_code]:font-mono [&_code]:text-[0.9em]">
            {v}
          </dd>
        </div>
      ))}
    </dl>
  );
}

/* ------------------------------------------------------------- Scrolling table */

/** Wraps a wide table so it scrolls inside its own container, never the page. */
export function TableScroll({ children }: { children: ReactNode }) {
  return (
    <div className="not-prose scroll-thin my-5 overflow-x-auto rounded-[var(--radius-card)] border border-line bg-raised">
      {children}
    </div>
  );
}

/* ------------------------------------------------------------------ Card link */

export function CardLink({
  to,
  title,
  children,
  meta,
}: {
  to: string;
  title: string;
  children?: ReactNode;
  meta?: ReactNode;
}) {
  const external = to.startsWith('http');
  const inner = (
    <>
      <span className="flex items-center gap-2">
        <span className="font-medium text-strong group-hover:text-accent">{title}</span>
        {meta}
        <span
          aria-hidden="true"
          className="ml-auto text-faint transition-transform group-hover:translate-x-0.5 group-hover:text-accent"
        >
          →
        </span>
      </span>
      {children && (
        <span className="mt-1 block text-sm leading-relaxed text-muted">{children}</span>
      )}
    </>
  );
  const cls =
    'group block rounded-[var(--radius-card)] border border-line bg-raised px-4 py-3 no-underline shadow-[var(--shadow-card)] transition-colors hover:border-accent hover:bg-hover';
  return external ? (
    <a href={to} className={cls}>
      {inner}
    </a>
  ) : (
    <Link to={to} className={cls}>
      {inner}
    </Link>
  );
}

/* ---------------------------------------------------------------- Exit chips */

export function ExitChip({ code }: { code: number }) {
  const tone =
    code === 0 ? 'ok' : code === 2 || code === 10 ? 'warn' : code === 6 ? 'danger' : 'muted';
  const cls = {
    ok: 'border-[color-mix(in_oklab,var(--ok)_35%,transparent)] bg-ok-soft text-ok',
    warn: 'border-[color-mix(in_oklab,var(--warn)_35%,transparent)] bg-warn-soft text-warn',
    danger: 'border-[color-mix(in_oklab,var(--danger)_35%,transparent)] bg-danger-soft text-danger',
    muted: 'border-line-strong bg-sunken text-muted',
  }[tone];
  return (
    <Link
      to={`/reference/exit-codes#code-${code}`}
      className={`inline-flex size-6 shrink-0 items-center justify-center rounded border font-mono text-xs font-medium no-underline ${cls}`}
      title={`Exit code ${code}`}
    >
      {code}
    </Link>
  );
}

/* --------------------------------------------------------------------- Kbd */

/** A key, as the reader would press it. */
export function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="rounded border border-line bg-sunken px-1.5 py-px font-mono text-2xs text-muted">
      {children}
    </kbd>
  );
}
