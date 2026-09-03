import type { ReactNode } from 'react';
import { ApiError } from '../lib/api';

/** A tone-carrying pill. The five tones are the only states this product has. */
export function Badge({
  tone = 'neutral',
  children,
}: {
  tone?: 'neutral' | 'ok' | 'warn' | 'danger' | 'accent';
  children: ReactNode;
}) {
  const tones: Record<string, string> = {
    neutral: 'bg-[var(--bg-sunken)] text-[var(--fg-muted)] border-[var(--border)]',
    ok: 'bg-[var(--ok-soft)] text-[var(--ok)] border-[var(--ok)]/25',
    warn: 'bg-[var(--warn-soft)] text-[var(--warn)] border-[var(--warn)]/25',
    danger: 'bg-[var(--danger-soft)] text-[var(--danger)] border-[var(--danger)]/25',
    accent: 'bg-[var(--accent-soft)] text-[var(--accent)] border-[var(--accent)]/25',
  };
  return (
    <span
      className={`inline-flex items-center gap-1 rounded-full border px-2 py-[0.1rem] text-2xs font-semibold ${tones[tone]}`}
    >
      {children}
    </span>
  );
}

/**
 * Every failure the interface shows goes through here.
 *
 * A ratline error is three things — what failed, its exit code, and what to do next
 * — and the third is the one people act on. An alert that renders only the message
 * throws away the half of the error that was written to be useful.
 */
export function ErrorBox({ error, title }: { error: ApiError | null; title?: string }) {
  if (!error) return null;
  return (
    <div
      role="alert"
      className="rounded-[var(--radius-card)] border border-[var(--danger)]/30 bg-[var(--danger-soft)] px-3.5 py-3 text-sm"
    >
      <div className="flex items-center gap-2">
        <span className="font-semibold text-[var(--danger)]">{title ?? 'That did not work'}</span>
        <Badge tone="danger">
          {error.name_} · exit {error.code}
        </Badge>
      </div>
      <p className="mt-1.5 text-[var(--fg)]">{error.message}</p>
      {error.hint && <p className="mt-1.5 text-xs text-[var(--fg-muted)]">→ {error.hint}</p>}
      {error.fields && Object.keys(error.fields).length > 0 && (
        <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-2xs text-[var(--fg-muted)]">
          {Object.entries(error.fields).map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="font-semibold">{k}</dt>
              <dd className="mono break-all">{v}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  );
}

export function Card({
  title,
  action,
  children,
  className = '',
}: {
  title?: ReactNode;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={`card ${className}`}>
      {(title || action) && (
        <header className="flex items-center justify-between gap-3 border-b border-[var(--border)] px-4 py-2.5">
          <h2 className="text-sm font-semibold">{title}</h2>
          {action}
        </header>
      )}
      <div className="p-4">{children}</div>
    </section>
  );
}

/**
 * The empty state.
 *
 * Named rather than improvised because the difference between "there is nothing
 * here" and "this failed and you were not told" is the whole reason people distrust
 * a dashboard.
 */
export function Empty({ children }: { children: ReactNode }) {
  return <p className="py-6 text-center text-sm text-[var(--fg-faint)]">{children}</p>;
}

export function Spinner({ label = 'Loading' }: { label?: string }) {
  return (
    <p className="py-6 text-center text-sm text-[var(--fg-faint)]" role="status">
      {label}…
    </p>
  );
}

export function Field({
  label,
  hint,
  children,
  required,
}: {
  label: string;
  hint?: ReactNode;
  children: ReactNode;
  required?: boolean;
}) {
  return (
    <label className="block">
      <span className="label">
        {label}
        {required && <span className="ml-1 text-[var(--danger)]">*</span>}
      </span>
      {children}
      {hint && <span className="hint mt-1 block">{hint}</span>}
    </label>
  );
}

/** A label/value block, the shape every detail page uses. */
export function Facts({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className="grid grid-cols-[minmax(7rem,auto)_1fr] gap-x-4 gap-y-1.5 text-sm">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-xs font-semibold text-[var(--fg-muted)] pt-[0.15rem]">{k}</dt>
          <dd className="break-words">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

/**
 * The argv the panel is about to run, or just ran.
 *
 * Shown everywhere on purpose. It is the difference between a web panel somebody
 * trusts and one they do not: the operation is a command they could have typed, they
 * can see exactly which one, and they can paste it into a terminal if they would
 * rather watch it there.
 */
export function Argv({ argv }: { argv: string[] | string | undefined }) {
  if (!argv || argv.length === 0) return null;
  const line = Array.isArray(argv) ? argv.join(' ') : argv;
  return (
    <pre className="mono overflow-x-auto rounded-md border border-[var(--border)] bg-[var(--bg-sunken)] px-3 py-2 text-2xs text-[var(--fg-muted)]">
      $ ratline {line.replace(/^ratline\s+/, '')}
    </pre>
  );
}

export function Table({ head, children }: { head: string[]; children: ReactNode }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-sm">
        <thead>
          <tr className="border-b border-[var(--border)]">
            {head.map((h) => (
              <th
                key={h}
                className="px-2 py-1.5 text-left text-2xs font-semibold uppercase tracking-wide text-[var(--fg-faint)]"
              >
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}

export function Row({ children }: { children: ReactNode }) {
  return <tr className="border-b border-[var(--border)] last:border-0 hover:bg-[var(--bg-hover)]">{children}</tr>;
}

export function Cell({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <td className={`px-2 py-2 align-top ${className}`}>{children}</td>;
}

/** Relative time, because "3 minutes ago" is what somebody watching a deploy wants. */
export function When({ at }: { at?: string }) {
  if (!at) return <span className="text-[var(--fg-faint)]">—</span>;
  const then = new Date(at);
  if (Number.isNaN(then.getTime())) return <span className="text-[var(--fg-faint)]">—</span>;
  const seconds = Math.round((Date.now() - then.getTime()) / 1000);
  const label = relative(seconds);
  return (
    <time dateTime={at} title={then.toLocaleString()} className="whitespace-nowrap">
      {label}
    </time>
  );
}

function relative(seconds: number): string {
  if (seconds < 0) return 'just now';
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  if (days < 30) return `${days}d ago`;
  return `${Math.round(days / 30)}mo ago`;
}

export function stateTone(state: string): 'ok' | 'warn' | 'danger' | 'neutral' | 'accent' {
  switch (state) {
    case 'done':
    case 'active':
    case 'running':
      return state === 'running' ? 'accent' : 'ok';
    case 'failed':
      return 'danger';
    case 'queued':
      return 'warn';
    default:
      return 'neutral';
  }
}
