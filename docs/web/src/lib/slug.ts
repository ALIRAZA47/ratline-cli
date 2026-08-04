/** Anchor id for a heading. Stable, because these ids are linked to. */
export function slugify(text: string): string {
  return text
    .toLowerCase()
    .replace(/[’']/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

/** Anchor id for one flag of one command: `site-add--runtime`. */
export function flagAnchor(commandId: string, flagName: string): string {
  return `${commandId}--${flagName.replace(/^--?/, '')}`;
}
