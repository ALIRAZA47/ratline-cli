import { useCallback, useState } from 'react';

interface Props {
  /**
   * A transcript. Lines are classified by their first character, matching what
   * the CLI actually writes:
   *   `$ ` / `# `  the operator's command
   *   `→ `         info      (no colour)
   *   `! `         warn      (ANSI 33, yellow)
   *   `✗ `         error     (ANSI 31, red)
   *   `· `         debug     (ANSI 2, dim)
   *   `~ `         a comment from us, not from the CLI
   * Anything else is plain output.
   */
  children: string;
  title?: string;
}

type Kind = 'cmd' | 'info' | 'warn' | 'error' | 'debug' | 'note' | 'plain';

function classify(line: string): { kind: Kind; text: string } {
  if (line.startsWith('$ ') || line === '$') return { kind: 'cmd', text: line.slice(2) };
  if (line.startsWith('# ')) return { kind: 'cmd', text: line.slice(2) };
  if (line.startsWith('→ ')) return { kind: 'info', text: line.slice(2) };
  if (line.startsWith('! ')) return { kind: 'warn', text: line.slice(2) };
  if (line.startsWith('✗ ')) return { kind: 'error', text: line.slice(2) };
  if (line.startsWith('· ')) return { kind: 'debug', text: line.slice(2) };
  if (line.startsWith('~ ')) return { kind: 'note', text: line.slice(2) };
  return { kind: 'plain', text: line };
}

const SIGIL: Record<Kind, string> = {
  cmd: '$',
  info: '→',
  warn: '!',
  error: '✗',
  debug: '·',
  note: '#',
  plain: ' ',
};

const CLS: Record<Kind, string> = {
  cmd: 'term-bold',
  info: 'term-info',
  warn: 'term-warn',
  error: 'term-error',
  debug: 'term-debug',
  note: 'term-dim',
  plain: 'term-info',
};

/**
 * A terminal transcript, rendered with the sigils and colours the CLI really
 * uses. Copying yields the commands only, because that is what a reader wants
 * from a transcript.
 */
export function Terminal({ children, title }: Props) {
  const lines = children.replace(/^\n+|\n+$/g, '').split('\n').map(classify);
  const [copied, setCopied] = useState(false);

  const commands = lines
    .filter((l) => l.kind === 'cmd')
    .map((l) => l.text)
    .join('\n');

  const copy = useCallback(() => {
    const write = navigator.clipboard?.writeText(commands);
    if (!write) return;
    write.then(
      () => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1600);
      },
      () => setCopied(false),
    );
  }, [commands]);

  return (
    <div className="not-prose my-5 overflow-hidden rounded-[var(--radius-panel)] border border-panel-line bg-panel shadow-[var(--shadow-panel)]">
      <div className="flex min-h-[2.25rem] items-center gap-2.5 border-b border-panel-line bg-panel-chrome px-3">
        <span aria-hidden="true" className="flex gap-1.5">
          <span className="size-2 rounded-full bg-panel-line-strong" />
          <span className="size-2 rounded-full bg-panel-line-strong" />
          <span className="size-2 rounded-full bg-panel-line-strong" />
        </span>
        <span className="ml-1 truncate font-mono text-2xs text-panel-muted">
          {title ?? 'root@server'}
        </span>
        {commands && (
          <button
            type="button"
            onClick={copy}
            className={[
              'ml-auto flex shrink-0 items-center gap-1.5 rounded border px-1.5 py-0.5 font-mono text-2xs transition-colors',
              copied
                ? 'border-[color-mix(in_oklab,var(--ok)_45%,transparent)] text-ok'
                : 'border-panel-line-strong text-panel-muted hover:bg-[color-mix(in_oklab,white_8%,transparent)] hover:text-panel-fg',
            ].join(' ')}
            aria-label={
              copied
                ? 'Commands copied to clipboard'
                : 'Copy the commands from this transcript, without their output'
            }
          >
            <span aria-hidden="true">{copied ? 'copied' : 'copy commands'}</span>
          </button>
        )}
      </div>
      <div className="scroll-thin-dark overflow-x-auto">
        <pre className="px-4 py-3 text-xs leading-[1.8] md:text-[0.8125rem]">
          <code className="block">
            {lines.map((l, i) => (
              <span key={i} className={`block ${CLS[l.kind]}`}>
                <span
                  aria-hidden="true"
                  className={`mr-2 inline-block w-[0.7em] select-none ${
                    l.kind === 'plain' ? '' : 'opacity-80'
                  }`}
                >
                  {l.kind === 'plain' ? '' : SIGIL[l.kind]}
                </span>
                {l.text || ' '}
              </span>
            ))}
          </code>
        </pre>
      </div>
    </div>
  );
}
