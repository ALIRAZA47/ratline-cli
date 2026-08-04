import { Link, useLocation } from 'react-router-dom';
import { PageHeader } from '../components/PageHeader';
import { CardLink } from '../components/ui';
import { kindLabel, suggest } from '../lib/search';

/**
 * Turn a bad path into search terms.
 *
 * `/guides/debug-503` becomes "guides debug 503". The whole path rather than the
 * last segment, because the section is usually right even when the page name is
 * not: someone typing `/reference/certs` wants the certificate page, and the
 * segment "reference" is what makes that unambiguous.
 */
function termsFrom(pathname: string): string {
  return decodeURIComponent(pathname)
    .split(/[/\-_.]+/)
    .filter((part) => part.length > 1)
    .join(' ');
}

export function NotFound() {
  const { pathname } = useLocation();
  // The same index the search dialog uses, scored for a query in which one term is
  // wrong by definition. This mirrors what the CLI does for `ratline explain <typo>`:
  // correcting a near-miss beats refusing it.
  const guesses = suggest(termsFrom(pathname));

  return (
    <article>
      <PageHeader
        eyebrow="404"
        title="No such page"
        lede="This documentation only describes what is in the command surface, so a missing page usually means the URL is wrong rather than that something is undocumented."
      />

      <div className="prose">
        <p>
          Nothing is served at <code>{pathname}</code>.
        </p>
      </div>

      {guesses.length > 0 && (
        <section className="not-prose my-7">
          <h2 className="mb-2 font-mono text-2xs font-semibold uppercase tracking-wider text-faint">
            Closest matches
          </h2>
          <ul className="divide-y divide-line overflow-hidden rounded-[var(--radius-card)] border border-line">
            {guesses.map((doc) => (
              <li key={`${doc.kind}-${doc.to}`}>
                <Link
                  to={doc.to}
                  className="flex items-baseline gap-3 px-4 py-2.5 no-underline transition-colors hover:bg-hover"
                >
                  <span className="w-[5.5rem] shrink-0 font-mono text-2xs uppercase tracking-wider text-faint">
                    {kindLabel[doc.kind]}
                  </span>
                  <span className="min-w-0">
                    <span className="block text-sm font-medium text-strong">{doc.title}</span>
                    <span className="mt-0.5 block truncate text-xs text-muted">{doc.context}</span>
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}

      <div className="not-prose grid gap-3 sm:grid-cols-2">
        <CardLink to="/" title="What ratline is">
          The scope of the tool in fifteen seconds.
        </CardLink>
        <CardLink to="/reference" title="Command surface at a glance">
          Every group, every verb, with build status.
        </CardLink>
        <CardLink to="/quickstart" title="60-second quickstart">
          Install, a user, a site, working HTTPS.
        </CardLink>
        <CardLink to="/guides/inherited-server" title="A server you did not set up">
          status, doctor, troubleshoot, explain.
        </CardLink>
      </div>

      <p className="mt-6 text-sm text-muted">
        Or press{' '}
        <kbd className="rounded border border-line bg-sunken px-1 font-mono text-xs">/</kbd> to search
        commands, flags, settings and exit codes. If you were looking for a command,{' '}
        <Link to="/reference" className="text-accent underline">
          the reference index
        </Link>{' '}
        lists all of them.
      </p>
    </article>
  );
}
