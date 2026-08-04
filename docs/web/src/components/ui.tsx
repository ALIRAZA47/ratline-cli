import { Link } from 'react-router-dom';
import type { ReactNode } from 'react';
import { isValidElement, useEffect, useId, useState } from 'react';
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
        'inline-flex shrink-0 items-center gap-1 rounded-full border font-medium',
        size === 'xs' ? 'px-1.5 py-px text-2xs' : 'px-2 py-0.5 text-2xs',
        built
          ? 'border-[color-mix(in_oklab,var(--ok)_35%,transparent)] bg-ok-soft text-ok'
          : 'border-line-strong bg-sunken text-muted',
      ].join(' ')}
      title={
        built
          ? 'Implemented today.'
          : 'Specified, not yet implemented. Phase 1 — skeleton, config, logging, safe-exec, validators — is complete; everything else is being built in order.'
      }
    >
      <span aria-hidden="true" className={`size-1.5 rounded-full ${built ? 'bg-ok' : 'bg-faint'}`} />
      {built ? 'built' : 'planned'}
    </span>
  );
}

/* -------------------------------------------------------------------- Callout */

type Tone = 'note' | 'warn' | 'danger' | 'ok';

const TONE: Record<Tone, { border: string; bg: string; fg: string; label: string; sigil: string }> =
  {
    note: {
      border: 'border-[color-mix(in_oklab,var(--info)_30%,transparent)]',
      bg: 'bg-info-soft',
      fg: 'text-info',
      label: 'Note',
      sigil: '→',
    },
    ok: {
      border: 'border-[color-mix(in_oklab,var(--ok)_30%,transparent)]',
      bg: 'bg-ok-soft',
      fg: 'text-ok',
      label: 'Good',
      sigil: '✓',
    },
    warn: {
      border: 'border-[color-mix(in_oklab,var(--warn)_35%,transparent)]',
      bg: 'bg-warn-soft',
      fg: 'text-warn',
      label: 'Careful',
      sigil: '!',
    },
    danger: {
      border: 'border-[color-mix(in_oklab,var(--danger)_35%,transparent)]',
      bg: 'bg-danger-soft',
      fg: 'text-danger',
      label: 'Refused',
      sigil: '✗',
    },
  };

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
      className={`not-prose my-5 rounded-[var(--radius-card)] border ${t.border} ${t.bg} px-4 py-3`}
    >
      <p className={`mb-1 flex items-center gap-1.5 text-xs font-semibold ${t.fg}`}>
        <span aria-hidden="true" className="font-mono">
          {t.sigil}
        </span>
        {title ?? t.label}
      </p>
      <div className="max-w-[var(--container-measure)] text-sm leading-relaxed text-fg [&_a]:text-accent [&_a]:underline [&_code]:rounded [&_code]:border [&_code]:border-line [&_code]:bg-bg [&_code]:px-1 [&_code]:py-px [&_code]:text-[0.85em] [&_p+p]:mt-2">
        {children}
      </div>
    </aside>
  );
}

/* ----------------------------------------------------------------------- Tabs */

export function Tabs({
  tabs,
  label,
}: {
  tabs: { id: string; label: string; content: ReactNode }[];
  label: string;
}) {
  const [active, setActive] = useState(tabs[0]?.id);
  const base = useId();
  return (
    <div className="not-prose my-5">
      <div
        role="tablist"
        aria-label={label}
        className="flex flex-wrap gap-1 border-b border-line"
      >
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
                const i = tabs.findIndex((x) => x.id === active);
                if (e.key === 'ArrowRight') setActive(tabs[(i + 1) % tabs.length].id);
                if (e.key === 'ArrowLeft') setActive(tabs[(i - 1 + tabs.length) % tabs.length].id);
              }}
              className={[
                '-mb-px rounded-t px-3 py-1.5 text-sm font-medium transition-colors',
                selected
                  ? 'border-b-2 border-accent text-strong'
                  : 'border-b-2 border-transparent text-muted hover:text-fg',
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
    <h2 id={anchor} className="group scroll-mt-24">
      <a href={`#${anchor}`} className="!no-underline !text-[inherit]">
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
    <h3 id={anchor} className="group scroll-mt-24">
      <a href={`#${anchor}`} className="!no-underline !text-[inherit]">
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
    <dl className="not-prose my-5 grid gap-x-6 gap-y-2.5 rounded-[var(--radius-card)] border border-line bg-sunken px-4 py-3.5 text-sm sm:grid-cols-[max-content_1fr]">
      {rows.map(([k, v], i) => (
        <div key={i} className="contents">
          <dt className="font-mono text-xs text-muted sm:pt-0.5">{k}</dt>
          <dd className="text-fg [&_a]:text-accent [&_a]:underline [&_code]:font-mono [&_code]:text-[0.9em]">
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
    <div className="not-prose scroll-thin my-4 overflow-x-auto rounded-[var(--radius-card)] border border-line">
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
        <span className="font-medium text-strong">{title}</span>
        {meta}
        <span
          aria-hidden="true"
          className="ml-auto text-muted transition-transform group-hover:translate-x-0.5"
        >
          →
        </span>
      </span>
      {children && <span className="mt-1 block text-sm leading-relaxed text-muted">{children}</span>}
    </>
  );
  const cls =
    'group block rounded-[var(--radius-card)] border border-line bg-raised px-4 py-3 no-underline transition-colors hover:border-line-strong hover:bg-hover';
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
      className={`inline-flex size-6 shrink-0 items-center justify-center rounded border font-mono text-xs no-underline ${cls}`}
      title={`Exit code ${code}`}
    >
      {code}
    </Link>
  );
}

/* --------------------------------------------------------- Copy-to-clipboard */

export function CopyButton({ text, label = 'copy' }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = window.setTimeout(() => setCopied(false), 1600);
    return () => window.clearTimeout(t);
  }, [copied]);
  return (
    <button
      type="button"
      onClick={() => {
        navigator.clipboard?.writeText(text).then(
          () => setCopied(true),
          () => setCopied(false),
        );
      }}
      className="rounded border border-line px-1.5 py-0.5 font-mono text-2xs text-muted transition-colors hover:border-line-strong hover:bg-hover hover:text-strong"
    >
      {copied ? 'copied' : label}
    </button>
  );
}
