import { useMemo, useState } from 'react';
import { CodeBlock } from '../components/CodeBlock';
import { PageHeader } from '../components/PageHeader';
import { Callout, H2 } from '../components/ui';
import { Inline } from '../components/Inline';
import { configPreamble, configSections } from '../data/config';

export function ConfigReference() {
  const [filter, setFilter] = useState('');

  const sections = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return configSections;
    return configSections
      .map((s) => ({
        ...s,
        settings: s.settings.filter(
          (x) =>
            x.key.toLowerCase().includes(q) ||
            x.value.toLowerCase().includes(q) ||
            (x.note ?? '').toLowerCase().includes(q),
        ),
      }))
      .filter((s) => s.settings.length > 0);
  }, [filter]);

  const count = configSections.reduce((n, s) => n + s.settings.length, 0);

  return (
    <article>
      <PageHeader
        eyebrow="Reference"
        title="Configuration"
        lede={
          <>
            {count} settings, generated from{' '}
            <code className="rounded border border-line bg-code px-1 py-0.5 font-mono text-[0.85em]">
              internal/config/defaults.yaml
            </code>
            . That file is both the source of every built-in default and the commented reference an
            operator reads, which keeps the two from drifting apart.
          </>
        }
      />

      <div className="prose">
        {configPreamble.map((p, i) => (
          <p key={i}>
            <Inline text={p} />
          </p>
        ))}
      </div>

      <Callout tone="note" title="Your file only needs what you are changing">
        <p>
          Values shown here are what ratline uses when a setting is absent, so anything you do not
          want to override can be deleted from <code>/etc/ratline/config.yaml</code> entirely. There
          is nothing to reload: the file is read on every invocation.
        </p>
      </Callout>

      <div className="not-prose sticky top-14 z-20 -mx-1 mb-6 bg-[color-mix(in_oklab,var(--bg)_92%,transparent)] px-1 py-3 backdrop-blur">
        <label className="flex items-center gap-2.5 rounded-md border border-line bg-raised px-3 py-2">
          <span aria-hidden="true" className="font-mono text-xs text-faint">
            /
          </span>
          <input
            type="search"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter settings — try memory, timeout, ssh, rate"
            className="min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-faint"
          />
          <span className="sr-only">Filter configuration settings</span>
          {filter && (
            <span className="font-mono text-2xs text-faint">
              {sections.reduce((n, s) => n + s.settings.length, 0)} of {count}
            </span>
          )}
        </label>
      </div>

      {sections.length === 0 && (
        <p className="text-sm text-muted">
          No setting matches “{filter}”. Only what is in <code>defaults.yaml</code> exists.
        </p>
      )}

      {sections.map((section) => (
        <section key={section.key} className="mb-9">
          <div className="prose">
            <H2 id={`cfg-${section.key}`}>{section.title}</H2>
            <p className="!mt-1 text-muted">
              <Inline text={section.blurb} />
            </p>
          </div>

          <dl className="not-prose mt-4 divide-y divide-[var(--border)] overflow-hidden rounded-[var(--radius-card)] border border-line">
            {section.settings.map((s) => (
              <div
                key={s.key}
                id={`setting-${s.key.replace(/\./g, '-')}`}
                className="scroll-mt-32 px-4 py-3 target:bg-accent-soft"
              >
                <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                  <dt className="font-mono text-sm font-medium text-strong">
                    <a href={`#setting-${s.key.replace(/\./g, '-')}`} className="no-underline">
                      {s.key}
                    </a>
                  </dt>
                  <span className="font-mono text-2xs text-faint">{s.type}</span>
                </div>
                <dd className="mt-1.5">
                  {s.value.includes('\n') ? (
                    <pre className="scroll-thin overflow-x-auto rounded border border-line bg-code px-2.5 py-2 font-mono text-xs text-fg">
                      {s.value}
                    </pre>
                  ) : (
                    <code className="inline-block rounded border border-line bg-code px-1.5 py-0.5 font-mono text-xs text-fg">
                      {s.value}
                    </code>
                  )}
                  {s.note && (
                    <p className="mt-2 max-w-[var(--container-measure)] border-l-2 border-line-strong pl-3 text-sm leading-relaxed text-muted">
                      <Inline text={s.note} />
                    </p>
                  )}
                </dd>
              </div>
            ))}
          </dl>
        </section>
      ))}

      <div className="prose">
        <H2 id="minimal">A realistic file</H2>
        <p>
          What an actual <code>/etc/ratline/config.yaml</code> tends to look like after{' '}
          <code>ratline init</code> and a little tuning. Everything absent falls back to the defaults
          above.
        </p>
      </div>

      <CodeBlock
        lang="yaml"
        filename="/etc/ratline/config.yaml"
        code={`version: 1

server:
  # This host is behind a provider NAT, so detection would find the wrong
  # address and cert preflight would refuse every issuance.
  public_ipv4:
    - 203.0.113.10
  admin_user: ali

defaults:
  # A Next.js build with source maps needs more than 512M during the build.
  memory_max: 1G
  client_max_body_size: 50M

users:
  quota_enabled: true
  reserved:
    - acme-internal

runtimes:
  node_default: "22"
  python_default: "3.12"

acme:
  email: ops@example.com
  tos_agreed: true
  alerts:
    email: ops@example.com
    warn_days: 14

logging:
  level: info`}
      />

      <Callout tone="warn" title="Two settings that will bite you if left alone">
        <p>
          <code>users.quota_enabled</code> is <code>false</code> by default, and while it is false{' '}
          <code>--quota</code> is <em>refused</em> rather than silently ignored. And{' '}
          <code>server.public_ipv4</code> matters on any host whose public address differs from a
          locally configured one: certificate preflight compares the domain’s A record against these,
          so on a NAT’d or floating-IP box, leaving it empty turns every issuance into a DNS-mismatch
          refusal.
        </p>
      </Callout>
    </article>
  );
}
