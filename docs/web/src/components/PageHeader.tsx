import type { ReactNode } from 'react';

/**
 * The top of a page: what kind of document this is, what it is called, and one
 * paragraph of what it is for.
 *
 * The title is the serif at a display size and a regular weight — see the note in
 * `index.css` on why a 42px Newsreader at 400 carries a page better than the same size
 * bolded. The lede under it is the serif too, because it is the first sentence somebody
 * reads rather than a label on the page.
 *
 * The rule under it is doing real work: it separates the page's own voice from the
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
      {eyebrow && <p className="label text-faint">{eyebrow}</p>}
      <h1 className="mt-3.5 max-w-[20ch] text-2xl tracking-tight text-strong sm:text-3xl md:text-4xl">
        {title}
      </h1>
      {lede && (
        <p className="lede mt-4 [&_a]:text-accent [&_a]:underline [&_a]:underline-offset-2">
          {lede}
        </p>
      )}
      {meta && <div className="mt-5">{meta}</div>}
    </header>
  );
}
