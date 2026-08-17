import { summaryOf, titleOf } from '../components/Markdown';

/**
 * The in-depth topics, imported from the same markdown the binary embeds.
 *
 * `docs/topics/*.md` is compiled into the binary for `ratline explain`, and copied into
 * this build by the prebuild step. One source of truth, which is what docs/README.md has
 * always claimed and — until now — was not true: the site did not render these at all, so
 * roughly seven thousand words of written, tested documentation existed only behind a
 * terminal command.
 *
 * Grouped rather than listed flat. Thirteen items in one sidebar block is a wall; the same
 * thirteen under four headings is a map of what the tool actually does.
 */

// Vite inlines these at build time, so there is no runtime fetch and nothing for the CSP
// to block. eager, because the sidebar needs every title and summary on first paint.
const files = import.meta.glob('../generated/topics/*.md', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

export interface Topic {
  /** The slug, which is also what `ratline explain <slug>` takes. */
  name: string;
  title: string;
  summary: string;
  body: string;
  path: string;
}

function slugOf(filePath: string): string {
  return filePath.replace(/^.*\//, '').replace(/\.md$/, '');
}

/** README is the section index for the directory, not a topic — the same exclusion
 *  `ratline explain` makes when it lists them. */
const notATopic = new Set(['README']);

export const topics: Topic[] = Object.entries(files)
  .map(([file, body]) => {
    const name = slugOf(file);
    return {
      name,
      title: titleOf(body) || name,
      summary: summaryOf(body),
      body,
      path: `/topics/${name}`,
    };
  })
  .filter((t) => !notATopic.has(t.name))
  .sort((a, b) => a.name.localeCompare(b.name));

export const topicByName = new Map(topics.map((t) => [t.name, t]));

/**
 * The groups the sidebar shows.
 *
 * Ordered as somebody learning the tool would meet them: what it builds on the disk, then
 * how an application runs, then the resources a site needs, then what to do when it
 * breaks. A topic named here that does not exist is skipped rather than rendered empty, so
 * removing a topic from the binary does not leave a dead sidebar entry.
 */
export const topicGroups: { title: string; blurb: string; names: string[] }[] = [
  {
    title: 'Anatomy',
    blurb: 'What ratline puts on the disk, and what it remembers.',
    names: ['layout', 'state', 'sockets'],
  },
  {
    title: 'Runtimes',
    blurb: 'How each kind of site is served and supervised.',
    names: ['static', 'node', 'bun', 'python'],
  },
  {
    title: 'Resources a site needs',
    blurb: 'Certificates, access and databases, each with its own lifecycle.',
    names: ['tls', 'ssh', 'databases'],
  },
  {
    title: 'Running it',
    blurb: 'Deploys, the ceilings that hold, and what to do when something breaks.',
    names: ['deploys', 'limits', 'safety', 'diagnose'],
  },
];

/** Every topic that a group claims, so a new one added to the binary is not silently
 *  left out of the navigation. */
export const groupedTopicNames = new Set(topicGroups.flatMap((g) => g.names));

/** ungroupedTopics is what the binary has and the groups above do not mention. Surfaced
 *  rather than hidden: a topic missing from the sidebar is a topic nobody reads. */
export const ungroupedTopics = topics.filter((t) => !groupedTopicNames.has(t.name));
