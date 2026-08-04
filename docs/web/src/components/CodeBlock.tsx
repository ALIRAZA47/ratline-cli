import { useCallback, useMemo, useState } from 'react';
import { normalizeLang, tokenize, type Lang } from '../lib/highlight';

interface Props {
  code: string;
  lang?: string;
  /** Shown in the block's header strip. Use a real path where there is one. */
  filename?: string;
  /** Render a dimmed `$` before each command line. Shell blocks only. */
  prompt?: boolean;
  /** Suppress the copy button — for output rather than input. */
  noCopy?: boolean;
  /** Extra label on the right of the header, e.g. "output". */
  tag?: string;
}

export function CodeBlock({ code, lang, filename, prompt, noCopy, tag }: Props) {
  const language: Lang = normalizeLang(lang);
  const source = code.replace(/\n+$/, '');
  const tokens = useMemo(() => tokenize(source, language), [source, language]);
  const [copied, setCopied] = useState(false);

  const copy = useCallback(() => {
    const write = navigator.clipboard?.writeText(source);
    if (!write) return;
    write.then(
      () => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1600);
      },
      () => setCopied(false),
    );
  }, [source]);

  const hasHeader = Boolean(filename || tag || !noCopy);

  return (
    <div className="not-prose my-4 overflow-hidden rounded-[var(--radius-card)] border border-line bg-code">
      {hasHeader && (
        <div className="flex items-center gap-3 border-b border-line bg-sunken px-3 py-1.5">
          {filename ? (
            <span className="truncate font-mono text-2xs text-muted">{filename}</span>
          ) : (
            <span className="font-mono text-2xs uppercase tracking-wider text-faint">
              {language === 'text' ? 'output' : language}
            </span>
          )}
          <span className="ml-auto flex items-center gap-2">
            {tag && (
              <span className="font-mono text-2xs uppercase tracking-wider text-faint">{tag}</span>
            )}
            {!noCopy && (
              <button
                type="button"
                onClick={copy}
                className="rounded border border-line px-1.5 py-0.5 text-2xs text-muted transition-colors hover:border-line-strong hover:bg-hover hover:text-strong"
                aria-label={copied ? 'Copied' : 'Copy code to clipboard'}
              >
                {copied ? 'copied' : 'copy'}
              </button>
            )}
          </span>
        </div>
      )}
      <div className="scroll-thin overflow-x-auto">
        <pre className="px-3.5 py-3 text-xs leading-[1.75] md:text-sm">
          <code className="block">
            {prompt ? withPrompts(source) : renderTokens(tokens)}
          </code>
        </pre>
      </div>
    </div>
  );
}

function renderTokens(tokens: ReturnType<typeof tokenize>) {
  return tokens.map((t, i) =>
    t.cls ? (
      <span key={i} className={t.cls}>
        {t.text}
      </span>
    ) : (
      <span key={i}>{t.text}</span>
    ),
  );
}

/**
 * Prefix each logical command with a dimmed `$`. Continuation lines (after a
 * trailing backslash), comments and blank lines get an aligned blank instead, so
 * a copied block is still valid without the sigils.
 */
function withPrompts(source: string) {
  let continuation = false;
  return source.split('\n').map((line, i) => {
    const isComment = line.trimStart().startsWith('#');
    const showPrompt = !continuation && !isComment && line.trim() !== '';
    const next = line.trimEnd().endsWith('\\');
    const el = (
      <span key={i} className="block">
        <span aria-hidden="true" className="mr-2 inline-block w-[0.6em] select-none text-faint">
          {showPrompt ? '$' : ''}
        </span>
        {renderTokens(tokenize(line, 'shell'))}
      </span>
    );
    continuation = next;
    return el;
  });
}
