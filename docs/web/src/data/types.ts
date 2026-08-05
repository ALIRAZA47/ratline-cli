/**
 * Types for the command surface.
 *
 * Everything on this site that describes a command, a flag, a default or an
 * exit code is one of these values, derived by hand from
 * `docs/reference/command-surface.md`, `internal/config/defaults.yaml`,
 * `internal/rlerr/rlerr.go` and `internal/validate/*.go`. Keeping it as typed
 * data rather than prose is what makes cross-linking, anchoring and the search
 * index possible without a build step.
 */

/** Implementation status, as marked in the command surface. */
export type Status = 'built' | 'planned';

export interface Flag {
  /** Long form, including the leading dashes: `--ssh-key`. */
  name: string;
  /** Short form, including the dash: `-y`. */
  short?: string;
  /** Value placeholder as the spec writes it: `<path|url|->`. Absent for booleans. */
  arg?: string;
  /** Human-readable type: `bool`, `path`, `duration`, `size`, `enum`… */
  type: string;
  /** Documented default. Absent means the flag has no default (it is off, or unset). */
  default?: string;
  repeatable?: boolean;
  /** Required for the command to run at all. */
  required?: boolean;
  /** Required only under some condition, described here. */
  requiredWhen?: string;
  description: string;
  /** Anything worth a second paragraph: a trap, a reason, a refusal. */
  note?: string;
}

export interface Example {
  title?: string;
  lang: 'shell' | 'json' | 'yaml' | 'nginx' | 'systemd' | 'text';
  code: string;
}

/** An exit code this command can produce, with the reason specific to it. */
export interface CommandExit {
  code: number;
  reason: string;
}

export interface Command {
  /** Stable slug, used for the anchor and the search index: `site-add`. */
  id: string;
  /** Full invocation without flags: `ratline site add`. */
  name: string;
  /** Positional arguments as the spec writes them: `<domain>`. */
  args?: string;
  status: Status;
  /** One sentence, used in listings and search results. */
  summary: string;
  /** Body paragraphs. Plain strings; inline markup is not interpreted. */
  description?: string[];
  /** Flag groups, so the runtime-specific flags of `site add` stay separable. */
  flagGroups?: { title: string; note?: string; flags: Flag[] }[];
  flags?: Flag[];
  /** What the command refuses to do, and why. */
  refuses?: string[];
  exits?: CommandExit[];
  examples?: Example[];
  /** Paths of related pages, `/reference/cert/issue` style. */
  seeAlso?: { label: string; to: string }[];
  /** Extra terms the search index should match on. */
  keywords?: string[];
}

export interface CommandGroup {
  id: string;
  /** Page title: `Sites`. */
  title: string;
  /** Route path: `/reference/site`. */
  path: string;
  /** One line for the nav and the reference index. */
  blurb: string;
  /** Introductory paragraphs for the group page. */
  intro?: string[];
  commands: Command[];
}

/** A configuration setting from defaults.yaml. */
export interface Setting {
  /** Dotted path as it appears in the file: `defaults.health_timeout`. */
  key: string;
  /** The default, rendered exactly as the YAML writes it. */
  value: string;
  type: string;
  /** The file's own comment, turned into prose. Empty when the file has none. */
  note?: string;
}

export interface SettingSection {
  /** Top-level YAML key: `defaults`. */
  key: string;
  title: string;
  blurb: string;
  settings: Setting[];
}

export interface ExitCode {
  code: number;
  name: string;
  meaning: string;
  /** What an operator or a script should do when it sees this. */
  action: string;
  /** Which commands most often produce it. */
  raisedBy?: string;
}
