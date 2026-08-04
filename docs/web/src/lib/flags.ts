import type { Command, Flag } from '../data/types';
import { slugify } from './slug';

export interface AnchoredFlag {
  flag: Flag;
  /** Element id, and therefore the deep link: `site-add--runtime`. */
  anchor: string;
}

/**
 * Anchors for every flag of a command, computed in one place so the rendered
 * page and the search index cannot disagree about them.
 *
 * Anchors stay short — `site-add--runtime` — but `site add` has a
 * `--build-command` in both its static and node flag groups, and two elements
 * cannot share an id. Only the genuinely ambiguous names get the group prefix
 * (`site-add-static--build-command`), so the common case is unaffected.
 */
export function anchoredFlags(command: Command): { groups: { title?: string; note?: string; flags: AnchoredFlag[] }[] } {
  const groups = [
    ...(command.flagGroups ?? []).map((g) => ({ title: g.title, note: g.note, flags: g.flags })),
    ...(command.flags && command.flags.length > 0
      ? [{ title: 'Flags' as string | undefined, note: undefined, flags: command.flags }]
      : []),
  ];

  const counts = new Map<string, number>();
  for (const g of groups) {
    for (const f of g.flags) counts.set(f.name, (counts.get(f.name) ?? 0) + 1);
  }

  return {
    groups: groups.map((g) => ({
      title: g.title,
      note: g.note,
      flags: g.flags.map((f) => {
        const bare = f.name.replace(/^--?/, '');
        const ambiguous = (counts.get(f.name) ?? 0) > 1;
        const scope = ambiguous && g.title ? `${command.id}-${slugify(g.title)}` : command.id;
        return { flag: f, anchor: `${scope}--${bare}` };
      }),
    })),
  };
}

/** Every flag of a command, flattened, with its anchor. */
export function flatAnchoredFlags(command: Command): AnchoredFlag[] {
  return anchoredFlags(command).groups.flatMap((g) => g.flags);
}
