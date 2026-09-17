import type { ReactNode } from 'react';

/**
 * Anything ratline returned, rendered as something a person can read.
 *
 * The panel shows values it did not choose the shape of: `ratline site show` returns
 * whatever that release of ratline returns, and a new field can appear in any
 * release. The first version of this handed an object to `JSON.stringify` and put
 * the result in a `<code>` — which is how a site's page came to carry a twenty-field
 * certificate as one unbreakable line that stretched the card past the window.
 *
 * So the rule here is general rather than a list of known fields: an object becomes
 * labelled rows, a list becomes chips, a timestamp becomes a date somebody can act
 * on, and anything else becomes text that wraps. A field nobody has named yet still
 * arrives readable, which is what stops JSON coming back the first time ratline adds
 * one.
 */

/** Names worth saying properly, where the wire name is not what a person calls it. */
const labels: Record<string, string> = {
  not_after: 'Expires',
  not_before: 'Issued',
  auto_renew: 'Renews automatically',
  key_type: 'Key',
  cert_path: 'Certificate file',
  key_path: 'Key file',
  chain_path: 'Chain file',
  last_renewal_at: 'Last renewal',
  last_renewal_status: 'Last renewal',
  last_renewal_error: 'Last renewal failed with',
  consecutive_failures: 'Failures in a row',
  created_at: 'Created',
  updated_at: 'Updated',
  last_deploy_at: 'Last deployed',
  dns_provider: 'DNS provider',
  app_module: 'App module',
  attached_sites: 'Used by',
  days_remaining: 'Days left',
};

/** A wire name as a person would say it: `not_after` → Expires, `app_dir` → App dir. */
export function labelFor(key: string): string {
  if (labels[key]) return labels[key];
  const words = key.replace(/[_-]+/g, ' ').trim();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/**
 * Nesting past this is a shape nobody designed for, and drawing it as rows would
 * make a page out of one field. It is shown as text instead — still wrapping, still
 * not JSON to look at, just not pretending to be structured.
 */
const MAX_DEPTH = 3;

const ISO_DATE = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/;

export function Value({ value, depth = 0 }: { value: unknown; depth?: number }): ReactNode {
  if (value === null || value === undefined || value === '') {
    return <span className="text-[var(--fg-faint)]">Not set</span>;
  }

  if (typeof value === 'boolean') {
    return <span>{value ? 'Yes' : 'No'}</span>;
  }

  if (typeof value === 'number') {
    return <span className="tabular-nums">{value.toLocaleString()}</span>;
  }

  if (typeof value === 'string') {
    if (ISO_DATE.test(value)) return <DateValue at={value} />;
    // `break-anywhere` rather than `break-words`: a fingerprint or a path has no
    // spaces to break at, and without this the column is as wide as the value.
    return <span className="[overflow-wrap:anywhere]">{value}</span>;
  }

  if (Array.isArray(value)) {
    if (value.length === 0) return <span className="text-[var(--fg-faint)]">None</span>;
    // A list of plain values reads as chips; a list of objects is rows, numbered
    // only because the position is the only name its items have.
    if (value.every((v) => v === null || typeof v !== 'object')) {
      return (
        <span className="flex flex-wrap gap-1">
          {value.map((v, i) => (
            <span
              key={i}
              className="inline-flex items-center rounded-full border border-[var(--border)] bg-[var(--bg-sunken)] px-2 py-[0.1rem] text-2xs font-semibold text-[var(--fg-muted)] [overflow-wrap:anywhere]"
            >
              {String(v)}
            </span>
          ))}
        </span>
      );
    }
    if (depth >= MAX_DEPTH) return <Deep value={value} />;
    return (
      <span className="flex flex-col gap-2">
        {value.map((v, i) => (
          <span key={i} className="min-w-0">
            <span className="mb-0.5 block text-2xs font-semibold uppercase tracking-wide text-[var(--fg-faint)]">
              {i + 1}
            </span>
            <Value value={v} depth={depth + 1} />
          </span>
        ))}
      </span>
    );
  }

  if (typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>).filter(
      ([, v]) => v !== null && v !== undefined && v !== '' && !(Array.isArray(v) && v.length === 0),
    );
    if (entries.length === 0) return <span className="text-[var(--fg-faint)]">Not set</span>;
    if (depth >= MAX_DEPTH) return <Deep value={value} />;
    return (
      <span className="grid grid-cols-[minmax(5rem,auto)_minmax(0,1fr)] gap-x-3 gap-y-1">
        {entries.map(([k, v]) => (
          <span key={k} className="contents">
            <span className="text-2xs text-[var(--fg-faint)] [overflow-wrap:anywhere]">
              {labelFor(k)}
            </span>
            <span className="min-w-0 text-xs">
              <Value value={v} depth={depth + 1} />
            </span>
          </span>
        ))}
      </span>
    );
  }

  return <span className="[overflow-wrap:anywhere]">{String(value)}</span>;
}

/**
 * A date, said twice: the day it is, and how far away that is.
 *
 * "2026-12-04T04:11:06Z" is the fact; "in 78 days" is the one somebody acts on, and
 * neither is much use on its own when the question is whether a certificate is about
 * to lapse.
 */
function DateValue({ at }: { at: string }) {
  const then = new Date(at);
  if (Number.isNaN(then.getTime())) {
    return <span className="[overflow-wrap:anywhere]">{at}</span>;
  }
  const absolute = then.toLocaleDateString(undefined, {
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  });
  return (
    <time dateTime={at} title={then.toLocaleString()}>
      {absolute}
      <span className="text-[var(--fg-muted)]"> — {awayFrom(then)}</span>
    </time>
  );
}

function awayFrom(then: Date): string {
  const days = Math.round((then.getTime() - Date.now()) / 86400000);
  if (days === 0) return 'today';
  const n = Math.abs(days);
  if (n < 45) return days > 0 ? `in ${n} ${n === 1 ? 'day' : 'days'}` : `${n} ${n === 1 ? 'day' : 'days'} ago`;
  const months = Math.round(n / 30);
  if (months < 24) return days > 0 ? `in ${months} months` : `${months} months ago`;
  const years = Math.round(n / 365);
  return days > 0 ? `in ${years} years` : `${years} years ago`;
}

/**
 * The floor of the recursion: shown as indented text rather than rows.
 *
 * Still not something to parse by eye — it wraps, it is not on one line, and it
 * cannot widen anything — but a shape this deep is rare enough that inventing a
 * layout for it would be designing for a case nobody has.
 */
function Deep({ value }: { value: unknown }) {
  return (
    <span className="mono block max-h-40 overflow-y-auto whitespace-pre-wrap break-words text-2xs text-[var(--fg-muted)]">
      {readable(value)}
    </span>
  );
}

/** Indented `key: value` lines — the shape, without the braces and quotes. */
function readable(value: unknown, indent = 0): string {
  const pad = '  '.repeat(indent);
  if (value === null || value === undefined) return `${pad}not set`;
  if (Array.isArray(value)) {
    return value.map((v) => `${pad}- ${readable(v, indent + 1).trimStart()}`).join('\n');
  }
  if (typeof value === 'object') {
    return Object.entries(value as Record<string, unknown>)
      .map(([k, v]) =>
        v !== null && typeof v === 'object'
          ? `${pad}${labelFor(k)}\n${readable(v, indent + 1)}`
          : `${pad}${labelFor(k)}: ${String(v)}`,
      )
      .join('\n');
  }
  return `${pad}${String(value)}`;
}
