/**
 * A ~180-line tokenizer for the five languages this site actually shows: shell,
 * nginx, systemd unit files, YAML and JSON.
 *
 * Written rather than imported because a docs site should not ship a
 * general-purpose highlighter to colour thirty code blocks. Each language is one
 * ordered list of patterns, combined into a single sticky regex.
 */

export type Lang = 'shell' | 'nginx' | 'systemd' | 'yaml' | 'json' | 'text';

export interface Token {
  text: string;
  cls?: string;
}

interface Pattern {
  re: RegExp;
  cls: string | ((m: RegExpExecArray) => string | undefined);
}

/** Programs that appear at the head of a command in this documentation. */
const KNOWN_CMDS =
  'ratline|sudo|systemctl|systemd-analyze|journalctl|loginctl|nginx|certbot|openssl|' +
  'ssh-keygen|ssh-keyscan|ssh|sftp|scp|rsync|curl|nc|ping|dig|drill|host|jq|git|' +
  'npm|pnpm|yarn|bun|node|python3|python|pip|uv|gunicorn|uvicorn|go|' +
  'apt-get|apt|install|cat|ls|tail|head|grep|tr|chmod|chown|stat|mkdir|cp|mv|rm|ln|' +
  'useradd|usermod|getent|visudo|id|logrotate|ss|lsof|namei|du|df|ps|' +
  'test|for|do|done|if|then|fi|case|esac|echo|printf|export|set|man';

const SHELL: Pattern[] = [
  { re: /#[^\n]*/y, cls: 'tok-comment' },
  { re: /'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"/y, cls: 'tok-string' },
  { re: /\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$[A-Za-z_][A-Za-z0-9_]*|\$\d/y, cls: 'tok-var' },
  { re: /--?[A-Za-z][A-Za-z0-9-]*/y, cls: 'tok-flag' },
  { re: new RegExp(`\\b(?:${KNOWN_CMDS})\\b`, 'y'), cls: 'tok-cmd' },
  { re: /\|\||&&|[|;&<>]+/y, cls: 'tok-keyword' },
  { re: /\\(?=\n|$)/y, cls: 'tok-punct' },
  { re: /\b\d+(?:\.\d+)*[A-Za-z%]?\b/y, cls: 'tok-number' },
];

const NGINX: Pattern[] = [
  { re: /#[^\n]*/y, cls: 'tok-comment' },
  { re: /"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'/y, cls: 'tok-string' },
  { re: /\$[A-Za-z_][A-Za-z0-9_]*/y, cls: 'tok-var' },
  {
    re: /(^|\n)([ \t]*)([a-z_][a-z0-9_]*)/y,
    cls: 'tok-key',
  },
  { re: /\b(?:on|off|http|https|unix|default_server|ssl|max|permanent)\b/y, cls: 'tok-keyword' },
  { re: /\b\d+(?:\.\d+){0,3}[a-zA-Z%]*\b/y, cls: 'tok-number' },
  { re: /[{};]/y, cls: 'tok-punct' },
];

const SYSTEMD: Pattern[] = [
  { re: /[#;][^\n]*/y, cls: 'tok-comment' },
  { re: /^\[[A-Za-z]+\]$/my, cls: 'tok-keyword' },
  { re: /(^|\n)([A-Za-z][A-Za-z0-9]*)(?==)/y, cls: 'tok-key' },
  { re: /"(?:[^"\\]|\\.)*"/y, cls: 'tok-string' },
  { re: /%[a-zA-Z]/y, cls: 'tok-var' },
  { re: /\b(?:yes|no|true|false|strict|tmpfs|always|on-failure)\b/y, cls: 'tok-keyword' },
  { re: /\b\d+(?:\.\d+)*[A-Za-z%]?\b/y, cls: 'tok-number' },
  { re: /=/y, cls: 'tok-punct' },
];

const YAML: Pattern[] = [
  { re: /#[^\n]*/y, cls: 'tok-comment' },
  { re: /(^|\n)([ \t]*)(-\s)?([A-Za-z_][A-Za-z0-9_.-]*)(?=:)/y, cls: 'tok-key' },
  { re: /"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'/y, cls: 'tok-string' },
  { re: /\b(?:true|false|null|yes|no)\b/y, cls: 'tok-keyword' },
  { re: /\b\d+(?:\.\d+)*[A-Za-z%]?\b/y, cls: 'tok-number' },
  { re: /[[\]{}:,-]/y, cls: 'tok-punct' },
];

const JSONL: Pattern[] = [
  { re: /"(?:[^"\\]|\\.)*"(?=\s*:)/y, cls: 'tok-key' },
  { re: /"(?:[^"\\]|\\.)*"/y, cls: 'tok-string' },
  { re: /\b(?:true|false|null)\b/y, cls: 'tok-keyword' },
  { re: /-?\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b/y, cls: 'tok-number' },
  { re: /[{}[\],:]/y, cls: 'tok-punct' },
];

const GRAMMARS: Record<Lang, Pattern[]> = {
  shell: SHELL,
  nginx: NGINX,
  systemd: SYSTEMD,
  yaml: YAML,
  json: JSONL,
  text: [],
};

/**
 * Tokenize source. Unmatched runs are emitted as plain tokens so the output is
 * always exactly the input when concatenated — a highlighter that can lose a
 * character is worse than no highlighter, because the reader copies the code.
 */
export function tokenize(source: string, lang: Lang): Token[] {
  const patterns = GRAMMARS[lang];
  if (patterns.length === 0) return [{ text: source }];

  const out: Token[] = [];
  let plainFrom = 0;
  let i = 0;

  const flushPlain = (to: number) => {
    if (to > plainFrom) out.push({ text: source.slice(plainFrom, to) });
  };

  while (i < source.length) {
    let matched = false;
    for (const p of patterns) {
      p.re.lastIndex = i;
      const m = p.re.exec(source);
      if (!m || m.index !== i || m[0].length === 0) continue;

      // Some patterns capture leading whitespace or a newline so they can anchor
      // to the start of a line. Emit that prefix as plain text.
      const full = m[0];
      const last = m[m.length - 1];
      const body = last && last.length > 0 ? last : full;
      const bodyAt = full.lastIndexOf(body);
      const prefix = bodyAt > 0 ? full.slice(0, bodyAt) : '';

      flushPlain(i);
      if (prefix) out.push({ text: prefix });
      const cls = typeof p.cls === 'function' ? p.cls(m) : p.cls;
      out.push({ text: body, cls });
      i += full.length;
      plainFrom = i;
      matched = true;
      break;
    }
    if (!matched) i += 1;
  }
  flushPlain(source.length);
  return out;
}

/** Language for a fenced block label, defaulting to text rather than guessing. */
export function normalizeLang(label: string | undefined): Lang {
  switch ((label ?? '').toLowerCase()) {
    case 'sh':
    case 'bash':
    case 'zsh':
    case 'shell':
    case 'console':
      return 'shell';
    case 'nginx':
    case 'conf':
      return 'nginx';
    case 'systemd':
    case 'ini':
    case 'unit':
      return 'systemd';
    case 'yaml':
    case 'yml':
      return 'yaml';
    case 'json':
      return 'json';
    default:
      return 'text';
  }
}
