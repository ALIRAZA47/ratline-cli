import { Link, Navigate, useLocation } from 'react-router-dom';
import { PageHeader } from '../components/PageHeader';
import { CardLink, H2, StatusBadge } from '../components/ui';
import { Inline } from '../components/Inline';
import { commandsIn, groupByPath } from '../data/groups';
import { meta } from '../data/pages';
import { settingsOf, subjectOfCommandGroup, topicsOf } from '../data/subjects';

/**
 * A command group: the index of its commands, then the rest of its subject.
 *
 * The subject block at the bottom is the point. Reading `ratline key` used to tell you the
 * eleven verbs and nothing else — not that there are three SSH scopes and what they do and
 * do not enforce, not that there is a lockout runbook worth reading before you need it, not
 * that two settings govern whether a key is verified before it is trusted. Those pages all
 * existed. They were in three other sections of this site.
 */
export function CommandGroupPage() {
  const { pathname, hash } = useLocation();
  const group = groupByPath.get(pathname);
  if (!group) return <Navigate to="/reference" replace />;

  const commands = commandsIn(group);

  // Every command used to be an anchor on this page — /reference/user#user-add was a
  // published URL, and the site has been live for four releases. Those links now land on
  // the command's own page rather than on an index with no such anchor.
  const anchored = hash ? commands.find((c) => c.command.id === hash.slice(1)) : undefined;
  if (anchored) return <Navigate to={anchored.path} replace />;
  const built = commands.filter((c) => c.command.status === 'built').length;
  const subject = subjectOfCommandGroup.get(group.id);

  // Only the rest of the subject: the other command groups have their own pages, and
  // listing this one under "related" would be circular.
  const siblingGroups = (subject?.commands ?? []).filter((id) => id !== group.id);
  const settings = subject ? settingsOf(subject) : [];
  const inDepth = subject ? topicsOf(subject) : [];
  const concepts = subject?.concepts ?? [];
  const guides = subject?.guides ?? [];

  return (
    <article>
      <PageHeader
        eyebrow="Command reference"
        title={group.title}
        lede={group.blurb}
        meta={
          <span className="flex flex-wrap items-center gap-3 text-sm text-muted">
            <span>{commands.length} commands</span>
            <span aria-hidden="true">·</span>
            {built > 0 && (
              <>
                <span className="flex items-center gap-1.5">
                  {built} <StatusBadge status="built" size="xs" />
                </span>
                <span aria-hidden="true">·</span>
              </>
            )}
            <span className="flex items-center gap-1.5">
              {commands.length - built} <StatusBadge status="planned" size="xs" />
            </span>
          </span>
        }
      />

      {group.intro && (
        <div className="prose mb-8">
          {group.intro.map((p, i) => (
            <p key={i}>
              <Inline text={p} />
            </p>
          ))}
        </div>
      )}

      <div className="prose">
        <H2 id="commands">Commands</H2>
      </div>
      <ul className="not-prose mt-4 divide-y divide-line border-y border-line">
        {commands.map(({ command, path }) => (
          <li key={command.id}>
            <Link
              to={path}
              className="group block px-1 py-3 no-underline transition-colors hover:bg-hover"
            >
              <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                <code className="font-mono text-[0.8125rem] font-semibold text-strong group-hover:text-accent">
                  {command.name}
                  {command.args && <span className="font-normal text-muted"> {command.args}</span>}
                </code>
                {command.status !== 'built' && <StatusBadge status={command.status} size="xs" />}
                <span
                  aria-hidden="true"
                  className="ml-auto text-faint transition-transform group-hover:translate-x-0.5 group-hover:text-accent"
                >
                  →
                </span>
              </span>
              <span className="mt-1 block text-sm leading-relaxed text-muted">
                <Inline text={command.summary} />
              </span>
            </Link>
          </li>
        ))}
      </ul>

      {subject && (
        <section className="mt-14 border-t border-line pt-8">
          <div className="prose">
            <H2 id="subject">The rest of {subject.title.toLowerCase()}</H2>
            <p>{subject.blurb}</p>
          </div>

          {concepts.length > 0 && (
            <Related title="How it works">
              {concepts.map((p) => (
                <CardLink key={p} to={p} title={meta(p).label}>
                  {meta(p).blurb}
                </CardLink>
              ))}
            </Related>
          )}

          {inDepth.length > 0 && (
            <Related title="In depth" note="The same pages `ratline explain` prints on the server.">
              {inDepth.map((t) => (
                <CardLink key={t.name} to={t.path} title={t.title}>
                  <Inline text={t.summary} />
                </CardLink>
              ))}
            </Related>
          )}

          {guides.length > 0 && (
            <Related title="Guides and runbooks">
              {guides.map((p) => (
                <CardLink key={p} to={p} title={meta(p).label}>
                  {meta(p).blurb}
                </CardLink>
              ))}
            </Related>
          )}

          {settings.length > 0 && (
            <Related title="Settings that change this">
              {settings.map((s) => (
                <CardLink
                  key={s.key}
                  to={`/reference/config#cfg-${s.key}`}
                  title={`${s.key} — ${s.settings.length} settings`}
                >
                  {s.blurb}
                </CardLink>
              ))}
            </Related>
          )}

          {siblingGroups.length > 0 && (
            <Related title="Also in this subject">
              {siblingGroups.map((id) => {
                const g = [...groupByPath.values()].find((x) => x.id === id);
                if (!g) return null;
                return (
                  <CardLink key={id} to={g.path} title={g.title}>
                    {g.blurb}
                  </CardLink>
                );
              })}
            </Related>
          )}
        </section>
      )}
    </article>
  );
}

function Related({
  title,
  note,
  children,
}: {
  title: string;
  note?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="mt-8">
      <h3 className="not-prose label mb-1 text-muted">{title}</h3>
      {note && (
        <p className="not-prose mb-3 max-w-[var(--content-w)] text-sm text-muted">
          <Inline text={note} />
        </p>
      )}
      <div className="not-prose mt-3 grid gap-3 sm:grid-cols-2">{children}</div>
    </div>
  );
}
