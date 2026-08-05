import { Link, useParams } from 'react-router-dom';
import { Markdown } from '../components/Markdown';
import { Inline } from '../components/Inline';
import { PageHeader } from '../components/PageHeader';
import { CodeBlock } from '../components/CodeBlock';
import { Callout } from '../components/ui';
import { NotFound } from './NotFound';
import { topicByName, topics } from '../data/topics';

/**
 * One in-depth topic, rendered from the markdown the binary embeds.
 *
 * The page says so, and gives the command that prints the same text — because the
 * situation these pages are written for is usually an SSH session with no browser, and
 * knowing the same words are available there is the useful part.
 */
export function TopicPage() {
  const { name = '' } = useParams();
  const topic = topicByName.get(name);
  if (!topic) return <NotFound />;

  const index = topics.findIndex((t) => t.name === name);
  const prev = index > 0 ? topics[index - 1] : undefined;
  const next = index < topics.length - 1 ? topics[index + 1] : undefined;

  return (
    <article>
      <PageHeader eyebrow="In depth" title={topic.title} lede={<Inline text={topic.summary} />} />

      <Callout tone="note" title="The same words, on the server">
        <p className="m-0">
          This page is the file the binary carries, so it is available where you will
          actually need it — no browser, no network.
        </p>
      </Callout>

      <CodeBlock lang="shell" code={`ratline explain ${topic.name}`} />

      {/* skipTitle: the header above already shows the heading and the summary, and
          repeating them immediately reads as a rendering mistake. */}
      <Markdown source={topic.body} skipTitle />

      <nav
        aria-label="Other topics"
        className="not-prose mt-12 flex flex-wrap gap-3 border-t border-line pt-6 text-sm"
      >
        {prev && (
          <Link to={prev.path} className="text-muted no-underline hover:text-strong">
            ← {prev.title}
          </Link>
        )}
        <span className="grow" />
        {next && (
          <Link to={next.path} className="text-muted no-underline hover:text-strong">
            {next.title} →
          </Link>
        )}
      </nav>
    </article>
  );
}
