import { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { kindLabel, search, type Doc } from '../lib/search';

const KIND_CLS: Record<Doc['kind'], string> = {
  command: 'bg-accent-soft text-accent',
  flag: 'bg-info-soft text-info',
  page: 'bg-sunken text-muted',
  setting: 'bg-ok-soft text-ok',
  exit: 'bg-warn-soft text-warn',
  rule: 'bg-sunken text-muted',
};

/**
 * Client-side search over the prebuilt index. Nothing is fetched: the index is
 * a module, built at load from the same data the pages render.
 */
export function SearchDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [query, setQuery] = useState('');
  const [cursor, setCursor] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);
  const navigate = useNavigate();

  const results = useMemo(() => search(query), [query]);

  useEffect(() => {
    if (!open) return;
    setQuery('');
    setCursor(0);
    const t = window.setTimeout(() => inputRef.current?.focus(), 10);
    return () => window.clearTimeout(t);
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

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center px-4 pt-[8vh] pb-8"
      role="presentation"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div aria-hidden="true" className="fixed inset-0 bg-[oklch(20%_0.02_255_/_0.45)]" />
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Search the documentation"
        className="relative flex max-h-[76vh] w-full max-w-2xl flex-col overflow-hidden rounded-xl border border-line-strong bg-raised shadow-[var(--shadow-pop)]"
      >
        <div className="flex items-center gap-2.5 border-b border-line px-4 py-3">
          <span aria-hidden="true" className="font-mono text-sm text-faint">
            /
          </span>
          <input
            ref={inputRef}
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') return onClose();
              if (e.key === 'ArrowDown') {
                e.preventDefault();
                setCursor((c) => Math.min(c + 1, results.length - 1));
              }
              if (e.key === 'ArrowUp') {
                e.preventDefault();
                setCursor((c) => Math.max(c - 1, 0));
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
            className="rounded border border-line px-1.5 py-0.5 font-mono text-2xs text-muted hover:bg-hover"
          >
            esc
          </button>
        </div>

        <div className="scroll-thin min-h-0 flex-1 overflow-y-auto">
          {query.trim() === '' ? (
            <p className="px-4 py-6 text-sm text-muted">
              Search every command, every flag, every configuration setting, the exit-code
              contract and the validation rules. Try{' '}
              <code className="rounded border border-line bg-code px-1 text-xs">--challenge</code>,{' '}
              <code className="rounded border border-line bg-code px-1 text-xs">health_timeout</code>{' '}
              or <code className="rounded border border-line bg-code px-1 text-xs">502</code>.
            </p>
          ) : results.length === 0 ? (
            <p className="px-4 py-6 text-sm text-muted">
              Nothing matches “{query}”. If it is not in the command surface, it does not exist —
              this documentation does not describe commands that have not been specified.
            </p>
          ) : (
            <ul ref={listRef} className="py-1.5">
              {results.map((doc, i) => (
                <li key={`${doc.kind}-${doc.to}-${i}`}>
                  <button
                    type="button"
                    data-active={i === cursor}
                    onMouseEnter={() => setCursor(i)}
                    onClick={() => go(doc)}
                    className={[
                      'flex w-full items-start gap-3 px-4 py-2 text-left transition-colors',
                      i === cursor ? 'bg-active' : 'hover:bg-hover',
                    ].join(' ')}
                  >
                    <span
                      className={`mt-0.5 shrink-0 rounded px-1.5 py-px text-2xs font-medium ${KIND_CLS[doc.kind]}`}
                    >
                      {kindLabel[doc.kind]}
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-mono text-sm text-strong">
                        {doc.title}
                      </span>
                      <span className="mt-0.5 block truncate text-xs text-muted">
                        {doc.context}
                      </span>
                    </span>
                    {doc.status === 'planned' && (
                      <span className="mt-0.5 shrink-0 font-mono text-2xs text-faint">planned</span>
                    )}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>

        <div className="flex items-center gap-3 border-t border-line bg-sunken px-4 py-2 font-mono text-2xs text-faint">
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
