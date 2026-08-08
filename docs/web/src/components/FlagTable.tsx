import type { Flag } from '../data/types';
import type { AnchoredFlag } from '../lib/flags';
import { Inline } from './Inline';
import { Pill, RefGroupHeading, RefList, RefNote, RefRow } from './Reference';

/**
 * The flags of a command, as reference rows.
 *
 * Every row is individually anchored — `#site-add--runtime` — because "which flag was it
 * that…" is the single most common thing anybody needs to link a colleague to.
 *
 * This used to be a four-column table with a second, stacked rendering for narrow columns,
 * chosen by measuring the container: rendering both and hiding one with CSS would have put
 * the anchor ids on a `display: none` element, and a deep link to a hidden element does not
 * scroll. One row shape that works at every width removes the measuring, the second
 * rendering and that hazard together — and gives the descriptions the whole measure instead
 * of whatever a fixed name column left over.
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
  if (flags.length === 0) return null;

  return (
    <>
      <RefGroupHeading title={caption ?? 'Flags'}>
        {note ? <Inline text={note} /> : undefined}
      </RefGroupHeading>
      <RefList label={caption ? `${caption} flags` : 'Flags'}>
        {flags.map(({ flag: f, anchor }) => (
          <RefRow
            key={anchor}
            anchor={anchor}
            name={f.name}
            lead={<FlagName anchor={anchor} flag={f} />}
            arg={f.arg}
            type={f.type}
            meta={f.default ? [['default', f.default]] : undefined}
            pills={
              <>
                {f.required && <Pill tone="required">required</Pill>}
                {f.requiredWhen && <Pill tone="required">required {f.requiredWhen}</Pill>}
                {f.repeatable && <Pill>repeatable</Pill>}
              </>
            }
          >
            <Inline text={f.description} />
            {f.note && (
              <RefNote>
                <Inline text={f.note} />
              </RefNote>
            )}
          </RefRow>
        ))}
      </RefList>
    </>
  );
}

function FlagName({ anchor, flag: f }: { anchor: string; flag: Flag }) {
  return (
    /* nowrap: a browser will happily break `--ssl` after its hyphens. */
    <a
      href={`#${anchor}`}
      className="font-mono text-[0.8125rem] font-semibold whitespace-nowrap text-strong no-underline hover:text-accent"
    >
      {f.name}
      {f.short && <span className="font-normal text-muted">, {f.short}</span>}
    </a>
  );
}
