import { Link, useLocation } from 'react-router-dom';
import { CommandEntry } from '../components/CommandEntry';
import { NotFound } from './NotFound';
import { commandByPath, commandsIn } from '../data/groups';

/**
 * One page per command.
 *
 * The eight group pages used to stack every command in the group — fourteen of them on the
 * sites page. A page that long is not read, it is searched, and a reader who searched it had
 * no URL to send anybody afterwards. So each command gets its own address, its own title,
 * and links to its neighbours: the shape `docker container run` and `git commit` have.
 *
 * The body is the same renderer the group page used, in standalone mode. There is no second
 * description of a command anywhere on this site.
 */
export function CommandPage() {
  const { pathname } = useLocation();
  const ref = commandByPath.get(pathname);
  if (!ref) return <NotFound />;

  const { group, command } = ref;
  const siblings = commandsIn(group);
  const index = siblings.findIndex((c) => c.path === pathname);
  const prev = index > 0 ? siblings[index - 1] : undefined;
  const next = index >= 0 && index < siblings.length - 1 ? siblings[index + 1] : undefined;

  return (
    <article>
      <nav aria-label="Breadcrumb" className="not-prose mb-4 text-sm text-muted">
        <Link to="/reference" className="no-underline hover:text-strong">
          Reference
        </Link>
        <span aria-hidden="true" className="px-2 text-faint">
          /
        </span>
        <Link to={group.path} className="no-underline hover:text-strong">
          {group.title}
        </Link>
      </nav>

      <CommandEntry command={command} />

      <nav
        aria-label={`Other ${group.title.toLowerCase()} commands`}
        className="not-prose mt-12 border-t border-line pt-6 text-sm"
      >
        <div className="flex flex-wrap items-baseline gap-x-6 gap-y-2">
          {prev && (
            <Link to={prev.path} className="text-muted no-underline hover:text-strong">
              ← <span className="font-mono">{prev.command.name.replace(/^ratline /, '')}</span>
            </Link>
          )}
          <span className="grow" />
          {next && (
            <Link to={next.path} className="text-muted no-underline hover:text-strong">
              <span className="font-mono">{next.command.name.replace(/^ratline /, '')}</span> →
            </Link>
          )}
        </div>
        <p className="mt-4 text-muted">
          All {siblings.length} in{' '}
          <Link to={group.path} className="text-accent underline">
            {group.title}
          </Link>
          .
        </p>
      </nav>
    </article>
  );
}
