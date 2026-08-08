import { Suspense, useEffect, useRef, useState } from 'react';
import { Link, Outlet, useLocation } from 'react-router-dom';
import { allNavItems } from '../data/nav';
import { releases } from '../data/releases';
import { useTheme } from '../lib/useTheme';
import { SearchDialog } from './Search';
import { Sidebar } from './Sidebar';
import { Toc, TocInline } from './Toc';

const currentVersion = releases[0]?.version ?? '';

function MenuIcon({ open }: { open: boolean }) {
  return (
    <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden="true" fill="none">
      {open ? (
        <path d="M4 4l10 10M14 4L4 14" stroke="currentColor" strokeWidth="1.6" />
      ) : (
        <path d="M2 5h14M2 9h14M2 13h14" stroke="currentColor" strokeWidth="1.6" />
      )}
    </svg>
  );
}

function SearchIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true" fill="none">
      <circle cx="6" cy="6" r="4.25" stroke="currentColor" strokeWidth="1.5" />
      <path d="M9.5 9.5L12.5 12.5" stroke="currentColor" strokeWidth="1.5" />
    </svg>
  );
}

export function Layout() {
  const { pathname, hash } = useLocation();
  const { theme, toggle } = useTheme();
  const [drawer, setDrawer] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const menuButton = useRef<HTMLButtonElement>(null);
  const drawerNav = useRef<HTMLDivElement>(null);

  // Close the mobile drawer on navigation; otherwise it covers the page you
  // just asked for.
  useEffect(() => setDrawer(false), [pathname, hash]);

  // The drawer is a modal over the page: lock the page's scroll while it is open, and hand
  // focus back to the button that opened it on the way out, so a keyboard reader is not
  // dropped at the top of the document.
  useEffect(() => {
    if (!drawer) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    // The page you are on, not the top of the list — so the first Tab from here goes to a
    // neighbour of what you are reading. `preventScroll`: the sidebar's own effect decides
    // where the column sits, and the browser's focus scroll would fight it.
    const entry =
      drawerNav.current?.querySelector<HTMLElement>('[data-nav-active]') ??
      drawerNav.current?.querySelector<HTMLElement>('a, summary');
    entry?.focus({ preventScroll: true });
    return () => {
      document.body.style.overflow = previous;
      menuButton.current?.focus();
    };
  }, [drawer]);

  // Scroll handling: to the anchor if there is one, to the top otherwise.
  //
  // One frame is not enough now that routes are lazy. The chunk for the page has to arrive
  // and render before its headings exist, and a single deferred frame looked for the
  // element while Suspense was still showing the fallback — so every deep link landed at
  // the top of the page instead. That is most of what the search index emits.
  //
  // So: keep looking until the element appears, for a bounded number of frames. Without a
  // hash there is nothing to wait for and the scroll is immediate; with one that never
  // resolves — a stale anchor, a renamed heading — the budget runs out and the page goes
  // to the top, which is where it would have gone anyway.
  useEffect(() => {
    if ('scrollRestoration' in history) history.scrollRestoration = 'manual';

    const id = hash ? decodeURIComponent(hash.slice(1)) : '';
    if (!id) {
      const raf = requestAnimationFrame(() => window.scrollTo({ top: 0, behavior: 'instant' }));
      return () => cancelAnimationFrame(raf);
    }

    // ~1s at 60fps. Long enough for a chunk on a slow connection, short enough that a
    // genuinely missing anchor does not leave the reader staring at the wrong place.
    let framesLeft = 60;
    let raf = 0;
    const look = () => {
      const el = document.getElementById(id);
      if (el) {
        el.scrollIntoView({ block: 'start', behavior: 'instant' });
        return;
      }
      if (framesLeft-- > 0) {
        raf = requestAnimationFrame(look);
        return;
      }
      window.scrollTo({ top: 0, behavior: 'instant' });
    };
    raf = requestAnimationFrame(look);
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

  const here = pathname + hash;
  const idx = allNavItems.findIndex((i) => i.to === pathname);
  const prev = idx > 0 ? allNavItems[idx - 1] : undefined;
  const next = idx >= 0 && idx < allNavItems.length - 1 ? allNavItems[idx + 1] : undefined;

  return (
    <div className="min-h-screen">
      <a
        href="#content"
        className="sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-3 focus:z-[60] focus:rounded focus:bg-accent focus:px-3 focus:py-2 focus:text-sm focus:font-medium focus:text-accent-fg"
      >
        Skip to content
      </a>

      <header className="sticky top-0 z-40 border-b border-line bg-[color-mix(in_oklab,var(--bg)_88%,transparent)] backdrop-blur-md">
        <div className="mx-auto flex h-[var(--header-h)] max-w-[var(--shell-max)] items-center gap-3 px-4 lg:px-6">
          <button
            ref={menuButton}
            type="button"
            onClick={() => setDrawer((d) => !d)}
            aria-expanded={drawer}
            aria-controls="sidebar-nav"
            className="-ml-1 rounded p-2 text-muted hover:bg-hover hover:text-strong lg:hidden"
          >
            <span className="sr-only">{drawer ? 'Close navigation' : 'Open navigation'}</span>
            <MenuIcon open={drawer} />
          </button>

          <Link to="/" className="flex items-center gap-2.5 no-underline">
            <span className="font-mono text-[0.9375rem] font-semibold tracking-tight text-strong">
              ratline
            </span>
            <span aria-hidden="true" className="hidden h-4 w-px bg-line-strong sm:block" />
            <span className="hidden text-sm text-muted sm:inline">docs</span>
          </Link>

          <div className="ml-auto flex items-center gap-2">
            {currentVersion && (
              <Link
                to="/releases"
                className="hidden rounded-full border border-line bg-sunken px-2.5 py-1 font-mono text-2xs text-muted no-underline transition-colors hover:border-line-strong hover:text-strong md:inline-block"
              >
                {currentVersion}
              </Link>
            )}

            <button
              type="button"
              onClick={() => setSearchOpen(true)}
              aria-label="Search the documentation"
              aria-keyshortcuts="/ Meta+K"
              className="flex items-center gap-2 rounded-md border border-line bg-raised px-2.5 py-1.5 text-sm text-muted shadow-[var(--shadow-card)] transition-colors hover:border-line-strong hover:bg-hover sm:w-[13rem]"
            >
              <SearchIcon />
              <span className="hidden sm:inline">Search</span>
              <kbd className="ml-auto hidden rounded border border-line bg-sunken px-1.5 py-px font-mono text-2xs text-faint sm:inline">
                ⌘K
              </kbd>
            </button>

            <button
              type="button"
              onClick={toggle}
              className="rounded-md border border-line bg-raised p-2 text-muted shadow-[var(--shadow-card)] transition-colors hover:border-line-strong hover:bg-hover hover:text-strong"
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

      {/* The shell: three columns centred as a group.
          `justify-center` is what does that. The content column is capped at the reading
          measure, so on a wide monitor it cannot grow to absorb the spare width — and
          before this, the spare width all landed to the right of the text, leaving the
          article huddled against the sidebar with half the screen empty beside it. */}
      <div className="mx-auto flex w-full max-w-[var(--shell-max)] justify-center px-4 lg:px-6">
        {drawer && (
          <button
            type="button"
            aria-label="Close navigation"
            onClick={() => setDrawer(false)}
            className="fixed inset-0 top-[var(--header-h)] z-30 bg-[oklch(20%_0.02_265_/_0.45)] lg:hidden"
          />
        )}
        <div
          id="sidebar-nav"
          className={[
            'w-[var(--sidebar-w)] shrink-0',
            drawer
              ? 'fixed inset-y-0 left-0 top-[var(--header-h)] z-40 block max-w-[calc(100vw-3rem)] border-r border-line bg-bg'
              : 'hidden lg:block lg:border-r lg:border-line',
          ].join(' ')}
        >
          <div
            ref={drawerNav}
            className={
              drawer
                ? 'scroll-thin h-full overflow-y-auto px-4 pb-16 pt-5'
                : 'scroll-thin sticky top-[var(--header-h)] max-h-[calc(100vh-var(--header-h))] overflow-y-auto py-8 pr-6'
            }
          >
            <Sidebar here={here} pathname={pathname} revealed={drawer} />
          </div>
        </div>

        {/* Content column. Capped at the reading measure; everything inside shares it. */}
        <main
          id="content"
          className="min-w-0 max-w-[var(--content-w)] flex-1 py-10 lg:py-12 lg:pl-[var(--content-gap)]"
        >
          <TocInline />

          {/* The boundary sits here rather than around the whole tree so the header
              and the sidebar stay put while a route's chunk arrives — only the article
              is replaced. min-h holds the scroll position steady on the way in. */}
          <Suspense
            fallback={
              <div className="min-h-[60vh]" role="status" aria-live="polite">
                <span className="sr-only">Loading…</span>
              </div>
            }
          >
            <Outlet />
          </Suspense>

          {(prev || next) && (
            <nav
              aria-label="Previous and next page"
              className="mt-16 grid gap-3 border-t border-line pt-6 sm:grid-cols-2"
            >
              {prev ? (
                <Link
                  to={prev.to}
                  className="group rounded-[var(--radius-card)] border border-line px-4 py-3 no-underline transition-colors hover:border-accent hover:bg-hover"
                >
                  <span className="label block text-faint">← Previous</span>
                  <span className="mt-1 block text-sm font-medium text-strong group-hover:text-accent">
                    {prev.label}
                  </span>
                </Link>
              ) : (
                <span />
              )}
              {next && (
                <Link
                  to={next.to}
                  className="group rounded-[var(--radius-card)] border border-line px-4 py-3 text-right no-underline transition-colors hover:border-accent hover:bg-hover sm:col-start-2"
                >
                  <span className="label block text-faint">Next →</span>
                  <span className="mt-1 block text-sm font-medium text-strong group-hover:text-accent">
                    {next.label}
                  </span>
                </Link>
              )}
            </nav>
          )}
        </main>

        {/* On-page contents. Only screens wide enough for a third column get it; the
            others get the collapsed version at the top of the article. */}
        <div className="hidden w-[var(--toc-w)] shrink-0 pl-[var(--toc-gap)] xl:block">
          <div className="scroll-thin sticky top-[var(--header-h)] max-h-[calc(100vh-var(--header-h))] overflow-y-auto py-12">
            <Toc />
          </div>
        </div>
      </div>

      <footer className="mt-4 border-t border-line py-8">
        <div className="mx-auto max-w-[var(--shell-max)] px-4 text-xs leading-relaxed text-muted lg:px-6">
          <p className="max-w-[var(--content-w)]">
            ratline documentation. No external fonts, no CDN, no analytics, no network calls at
            runtime — this page is the whole thing.
          </p>
        </div>
      </footer>

      <SearchDialog open={searchOpen} onClose={() => setSearchOpen(false)} />
    </div>
  );
}
