/**
 * The interface's icons, drawn rather than typed.
 *
 * A glyph from the font — ☰, •, ◆ — inherits whatever the text stack resolves to,
 * renders at a different weight on every platform, and cannot be recoloured
 * independently of its label. These are stroke-based on a 24px grid and take their
 * colour from `currentColor`, so one set works in both themes.
 */

const base = {
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.8,
  strokeLinecap: 'round' as const,
  strokeLinejoin: 'round' as const,
};

/** The server itself: the thing every other page is about. */
export function ServerIcon({ size = 15 }: { size?: number }) {
  return (
    <svg {...base} width={size} height={size} className="shrink-0 opacity-75" aria-hidden="true">
      <rect x="3" y="4" width="18" height="7" rx="1.5" />
      <rect x="3" y="14" width="18" height="6" rx="1.5" />
      <path d="M7 7.5h.01M7 17h.01" />
    </svg>
  );
}

/** Sites — a served domain. */
export function GlobeIcon({ size = 15 }: { size?: number }) {
  return (
    <svg {...base} width={size} height={size} className="shrink-0 opacity-75" aria-hidden="true">
      <circle cx="12" cy="12" r="9" />
      <path d="M3 12h18M12 3c2.4 2.6 2.4 15.4 0 18M12 3c-2.4 2.6-2.4 15.4 0 18" />
    </svg>
  );
}

export function MenuIcon({ size = 20 }: { size?: number }) {
  return (
    <svg {...base} width={size} height={size} strokeWidth={2} aria-hidden="true">
      <path d="M4 7h16M4 12h16M4 17h16" />
    </svg>
  );
}

export function CloseIcon({ size = 20 }: { size?: number }) {
  return (
    <svg {...base} width={size} height={size} strokeWidth={2} aria-hidden="true">
      <path d="M6 6l12 12M18 6L6 18" />
    </svg>
  );
}

/** Beside anything that needs a human to decide something. */
export function WarningIcon({ size = 16 }: { size?: number }) {
  return (
    <svg {...base} width={size} height={size} strokeWidth={2} className="shrink-0" aria-hidden="true">
      <path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z" />
      <path d="M12 9v4M12 17h.01" />
    </svg>
  );
}

export function ChevronDownIcon({ size = 14 }: { size?: number }) {
  return (
    <svg {...base} width={size} height={size} aria-hidden="true">
      <path d="M6 9l6 6 6-6" />
    </svg>
  );
}

/**
 * The marks the command catalogue uses for "cannot be undone" and "super admin
 * only", which were a • and a ◆ in the text stack.
 */
export function DestructiveIcon({ size = 13 }: { size?: number }) {
  return (
    <svg {...base} width={size} height={size} className="shrink-0" aria-hidden="true">
      <path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6" />
    </svg>
  );
}

export function SuperAdminIcon({ size = 13 }: { size?: number }) {
  return (
    <svg {...base} width={size} height={size} className="shrink-0" aria-hidden="true">
      <path d="M12 3l7 3v5.5c0 4.2-2.8 7.9-7 9.5-4.2-1.6-7-5.3-7-9.5V6l7-3Z" />
    </svg>
  );
}
