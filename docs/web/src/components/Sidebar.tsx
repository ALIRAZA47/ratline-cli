import { useEffect, useRef } from 'react';
import type { RefObject } from 'react';
import { Link, NavLink } from 'react-router-dom';
import { nav } from '../data/nav';
import type { NavItem } from '../data/nav';

/**
 * One level of the sidebar.
 *
 * `here` is pathname + hash rather than pathname alone, because the settings entries link
 * into sections of the configuration page. Matching on pathname would light up all twelve
 * of them at once the moment you opened that page.
 *
 * The active entry is the accent colour, semibold, against a two-pixel accent rail. No
 * filled background: in a column that can hold eighty-six monospace command names, a block
 * of colour reads as a selected row in a table rather than as "you are here", and the rail
 * alone tracks the eye down the list.
 */
function NavList({ items, here }: { items: NavItem[]; here: string }) {
  return (
    <ul className="space-y-px border-l border-line">
      {items.map((item) => {
        const cls = (active: boolean) =>
          [
            '-ml-px block border-l-2 py-[0.3rem] pl-3 pr-2 leading-snug no-underline transition-colors',
            item.mono ? 'font-mono text-[0.8125rem]' : 'text-sm',
            active
              ? 'border-accent font-semibold text-accent'
              : 'border-transparent text-muted hover:border-line-strong hover:text-strong',
          ].join(' ');
        const active = here === item.to;
        return (
          <li key={item.to}>
            {item.to.includes('#') ? (
              <Link
                to={item.to}
                aria-current={active ? 'page' : undefined}
                data-nav-active={active || undefined}
                className={cls(active)}
              >
                {item.label}
              </Link>
            ) : (
              <NavLink
                to={item.to}
                end
                data-nav-active={here === item.to || undefined}
                className={({ isActive }) => cls(isActive)}
              >
                {item.label}
              </NavLink>
            )}
          </li>
        );
      })}
    </ul>
  );
}

/**
 * The documentation navigation, in full.
 *
 * `scrollHost` is the element that actually scrolls — the sticky column on a wide screen,
 * the drawer on a narrow one. On navigation the active entry is scrolled into view inside
 * that element by setting its `scrollTop` directly rather than by calling
 * `scrollIntoView`, which would also move the window: arriving at a deep-linked heading
 * and being yanked back to the top of the article because the sidebar wanted to adjust
 * itself is a worse bug than an off-screen nav entry.
 */
export function Sidebar({
  here,
  pathname,
  scrollHost,
}: {
  here: string;
  pathname: string;
  scrollHost: RefObject<HTMLDivElement | null>;
}) {
  const navRef = useRef<HTMLElement>(null);

  useEffect(() => {
    const host = scrollHost.current;
    const active = navRef.current?.querySelector<HTMLElement>('[data-nav-active]');
    if (!host || !active) return;
    const hostBox = host.getBoundingClientRect();
    const box = active.getBoundingClientRect();
    const margin = 96;
    if (box.top < hostBox.top + margin) {
      host.scrollTop -= hostBox.top + margin - box.top;
    } else if (box.bottom > hostBox.bottom - margin) {
      host.scrollTop += box.bottom - (hostBox.bottom - margin);
    }
  }, [pathname, scrollHost]);

  return (
    <nav ref={navRef} aria-label="Documentation">
      {nav.map((section) => (
        <div key={section.title} className="mb-7">
          <h2 className="label mb-2 text-faint">{section.title}</h2>
          {section.items && <NavList items={section.items} here={here} />}
          {section.groups?.map((group) =>
            group.collapsible ? (
              // Native <details>, so 86 command pages fit in a 17rem column without a line
              // of state. Open when the page you are on is inside it — which is also what
              // makes a deep link arrive with its context expanded.
              <details
                key={group.title}
                open={group.items.some((i) => i.to === pathname)}
                className="mt-1.5 first:mt-1"
              >
                <summary className="cursor-pointer list-none rounded py-1 text-sm font-medium text-muted marker:content-none hover:text-strong [&::-webkit-details-marker]:hidden">
                  <span className="inline-flex items-center gap-1.5">
                    <svg
                      width="9"
                      height="9"
                      viewBox="0 0 9 9"
                      aria-hidden="true"
                      className="shrink-0 text-faint transition-transform [details[open]>summary_&]:rotate-90"
                    >
                      <path
                        d="M2.5 1L6.5 4.5L2.5 8"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="1.5"
                      />
                    </svg>
                    {group.title}
                  </span>
                </summary>
                <div className="ml-2 mt-0.5">
                  <NavList items={group.items} here={here} />
                </div>
              </details>
            ) : (
              <div key={group.title} className="mt-4">
                <h3 className="mb-1.5 text-2xs font-semibold uppercase tracking-[0.07em] text-faint">
                  {group.title}
                </h3>
                <NavList items={group.items} here={here} />
              </div>
            ),
          )}
        </div>
      ))}
      <p className="mt-8 border-t border-line pt-4 text-xs leading-relaxed text-muted">
        Everything here is derived from <code className="font-mono">command-surface.md</code>,{' '}
        <code className="font-mono">defaults.yaml</code> and the validators in{' '}
        <code className="font-mono">internal/validate</code>. Commands marked{' '}
        <span className="font-medium text-faint">planned</span> are specified, not yet
        implemented.
      </p>
    </nav>
  );
}
