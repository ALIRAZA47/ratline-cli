import { CodeBlock } from '../components/CodeBlock';
import { PageHeader } from '../components/PageHeader';
import { Callout, H2 } from '../components/ui';
import { RefList, RefRow } from '../components/Reference';
import { Inline } from '../components/Inline';
import { exitCodes } from '../data/globals';

export function ExitCodesPage() {
  return (
    <article>
      <PageHeader
        eyebrow="Reference"
        title="Exit codes"
        lede="Eleven codes, declared once in internal/rlerr/rlerr.go and never inferred from error text. Automation branches on them, so they are part of the public interface and are not renumbered."
      />

      {/* The codes as reference rows, the way the flags are: the number and its stable
          name on one monospace line, what to do about it on the measure underneath.
          Every row keeps its `#code-N` anchor — the exit chips all over the site link
          straight into it, and so do the CLI's own hints. */}
      <RefList label="Exit codes">
        {exitCodes.map((e) => (
          <RefRow
            key={e.code}
            anchor={`code-${e.code}`}
            name={e.name}
            lead={
              <span className="flex items-baseline gap-2.5">
                <span
                  className={[
                    'inline-flex size-6 shrink-0 translate-y-[0.15em] items-center justify-center rounded border font-mono text-xs font-semibold',
                    e.code === 0
                      ? 'border-[color-mix(in_oklab,var(--ok)_35%,transparent)] bg-ok-soft text-ok'
                      : e.code === 6
                        ? 'border-[color-mix(in_oklab,var(--danger)_35%,transparent)] bg-danger-soft text-danger'
                        : 'border-line-strong bg-sunken text-fg',
                  ].join(' ')}
                >
                  {e.code}
                </span>
                <a
                  href={`#code-${e.code}`}
                  className="font-mono text-[0.8125rem] font-semibold whitespace-nowrap text-strong no-underline hover:text-accent"
                >
                  {e.name}
                </a>
              </span>
            }
            meta={e.raisedBy ? [['from', e.raisedBy]] : undefined}
          >
            <span className="block font-medium text-strong">{e.meaning}</span>
            <span className="mt-1 block">
              <Inline text={e.action} />
            </span>
          </RefRow>
        ))}
      </RefList>

      <div className="prose mt-12">
        <H2 id="branching">Branching on them</H2>
        <p>
          The reason 8 and 9 are separate from 4 is that they mean different things to a script. A 4
          means an external command failed and should probably be retried or reported. An 8 means
          validation did not pass — retrying immediately will fail the same way and burn budget. A 9
          means <em>do not retry at all</em> until the retry-after has elapsed.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        filename="deploy.sh"
        code={`#!/usr/bin/env bash
set -uo pipefail

ratline cert issue "$DOMAIN" --json > /tmp/cert.json
case $? in
  0) echo "issued and verified" ;;
  9) echo "rate limited: $(jq -r .error.message /tmp/cert.json)"; exit 0 ;;
  8) echo "challenge failed; check DNS and the webroot"; exit 1 ;;
  5) echo "another ratline run holds the lock; retrying later"; exit 75 ;;
  6) echo "ROLLBACK FAILED — needs a human"; exit 1 ;;
  *) echo "failed: $(jq -r .error.message /tmp/cert.json)"; exit 1 ;;
esac`}
      />

      <Callout tone="danger" title="Exit 6 is the only one that wants a person">
        <p>
          Every other code leaves the system in a known state: either the change happened, or it did
          not. A 6 means the change failed <em>and</em> the rollback failed, so the system is
          partial. The output names exactly what was rolled back and what could not be. Run{' '}
          <code>ratline doctor</code>, read it, and only then consider{' '}
          <code>ratline reconcile --fix</code>.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="envelope">In the JSON envelope</H2>
        <p>
          The same code and its stable name appear in the error payload, so a caller does not have to
          capture <code>$?</code> and parse stdout separately.
        </p>
      </div>

      <CodeBlock
        lang="json"
        code={`{
  "ok": false,
  "command": "ratline cert issue",
  "version": "1.0.0",
  "error": {
    "code": 9,
    "name": "rate_limited",
    "message": "…",
    "hint": "…",
    "fields": {}
  }
}`}
      />
    </article>
  );
}
