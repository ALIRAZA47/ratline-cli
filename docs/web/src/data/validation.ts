/**
 * The real validation rules, taken from `internal/validate/*.go` rather than
 * paraphrased from the spec. Every one of these returns exit code 2 and touches
 * nothing: the validators are pure functions with no system access, which is
 * what makes them cheap to fuzz.
 */

export interface Rule {
  id: string;
  subject: string;
  /** The regex or bound as the code writes it. */
  rule?: string;
  source: string;
  points: string[];
  /** A real message the validator produces. */
  message?: string;
  hint?: string;
}

export const validationIntro = [
  'Inputs routinely arrive from a web form by way of an automation layer, so each one is treated as hostile. Validation happens before anything on the system is touched, and it returns exit code 2 — meaning nothing was changed and the fix is in the invocation.',
  'The rules below are the ones the code actually enforces. Where a bound looks arbitrary, the reason is given: most of them are a limit in something downstream — sockaddr_un, systemd, DNS, nginx — rather than a preference.',
];

export const rules: Rule[] = [
  {
    id: 'username',
    subject: 'Username',
    rule: '^[a-z_][a-z0-9_-]{0,31}$',
    source: 'internal/validate/username.go',
    points: [
      'At most 32 characters. That is the practical limit for a Linux account name that also has to fit inside a systemd unit name and an nginx log path.',
      'Must not end with a hyphen, and must contain at least one letter or digit — so `_` and `___` are refused.',
      'Checked against a built-in reserved list of 40-odd names (root, admin, nginx, www-data, ratline, postgres, ubuntu, …), extended by users.reserved from configuration. Both lists are checked.',
      'Checked for a collision in /etc/passwd and /etc/group. ratline creates a group per user and will not adopt a group it did not create.',
      'Syntax and availability are separate steps, because availability touches the system.',
    ],
    message: 'invalid username "Acme_Web"',
    hint: 'use lowercase letters, digits, underscores and hyphens, starting with a letter or underscore, for example "acme-web"',
  },
  {
    id: 'domain',
    subject: 'Domain',
    rule: 'per label: ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$',
    source: 'internal/validate/domain.go',
    points: [
      'At most 253 bytes in punycode form — the DNS limit on a fully qualified name — and at least two labels. Each label is at most 63 characters.',
      'Internationalised names are converted to punycode before use, and the canonical lowercase punycode form — not the operator’s input — is what gets written into configs. A name is normalised exactly once.',
      'A set of characters is refused outright before anything else runs, because they must never survive into an nginx server_name, a certbot argument or a filesystem path: / \\ ; $ ` \' " | & < > * ? ! ( ) { } [ ] # % ^ ~ , : = space and tab. Control characters are refused too.',
      'One trailing dot is accepted as the legal absolute form and stripped. A leading dot, an empty label, or ".." is refused.',
      'A last label that is all digits is refused: it looks like an IP address, and certificates cannot be issued for bare IP addresses.',
      'A bare public suffix is refused, because it is not registrable and issuing for it would fail at the CA after burning a rate-limit attempt. `com` gets "use a name under it, for example app.com".',
      'IDNA CheckHyphens is deliberately off: it rejects a double hyphen in the third and fourth positions, which would refuse perfectly ordinary registered domains like my--site.com. Leading and trailing hyphens are still caught by the label rule.',
      'Wildcards are validated separately, and only a leading "*." is legal. Neither DNS nor the CAs support *.*.example.com or a*.example.com, and accepting them here would fail much later with a worse message.',
      'The registrable domain (eTLD+1) is computed from the public suffix list, because that is the unit the CA applies its per-domain rate limits to.',
    ],
    message:
      'invalid domain "Exam ple.com": the character " " is not allowed in a hostname',
  },
  {
    id: 'app-module',
    subject: 'App module',
    rule: '^[A-Za-z_][A-Za-z0-9_.]*:[A-Za-z_][A-Za-z0-9_]*$',
    source: 'internal/validate/module.go',
    points: [
      'The form is module.path:callable — app.main:app, myproject.wsgi:application.',
      'At most 255 characters. The module path must not start or end with a dot, and every dotted segment must independently be a valid Python identifier.',
      'This string lands on a Gunicorn command line and inside a systemd unit file, which is why it is pinned to identifier characters and a single colon rather than merely "no spaces".',
    ],
    message: 'invalid application module "app.main"',
    hint: 'the form is module.path:callable, for example app.main:app or myproject.wsgi:application',
  },
  {
    id: 'path-containment',
    subject: 'Path containment',
    source: 'internal/validate/path.go',
    points: [
      'A document root is always under the owning user’s home. There are two checks, and the second is the one that matters.',
      'The lexical check (WithinRoot) cleans the path and compares prefixes. It is used for paths that do not exist yet.',
      'The resolving check (ResolveWithin) resolves symlinks before comparing, so a link planted inside a tenant’s home cannot point a document root at /etc or at another tenant’s files. The candidate need not exist: the deepest existing ancestor is resolved and the remainder appended, which is what lets it run before a directory tree has been created.',
      'ENOTDIR is treated as "does not exist" — a path whose parent is a regular file reports ENOTDIR, and for containment purposes that is the same answer.',
      'Relative operator-supplied directory names (--root, --build-output, --static-dir) are matched against ^[A-Za-z0-9_][A-Za-z0-9._-]*$ per segment. A leading underscore is permitted because _next and _assets are real build-output directories; a leading dot is not, because nginx is configured to deny dotfiles; a leading hyphen is not, because git and tar would read it as a flag.',
      'Absolute paths, backslashes, empty segments, "." and ".." are refused rather than cleaned away silently.',
    ],
    message:
      'public resolves to /etc/nginx, which is outside /home/acme/example.com',
    hint: 'symlinks are followed before this check; a path may not escape the owner’s home directory',
  },
  {
    id: 'slug',
    subject: 'Slug and unit name',
    source: 'internal/validate/slug.go',
    points: [
      '`<user>-<domain>` with dots replaced by underscores, lowercased, and anything else collapsed to a hyphen. alice + example.com → alice-example_com, giving ratline-alice-example_com.service.',
      'Dots become underscores so that alice/example.com reads as alice-example_com. Unit names accept dots, but a name with two separators carrying different meanings is much harder to scan in a systemctl listing.',
      'Capped at 64 characters, and the limit is driven by sockaddr_un.sun_path, not by systemd. A site’s socket lives at /run/ratline/<slug>/app.sock; sun_path is 108 bytes on Linux and the fixed part of that path is 22 characters, so a slug over about 85 characters produces a socket the application cannot bind — which surfaces as an opaque "invalid argument" at start time. 64 leaves room for multi-instance suffixes like app-1.sock.',
      'Truncating alone would risk two long domains colliding on one unit name, so an over-long slug is truncated and given an 8-character SHA-256 suffix.',
      'The result is collision-checked against existing units.',
    ],
  },
  {
    id: 'command-strings',
    subject: 'Command strings',
    source: 'internal/system/shellwords.go',
    points: [
      '--start-command, --build-command and --install-command are parsed into an argv slice. There is no shell in the binary registry at all, which makes argv-only execution structural rather than a convention.',
      'These constructs are refused, each named in the error along with its position: $( and ${ and backtick (substitution and expansion), && and || (chaining), > and >> and < and << (redirection), ; (separator), | (pipe), & (backgrounding), $ (expansion), newline, carriage return, NUL.',
      'sh, bash, zsh, dash, ksh, csh, tcsh, fish, env, eval, exec, nohup, setsid, sudo, su, doas, xargs, time, watch and script may not be the program: they exist to reinterpret their arguments, which would defeat argv-only execution.',
      'Quoting is supported so arguments may contain spaces. Single and double quotes are literal — there is no expansion to suppress — and a backslash escapes the next character outside single quotes. An unterminated quote or a trailing backslash is an error.',
      'Bounds: 4096 bytes, 128 words, 1024 bytes per word.',
      'Glob and brace characters pass through literally because no shell expands them. That is usually a mistake, so it warns rather than failing.',
    ],
    message:
      'command contains "&&" (command chaining) at position 12, which needs a shell',
    hint: 'put the pipeline in a script inside your repository and reference that script instead, for example --start-command "./bin/start"',
  },
  {
    id: 'label',
    subject: 'SSH key label',
    source: 'internal/validate/misc.go',
    points: [
      'Required on every key, at most 64 characters, printable.',
      'A double quote and a backslash are refused rather than escaped: the label is rendered inside a label="…" comment, and a validator that cannot produce an ambiguous file is worth more than one that accepts every character.',
      'Newlines and NUL bytes are refused.',
    ],
    message: 'the label is empty',
    hint: 'every key needs a label so it can be recognised later, for example --label "Ali MacBook"',
  },
  {
    id: 'size-duration',
    subject: 'Sizes, durations, percentages, dates',
    source: 'internal/validate/misc.go',
    points: [
      'Sizes are systemd-style: 512M, 1.5G, 20G, or a bare byte count. Units are powers of 1024, matching systemd and human expectations for RAM.',
      'Durations extend Go’s syntax with d (day) and w (week), because key expiry and certificate windows are naturally expressed that way: 30s, 15m, 90d, 2w. Zero and negative are refused, and so is anything over 100 years.',
      'CPUQuota is a percentage; over 100% is legal and means more than one core. 0% is refused, because it would stop the application entirely.',
      '--expires takes either an absolute date (2026-12-31) or a duration from now (90d). A bare date means valid through the end of that day, in UTC, which is what an operator writing 2026-12-31 intends — and OpenSSH compares against UTC too. A date in the past is refused.',
      'Ports must be 1024–65535. A configured allocation range must span at least 16 ports.',
    ],
  },
  {
    id: 'env',
    subject: 'Environment variables',
    rule: 'name: ^[A-Za-z_][A-Za-z0-9_]*$',
    source: 'internal/validate/misc.go',
    points: [
      'Names are at most 128 characters.',
      'A value containing a newline is refused, because systemd’s EnvironmentFile cannot represent multi-line values. The hint says to store the payload in a file inside the site directory and point a variable at it — which is the right answer for a PEM key or a JSON service account.',
      'A NUL byte is refused, and values are capped at 32768 bytes.',
      'LD_PRELOAD, LD_LIBRARY_PATH, LD_AUDIT and DYLD_INSERT_LIBRARIES are refused: they change how the runtime itself loads code, which is a foot-gun rather than a feature.',
    ],
  },
  {
    id: 'git',
    subject: 'Git URL and ref',
    source: 'internal/validate/misc.go',
    points: [
      'https:// and ssh:// (or scp-style user@host:path) only, at most 512 characters, ASCII printable with no spaces.',
      'Refused on purpose, each with its own reason: ext:: runs arbitrary commands; file:: local clones are not supported; git:// is neither authenticated nor encrypted; http:// is not encrypted.',
      'A leading hyphen is refused because git would read it as a flag. A URL containing ".." is refused.',
      'Branch names follow a reduced form of git check-ref-format: no spaces, no ~ ^ : ? * [ or backslash, no "..", no .lock suffix, no leading or trailing slash, no leading hyphen.',
    ],
  },
  {
    id: 'fingerprint',
    subject: 'SSH fingerprint',
    rule: '^SHA256:[A-Za-z0-9+/]{43}=?$',
    source: 'internal/validate/misc.go',
    points: [
      'The SHA256 form printed by ssh-keygen -lf.',
      'The SHA256: prefix is added for you when you omit it, so pasting just the base64 body works.',
    ],
  },
  {
    id: 'email',
    subject: 'Email',
    rule: '^[^\\s@,;<>"\']+@[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?\\.[A-Za-z]{2,63}$',
    source: 'internal/validate/misc.go',
    points: [
      'Deliberately conservative rather than RFC-complete: this address reaches a certificate authority, and a typo means no expiry warnings.',
      'At most 254 characters.',
    ],
  },
  {
    id: 'runtime-versions',
    subject: 'Runtime versions, entry points, package managers',
    source: 'internal/validate/module.go',
    points: [
      'Node: a major version (22) or a full one (22.11.0); a leading "v" is stripped.',
      'Python: 3.x or 3.x.y. Python 2 is not supported, and the validator says so rather than failing later.',
      'A Node entry point must be a .js, .mjs, .cjs, .ts, .mts or .cts file, and any directory part is validated as a subdirectory — so dist/main.js is fine and ../../etc/main.js is not.',
      'Package manager: npm, pnpm, yarn or bun.',
      'Runtime: static, node or python.',
      'A systemd unit name needs a .service, .timer, .socket or .target suffix, is at most 255 characters, and may only contain characters systemd accepts.',
    ],
  },
];
