import { CodeBlock } from '../components/CodeBlock';
import { PageHeader } from '../components/PageHeader';
import { Inline } from '../components/Inline';
import { Callout, H2 } from '../components/ui';
import { releases, type ReleaseChange } from '../data/releases';

/** A short label for the kinds of change worth marking. */
const KIND: Record<NonNullable<ReleaseChange['kind']>, { label: string; className: string }> = {
  fix: {
    label: 'fix',
    className:
      'border-[color-mix(in_oklab,var(--warn)_35%,transparent)] bg-warn-soft text-warn',
  },
  security: {
    label: 'security',
    className:
      'border-[color-mix(in_oklab,var(--danger)_35%,transparent)] bg-danger-soft text-danger',
  },
  feature: {
    label: 'new',
    className: 'border-[color-mix(in_oklab,var(--ok)_35%,transparent)] bg-ok-soft text-ok',
  },
};

export function Releases() {
  return (
    <article>
      <PageHeader
        eyebrow="Releases"
        title="Release notes"
        lede={
          <>
            What changed for whoever runs this, and what is still missing. Every release is
            published on{' '}
            <a href="https://github.com/ALIRAZA47/ratline-cli/releases">GitHub</a> with
            checksums; from v0.3.0 onward the binaries can be reproduced from the tag.
          </>
        }
      />

      <div className="prose">
        <p>
          Upgrading is one command. It checksums the new release against the release’s own{' '}
          <code>SHA256SUMS</code>, runs the new binary and asks its version, makes it prove
          it can read this server’s state — which is what catches a downgrade past a schema
          migration — then swaps it atomically and keeps the old one.
        </p>
      </div>

      <CodeBlock lang="shell" code={'sudo ratline update\nsudo ratline init   # picks up any corrected systemd units'} />

      <Callout tone="note" title="Run init after an upgrade">
        A release can ship a corrected unit or a new directory, and <code>init</code> is
        idempotent — it changes only what is missing, and never overwrites a file you have
        edited. <code>ratline update --rollback</code> puts the previous binary back if
        something is wrong.
      </Callout>

      {releases.map((r) => (
        <section key={r.version} id={r.version} className="mt-12 scroll-mt-24">
          <div className="prose">
            {/* H2 supplies its own anchor link; a second one nests <a> inside <a>,
                which is invalid HTML and produced a hydration error. */}
            <H2 id={r.version}>{r.version}</H2>
          </div>

          <div className="not-prose mb-5 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-muted">
            <time dateTime={r.date} className="font-mono text-xs">
              {r.date}
            </time>
            {r.assertions !== undefined && (
              <>
                <span aria-hidden="true" className="text-faint">
                  ·
                </span>
                <span>
                  {r.assertions} integration assertions,{' '}
                  <span className="text-ok">0 failed</span>
                </span>
              </>
            )}
            <span aria-hidden="true" className="text-faint">
              ·
            </span>
            <a
              href={`https://github.com/ALIRAZA47/ratline-cli/releases/tag/${r.version}`}
              className="text-sm"
            >
              downloads
            </a>
          </div>

          <p className="mb-6 max-w-[var(--content-w)] text-lg leading-relaxed text-strong">
            <Inline text={r.summary} />
          </p>

          {r.upgrade && <CodeBlock lang="shell" code={r.upgrade} />}

          <div className="not-prose space-y-4">
            {r.changes.map((c) => (
              <section
                key={c.title}
                className="rounded-[var(--radius-card)] border border-line bg-raised"
              >
                <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b border-line bg-sunken px-4 py-2.5">
                  {c.kind && (
                    <span
                      className={[
                        'inline-flex shrink-0 items-center rounded border px-1.5 py-0.5 font-mono text-2xs font-medium uppercase tracking-wide',
                        KIND[c.kind].className,
                      ].join(' ')}
                    >
                      {KIND[c.kind].label}
                    </span>
                  )}
                  <h3 className="font-semibold text-strong">
                    <Inline text={c.title} />
                  </h3>
                </div>
                <div className="px-4 py-3.5">
                  <p className="max-w-[var(--content-w)] text-[0.9375rem] leading-relaxed text-muted">
                    <Inline text={c.body} />
                  </p>
                  {c.code && (
                    <div className="mt-3">
                      <CodeBlock lang="shell" code={c.code} />
                    </div>
                  )}
                </div>
              </section>
            ))}
          </div>

          {r.known && r.known.length > 0 && (
            <div className="mt-5">
              <Callout tone="note" title="Still open at this release">
                <ul className="m-0 list-disc space-y-1 pl-5">
                  {r.known.map((k) => (
                    <li key={k}>
                      <Inline text={k} />
                    </li>
                  ))}
                </ul>
              </Callout>
            </div>
          )}
        </section>
      ))}
    </article>
  );
}
