import { useEffect, useRef, useState } from 'react';
import type { Flag } from '../data/types';
import type { AnchoredFlag } from '../lib/flags';
import { Inline } from './Inline';

/** Below this content width the four columns cannot be read without clipping. */
const TABLE_MIN_PX = 700;

/**
 * The flag table. Every row is individually anchored — `#site-add--runtime` —
 * because "which flag was it that…" is the single most common thing anybody needs
 * to link a colleague to.
 *
 * Two presentations of the same rows, and exactly one is in the DOM at a time:
 * a table when the column is wide enough, a stacked block when it is not. The
 * choice is made by measuring the container rather than the viewport, because the
 * width that matters changes when the sidebar and the on-page contents appear, not
 * when the window does. Rendering one and hiding the other with CSS would put the
 * anchor ids on a `display: none` element, and a deep link to a hidden element
 * does not scroll.
 */
export function FlagTable({
  flags,
  caption,
  note,
}: {
  flags: AnchoredFlag[];
  caption?: string;
  note?: string;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [wide, setWide] = useState(true);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const measure = () => setWide(host.clientWidth >= TABLE_MIN_PX);
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(host);
    return () => ro.disconnect();
  }, []);

  if (flags.length === 0) return null;

  const label = caption ? `${caption} flags` : 'Flags';

  return (
    <div ref={hostRef} className="not-prose my-4">
      {wide ? (
        <div className="overflow-hidden rounded-[var(--radius-card)] border border-line">
          <table className="w-full border-collapse text-left text-sm">
            <caption className="border-b border-line bg-sunken px-3 py-1.5 text-left">
              <span className="font-mono text-xs font-semibold uppercase tracking-wider text-muted">
                {caption ?? 'Flags'}
              </span>
              {note && (
                <span className="mt-1 block max-w-[var(--container-measure)] text-sm font-normal normal-case leading-relaxed text-muted">
                  <Inline text={note} />
                </span>
              )}
            </caption>
            <thead>
              <tr className="border-b border-line text-2xs uppercase tracking-wider text-faint">
                <th scope="col" className="w-[15rem] px-3 py-1.5 font-medium">
                  Flag
                </th>
                <th scope="col" className="w-[6.5rem] px-3 py-1.5 font-medium">
                  Type
                </th>
                <th scope="col" className="w-[8.5rem] px-3 py-1.5 font-medium">
                  Default
                </th>
                <th scope="col" className="px-3 py-1.5 font-medium">
                  Description
                </th>
              </tr>
            </thead>
            <tbody>
              {flags.map(({ flag: f, anchor }) => (
                <tr
                  key={anchor}
                  id={anchor}
                  className="border-t border-line align-top scroll-mt-24 target:bg-accent-soft"
                >
                  <th scope="row" className="px-3 py-2.5 text-left font-normal">
                    <FlagName anchor={anchor} flag={f} />
                  </th>
                  <td className="px-3 py-2.5 font-mono text-xs text-muted">{f.type}</td>
                  <td className="px-3 py-2.5 font-mono text-xs text-muted">
                    {f.default ?? <span className="text-faint">—</span>}
                  </td>
                  <td className="px-3 py-2.5 leading-relaxed">
                    <Description flag={f} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <>
          <p className="mb-1 font-mono text-xs font-semibold uppercase tracking-wider text-muted">
            {caption ?? 'Flags'}
          </p>
          {note && (
            <p className="mb-2 max-w-[var(--container-measure)] text-sm leading-relaxed text-muted">
              <Inline text={note} />
            </p>
          )}
          <dl
            aria-label={label}
            className="divide-y divide-[var(--border)] overflow-hidden rounded-[var(--radius-card)] border border-line"
          >
            {flags.map(({ flag: f, anchor }) => (
              <div
                key={anchor}
                id={anchor}
                className="scroll-mt-24 px-3.5 py-3 target:bg-accent-soft"
              >
                <dt>
                  <FlagName anchor={anchor} flag={f} />
                </dt>
                <dd className="mt-2">
                  <p className="flex flex-wrap gap-x-4 gap-y-1 font-mono text-2xs text-muted">
                    <span>
                      <span className="text-faint">type </span>
                      {f.type}
                    </span>
                    <span>
                      <span className="text-faint">default </span>
                      {f.default ?? '—'}
                    </span>
                  </p>
                  <div className="mt-2 text-sm leading-relaxed">
                    <Description flag={f} />
                  </div>
                </dd>
              </div>
            ))}
          </dl>
        </>
      )}
    </div>
  );
}

function FlagName({ anchor, flag: f }: { anchor: string; flag: Flag }) {
  return (
    <>
      {/* nowrap: a browser will happily break `--ssl` after its hyphens. */}
      <a href={`#${anchor}`} className="whitespace-nowrap font-mono text-xs text-accent no-underline">
        {f.name}
        {f.short && <span className="text-muted">, {f.short}</span>}
      </a>
      {f.arg && <span className="ml-1 font-mono text-xs break-words text-muted">{f.arg}</span>}
      {(f.required || f.requiredWhen || f.repeatable) && (
        <span className="mt-1 flex flex-wrap gap-1">
          {f.required && (
            <span className="rounded bg-warn-soft px-1 py-px text-2xs font-medium text-warn">
              required
            </span>
          )}
          {f.requiredWhen && (
            <span className="rounded bg-warn-soft px-1 py-px text-2xs font-medium text-warn">
              required {f.requiredWhen}
            </span>
          )}
          {f.repeatable && (
            <span className="rounded bg-sunken px-1 py-px text-2xs text-muted">repeatable</span>
          )}
        </span>
      )}
    </>
  );
}

function Description({ flag: f }: { flag: Flag }) {
  return (
    <>
      <span className="block text-fg">
        <Inline text={f.description} />
      </span>
      {f.note && (
        <span className="mt-1.5 block border-l-2 border-line-strong pl-2.5 text-xs leading-relaxed text-muted">
          <Inline text={f.note} />
        </span>
      )}
    </>
  );
}
