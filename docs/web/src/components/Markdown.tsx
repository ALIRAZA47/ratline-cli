import { CodeBlock } from './CodeBlock';
import { Inline } from './Inline';
import { Callout, H2, H3, TableScroll } from './ui';

/**
 * A renderer for exactly the markdown the embedded topics use, and nothing else.
 *
 * These pages are the same text `ratline explain <topic>` prints, so they are written to
 * read in a terminal: a level-one heading, a blockquote summary, level-two and -three
 * headings, paragraphs, `*` bullets, four-space code blocks, one table, and inline code
 * and bold. There is no HTML, no footnote, no nested list, and no fenced block.
 *
 * So no markdown dependency. The same reasoning as the Inline component: a full pipeline
 * is a large dependency for a grammar this small, and the failure mode of a hand-written
 * parser here is a page that renders plainly rather than one that renders wrongly —
 * anything unrecognised falls through as a paragraph.
 *
 * What this is NOT is a general markdown renderer, and it should not become one. If a
 * topic ever needs something outside this grammar, the honest move is to add the
 * construct here deliberately rather than to reach for a library and inherit its
 * behaviour for the other twelve pages.
 */

type Block =
  | { kind: 'heading'; level: 1 | 2 | 3; text: string }
  | { kind: 'quote'; lines: string[] }
  | { kind: 'code'; lines: string[] }
  | { kind: 'list'; items: string[] }
  | { kind: 'table'; rows: string[][] }
  | { kind: 'para'; lines: string[] };

/** parseBlocks turns the markdown into a flat list. Deliberately not a tree: nothing in
 *  these topics nests. */
function parseBlocks(src: string): Block[] {
  const lines = src.replace(/\r\n/g, '\n').split('\n');
  const out: Block[] = [];
  let i = 0;

  // Four *or more* spaces. Requiring a non-space at exactly the fourth column broke a
  // wrapped command — `      | ratline db connect --stdin` is a continuation indented six
  // spaces, and it fell through to the table branch and rendered as a one-cell table.
  const isCode = (l: string) => /^(?: {4,}|\t)\S/.test(l);

  while (i < lines.length) {
    const line = lines[i];

    if (line.trim() === '') {
      i++;
      continue;
    }

    const heading = /^(#{1,3})\s+(.*)$/.exec(line);
    if (heading) {
      out.push({
        kind: 'heading',
        level: heading[1].length as 1 | 2 | 3,
        text: heading[2].trim(),
      });
      i++;
      continue;
    }

    if (line.startsWith('> ') || line === '>') {
      const body: string[] = [];
      while (i < lines.length && (lines[i].startsWith('> ') || lines[i] === '>')) {
        body.push(lines[i].replace(/^>\s?/, ''));
        i++;
      }
      out.push({ kind: 'quote', lines: body });
      continue;
    }

    if (isCode(line)) {
      const body: string[] = [];
      // A blank line inside a code block is kept, so a two-command example with a gap
      // stays one block rather than becoming two.
      while (i < lines.length && (isCode(lines[i]) || lines[i].trim() === '')) {
        if (lines[i].trim() === '' && !isCode(lines[i + 1] ?? '')) break;
        body.push(lines[i].replace(/^( {4}|\t)/, ''));
        i++;
      }
      out.push({ kind: 'code', lines: body });
      continue;
    }

    if (/^\s*[*-]\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*[*-]\s+/.test(lines[i])) {
        let item = lines[i].replace(/^\s*[*-]\s+/, '');
        i++;
        // A wrapped continuation line is indented and not itself a bullet.
        while (i < lines.length && /^\s{2,}\S/.test(lines[i]) && !/^\s*[*-]\s+/.test(lines[i]) && !isCode(lines[i])) {
          item += ' ' + lines[i].trim();
          i++;
        }
        items.push(item);
      }
      out.push({ kind: 'list', items });
      continue;
    }

    if (line.trimStart().startsWith('|')) {
      const rows: string[][] = [];
      while (i < lines.length && lines[i].trimStart().startsWith('|')) {
        const cells = lines[i].trim().replace(/^\|/, '').replace(/\|$/, '').split('|');
        // The |---|---| separator carries no content.
        if (!cells.every((c) => /^[\s:-]*$/.test(c))) {
          rows.push(cells.map((c) => c.trim()));
        }
        i++;
      }
      if (rows.length > 0) out.push({ kind: 'table', rows });
      continue;
    }

    const body: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== '' &&
      !/^#{1,3}\s/.test(lines[i]) &&
      !lines[i].startsWith('>') &&
      !/^\s*[*-]\s+/.test(lines[i]) &&
      !lines[i].trimStart().startsWith('|') &&
      !isCode(lines[i])
    ) {
      body.push(lines[i]);
      i++;
    }
    out.push({ kind: 'para', lines: body });
  }
  return out;
}

/** slug matches the anchor scheme the hand-written pages use, so a deep link behaves the
 *  same on either kind of page. */
function slug(text: string): string {
  return text
    .toLowerCase()
    .replace(/[`*_]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/(^-|-$)/g, '');
}

export interface MarkdownProps {
  source: string;
  /** Drop the level-one heading and the blockquote under it: the page header shows both,
   *  and repeating them reads as a mistake. */
  skipTitle?: boolean;
}

export function Markdown({ source, skipTitle = false }: MarkdownProps) {
  let blocks = parseBlocks(source);

  if (skipTitle) {
    const firstReal = blocks.findIndex(
      (b) => !(b.kind === 'heading' && b.level === 1) && b.kind !== 'quote',
    );
    blocks = firstReal > 0 ? blocks.slice(firstReal) : blocks;
  }

  return (
    <div className="space-y-4">
      {blocks.map((b, n) => {
        switch (b.kind) {
          case 'heading': {
            if (b.level === 1) {
              return (
                <div key={n} className="prose">
                  <h1 className="text-2xl font-semibold text-strong">
                    <Inline text={b.text} />
                  </h1>
                </div>
              );
            }
            const Tag = b.level === 2 ? H2 : H3;
            return (
              <div key={n} className="prose pt-4">
                <Tag id={slug(b.text)}>
                  <Inline text={b.text} />
                </Tag>
              </div>
            );
          }

          case 'quote':
            return (
              <Callout key={n} tone="note">
                <p className="m-0">
                  <Inline text={b.lines.join(' ')} />
                </p>
              </Callout>
            );

          case 'code':
            return (
              <CodeBlock key={n} lang="shell" code={b.lines.join('\n').replace(/\n+$/, '')} />
            );

          case 'list':
            return (
              <div key={n} className="prose">
                <ul>
                  {b.items.map((item, j) => (
                    <li key={j}>
                      <Inline text={item} />
                    </li>
                  ))}
                </ul>
              </div>
            );

          case 'table': {
            const [head, ...body] = b.rows;
            return (
              <TableScroll key={n}>
                <table className="w-full border-collapse text-sm">
                  <thead>
                    <tr className="border-b border-line text-left">
                      {head.map((c, j) => (
                        <th key={j} className="px-3 py-2 font-medium text-strong">
                          <Inline text={c} />
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {body.map((row, j) => (
                      <tr key={j} className="border-b border-line/60 align-top">
                        {row.map((c, k) => (
                          <td key={k} className="px-3 py-2 text-muted">
                            <Inline text={c} />
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </TableScroll>
            );
          }

          case 'para':
          default:
            return (
              <div key={n} className="prose">
                <p>
                  <Inline text={b.lines.join(' ')} />
                </p>
              </div>
            );
        }
      })}
    </div>
  );
}

/**
 * plain strips the inline markers, so indexed text matches what a reader would type.
 *
 * Backticks and asterisks only — deliberately not underscores. Stripping `_` as an
 * emphasis marker turns `max_memory_restart` into `maxmemoryrestart`, and an identifier
 * with underscores in it is precisely what somebody searches a CLI reference for. It cost
 * ten terms before this was noticed. None of the topics use `_emphasis_` anyway; they use
 * asterisks, and intra-word underscores are not emphasis in any flavour that matters.
 */
function plain(text: string): string {
  return text.replace(/[`*]/g, '');
}

/** textOf flattens one block to searchable prose. */
function textOf(b: Block): string {
  switch (b.kind) {
    case 'heading':
      return plain(b.text);
    case 'quote':
    case 'code':
    case 'para':
      return plain(b.lines.join(' '));
    case 'list':
      return plain(b.items.join(' '));
    case 'table':
      return plain(b.rows.flat().join(' '));
  }
}

export interface Section {
  /** The anchor, matching what the rendered heading gets. Empty for the preamble. */
  id: string;
  title: string;
  text: string;
}

/**
 * Split a topic into its level-two sections, for the search index.
 *
 * Without this the index held each topic's title and its one-line summary and nothing
 * else, so 6,801 words of the best-written documentation in the project were the least
 * findable text on the site: of 510 distinctive terms in those bodies, 190 returned no
 * results at all — `readWriteAnyDatabase` and `authSource` among them.
 *
 * Sections rather than whole pages, because a hit in a 900-word page that scrolls you to
 * the top has told you the answer is somewhere on it. Level three folds into its parent:
 * the anchor exists, but a subsection is rarely what somebody means.
 */
export function sectionsOf(source: string): Section[] {
  const blocks = parseBlocks(source);
  const out: Section[] = [];
  let current: Section = { id: '', title: '', text: '' };

  for (const b of blocks) {
    if (b.kind === 'heading' && b.level === 1) {
      current.title ||= plain(b.text);
      continue;
    }
    if (b.kind === 'heading' && b.level === 2) {
      out.push(current);
      current = { id: slug(b.text), title: plain(b.text), text: '' };
      continue;
    }
    current.text += (current.text ? ' ' : '') + textOf(b);
  }
  out.push(current);
  // A section with a heading and no prose under it is still worth finding by its heading;
  // an empty preamble is not.
  return out.filter((s) => s.id !== '' || s.text !== '');
}

/** headingsOf extracts the level-two headings, for an on-this-page index. */
export function headingsOf(source: string): { id: string; text: string }[] {
  return parseBlocks(source)
    .filter((b): b is Extract<Block, { kind: 'heading' }> => b.kind === 'heading' && b.level === 2)
    .map((b) => ({ id: slug(b.text), text: b.text.replace(/[`*]/g, '') }));
}

/** summaryOf returns the blockquote under the title — the same one-liner `ratline explain`
 *  prints in its topic table. */
export function summaryOf(source: string): string {
  const q = parseBlocks(source).find((b) => b.kind === 'quote');
  return q && q.kind === 'quote' ? q.lines.join(' ') : '';
}

/** titleOf returns the level-one heading. */
export function titleOf(source: string): string {
  const h = parseBlocks(source).find((b) => b.kind === 'heading' && b.level === 1);
  return h && h.kind === 'heading' ? h.text : '';
}

export type { Block as MarkdownBlock };
