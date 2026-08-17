import { commandGroups } from './groups';
import { configSections } from './config';
import { topicByName } from './topics';

/**
 * The documentation organised by subject rather than by kind of document.
 *
 * The site was split the way it was written: concepts here, command reference there,
 * guides somewhere else, and the in-depth topics somewhere else again. That is a sensible
 * order to *write* in and a poor one to *read* in. Somebody working on SSH access had to
 * visit four sections to find the commands, the three scopes, the lockout runbook and the
 * two settings that govern verification — and had no way of knowing the fourth existed.
 *
 * So a subject owns everything about itself: its commands, the concepts behind them, the
 * in-depth topics, the guides and runbooks, and the configuration sections that change how
 * it behaves. Nothing is duplicated — these are references to pages that already exist —
 * but each subject can now be read end to end.
 */

export interface Subject {
  id: string;
  /** The sidebar heading. */
  title: string;
  /** One line, shown on the subject's own index. */
  blurb: string;
  /** Command-group ids, from commandGroups. */
  commands: string[];
  /** Paths of concept pages. */
  concepts: string[];
  /** Topic slugs, from the binary's embedded pages. */
  topics: string[];
  /** Paths of guides and runbooks. */
  guides: string[];
  /** Configuration section keys whose settings change this subject's behaviour. */
  settings: string[];
}

export const subjects: Subject[] = [
  {
    id: 'access',
    title: 'Users and access',
    blurb:
      'A system account per tenant, and the three scopes an SSH key can have. Who can reach what, and how a key is revoked.',
    commands: ['user', 'key'],
    concepts: ['/concepts/ssh-scopes'],
    topics: ['ssh'],
    guides: [
      '/guides/contractor-access',
      '/guides/new-laptop-key',
      '/guides/ci-deploy-keys',
      '/guides/ssh-lockout',
    ],
    settings: ['users', 'ssh'],
  },
  {
    id: 'sites',
    title: 'Sites and runtimes',
    blurb:
      'One domain, one owner, one systemd unit. What each runtime generates, how it is supervised, and what a deploy does when a step fails.',
    commands: ['new', 'site', 'runtime'],
    concepts: ['/concepts/model', '/concepts/supervision'],
    topics: ['layout', 'sockets', 'static', 'node', 'bun', 'python', 'deploys', 'jobs', 'limits'],
    guides: [
      '/guides/deploy-node',
      '/guides/deploy-python',
      '/guides/github-actions',
      '/guides/agents',
      '/guides/node',
      '/guides/fastapi',
      '/guides/nextjs',
      '/guides/astro',
      '/guides/debug-502',
    ],
    settings: ['defaults', 'runtimes', 'nginx', 'ports'],
  },
  {
    id: 'tls',
    title: 'Certificates',
    blurb:
      'TLS as a resource with its own lifecycle, the rate limits that make an attempt cost something, and what to do when a renewal fails.',
    commands: ['cert'],
    concepts: ['/concepts/tls-lifecycle', '/concepts/rate-limits'],
    topics: ['tls'],
    guides: ['/guides/issue-cert', '/guides/cloudflare', '/guides/renewal-runbook'],
    settings: ['acme'],
  },
  {
    id: 'databases',
    title: 'Databases',
    blurb:
      'MongoDB databases and least-privilege users, one per tenant, with roles that cannot reach past their own database.',
    commands: ['db'],
    concepts: [],
    topics: ['databases'],
    guides: ['/guides/mongodb'],
    settings: ['databases'],
  },
  {
    id: 'operations',
    title: 'Running a server',
    blurb:
      'What is on the disk, what ratline remembers, what it promises never to do, and the order to check things in when something breaks.',
    commands: ['ops', 'config'],
    concepts: [
      '/concepts/transactions',
      '/concepts/security',
      '/concepts/filesystem',
      '/concepts/interactive',
    ],
    topics: ['state', 'safety', 'diagnose', 'health'],
    guides: ['/guides/inherited-server'],
    settings: ['paths', 'logging', 'features', 'server'],
  },
];

export const subjectById = new Map(subjects.map((s) => [s.id, s]));

/** The command group a subject claims, resolved. */
export function commandsOf(subject: Subject) {
  return subject.commands
    .map((id) => commandGroups.find((g) => g.id === id))
    .filter((g): g is NonNullable<typeof g> => g !== undefined);
}

/** The topics a subject claims, resolved and skipping any the binary no longer carries. */
export function topicsOf(subject: Subject) {
  return subject.topics
    .map((name) => topicByName.get(name))
    .filter((t): t is NonNullable<typeof t> => t !== undefined);
}

/** The configuration sections a subject claims. */
export function settingsOf(subject: Subject) {
  return subject.settings
    .map((key) => configSections.find((s) => s.key === key))
    .filter((s): s is NonNullable<typeof s> => s !== undefined);
}

/**
 * Which subject a command group belongs to, so a group's own page can offer the rest of
 * its subject. Built from the subjects rather than declared twice.
 */
export const subjectOfCommandGroup = new Map<string, Subject>(
  subjects.flatMap((s) => s.commands.map((id) => [id, s] as [string, Subject])),
);

/**
 * Anything the subjects do not claim, so a page added later is not silently orphaned.
 *
 * This is the check that matters: the previous structure listed everything explicitly, so
 * a new page was simply absent from the sidebar until somebody noticed. Here a page nobody
 * assigned still turns up, under a heading that says so.
 */
export function unclaimed(allPaths: string[], allTopicNames: string[], allGroupIds: string[]) {
  const claimedPages = new Set(subjects.flatMap((s) => [...s.concepts, ...s.guides]));
  const claimedTopics = new Set(subjects.flatMap((s) => s.topics));
  const claimedGroups = new Set(subjects.flatMap((s) => s.commands));
  return {
    pages: allPaths.filter((p) => !claimedPages.has(p)),
    topics: allTopicNames.filter((t) => !claimedTopics.has(t)),
    groups: allGroupIds.filter((g) => !claimedGroups.has(g)),
  };
}
