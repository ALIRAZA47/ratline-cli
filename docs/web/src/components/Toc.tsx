import { useEffect, useState } from 'react';
import { useLocation } from 'react-router-dom';

interface Item {
  id: string;
  text: string;
  level: 2 | 3;
}

/**
 * On-page table of contents, built by reading the rendered headings rather than
 * being declared twice. Rebuilt on navigation; the active entry follows the
 * heading nearest the top of the viewport.
 */
export function Toc() {
  const { pathname } = useLocation();
  const [items, setItems] = useState<Item[]>([]);
  const [active, setActive] = useState<string>('');

  useEffect(() => {
    const main = document.getElementById('content');
    if (!main) return;
    const collect = () => {
      const found: Item[] = [];
      main.querySelectorAll<HTMLElement>('h2[id], h3[id]').forEach((el) => {
        const text = (el.textContent ?? '').replace(/#$/, '').trim();
        if (!text) return;
        found.push({ id: el.id, text, level: el.tagName === 'H2' ? 2 : 3 });
      });
      setItems(found);
      setActive(found[0]?.id ?? '');
    };
    // One frame, so the route's content is committed before we read it.
    const raf = requestAnimationFrame(collect);
    return () => cancelAnimationFrame(raf);
  }, [pathname]);

  useEffect(() => {
    if (items.length === 0) return;
    const onScroll = () => {
      let current = items[0].id;
      for (const item of items) {
        const el = document.getElementById(item.id);
        if (!el) continue;
        if (el.getBoundingClientRect().top <= 100) current = item.id;
        else break;
      }
      setActive(current);
    };
    onScroll();
    window.addEventListener('scroll', onScroll, { passive: true });
    return () => window.removeEventListener('scroll', onScroll);
  }, [items]);

  if (items.length < 2) return null;

  return (
    <nav aria-labelledby="toc-heading" className="text-sm">
      <h2
        id="toc-heading"
        className="mb-2.5 font-mono text-2xs font-semibold uppercase tracking-wider text-faint"
      >
        On this page
      </h2>
      <ul className="space-y-px border-l border-line">
        {items.map((item) => (
          <li key={item.id}>
            <a
              href={`#${item.id}`}
              className={[
                'block border-l-2 py-1 pr-2 leading-snug transition-colors',
                item.level === 3 ? 'pl-5 text-xs' : 'pl-3',
                active === item.id
                  ? 'border-accent font-medium text-strong'
                  : 'border-transparent text-muted hover:border-line-strong hover:text-fg',
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
