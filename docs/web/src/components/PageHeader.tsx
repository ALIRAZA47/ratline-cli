import type { ReactNode } from 'react';

/**
 * The top of a page: what kind of document this is, what it is called, and one
 * paragraph of what it is for.
 *
 * The rule under it is doing real work — it separates the page's own voice from the
 * reference material below, which on most of these pages starts immediately.
 */
export function PageHeader({
  eyebrow,
  title,
  lede,
  meta,
}: {
  eyebrow?: string;
  title: string;
  lede?: ReactNode;
  meta?: ReactNode;
}) {
  return (
    <header className="not-prose mb-10 border-b border-line pb-8">
      {eyebrow && <p className="label text-muted">{eyebrow}</p>}
      <h1 className="mt-3 max-w-[34rem] text-3xl font-bold tracking-tight text-strong md:text-4xl">
        {title}
      </h1>
      {lede && (
        <p className="mt-4 max-w-[var(--content-w)] text-lg leading-relaxed text-muted [&_a]:font-medium [&_a]:text-accent [&_a]:underline [&_a]:underline-offset-2">
          {lede}
        </p>
      )}
      {meta && <div className="mt-5">{meta}</div>}
    </header>
  );
}
