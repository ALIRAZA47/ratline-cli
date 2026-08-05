import { users } from './commands/users';
import { keys } from './commands/keys';
import { sites } from './commands/sites';
import { certs } from './commands/certs';
import { databases } from './commands/databases';
import { runtimes } from './commands/runtimes';
import { configuration } from './commands/configuration';
import { ops } from './commands/ops';
import type { Command, CommandGroup } from './types';

/**
 * Every command group, and the per-command page each one expands into.
 *
 * Separated from nav.ts because subjects.ts needs the list and nav.ts needs subjects —
 * importing it from nav would make that a cycle, which Vite resolves by handing one of
 * them a half-initialised module rather than by failing.
 *
 * The eight group pages used to carry every command stacked one after another: the sites
 * page was fourteen commands of full reference, which is not a page anybody reads — it is
 * a page people ctrl-F. Every command now has its own URL, the way `docker container run`
 * and `kubectl apply` do, and the group page became an index.
 */
export const commandGroups: CommandGroup[] = [
  users,
  keys,
  sites,
  certs,
  runtimes,
  databases,
  configuration,
  ops,
];

export const groupByPath = new Map(commandGroups.map((g) => [g.path, g]));
export const groupById = new Map(commandGroups.map((g) => [g.id, g]));

/**
 * The page path for one command.
 *
 * `user-add` in the group at /reference/user becomes /reference/user/add. An id that does
 * not begin with its group's id keeps the whole id: `site-deploy-key-create` is documented
 * in the keys group because that is where somebody looks for it, and shortening it to
 * /reference/key/create would read as a lie about what the command is called.
 */
export function commandPath(group: CommandGroup, cmd: Command): string {
  const slug = cmd.id.startsWith(`${group.id}-`) ? cmd.id.slice(group.id.length + 1) : cmd.id;
  return `${group.path}/${slug}`;
}

export interface CommandRef {
  group: CommandGroup;
  command: Command;
  path: string;
}

export const allCommands: CommandRef[] = commandGroups.flatMap((group) =>
  group.commands.map((command) => ({ group, command, path: commandPath(group, command) })),
);

export const commandByPath = new Map(allCommands.map((c) => [c.path, c]));

/** Every command in a group, with its page path resolved. */
export function commandsIn(group: CommandGroup): CommandRef[] {
  return allCommands.filter((c) => c.group.id === group.id);
}

/**
 * Two commands resolving to the same path would leave one of them unreachable, and the
 * symptom is a page that quietly shows the wrong command rather than an error. That mistake
 * has already happened once on this site — a new command group claimed /reference/config,
 * which was already the settings page — so it is checked rather than trusted.
 */
if (allCommands.length !== commandByPath.size) {
  const seen = new Set<string>();
  const clashes = allCommands.filter((c) => (seen.has(c.path) ? true : (seen.add(c.path), false)));
  throw new Error(
    `two commands share a page path: ${clashes.map((c) => `${c.command.id} → ${c.path}`).join(', ')}`,
  );
}
