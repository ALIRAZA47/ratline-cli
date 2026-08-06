import type { ExitCode, Flag } from './types';

/** Global flags, verbatim from the command surface and confirmed against
 *  `internal/cli/globals.go`. */
export const globalFlags: Flag[] = [
  {
    name: '--json',
    type: 'bool',
    default: 'false',
    description: 'Machine-readable output on stdout; logs go to stderr.',
    note: 'Exactly one JSON object is written to stdout, so a caller can parse stdout unconditionally. Private key material never appears in it.',
  },
  {
    name: '--quiet',
    short: '-q',
    type: 'bool',
    default: 'false',
    description: 'Errors only.',
  },
  {
    name: '--verbose',
    short: '-v',
    type: 'bool',
    default: 'false',
    description: 'Debug logging.',
  },
  {
    name: '--dry-run',
    type: 'bool',
    default: 'false',
    description:
      'Print every mutation — files, commands, permissions — without executing it.',
    note: 'Reads still run, so the preview reflects the real system rather than a guess about it. No lock is taken.',
  },
  {
    name: '--yes',
    short: '-y',
    type: 'bool',
    default: 'false',
    description:
      'Assume yes. Required for destructive operations when there is no terminal.',
    note: '--yes also implies --no-input, because a flag that answers every prompt has nothing left to prompt for.',
  },
  {
    name: '--interactive',
    short: '-i',
    type: 'bool',
    default: 'false',
    description:
      'Ask which options to set before running. Works on every command.',
    note: 'Four commands — user add, site add, cert issue, key add — have richer wizards that also suggest a runtime or read a key from a URL, and -i runs those instead. Everywhere else it offers the command’s own flags, the same list the bare-`ratline` menu shows, and writes the answers into the flagset so what runs is exactly what would have run had you typed them. Positional arguments are still required on the command line: cobra validates those before any prompt could happen.',
  },
  {
    name: '--no-input',
    type: 'bool',
    default: 'false',
    description: 'Never prompt; error instead.',
    note: 'Implied when stdout is not a TTY. This is what keeps a prompt from hanging a CI pipeline forever.',
  },
  {
    name: '--config',
    arg: '<path>',
    type: 'path',
    default: '/etc/ratline/config.yaml',
    description: 'Configuration file. `RATLINE_CONFIG` is also honoured.',
    note: 'Precedence is --config, then RATLINE_CONFIG, then the default path. A missing file is not an error: the built-in defaults are used and mutating commands warn.',
  },
];

/** Combinations that are refused as usage errors rather than one silently
 *  winning. Each pair is checked in `Globals.resolve`. */
export const refusedFlagPairs: { pair: string; message: string; hint?: string }[] = [
  {
    pair: '--quiet --verbose',
    message: '--quiet and --verbose contradict each other',
  },
  {
    pair: '--json --interactive',
    message: '--json and --interactive contradict each other',
    hint: '--json exists for automation, which cannot answer prompts',
  },
  {
    pair: '--interactive --no-input',
    message: '--interactive and --no-input contradict each other',
  },
  {
    pair: '--interactive --yes',
    message: '--interactive and --yes contradict each other',
    hint: '--yes suppresses every prompt, including the wizard’s',
  },
];

/** The exit-code contract. Numbers and names are from `internal/rlerr/rlerr.go`;
 *  do not renumber. */
export const exitCodes: ExitCode[] = [
  {
    code: 0,
    name: 'ok',
    meaning: 'Success.',
    action:
      'Nothing. Note that a re-run of an already-satisfied operation also exits 0 with "already configured" — idempotency is deliberate, so a script may re-run safely.',
  },
  {
    code: 1,
    name: 'error',
    meaning: 'Unclassified failure.',
    action:
      'Read the message. A 1 means the failure did not fit a category, which is worth reporting: re-run with --verbose and include the output.',
  },
  {
    code: 2,
    name: 'usage',
    meaning: 'Bad flags, bad arguments, or failed validation.',
    action:
      'Fix the invocation. Nothing was touched. Every validator in internal/validate returns this code, so an invalid username, domain, size, duration or app module lands here.',
    raisedBy: 'Every command, before it reaches the system.',
  },
  {
    code: 3,
    name: 'precondition_failed',
    meaning: 'The system is not in a state where this can run.',
    action:
      'The message names the precondition. Typical causes: the user does not exist, the domain is already configured, the runtime is not installed, the port is taken, the binary is not running as root.',
    raisedBy: 'site add, cert issue, user delete, key add',
  },
  {
    code: 4,
    name: 'external_command_failed',
    meaning: 'An external command failed.',
    action:
      'The raw stderr of the child is included rather than summarised. Look at what nginx, systemctl, certbot, npm or pip actually said.',
    raisedBy: 'site deploy, cert issue, runtime install',
  },
  {
    code: 5,
    name: 'locked',
    meaning: 'Another ratline invocation holds the lock.',
    action:
      'Wait and retry. The message names the holder. Mutating commands take an exclusive flock on /run/ratline.lock and wait up to defaults.lock_timeout (30s) before failing fast.',
    raisedBy: 'Any mutating command.',
  },
  {
    code: 6,
    name: 'rollback_failed',
    meaning: 'The operation failed and so did its rollback.',
    action:
      'This one needs a human. The system is in a partial state, and the output names exactly what was rolled back and what could not be. Run `ratline doctor`, then `ratline reconcile` once you have read its report.',
  },
  {
    code: 7,
    name: 'health_check_failed',
    meaning: 'The application started, but never became healthy.',
    action:
      'The unit is running or has exited; the socket never answered a real HTTP request within defaults.health_timeout (30s). The last 20 lines of journalctl are printed automatically. A "successful" deploy that returns 502 is a bug, which is why this code exists.',
    raisedBy: 'site start, site restart, site deploy',
  },
  {
    code: 8,
    name: 'acme_challenge_failed',
    meaning: 'The ACME challenge failed.',
    action:
      'Distinct from 4 so automation can tell "certbot is broken" from "validation did not pass". Check DNS, the webroot fetch and any proxy in front of the box.',
    raisedBy: 'cert issue, cert renew',
  },
  {
    code: 9,
    name: 'rate_limited',
    meaning: 'The attempt would exceed a CA rate limit.',
    action:
      'Nothing was sent to the CA — the budget is tracked locally and checked first. The message includes a retry-after. Use --staging or --dry-run while you debug.',
    raisedBy: 'cert issue',
  },
  {
    code: 10,
    name: 'input_required',
    meaning: 'A prompt was needed but input is unavailable.',
    action:
      'Supply the missing flag, or pass --yes for a confirmation. This is what you get instead of a hung build when a command wanted a terminal and there was not one.',
  },
];
