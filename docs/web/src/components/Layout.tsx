import { useEffect, useState } from 'react';
import { Link, NavLink, Outlet, useLocation } from 'react-router-dom';
import { allNavItems, nav } from '../data/nav';
import { useTheme } from '../lib/useTheme';
import { SearchDialog } from './Search';
import { Toc } from './Toc';

export function Layout() {
  const { pathname, hash } = useLocation();
  const { theme, toggle } = useTheme();
  const [drawer, setDrawer] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);

  // Close the mobile drawer on navigation; otherwise it covers the page you
  // just asked for.
  useEffect(() => setDrawer(false), [pathname, hash]);

  // Scroll handling: to the anchor if there is one, to the top otherwise.
  //
  // Deferred by one frame so the route's content — and therefore the anchor — is
  // committed before we look for it, and 'instant' so a deep link lands rather
  // than animating from wherever the previous page was.
  useEffect(() => {
    if ('scrollRestoration' in history) history.scrollRestoration = 'manual';
    const raf = requestAnimationFrame(() => {
      const id = hash ? decodeURIComponent(hash.slice(1)) : '';
      const el = id ? document.getElementById(id) : null;
      if (el) {
        el.scrollIntoView({ block: 'start', behavior: 'instant' });
        return;
      }
      window.scrollTo({ top: 0, behavior: 'instant' });
    });
    return () => cancelAnimationFrame(raf);
  }, [pathname, hash]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      const typing =
        target &&
        (target.tagName === 'INPUT' ||
          target.tagName === 'TEXTAREA' ||
          target.isContentEditable);
      if ((e.key === 'k' && (e.metaKey || e.ctrlKey)) || (e.key === '/' && !typing)) {
        e.preventDefault();
        setSearchOpen(true);
      }
      if (e.key === 'Escape') setDrawer(false);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const idx = allNavItems.findIndex((i) => i.to === pathname);
  const prev = idx > 0 ? allNavItems[idx - 1] : undefined;
  const next = idx >= 0 && idx < allNavItems.length - 1 ? allNavItems[idx + 1] : undefined;

  return (
    <div className="min-h-screen">
      <a
        href="#content"
        className="sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-60 focus:rounded focus:bg-accent focus:px-3 focus:py-2 focus:text-sm focus:text-accent-fg"
      >
        Skip to content
      </a>

      <header className="sticky top-0 z-40 border-b border-line bg-[color-mix(in_oklab,var(--bg)_88%,transparent)] backdrop-blur-md">
        <div className="mx-auto flex h-14 max-w-[100rem] items-center gap-3 px-4 lg:px-6">
          <button
            type="button"
            onClick={() => setDrawer((d) => !d)}
            aria-expanded={drawer}
            aria-controls="sidebar-nav"
            className="-ml-1 rounded p-2 text-muted hover:bg-hover hover:text-fg lg:hidden"
          >
            <span className="sr-only">{drawer ? 'Close navigation' : 'Open navigation'}</span>
            <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true" fill="none">
              {drawer ? (
                <path d="M4 4l10 10M14 4L4 14" stroke="currentColor" strokeWidth="1.6" />
              ) : (
                <path d="M2 5h14M2 9h14M2 13h14" stroke="currentColor" strokeWidth="1.6" />
              )}
            </svg>
          </button>

          <Link to="/" className="flex items-baseline gap-2 no-underline">
            <span className="font-mono text-[0.95rem] font-semibold tracking-tight text-strong">
              ratline
            </span>
            <span className="hidden text-xs text-muted sm:inline">provisioning docs</span>
          </Link>

          <div className="ml-auto flex items-center gap-2">
            <button
              type="button"
              onClick={() => setSearchOpen(true)}
              aria-label="Search the documentation"
              aria-keyshortcuts="/"
              className="flex items-center gap-2 rounded-md border border-line bg-raised px-2.5 py-1.5 text-sm text-muted transition-colors hover:border-line-strong hover:bg-hover"
            >
              <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true" fill="none">
                <circle cx="6" cy="6" r="4.25" stroke="currentColor" strokeWidth="1.5" />
                <path d="M9.5 9.5L12.5 12.5" stroke="currentColor" strokeWidth="1.5" />
              </svg>
              <span className="hidden sm:inline">Search</span>
              <kbd className="hidden rounded border border-line bg-sunken px-1 font-mono text-2xs text-faint sm:inline">
                /
              </kbd>
            </button>

            <button
              type="button"
              onClick={toggle}
              className="rounded-md border border-line bg-raised p-2 text-muted transition-colors hover:border-line-strong hover:bg-hover hover:text-fg"
              aria-label={`Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`}
            >
              {theme === 'dark' ? (
                <svg width="15" height="15" viewBox="0 0 15 15" aria-hidden="true" fill="none">
                  <circle cx="7.5" cy="7.5" r="3.1" stroke="currentColor" strokeWidth="1.4" />
                  <path
                    d="M7.5 1v1.6M7.5 12.4V14M1 7.5h1.6M12.4 7.5H14M3.1 3.1l1.1 1.1M10.8 10.8l1.1 1.1M11.9 3.1l-1.1 1.1M4.2 10.8L3.1 11.9"
                    stroke="currentColor"
                    strokeWidth="1.4"
                  />
                </svg>
              ) : (
                <svg width="15" height="15" viewBox="0 0 15 15" aria-hidden="true" fill="none">
                  <path
                    d="M13 9.4A5.9 5.9 0 015.6 2 5.9 5.9 0 108.9 13a5.9 5.9 0 004.1-3.6z"
                    stroke="currentColor"
                    strokeWidth="1.4"
                    strokeLinejoin="round"
                  />
                </svg>
              )}
            </button>
          </div>
        </div>
      </header>

      <div className="mx-auto flex max-w-[100rem] px-4 lg:px-6">
        {/* Sidebar. A drawer under lg, a sticky column above it. */}
        {drawer && (
          <button
            type="button"
            aria-label="Close navigation"
            onClick={() => setDrawer(false)}
            className="fixed inset-0 top-14 z-30 bg-[oklch(20%_0.02_255_/_0.4)] lg:hidden"
          />
        )}
        <aside
          id="sidebar-nav"
          className={[
            'z-35 w-[16rem] shrink-0',
            drawer
              ? 'fixed inset-y-0 left-0 top-14 block overflow-y-auto border-r border-line bg-bg px-4 pb-10 pt-4'
              : 'hidden lg:block',
          ].join(' ')}
        >
          <nav
            aria-label="Documentation"
            className={
              drawer
                ? ''
                : 'scroll-thin sticky top-14 max-h-[calc(100vh-3.5rem)] overflow-y-auto py-7 pr-5'
            }
          >
            {nav.map((section) => (
              <div key={section.title} className="mb-6">
                <h2 className="mb-1.5 font-mono text-2xs font-semibold uppercase tracking-wider text-faint">
                  {section.title}
                </h2>
                <ul className="space-y-px border-l border-line">
                  {section.items.map((item) => (
                    <li key={item.to}>
                      <NavLink
                        to={item.to}
                        end
                        className={({ isActive }) =>
                          [
                            '-ml-px block border-l-2 py-1 pl-3 pr-2 text-sm leading-snug no-underline transition-colors',
                            isActive
                              ? 'border-accent bg-accent-soft font-medium text-strong'
                              : 'border-transparent text-muted hover:border-line-strong hover:text-fg',
                          ].join(' ')
                        }
                      >
                        {item.label}
                      </NavLink>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
            <p className="mt-8 max-w-[14rem] border-t border-line pt-4 text-xs leading-relaxed text-faint">
              Everything here is derived from{' '}
              <code className="font-mono">docs/command-surface.md</code>,{' '}
              <code className="font-mono">internal/config/defaults.yaml</code> and the validators in{' '}
              <code className="font-mono">internal/validate</code>. Commands marked{' '}
              <span className="text-muted">planned</span> are specified, not yet implemented.
            </p>
          </nav>
        </aside>

        {/* Content column. */}
        <div className="min-w-0 flex-1">
          <main id="content" className="min-w-0 py-9 lg:py-11 lg:pl-9 xl:pr-9">
            <Outlet />

            {(prev || next) && (
              <nav
                aria-label="Previous and next page"
                className="mt-14 grid gap-3 border-t border-line pt-6 sm:grid-cols-2"
              >
                {prev ? (
                  <Link
                    to={prev.to}
                    className="group rounded-[var(--radius-card)] border border-line px-4 py-3 no-underline transition-colors hover:border-line-strong hover:bg-hover"
                  >
                    <span className="block font-mono text-2xs uppercase tracking-wider text-faint">
                      ← Previous
                    </span>
                    <span className="mt-0.5 block text-sm font-medium text-strong">
                      {prev.label}
                    </span>
                  </Link>
                ) : (
                  <span />
                )}
                {next && (
                  <Link
                    to={next.to}
                    className="group rounded-[var(--radius-card)] border border-line px-4 py-3 text-right no-underline transition-colors hover:border-line-strong hover:bg-hover sm:col-start-2"
                  >
                    <span className="block font-mono text-2xs uppercase tracking-wider text-faint">
                      Next →
                    </span>
                    <span className="mt-0.5 block text-sm font-medium text-strong">
                      {next.label}
                    </span>
                  </Link>
                )}
              </nav>
            )}
          </main>
        </div>

        {/* On-page TOC. Only wide enough screens get it. */}
        <div className="hidden w-[12.5rem] shrink-0 xl:block">
          <div className="scroll-thin sticky top-14 max-h-[calc(100vh-3.5rem)] overflow-y-auto py-11 pl-1">
            <Toc />
          </div>
        </div>
      </div>

      <footer className="border-t border-line py-8">
        <div className="mx-auto max-w-[100rem] px-4 text-xs leading-relaxed text-faint lg:px-6">
          <p className="max-w-[var(--container-measure)]">
            ratline documentation. No external fonts, no CDN, no analytics, no network calls at
            runtime — this page is the whole thing.
          </p>
        </div>
      </footer>

      <SearchDialog open={searchOpen} onClose={() => setSearchOpen(false)} />
    </div>
  );
}
