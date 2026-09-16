import { useEffect, useRef, useState } from 'react';
import type { Action } from '../lib/types';
import { ChevronDownIcon, DestructiveIcon, SuperAdminIcon } from './icons';

/**
 * Every command for this thing that is not the primary one.
 *
 * This replaces the card that used to sit at the bottom of each page listing
 * "everything else this site can do" as forty equal buttons. That card was honest —
 * the panel really can run all of it — but it made the page's shape depend on how
 * many commands the installed ratline happens to have, and it put `site delete`
 * beside `site show` at the same visual weight.
 *
 * The list is still generated from the catalogue, so a ratline release that adds a
 * command still shows up here without anybody editing this file.
 */
export function MoreMenu({
  actions,
  loading,
  onPick,
  label = 'More',
  empty = 'No other commands are available to you here.',
}: {
  actions: Action[];
  loading?: boolean;
  onPick: (id: string) => void;
  label?: string;
  empty?: string;
}) {
  const [open, setOpen] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      if (!wrap.current?.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        setOpen(false);
        // Focus goes back to what opened the menu, or a keyboard user is left
        // at the top of the document with no idea where they were.
        button.current?.focus();
      }
    }
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <div className="relative" ref={wrap}>
      <button
        ref={button}
        className="btn"
        aria-expanded={open}
        aria-haspopup="menu"
        onClick={() => setOpen((v) => !v)}
      >
        {label}
        <ChevronDownIcon />
      </button>
      {open && (
        <div
          role="menu"
          className="absolute right-0 z-30 mt-1 max-h-[22rem] w-[19rem] overflow-y-auto rounded-[var(--radius-card)] border border-[var(--border)] bg-[var(--bg-raised)] p-1 shadow-lg"
        >
          {loading && <p className="px-2 py-3 text-sm text-[var(--fg-faint)]">Loading…</p>}
          {!loading && actions.length === 0 && (
            <p className="px-2 py-3 text-sm text-[var(--fg-faint)]">{empty}</p>
          )}
          {actions.map((a) => (
            <button
              key={a.id}
              role="menuitem"
              className="flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left hover:bg-[var(--bg-hover)]"
              onClick={() => {
                setOpen(false);
                onPick(a.id);
              }}
            >
              <span className="min-w-0 flex-1">
                <span className="mono block text-xs">{a.verb}</span>
                {a.summary && (
                  <span className="mt-0.5 block text-2xs leading-snug text-[var(--fg-muted)]">
                    {a.summary}
                  </span>
                )}
              </span>
              <span className="mt-0.5 flex shrink-0 items-center gap-1">
                {a.destructive && (
                  <span className="text-[var(--danger)]" title="Cannot be undone by another command">
                    <DestructiveIcon />
                  </span>
                )}
                {a.min_role === 'superadmin' && (
                  <span className="text-[var(--fg-faint)]" title="Super admin only">
                    <SuperAdminIcon />
                  </span>
                )}
              </span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
