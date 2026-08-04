import { PageHeader } from '../components/PageHeader';
import { Terminal } from '../components/Terminal';
import { Callout } from '../components/ui';
import { Inline } from '../components/Inline';
import { rules, validationIntro } from '../data/validation';

export function ValidationPage() {
  return (
    <article>
      <PageHeader
        eyebrow="Reference"
        title="Validation rules"
        lede="The rules the code enforces, taken from internal/validate rather than paraphrased. Every one of them returns exit code 2 and touches nothing."
      />

      <div className="prose">
        {validationIntro.map((p, i) => (
          <p key={i}>
            <Inline text={p} />
          </p>
        ))}
      </div>

      <Callout tone="note" title="Why they are pure functions">
        <p>
          None of these validators touch the system. Availability checks — does this user exist in{' '}
          <code>/etc/passwd</code>, is this group taken — are a separate step, injected as function
          arguments. That separation is what makes the validators cheap to fuzz, and it is why a bad
          username fails before anything has been created.
        </p>
      </Callout>

      <div className="not-prose mt-8 space-y-4">
        {rules.map((r) => (
          <section
            key={r.id}
            id={r.id}
            aria-labelledby={`${r.id}-heading`}
            className="scroll-mt-24 overflow-hidden rounded-[var(--radius-card)] border border-line bg-raised target:border-accent"
          >
            <div className="border-b border-line px-4 py-3">
              <h2 id={`${r.id}-heading`} className="text-base font-semibold text-strong">
                <a href={`#${r.id}`} className="no-underline">
                  {r.subject}
                </a>
              </h2>
              {r.rule && (
                <div className="scroll-thin mt-2 overflow-x-auto">
                  <code className="whitespace-pre font-mono text-xs text-accent">{r.rule}</code>
                </div>
              )}
              <p className="mt-2 font-mono text-2xs text-faint">{r.source}</p>
            </div>
            <div className="px-4 py-3">
              <ul className="max-w-[var(--container-measure)] space-y-2 text-sm leading-relaxed">
                {r.points.map((p, i) => (
                  <li key={i} className="flex gap-2.5">
                    <span
                      aria-hidden="true"
                      className="mt-[0.5em] size-1 shrink-0 rounded-full bg-faint"
                    />
                    <span>
                      <Inline text={p} />
                    </span>
                  </li>
                ))}
              </ul>
              {r.message && (
                <div className="mt-3.5 rounded border border-line bg-code px-3 py-2 font-mono text-xs">
                  <p className="text-danger">
                    <span aria-hidden="true">✗ </span>
                    error: {r.message}
                  </p>
                  {r.hint && <p className="mt-1 text-muted">&nbsp;&nbsp;hint: {r.hint}</p>}
                </div>
              )}
            </div>
          </section>
        ))}
      </div>

      <div className="prose mt-10">
        <h2 id="what-it-looks-like">What a refusal looks like</h2>
        <p>
          Errors state what failed, why, and the next action. The bad version of this is{' '}
          <code>error: exit status 1</code>.
        </p>
      </div>

      <Terminal title="root@server">{`$ ratline site add example.com --user acme --runtime python --app-module app.main
✗ error: invalid application module "app.main"
  hint: the form is module.path:callable, for example app.main:app or myproject.wsgi:application
  see:  ratline site add --help
~ exit 2 — nothing was created

$ ratline site add example.com --user acme --runtime static --build-command "npm ci && npm run build"
✗ error: command contains "&&" (command chaining) at position 8, which needs a shell
  hint: put the pipeline in a script inside your repository and reference that script instead, for example --start-command "./bin/start"
~ exit 2

$ ratline user add Acme_Web
✗ error: invalid username "Acme_Web"
  hint: use lowercase letters, digits, underscores and hyphens, starting with a letter or underscore, for example "acme-web"
~ exit 2`}</Terminal>
    </article>
  );
}
