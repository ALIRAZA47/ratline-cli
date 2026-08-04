import { commandGroups, nav } from '../data/nav';
import { configSections } from '../data/config';
import { exitCodes, globalFlags } from '../data/globals';
import { rules } from '../data/validation';
import type { Command } from '../data/types';
import { flatAnchoredFlags } from './flags';

export type ResultKind = 'page' | 'command' | 'flag' | 'setting' | 'exit' | 'rule';

export interface Doc {
  kind: ResultKind;
  /** What is shown as the result title. */
  title: string;
  /** Secondary line: the parent command, the group, the default. */
  context: string;
  to: string;
  /** Lowercased haystack. */
  hay: string;
  /** Tokens that should score as an exact hit. */
  exact: string[];
  status?: 'built' | 'planned';
}

function flagDocs(command: Command, to: string): Doc[] {
  return flatAnchoredFlags(command).map(({ flag: f, anchor }) => ({
    kind: 'flag' as const,
    title: f.short ? `${f.name}, ${f.short}` : f.name,
    context: `${command.name}${f.default ? ` — default ${f.default}` : ''}`,
    to: `${to}#${anchor}`,
    hay: `${f.name} ${f.short ?? ''} ${f.arg ?? ''} ${f.description} ${f.note ?? ''} ${command.name}`.toLowerCase(),
    exact: [f.name.toLowerCase(), ...(f.short ? [f.short.toLowerCase()] : [])],
  }));
}

/** The whole index, built once at module load. It is small — a few hundred
 *  entries — so there is nothing to fetch and nothing to serve. */
export const index: Doc[] = (() => {
  const docs: Doc[] = [];

  for (const section of nav) {
    for (const item of section.items) {
      docs.push({
        kind: 'page',
        title: item.label,
        context: section.title,
        to: item.to,
        hay: `${item.label} ${item.blurb ?? ''} ${(item.keywords ?? []).join(' ')} ${section.title}`.toLowerCase(),
        exact: [item.label.toLowerCase(), ...(item.keywords ?? []).map((k) => k.toLowerCase())],
      });
    }
  }

  for (const group of commandGroups) {
    for (const cmd of group.commands) {
      docs.push({
        kind: 'command',
        title: cmd.name,
        context: cmd.summary,
        to: `${group.path}#${cmd.id}`,
        status: cmd.status,
        hay: `${cmd.name} ${cmd.args ?? ''} ${cmd.summary} ${(cmd.description ?? []).join(' ')} ${(cmd.keywords ?? []).join(' ')} ${group.title}`.toLowerCase(),
        exact: [cmd.name.toLowerCase(), cmd.name.replace(/^ratline /, '').toLowerCase()],
      });
      docs.push(...flagDocs(cmd, group.path));
    }
  }

  docs.push(
    ...flagDocs(
      {
        id: 'global',
        name: 'Global flag',
        status: 'built',
        summary: '',
        flags: globalFlags,
      },
      '/reference/global-flags',
    ),
  );

  for (const section of configSections) {
    for (const s of section.settings) {
      docs.push({
        kind: 'setting',
        title: s.key,
        context: `default ${s.value}`,
        to: `/reference/config#setting-${s.key.replace(/\./g, '-')}`,
        hay: `${s.key} ${s.value} ${s.type} ${s.note ?? ''}`.toLowerCase(),
        exact: [s.key.toLowerCase(), s.key.split('.').pop()!.toLowerCase()],
      });
    }
  }

  for (const e of exitCodes) {
    docs.push({
      kind: 'exit',
      title: `${e.code} — ${e.name}`,
      context: e.meaning,
      to: `/reference/exit-codes#code-${e.code}`,
      hay: `exit ${e.code} ${e.name} ${e.meaning} ${e.action}`.toLowerCase(),
      exact: [e.name.toLowerCase(), String(e.code)],
    });
  }

  for (const r of rules) {
    docs.push({
      kind: 'rule',
      title: r.subject,
      context: r.rule ?? r.source,
      to: `/reference/validation#${r.id}`,
      hay: `${r.subject} ${r.rule ?? ''} ${r.source} ${r.points.join(' ')} ${r.message ?? ''}`.toLowerCase(),
      exact: [r.subject.toLowerCase()],
    });
  }

  return docs;
})();

const kindWeight: Record<ResultKind, number> = {
  command: 6,
  flag: 5,
  page: 5,
  setting: 3,
  exit: 3,
  rule: 3,
};

/**
 * Scored substring search. Deliberately not fuzzy: in a reference, `--san`
 * matching `--sans` is helpful and `--san` matching `--dns-propagation` is not.
 */
export function search(query: string, limit = 24): Doc[] {
  const q = query.trim().toLowerCase();
  if (q.length < 1) return [];
  const terms = q.split(/\s+/).filter(Boolean);

  const scored: { doc: Doc; score: number }[] = [];
  for (const doc of index) {
    let score = 0;
    let matchedAll = true;
    for (const term of terms) {
      let termScore = 0;
      if (doc.exact.some((e) => e === term)) termScore += 60;
      else if (doc.exact.some((e) => e.startsWith(term))) termScore += 34;
      else if (doc.title.toLowerCase().includes(term)) termScore += 20;

      const at = doc.hay.indexOf(term);
      if (at >= 0) {
        termScore += 8;
        // Earlier is better, but only slightly.
        termScore += Math.max(0, 6 - Math.floor(at / 40));
      }
      if (termScore === 0) {
        matchedAll = false;
        break;
      }
      score += termScore;
    }
    if (!matchedAll) continue;
    score += kindWeight[doc.kind];
    // Shorter titles are usually the more precise hit.
    score += Math.max(0, 10 - Math.floor(doc.title.length / 6));
    scored.push({ doc, score });
  }

  scored.sort((a, b) => b.score - a.score || a.doc.title.length - b.doc.title.length);
  return scored.slice(0, limit).map((s) => s.doc);
}

/**
 * Rank the index against terms where some are expected to be wrong.
 *
 * `search` requires every term to match, which is right for a typed query: in a
 * reference, a result that matches half of what you asked for is noise. But it is
 * exactly wrong for guessing at a mistyped URL, where the whole premise is that one
 * term is not in the index — `/guides/debug-503` has "guides" and "debug" right and
 * "503" wrong, and requiring all three returns nothing at all.
 *
 * So this scores the terms that do hit and ignores the rest, then holds a floor so
 * that genuine nonsense still returns nothing rather than four arbitrary pages.
 */
export function suggest(query: string, limit = 4): Doc[] {
  const terms = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (terms.length === 0) return [];

  const scored: { doc: Doc; score: number; hits: number }[] = [];
  for (const doc of index) {
    let score = 0;
    let hits = 0;
    for (const term of terms) {
      let termScore = 0;
      if (doc.exact.some((e) => e === term)) termScore += 60;
      else if (doc.exact.some((e) => e.startsWith(term))) termScore += 34;
      else if (doc.title.toLowerCase().includes(term)) termScore += 20;
      else if (doc.hay.includes(term)) termScore += 8;
      if (termScore > 0) {
        hits++;
        score += termScore;
      }
    }
    // Two weak hits on one term is what a single stray word produces, and offering
    // a page on that basis is worse than offering nothing.
    if (hits === 0 || score < 20) continue;
    // More of the query accounted for is a better guess than one strong term.
    score += hits * 12 + kindWeight[doc.kind];
    scored.push({ doc, score, hits });
  }

  scored.sort((a, b) => b.hits - a.hits || b.score - a.score);
  return scored.slice(0, limit).map((s) => s.doc);
}

export const kindLabel: Record<ResultKind, string> = {
  page: 'Page',
  command: 'Command',
  flag: 'Flag',
  setting: 'Config',
  exit: 'Exit code',
  rule: 'Validation',
};
