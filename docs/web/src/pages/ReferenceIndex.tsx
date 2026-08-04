import { Link } from 'react-router-dom';
import { PageHeader } from '../components/PageHeader';
import { CardLink, H2, StatusBadge } from '../components/ui';
import { Inline } from '../components/Inline';
import { commandGroups } from '../data/nav';

export function ReferenceIndex() {
  const total = commandGroups.reduce((n, g) => n + g.commands.length, 0);
  const built = commandGroups.reduce(
    (n, g) => n + g.commands.filter((c) => c.status === 'built').length,
    0,
  );

  return (
    <article>
      <PageHeader
        eyebrow="Reference"
        title="Command surface at a glance"
        lede={
          <>
            Every group, every verb, with its build status. Invocation is always{' '}
            <code className="rounded border border-line bg-code px-1 py-0.5 font-mono text-[0.85em]">
              ratline &lt;group&gt; &lt;verb&gt; [args]
            </code>
            .
          </>
        }
        meta={
          <p className="text-sm text-muted">
            {built === total
              ? `${total} commands, all built`
              : `${built} built · ${total - built} planned · ${total} documented`}
          </p>
        }
      />

      <div className="not-prose mb-10 grid gap-3 sm:grid-cols-2">
        <CardLink to="/reference/global-flags" title="Global flags">
          Eight flags every command accepts, and the four combinations that are refused as usage
          errors rather than one silently winning.
        </CardLink>
        <CardLink to="/reference/exit-codes" title="Exit codes">
          Eleven codes, declared once and never inferred from error text. This is what automation
          branches on.
        </CardLink>
        <CardLink to="/reference/json" title="The --json envelope">
          One object on stdout, the same shape for success and failure.
        </CardLink>
        <CardLink to="/reference/validation" title="Validation rules">
          The regexes and bounds the code actually enforces, with the reason each bound exists.
        </CardLink>
      </div>

      {commandGroups.map((group) => (
        <section key={group.id} className="mb-10">
          <div className="prose">
            <H2 id={group.id}>
              {group.title}
            </H2>
            <p className="!mt-1 text-muted">{group.blurb}</p>
          </div>
          <ul className="not-prose mt-4 divide-y divide-[var(--border)] overflow-hidden rounded-[var(--radius-card)] border border-line">
            {group.commands.map((cmd) => (
              <li key={cmd.id}>
                <Link
                  to={`${group.path}#${cmd.id}`}
                  className="group flex flex-col gap-1 px-4 py-3 no-underline transition-colors hover:bg-hover sm:flex-row sm:items-baseline sm:gap-4"
                >
                  <span className="flex min-w-0 items-center gap-2 sm:w-[21rem] sm:shrink-0">
                    <code className="truncate font-mono text-sm text-accent">{cmd.name}</code>
                    <StatusBadge status={cmd.status} size="xs" />
                  </span>
                  <span className="min-w-0 flex-1 text-sm leading-relaxed text-muted">
                    <Inline text={cmd.summary} />
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </article>
  );
}
