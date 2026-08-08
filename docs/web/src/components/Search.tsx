import { useEffect, useId, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import { kindLabel, search, type Doc } from '../lib/search';

const KIND_CLS: Record<Doc['kind'], string> = {
  command: 'bg-accent-soft text-accent',
  flag: 'bg-info-soft text-info',
  page: 'bg-sunken text-muted',
  topic: 'bg-sunken text-muted',
  setting: 'bg-ok-soft text-ok',
  exit: 'bg-warn-soft text-warn',
  rule: 'bg-sunken text-muted',
};

/** Things worth suggesting when the box is empty: one of each kind of answer. */
const STARTERS = ['--challenge', 'health_timeout', 'site add', 'rate limit', '502'];

/**
 * Wrap the parts of `text` that the query matched.
 *
 * Substring, case-insensitive, longest term first — so a query of `site add` marks both
 * words rather than marking `site` and then failing to find `add` inside the span it just
 * created.
 */
function mark(text: string, terms: string[]): ReactNode {
  if (terms.length === 0) return text;
  const lower = text.toLowerCase();
  const hits: [number, number][] = [];
  for (const term of terms) {
    let from = 0;
    for (;;) {
      const at = lower.indexOf(term, from);
      if (at < 0) break;
      hits.push([at, at + term.length]);
      from = at + term.length;
    }
  }
  if (hits.length === 0) return text;
  hits.sort((a, b) => a[0] - b[0]);

  const merged: [number, number][] = [];
  for (const [start, end] of hits) {
    const last = merged[merged.length - 1];
    if (last && start <= last[1]) last[1] = Math.max(last[1], end);
    else merged.push([start, end]);
  }

  const out: ReactNode[] = [];
  let at = 0;
  merged.forEach(([start, end], i) => {
    if (start > at) out.push(text.slice(at, start));
    out.push(
      <mark key={i} className="rounded-sm bg-[color-mix(in_oklab,var(--accent)_22%,transparent)] text-strong">
        {text.slice(start, end)}
      </mark>,
    );
    at = end;
  });
  if (at < text.length) out.push(text.slice(at));
  return out;
}

/**
 * Client-side search over the prebuilt index. Nothing is fetched: the index is
 * a module, built at load from the same data the pages render.
 *
 * Results are grouped by what they are — a command, one of its flags, a configuration
 * setting, an exit code — because the flat list mixed all seven kinds and a reader looking
 * for a flag had to read the badge on every row to find one. The keyboard cursor still runs
 * across the whole list in relevance order, so the grouping costs nothing to navigate: the
 * first result is still the best result, and Enter still opens it.
 */
export function SearchDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [query, setQuery] = useState('');
  const [cursor, setCursor] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const returnTo = useRef<HTMLElement | null>(null);
  const navigate = useNavigate();
  const base = useId();

  const results = useMemo(() => search(query), [query]);
  const terms = useMemo(
    () => query.trim().toLowerCase().split(/\s+/).filter(Boolean),
    [query],
  );

  /** Results grouped by kind, in the order the best hit of each kind appeared. */
  const groups = useMemo(() => {
    const out: { kind: Doc['kind']; docs: { doc: Doc; index: number }[] }[] = [];
    results.forEach((doc, index) => {
      const existing = out.find((g) => g.kind === doc.kind);
      if (existing) existing.docs.push({ doc, index });
      else out.push({ kind: doc.kind, docs: [{ doc, index }] });
    });
    return out;
  }, [results]);

  useEffect(() => {
    if (!open) return;
    returnTo.current = document.activeElement as HTMLElement | null;
    setQuery('');
    setCursor(0);
    const t = window.setTimeout(() => inputRef.current?.focus(), 10);
    return () => {
      window.clearTimeout(t);
      // Back to the button that opened it, so a keyboard reader is not dropped at the top
      // of the document every time they dismiss the dialog.
      returnTo.current?.focus();
    };
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = prev;
    };
  }, [open]);

  useEffect(() => setCursor(0), [query]);

  useEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>('[data-active="true"]');
    el?.scrollIntoView({ block: 'nearest' });
  }, [cursor, results]);

  if (!open) return null;

  const go = (doc: Doc) => {
    onClose();
    navigate(doc.to);
  };

  const activeId = results[cursor] ? `${base}-opt-${cursor}` : undefined;

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center px-4 pt-[8vh] pb-8"
      role="presentation"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div aria-hidden="true" className="fixed inset-0 bg-[oklch(20%_0.02_265_/_0.5)] backdrop-blur-[2px]" />
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Search the documentation"
        className="relative flex max-h-[76vh] w-full max-w-2xl flex-col overflow-hidden rounded-xl border border-line-strong bg-raised shadow-[var(--shadow-pop)]"
      >
        <div className="flex items-center gap-2.5 border-b border-line px-4 py-3">
          <svg
            width="15"
            height="15"
            viewBox="0 0 14 14"
            aria-hidden="true"
            fill="none"
            className="shrink-0 text-faint"
          >
            <circle cx="6" cy="6" r="4.25" stroke="currentColor" strokeWidth="1.5" />
            <path d="M9.5 9.5L12.5 12.5" stroke="currentColor" strokeWidth="1.5" />
          </svg>
          <input
            ref={inputRef}
            type="text"
            role="combobox"
            aria-expanded={results.length > 0}
            aria-controls={`${base}-listbox`}
            aria-activedescendant={activeId}
            aria-autocomplete="list"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') return onClose();
              if (results.length === 0) return;
              if (e.key === 'ArrowDown') {
                e.preventDefault();
                setCursor((c) => (c + 1) % results.length);
              }
              if (e.key === 'ArrowUp') {
                e.preventDefault();
                setCursor((c) => (c - 1 + results.length) % results.length);
              }
              if (e.key === 'Home') {
                e.preventDefault();
                setCursor(0);
              }
              if (e.key === 'End') {
                e.preventDefault();
                setCursor(results.length - 1);
              }
              if (e.key === 'Enter' && results[cursor]) {
                e.preventDefault();
                go(results[cursor]);
              }
            }}
            placeholder="Commands, flags, settings, exit codes…"
            aria-label="Search query"
            autoComplete="off"
            spellCheck={false}
            className="min-w-0 flex-1 bg-transparent text-base text-fg outline-none placeholder:text-faint"
          />
          <button
            type="button"
            onClick={onClose}
            className="rounded border border-line px-1.5 py-0.5 font-mono text-2xs text-muted hover:bg-hover hover:text-strong"
          >
            esc
          </button>
        </div>

        <div ref={listRef} className="scroll-thin min-h-0 flex-1 overflow-y-auto">
          {query.trim() === '' ? (
            <div className="px-4 py-5">
              <p className="max-w-[34rem] text-sm leading-relaxed text-muted">
                Every command, every flag, every configuration setting, the exit-code contract
                and the validation rules.
              </p>
              <p className="label mt-4 text-faint">Try</p>
              <div className="mt-2 flex flex-wrap gap-2">
                {STARTERS.map((s) => (
                  <button
                    key={s}
                    type="button"
                    onClick={() => {
                      setQuery(s);
                      inputRef.current?.focus();
                    }}
                    className="rounded-full border border-line bg-sunken px-2.5 py-1 font-mono text-xs text-muted transition-colors hover:border-accent hover:text-accent"
                  >
                    {s}
                  </button>
                ))}
              </div>
            </div>
          ) : results.length === 0 ? (
            <p className="px-4 py-6 text-sm leading-relaxed text-muted">
              Nothing matches “{query}”. If it is not in the command surface, it does not exist —
              this documentation does not describe commands that have not been specified.
            </p>
          ) : (
            <div role="listbox" id={`${base}-listbox`} aria-label="Search results" className="pb-1.5">
              {groups.map((group) => (
                <div key={group.kind} role="group" aria-labelledby={`${base}-grp-${group.kind}`}>
                  <p
                    id={`${base}-grp-${group.kind}`}
                    className="label sticky top-0 z-10 flex items-center gap-2 border-b border-line bg-sunken px-4 py-1.5 text-faint"
                  >
                    <span>{kindLabel[group.kind]}</span>
                    <span className="font-normal tracking-normal">·</span>
                    <span className="font-normal tracking-normal">{group.docs.length}</span>
                  </p>
                  <ul>
                    {group.docs.map(({ doc, index }) => (
                      <li key={`${doc.to}-${index}`}>
                        <button
                          type="button"
                          role="option"
                          id={`${base}-opt-${index}`}
                          aria-selected={index === cursor}
                          data-active={index === cursor}
                          onMouseEnter={() => setCursor(index)}
                          onClick={() => go(doc)}
                          tabIndex={-1}
                          className={[
                            'flex w-full items-start gap-3 border-l-2 px-4 py-2 text-left transition-colors',
                            index === cursor
                              ? 'border-accent bg-active'
                              : 'border-transparent hover:bg-hover',
                          ].join(' ')}
                        >
                          <span className="min-w-0 flex-1">
                            <span className="block truncate font-mono text-sm text-strong">
                              {mark(doc.title, terms)}
                            </span>
                            <span className="mt-0.5 block truncate text-xs text-muted">
                              {mark(doc.context, terms)}
                            </span>
                          </span>
                          {doc.status === 'planned' && (
                            <span className="mt-0.5 shrink-0 rounded border border-line-strong bg-sunken px-1.5 py-px font-mono text-2xs text-muted">
                              planned
                            </span>
                          )}
                          <span
                            aria-hidden="true"
                            className={`mt-0.5 shrink-0 rounded px-1.5 py-px text-2xs font-medium ${KIND_CLS[doc.kind]}`}
                          >
                            ↵
                          </span>
                        </button>
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            </div>
          )}
        </div>

        <div className="flex items-center gap-3 border-t border-line bg-sunken px-4 py-2 font-mono text-2xs text-muted">
          <span>↑↓ navigate</span>
          <span>↵ open</span>
          <span>esc close</span>
          <span className="ml-auto">
            {results.length > 0 && `${results.length} result${results.length === 1 ? '' : 's'}`}
          </span>
        </div>
      </div>
    </div>
  );
}
