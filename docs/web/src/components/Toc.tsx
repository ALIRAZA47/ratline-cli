import { useEffect, useState } from 'react';
import { useLocation } from 'react-router-dom';

interface Item {
  id: string;
  text: string;
  level: 2 | 3;
}

/** How far below the sticky header a heading counts as "the one you are reading". */
const ACTIVATION_LINE = 112;

/**
 * The headings of the current page, read from the DOM rather than declared a second time.
 *
 * Every route on this site is lazily loaded, so on the frame after navigation the article
 * is still a Suspense fallback and `querySelectorAll('h2[id]')` finds nothing. The previous
 * version collected on a single `requestAnimationFrame` and therefore produced an empty
 * list on every first visit to a page — the on-this-page column simply did not render, and
 * because it renders nothing when it has fewer than two entries, it failed silently. It
 * only ever appeared if you navigated away and back with the chunk already cached.
 *
 * So: keep looking until headings appear, for a bounded number of frames, and then keep
 * watching. The observer matters as much as the retry — `ConfigReference` filters its
 * sections as you type and the topic pages render from markdown, so headings appear and
 * disappear long after the route has settled.
 */
function usePageHeadings(pathname: string): Item[] {
  const [items, setItems] = useState<Item[]>([]);

  useEffect(() => {
    const read = (): Item[] => {
      const main = document.getElementById('content');
      if (!main) return [];
      const found: Item[] = [];
      main.querySelectorAll<HTMLElement>('h2[id], h3[id]').forEach((el) => {
        const text = (el.textContent ?? '').replace(/#$/, '').trim();
        if (!text) return;
        found.push({ id: el.id, text, level: el.tagName === 'H2' ? 2 : 3 });
      });
      return found;
    };

    const same = (a: Item[], b: Item[]) =>
      a.length === b.length && a.every((x, i) => x.id === b[i].id && x.text === b[i].text);

    let current: Item[] = [];
    const publish = (next: Item[]) => {
      if (same(current, next)) return;
      current = next;
      setItems(next);
    };

    // Clear immediately: the outgoing page's headings are not this page's headings, and
    // showing them until the chunk lands is worse than showing nothing.
    publish([]);

    // ~1s at 60fps, the same budget the scroll-to-anchor uses. Long enough for a chunk on
    // a slow connection; short enough that a page with genuinely no headings is not
    // waited on forever.
    let framesLeft = 60;
    let raf = 0;
    const look = () => {
      const found = read();
      if (found.length > 0) {
        publish(found);
        return;
      }
      if (framesLeft-- > 0) raf = requestAnimationFrame(look);
    };
    raf = requestAnimationFrame(look);

    const main = document.getElementById('content');
    const observer = main
      ? new MutationObserver(() => {
          const found = read();
          if (found.length > 0) publish(found);
        })
      : null;
    observer?.observe(main!, { childList: true, subtree: true });

    return () => {
      cancelAnimationFrame(raf);
      observer?.disconnect();
    };
  }, [pathname]);

  return items;
}

/** The id of the heading the reader is currently under. */
function useActiveHeading(items: Item[]): string {
  const [active, setActive] = useState('');

  useEffect(() => {
    if (items.length === 0) {
      setActive('');
      return;
    }
    let raf = 0;
    const measure = () => {
      raf = 0;
      // At the bottom of the page the last heading is the one being read, even though it
      // may be well above the activation line.
      const atBottom =
        window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 4;
      if (atBottom) {
        setActive(items[items.length - 1].id);
        return;
      }
      let current = items[0].id;
      for (const item of items) {
        const el = document.getElementById(item.id);
        if (!el) continue;
        if (el.getBoundingClientRect().top <= ACTIVATION_LINE) current = item.id;
        else break;
      }
      setActive(current);
    };
    const onScroll = () => {
      if (raf === 0) raf = requestAnimationFrame(measure);
    };
    measure();
    window.addEventListener('scroll', onScroll, { passive: true });
    window.addEventListener('resize', onScroll, { passive: true });
    return () => {
      if (raf !== 0) cancelAnimationFrame(raf);
      window.removeEventListener('scroll', onScroll);
      window.removeEventListener('resize', onScroll);
    };
  }, [items]);

  return active;
}

/** The sticky column, on screens wide enough to carry a third column. */
export function Toc() {
  const { pathname } = useLocation();
  const items = usePageHeadings(pathname);
  const active = useActiveHeading(items);

  if (items.length < 2) return null;

  return (
    <nav aria-labelledby="toc-heading" className="text-sm">
      <h2 id="toc-heading" className="label mb-2.5 text-faint">
        On this page
      </h2>
      <ul className="space-y-px border-l border-line">
        {items.map((item) => (
          <li key={item.id}>
            <a
              href={`#${item.id}`}
              aria-current={active === item.id ? 'true' : undefined}
              className={[
                '-ml-px block border-l-2 py-1 pr-2 leading-snug no-underline transition-colors',
                item.level === 3 ? 'pl-5 text-[0.8125rem]' : 'pl-3',
                active === item.id
                  ? 'border-accent font-medium text-accent'
                  : 'border-transparent text-muted hover:border-line-strong hover:text-strong',
              ].join(' ')}
            >
              {item.text}
            </a>
          </li>
        ))}
      </ul>
    </nav>
  );
}

/**
 * The same contents, collapsed, for screens that have no third column.
 *
 * A native <details> rather than the sticky list: on a phone the useful question is "how
 * long is this and what is in it", asked once on arrival, not "where am I now".
 */
export function TocInline() {
  const { pathname } = useLocation();
  const items = usePageHeadings(pathname);

  if (items.length < 3) return null;

  return (
    <details className="not-prose mb-8 rounded-[var(--radius-card)] border border-line bg-sunken xl:hidden">
      <summary className="label cursor-pointer list-none px-4 py-2.5 text-muted marker:content-none [&::-webkit-details-marker]:hidden">
        <span className="inline-flex items-center gap-2">
          <svg
            width="9"
            height="9"
            viewBox="0 0 9 9"
            aria-hidden="true"
            className="shrink-0 transition-transform [details[open]_&]:rotate-90"
          >
            <path d="M2.5 1L6.5 4.5L2.5 8" fill="none" stroke="currentColor" strokeWidth="1.5" />
          </svg>
          On this page — {items.length} sections
        </span>
      </summary>
      <ul className="border-t border-line px-4 py-3 text-sm">
        {items.map((item) => (
          <li key={item.id} className={item.level === 3 ? 'pl-4' : ''}>
            <a
              href={`#${item.id}`}
              className="block py-1 leading-snug text-muted no-underline hover:text-accent"
            >
              {item.text}
            </a>
          </li>
        ))}
      </ul>
    </details>
  );
}
