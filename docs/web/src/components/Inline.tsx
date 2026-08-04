import type { ReactNode } from 'react';

/**
 * The smallest possible inline renderer for the prose held in the command data:
 * `code` becomes <code>, **bold** becomes <strong>. Nothing else.
 *
 * The alternative is either a Markdown pipeline (a large dependency for two
 * constructs) or writing every description as JSX (which would stop the data
 * being data, and stop the search index reading it as text). Two delimiters is
 * the whole grammar, and an unmatched delimiter renders literally rather than
 * swallowing the rest of the string.
 */
const TOKEN = /(`[^`\n]+`|\*\*[^*\n]+\*\*)/g;

export function Inline({ text }: { text: string }): ReactNode {
  if (!text.includes('`') && !text.includes('**')) return text;

  const parts = text.split(TOKEN);
  return parts.map((part, i) => {
    if (part.startsWith('`') && part.endsWith('`') && part.length > 2) {
      return (
        <code
          key={i}
          className="rounded border border-line bg-code px-1 py-px font-mono text-[0.86em] text-strong"
        >
          {part.slice(1, -1)}
        </code>
      );
    }
    if (part.startsWith('**') && part.endsWith('**') && part.length > 4) {
      return (
        <strong key={i} className="font-semibold text-strong">
          {part.slice(2, -2)}
        </strong>
      );
    }
    return part;
  });
}
