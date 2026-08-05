import { allCommands, commandGroups, commandsIn, groupByPath } from './groups';
import { meta, pageMeta } from './pages';
import { topics } from './topics';
import { commandsOf, settingsOf, subjects, topicsOf, unclaimed } from './subjects';

// Re-exported so the pages that already import them from here keep working.
export { commandGroups, groupByPath };

export interface NavItem {
  label: string;
  to: string;
  /** One line, used as the description in search results and index cards. */
  blurb?: string;
  /** Extra terms the search index matches on. */
  keywords?: string[];
  /** Rendered in a monospace face: a command name, not a sentence. */
  mono?: boolean;
}

/**
 * A labelled block inside a section.
 *
 * `collapsible` means the block is a <details> — closed unless the current page is inside
 * it. Native rather than React state, so it works before hydration and keyboard support
 * comes for free. This is what makes 86 command pages fit in a sidebar at all.
 */
export interface NavGroup {
  title: string;
  items: NavItem[];
  collapsible?: boolean;
}

export interface NavSection {
  title: string;
  /** Items directly under the section heading, above any groups. */
  items?: NavItem[];
  groups?: NavGroup[];
}

/** item builds a nav entry from the page registry, so the label lives in one place. */
function item(path: string): NavItem {
  const m = meta(path);
  return { label: m.label, to: path, blurb: m.blurb, keywords: m.keywords };
}

/**
 * One sidebar section per subject.
 *
 * Everything about a subject is here: the commands, the concepts behind them, the in-depth
 * topics, the runbooks, and the configuration sections that change how it behaves. Somebody
 * working on SSH access previously had to visit four separate parts of the site to find
 * those, and had no way of knowing the fourth existed.
 */
function subjectSections(): NavSection[] {
  return subjects.map((subject) => {
    const groups: NavGroup[] = [];

    for (const group of commandsOf(subject)) {
      groups.push({
        title: group.title,
        collapsible: true,
        items: [
          { label: 'Overview', to: group.path, blurb: group.blurb },
          ...commandsIn(group).map((c) => ({
            // `ratline ` on every one of 86 entries is 8 characters of noise in a 16rem
            // column; the group heading already says which tool this is.
            label: c.command.name.replace(/^ratline /, ''),
            to: c.path,
            blurb: c.command.summary,
            keywords: c.command.keywords,
            mono: true,
          })),
        ],
      });
    }

    if (subject.concepts.length > 0) {
      groups.push({ title: 'How it works', items: subject.concepts.map(item) });
    }

    const inDepth = topicsOf(subject);
    if (inDepth.length > 0) {
      groups.push({
        title: 'In depth',
        items: inDepth.map((t) => ({ label: t.title, to: t.path, blurb: t.summary })),
      });
    }

    if (subject.guides.length > 0) {
      groups.push({ title: 'Guides and runbooks', items: subject.guides.map(item) });
    }

    const settings = settingsOf(subject);
    if (settings.length > 0) {
      groups.push({
        title: 'Settings',
        items: settings.map((s) => ({
          label: `${s.key}: ${s.settings.length} settings`,
          to: `/reference/config#cfg-${s.key}`,
          blurb: s.blurb,
        })),
      });
    }

    return { title: subject.title, groups };
  });
}

/**
 * Pages the subjects do not claim, surfaced rather than dropped.
 *
 * The old navigation listed every page by hand, so a page added later was simply absent
 * from the sidebar until somebody noticed — which is how thirteen topics and seven thousand
 * words stayed invisible for weeks. Anything unassigned now turns up here, under a heading
 * that says what it is.
 */
const crossCutting = [
  '/reference',
  '/reference/global-flags',
  '/reference/exit-codes',
  '/reference/json',
  '/reference/validation',
  '/reference/config',
  '/topics',
];

const startHere = ['/', '/quickstart', '/releases'];

function orphans(): NavItem[] {
  const spokenFor = new Set([...startHere, ...crossCutting]);
  const missed = unclaimed(
    Object.keys(pageMeta).filter((p) => !spokenFor.has(p)),
    topics.map((t) => t.name),
    commandGroups.map((g) => g.id),
  );
  return [
    ...missed.pages.map(item),
    ...missed.topics.map((name) => {
      const t = topics.find((x) => x.name === name)!;
      return { label: t.title, to: t.path, blurb: t.summary };
    }),
    ...missed.groups.map((id) => {
      const g = commandGroups.find((x) => x.id === id)!;
      return { label: g.title, to: g.path, blurb: g.blurb };
    }),
  ];
}

const rawNav: NavSection[] = [
  { title: 'Start here', items: startHere.map(item) },
  ...subjectSections(),
  { title: 'Across everything', items: crossCutting.map(item) },
];

const unassigned = orphans();
if (unassigned.length > 0) {
  rawNav.push({ title: 'Not yet filed', items: unassigned });
}

export const nav: NavSection[] = rawNav;

/**
 * Flat list of every page, for search and for the prev/next footer.
 *
 * Order matters: this is reading order, so prev/next walks a subject end to end — its
 * commands, then the concepts, then the runbooks — rather than jumping between kinds of
 * document the way the old kind-first structure did.
 */
export const allNavItems: NavItem[] = nav.flatMap((s) => [
  ...(s.items ?? []),
  ...(s.groups ?? []).flatMap((g) => g.items),
]);

/** Every command page, for the search index. */
export { allCommands };
