import { PageHeader } from '../components/PageHeader';
import { CodeBlock } from '../components/CodeBlock';
import { Inline } from '../components/Inline';
import { CardLink, H2 } from '../components/ui';
import { topicByName, topicGroups, topics, ungroupedTopics } from '../data/topics';

/**
 * The index of the in-depth topics, grouped.
 *
 * Thirteen entries in one list is a wall. The same thirteen under four headings — what is
 * on the disk, how a site runs, what a site needs, and what to do when it breaks — is a
 * map of the tool.
 */
export function TopicsIndex() {
  return (
    <article>
      <PageHeader
        eyebrow="In depth"
        title="Topics"
        lede={
          <>
            {topics.length} pages on how each part works and why it works that way. These are
            the same files the binary carries, so every one of them is readable on a server
            with no browser.
          </>
        }
      />

      <CodeBlock lang="shell" code={'ratline explain            # the list\nratline explain sockets    # one of them'} />

      {topicGroups.map((group) => {
        const present = group.names.map((n) => topicByName.get(n)).filter((t) => t !== undefined);
        if (present.length === 0) return null;
        return (
          <section key={group.title} className="mt-10">
            <div className="prose">
              <H2>{group.title}</H2>
              <p>{group.blurb}</p>
            </div>
            <div className="not-prose mt-4 grid gap-3 sm:grid-cols-2">
              {present.map((t) => (
                <CardLink key={t!.name} to={t!.path} title={t!.title}>
                  <Inline text={t!.summary} />
                </CardLink>
              ))}
            </div>
          </section>
        );
      })}

      {/* A topic the binary has and the groups do not mention. Shown rather than dropped:
          a page missing from the navigation is a page nobody reads. */}
      {ungroupedTopics.length > 0 && (
        <section className="mt-10">
          <div className="prose">
            <H2>Also</H2>
          </div>
          <div className="not-prose mt-4 grid gap-3 sm:grid-cols-2">
            {ungroupedTopics.map((t) => (
              <CardLink key={t.name} to={t.path} title={t.title}>
                <Inline text={t.summary} />
              </CardLink>
            ))}
          </div>
        </section>
      )}
    </article>
  );
}
