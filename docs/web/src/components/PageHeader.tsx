import type { ReactNode } from 'react';

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
    <header className="not-prose mb-9 border-b border-line pb-7">
      {eyebrow && (
        <p className="font-mono text-2xs uppercase tracking-[0.18em] text-faint">{eyebrow}</p>
      )}
      <h1 className="mt-2 max-w-[34rem] text-3xl font-semibold tracking-tight text-strong md:text-4xl">
        {title}
      </h1>
      {lede && (
        <p className="mt-4 max-w-[var(--container-measure)] text-lg leading-relaxed text-muted">
          {lede}
        </p>
      )}
      {meta && <div className="mt-4">{meta}</div>}
    </header>
  );
}
