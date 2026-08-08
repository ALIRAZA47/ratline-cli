import { useCallback, useId, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { normalizeLang, tokenize, type Lang } from '../lib/highlight';

/* ------------------------------------------------------------------ the shell */

/**
 * The dark panel every code sample on this site sits in — in the light theme too.
 *
 * Code is what a reader came here to copy, so it gets one surface with one set of syntax
 * hues tuned against it, rather than two half-balanced sets that have to work on paper and
 * on slate. In the light theme it also means a code sample reads as an object on the page
 * instead of as a slightly grey paragraph.
 */
function Panel({
  label,
  right,
  children,
  className = '',
}: {
  label?: ReactNode;
  right?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    /* `first:mt-0 last:mb-0` so a panel dropped into a card that already has padding does
       not add a second gap to it. */
    <div
      className={`not-prose my-5 overflow-hidden rounded-[var(--radius-panel)] border border-panel-line bg-panel shadow-[var(--shadow-panel)] first:mt-0 last:mb-0 ${className}`}
    >
      {(label || right) && (
        <div className="flex min-h-[2.25rem] items-center gap-3 border-b border-panel-line bg-panel-chrome px-3">
          {label}
          {right && <span className="ml-auto flex items-center gap-2">{right}</span>}
        </div>
      )}
      {children}
    </div>
  );
}

/** The filename or language label in a panel's chrome. A real path where there is one. */
function PanelLabel({ children, mono = true }: { children: ReactNode; mono?: boolean }) {
  return (
    <span
      className={
        mono
          ? 'truncate font-mono text-2xs text-panel-muted'
          : 'label truncate text-panel-muted'
      }
    >
      {children}
    </span>
  );
}

function CheckIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" fill="none">
      <path
        d="M2.5 6.3l2.4 2.4L9.6 3.9"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function ClipboardIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" fill="none">
      <rect x="3.5" y="3.5" width="6.5" height="7" rx="1.2" stroke="currentColor" strokeWidth="1.2" />
      <path
        d="M8 3.2V2.6A1.1 1.1 0 006.9 1.5H3.1A1.1 1.1 0 002 2.6v5.1"
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinecap="round"
      />
    </svg>
  );
}

/** Copy, inside a dark panel's chrome. */
export function PanelCopy({ text, what = 'code' }: { text: string; what?: string }) {
  const [copied, setCopied] = useState(false);
  const copy = useCallback(() => {
    const write = navigator.clipboard?.writeText(text);
    if (!write) return;
    write.then(
      () => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1600);
      },
      () => setCopied(false),
    );
  }, [text]);

  return (
    <button
      type="button"
      onClick={copy}
      aria-label={copied ? `${what} copied to clipboard` : `Copy ${what} to clipboard`}
      className={[
        'flex shrink-0 items-center gap-1.5 rounded border px-1.5 py-0.5 font-mono text-2xs transition-colors',
        copied
          ? 'border-[color-mix(in_oklab,var(--ok)_45%,transparent)] text-ok'
          : 'border-panel-line-strong text-panel-muted hover:bg-[color-mix(in_oklab,white_8%,transparent)] hover:text-panel-fg',
      ].join(' ')}
    >
      {copied ? <CheckIcon /> : <ClipboardIcon />}
      <span aria-hidden="true">{copied ? 'copied' : 'copy'}</span>
    </button>
  );
}

/* -------------------------------------------------------------------- the code */

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
        <span
          aria-hidden="true"
          className="mr-2 inline-block w-[0.6em] select-none text-[var(--syn-punct)]"
        >
          {showPrompt ? '$' : ''}
        </span>
        {renderTokens(tokenize(line, 'shell'))}
      </span>
    );
    continuation = next;
    return el;
  });
}

function Source({ code, lang, prompt }: { code: string; lang: Lang; prompt?: boolean }) {
  const tokens = useMemo(() => tokenize(code, lang), [code, lang]);
  return (
    <div className="scroll-thin-dark overflow-x-auto">
      <pre className="px-4 py-3 text-xs leading-[1.75] text-panel-fg md:text-[0.8125rem]">
        <code className="block">{prompt ? withPrompts(code) : renderTokens(tokens)}</code>
      </pre>
    </div>
  );
}

/* --------------------------------------------------------------- the component */

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
  /**
   * What the command prints, attached to the bottom of the same panel.
   *
   * Two separate blocks left the reader to infer that the second was the first one's
   * output — which is the whole point of showing it. One panel, one hairline, one label:
   * the relationship is in the layout rather than in a sentence above it.
   */
  output?: string;
  /** Overrides the "output" label: "on a healthy server", "what you get back". */
  outputLabel?: string;
}

export function CodeBlock({
  code,
  lang,
  filename,
  prompt,
  noCopy,
  tag,
  output,
  outputLabel,
}: Props) {
  const language: Lang = normalizeLang(lang);
  const source = code.replace(/\n+$/, '');

  return (
    <Panel
      label={
        filename ? (
          <PanelLabel>{filename}</PanelLabel>
        ) : (
          <PanelLabel mono={false}>{language === 'text' ? 'output' : language}</PanelLabel>
        )
      }
      right={
        <>
          {tag && <span className="label shrink-0 text-panel-muted">{tag}</span>}
          {!noCopy && <PanelCopy text={source} what={prompt ? 'command' : 'code'} />}
        </>
      }
    >
      <Source code={source} lang={language} prompt={prompt} />
      {output !== undefined && (
        <div className="border-t border-panel-line bg-[color-mix(in_oklab,var(--code-bg-chrome)_60%,var(--code-bg))]">
          <p className="label px-4 pt-2 text-panel-muted">{outputLabel ?? 'output'}</p>
          <div className="scroll-thin-dark overflow-x-auto">
            <pre className="px-4 pb-3 pt-1.5 text-xs leading-[1.75] text-panel-muted md:text-[0.8125rem]">
              <code className="block">{output.replace(/\n+$/, '')}</code>
            </pre>
          </div>
        </div>
      )}
    </Panel>
  );
}

/* -------------------------------------------------------------------- variants */

export interface CodeVariant {
  /** Stable id, used for the tab's element ids. */
  id: string;
  /** What the reader is choosing between: "npm", "pnpm", "systemd timer", "cron". */
  label: string;
  code: string;
  lang?: string;
  filename?: string;
  prompt?: boolean;
}

/**
 * One thing in several forms, as tabs in a single panel's chrome.
 *
 * The same instruction under npm and pnpm, or a schedule written for systemd and for cron,
 * is one decision the reader has already made. Stacking both invites reading the wrong one;
 * tabs put the choice where it belongs and keep the page the length of one answer.
 */
export function CodeTabs({ variants, label }: { variants: CodeVariant[]; label: string }) {
  const [active, setActive] = useState(variants[0]?.id);
  const base = useId();
  const current = variants.find((v) => v.id === active) ?? variants[0];
  if (!current) return null;

  const move = (delta: number) => {
    const i = variants.findIndex((v) => v.id === current.id);
    const nextTab = variants[(i + delta + variants.length) % variants.length];
    setActive(nextTab.id);
    document.getElementById(`${base}-tab-${nextTab.id}`)?.focus();
  };

  const source = current.code.replace(/\n+$/, '');

  return (
    <Panel
      label={
        <div role="tablist" aria-label={label} className="-mb-px flex gap-1 overflow-x-auto">
          {variants.map((v) => {
            const selected = v.id === current.id;
            return (
              <button
                key={v.id}
                role="tab"
                id={`${base}-tab-${v.id}`}
                aria-selected={selected}
                aria-controls={`${base}-panel-${v.id}`}
                tabIndex={selected ? 0 : -1}
                onClick={() => setActive(v.id)}
                onKeyDown={(e) => {
                  if (e.key === 'ArrowRight') {
                    e.preventDefault();
                    move(1);
                  }
                  if (e.key === 'ArrowLeft') {
                    e.preventDefault();
                    move(-1);
                  }
                }}
                className={[
                  'shrink-0 border-b-2 px-2 py-2 font-mono text-2xs transition-colors',
                  selected
                    ? 'border-accent text-panel-fg'
                    : 'border-transparent text-panel-muted hover:text-panel-fg',
                ].join(' ')}
              >
                {v.label}
              </button>
            );
          })}
        </div>
      }
      right={<PanelCopy text={source} what={current.label} />}
    >
      <div
        role="tabpanel"
        id={`${base}-panel-${current.id}`}
        aria-labelledby={`${base}-tab-${current.id}`}
      >
        {current.filename && (
          <p className="border-b border-panel-line px-4 py-1.5 font-mono text-2xs text-panel-muted">
            {current.filename}
          </p>
        )}
        <Source code={source} lang={normalizeLang(current.lang)} prompt={current.prompt} />
      </div>
    </Panel>
  );
}

export { Panel as CodePanel, PanelLabel as CodePanelLabel };
