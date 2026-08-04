import { Link } from 'react-router-dom';
import { PageHeader } from '../components/PageHeader';
import { CardLink } from '../components/ui';

export function NotFound() {
  return (
    <article>
      <PageHeader
        eyebrow="404"
        title="No such page"
        lede="This documentation only describes what is in the command surface, so a missing page usually means the URL is wrong rather than that something is undocumented."
      />
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
        <CardLink to="/reference/config" title="Configuration reference">
          Every setting and its default.
        </CardLink>
      </div>
      <p className="mt-6 text-sm text-muted">
        Or press <kbd className="rounded border border-line bg-sunken px-1 font-mono text-xs">/</kbd> to
        search commands, flags, settings and exit codes. If you were looking for a command,{' '}
        <Link to="/reference" className="text-accent underline">
          the reference index
        </Link>{' '}
        lists all of them.
      </p>
    </article>
  );
}
