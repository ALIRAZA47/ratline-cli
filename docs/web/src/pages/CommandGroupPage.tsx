import { Navigate, useLocation } from 'react-router-dom';
import { CommandEntry } from '../components/CommandEntry';
import { PageHeader } from '../components/PageHeader';
import { StatusBadge } from '../components/ui';
import { Inline } from '../components/Inline';
import { groupByPath } from '../data/nav';

/** One page per command group, rendered entirely from the typed command data. */
export function CommandGroupPage() {
  const { pathname } = useLocation();
  const group = groupByPath.get(pathname);
  if (!group) return <Navigate to="/reference" replace />;

  const built = group.commands.filter((c) => c.status === 'built').length;

  return (
    <article>
      <PageHeader
        eyebrow="Command reference"
        title={group.title}
        lede={group.blurb}
        meta={
          <span className="flex flex-wrap items-center gap-3 text-sm text-muted">
            <span>{group.commands.length} commands</span>
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
              {group.commands.length - built} <StatusBadge status="planned" size="xs" />
            </span>
          </span>
        }
      />

      {group.intro && (
        <div className="prose mb-4">
          {group.intro.map((p, i) => (
            <p key={i}>
              <Inline text={p} />
            </p>
          ))}
        </div>
      )}

      {group.commands.map((cmd) => (
        <CommandEntry key={cmd.id} command={cmd} />
      ))}
    </article>
  );
}
