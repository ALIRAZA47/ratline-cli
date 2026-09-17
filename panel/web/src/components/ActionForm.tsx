import { useEffect, useMemo, useState } from 'react';
import { ApiError, api } from '../lib/api';
import type { Action, ActionFlag, RunResult } from '../lib/types';
import { Argv, Badge, ErrorBox, Field } from './ui';
import { Value } from './Value';

/**
 * One step of a form asked a few questions at a time.
 *
 * `fields` names positional arguments and flags by their ratline names. It is a
 * *grouping*, never a declaration: a field the steps do not mention is not dropped, it
 * is collected into an "Anything else" step before the review. That is what keeps a
 * wizard honest against a binary it does not control — a ratline release that adds a
 * flag to `site add` still offers it here, in a step nobody had to write.
 */
export interface FormStep {
  /** The short name in the rail down the side. */
  title: string;
  /** The question at the top of the step. */
  heading: string;
  lede?: string;
  fields: string[];
}

/**
 * A form for any ratline command.
 *
 * The fields are not written down anywhere in this application. They come from
 * `ratline schema`, which the binary generates by walking its own command tree — so
 * the form offers exactly the flags the installed ratline takes, with the types and
 * required-ness it declares, and a ratline release that adds a flag adds a field
 * here without anybody touching this file.
 *
 * Three things this does that a generic form generator would not, and each earns its
 * place:
 *
 *   - Runtime-specific flags are hidden until they apply. `site add` takes forty
 *     flags and roughly ten belong to any one runtime; showing all of them means an
 *     operator provisioning a static site scrolls past --app-module and --workers
 *     wondering whether they matter.
 *   - A secret is a separate field that never joins the flags. It is sent on its own
 *     and reaches ratline on stdin, because a value in argv is a value in
 *     /proc/PID/cmdline, which every account on the server can read.
 *   - Anything destructive is behind the target's name typed back. The server
 *     enforces this too — it is not a client-side courtesy — but asking here is what
 *     makes the enforcement something people meet rather than something they hit.
 */
export function ActionForm({
  action,
  initialArgs = {},
  onDone,
  compact = false,
  steps,
  onCancel,
}: {
  action: Action;
  /** Pre-filled positional arguments, for a form opened from a site or a tenant. */
  initialArgs?: Record<string, string>;
  onDone?: (result: RunResult) => void;
  compact?: boolean;
  /**
   * Ask the same questions a few at a time instead of all at once.
   *
   * Everything below — the argv preview, the secret on stdin, the typed-back
   * confirmation, the submit — is unchanged and shared. Only which fields are on
   * screen changes, because a second form component would be a second place for the
   * rules that stop argv injection to drift out of step with the first.
   */
  steps?: FormStep[];
  onCancel?: () => void;
}) {
  const [args, setArgs] = useState<Record<string, string>>(() => ({ ...initialArgs }));
  const [flags, setFlags] = useState<Record<string, string | boolean>>({});
  const [secret, setSecret] = useState('');
  const [secretKey, setSecretKey] = useState('');
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState<'preview' | 'run' | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [result, setResult] = useState<RunResult | null>(null);
  const [showAll, setShowAll] = useState(false);

  // The runtime chosen on this form, if it has such a flag. Everything
  // runtime-specific is filtered against it.
  const runtime = typeof flags.runtime === 'string' ? flags.runtime : '';

  const visibleFlags = useMemo(() => {
    const all = action.flags ?? [];
    if (showAll) return all;
    return all.filter((f) => {
      if (!f.runtime || f.runtime.length === 0) return true;
      if (!runtime) return false;
      return f.runtime.includes(runtime);
    });
  }, [action.flags, runtime, showAll]);

  const hiddenCount = (action.flags?.length ?? 0) - visibleFlags.length;

  const target = action.args?.[0] ? (args[action.args[0].name] ?? '') : '';
  const confirmed = !action.destructive || confirm.trim() === target;

  /**
   * The steps, resolved against the fields this action actually has.
   *
   * Recomputed as the runtime changes, because `visibleFlags` does: choosing Python
   * on `site add` makes --app-module apply and --entry not, and a step that had
   * resolved once would go on showing the Node question.
   */
  const [stepIndex, setStepIndex] = useState(0);
  const plan = useMemo(() => {
    if (!steps) return null;
    // In the order the step names them, not the catalogue's alphabetical one. A step
    // headed "Which server user owns it?" that opens with --branch because b sorts
    // before u has asked its question and then buried the answer.
    const argsOf = (names: string[]) =>
      names.flatMap((n) => (action.args ?? []).filter((a) => a.name === n));
    const flagsOf = (names: string[]) =>
      names.flatMap((n) => visibleFlags.filter((f) => f.name === n));

    const claimed = new Set(steps.flatMap((s) => s.fields));
    const resolved = steps.map((s) => ({
      ...s,
      args: argsOf(s.fields),
      flags: flagsOf(s.fields),
      review: false,
    }));

    // Everything the steps did not name. Offered rather than hidden — see FormStep.
    const restArgs = (action.args ?? []).filter((a) => !claimed.has(a.name));
    const restFlags = visibleFlags.filter((f) => !claimed.has(f.name));
    if (restArgs.length > 0 || restFlags.length > 0) {
      resolved.push({
        title: 'Anything else',
        heading: 'Anything else you want to set',
        lede: 'These have sensible defaults. Most sites never need them.',
        fields: [],
        args: restArgs,
        flags: restFlags,
        review: false,
      });
    }

    resolved.push({
      title: 'Check and create',
      heading: 'Check it over',
      lede: 'Nothing has happened yet. This is exactly what is about to run.',
      fields: [],
      args: [],
      flags: [],
      review: true,
    });
    return resolved;
  }, [steps, action.args, visibleFlags]);

  // A step that emptied out — the last runtime-specific question disappearing when the
  // runtime changed — must not strand somebody on a blank page.
  const at = plan ? Math.min(stepIndex, plan.length - 1) : 0;
  const current = plan?.[at];
  const onReview = !plan || (current?.review ?? false);

  /** Required fields in this step only, so Continue gates on what is on screen. */
  const stepIncomplete =
    current !== undefined &&
    !current.review &&
    (current.args.some((a) => a.required && !args[a.name]) ||
      current.flags.some((f) => f.required && !flags[f.name]));

  /**
   * The command this form is about to run, as the server would build it.
   *
   * Asked for rather than assembled here on purpose. The rules that stop argv
   * injection — one `--name=value` element, positionals after a bare `--` — live in
   * one Go function, and a TypeScript copy of them would be a second set of rules
   * that can disagree with the one that actually execs. So the browser sends the
   * form's state and is told the answer.
   */
  const [preview, setPreview] = useState<string[] | null>(null);
  const previewBody = JSON.stringify({
    args: (action.args ?? []).map((a) => args[a.name] ?? '').filter((v) => v !== ''),
    flags: cleanedFlags(action, flags),
    has_secret: secret !== '',
  });

  useEffect(() => {
    // Debounced: this is a keystroke-driven read, and the endpoint parses the
    // catalogue on the far side of it.
    let live = true;
    const id = window.setTimeout(() => {
      api
        .post<{ argv: string[] }>(`/api/actions/${action.id}/argv`, JSON.parse(previewBody))
        // `live` guards against an earlier, slower reply landing after a later one
        // and putting a command on screen that is not the one in the form.
        .then((res) => live && setPreview(res.argv))
        // A form that is not filled in yet cannot be built into a command, which
        // is a perfectly ordinary state and not worth an error box.
        .catch(() => live && setPreview(null));
    }, 250);
    return () => {
      live = false;
      window.clearTimeout(id);
    };
  }, [action.id, previewBody]);

  function body() {
    return {
      args: (action.args ?? []).map((a) => args[a.name] ?? '').filter((v) => v !== ''),
      flags: cleanedFlags(action, flags),
      secret: secret || undefined,
      secret_key: secretKey || undefined,
      confirm: confirm || undefined,
    };
  }

  async function submit(mode: 'preview' | 'run') {
    setBusy(mode);
    setError(null);
    setResult(null);
    try {
      const res = await api.post<RunResult>(`/api/actions/${action.id}/${mode}`, body());
      setResult(res);
      if (mode === 'run') onDone?.(res);
    } catch (err) {
      setError(err instanceof ApiError ? err : null);
    } finally {
      setBusy(null);
    }
  }

  const missingRequired =
    (action.args ?? []).some((a) => a.required && !args[a.name]) ||
    (action.flags ?? []).some((f) => f.required && !flags[f.name]) ||
    (action.stdin !== undefined && secret === '') ||
    (action.stdin?.key_label !== undefined && secretKey === '');

  const form = (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        // Enter on a question is "next", not "create". A wizard exists so that
        // nothing is provisioned before somebody has seen the whole of it.
        if (!onReview) {
          if (!stepIncomplete) setStepIndex(at + 1);
          return;
        }
        void submit('run');
      }}
    >
      {current && !current.review && (
        <header className="space-y-1">
          <h2 className="font-serif text-xl">{current.heading}</h2>
          {current.lede && <p className="text-sm text-[var(--fg-muted)]">{current.lede}</p>}
        </header>
      )}

      {!compact && !plan && (
        <header className="space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-lg font-semibold">{action.title}</h2>
            <Badge tone="neutral">
              <span className="mono">ratline {action.verb}</span>
            </Badge>
            {action.destructive && <Badge tone="danger">destructive</Badge>}
            {action.long && <Badge tone="accent">runs as a job</Badge>}
            {!action.mutates && <Badge tone="ok">read-only</Badge>}
          </div>
          <p className="text-sm text-[var(--fg-muted)]">{action.summary}</p>
          {action.description && (
            <p className="text-xs text-[var(--fg-faint)]">{action.description}</p>
          )}
        </header>
      )}

      {(current ? current.args : (action.args ?? [])).map((arg) => (
        <Field key={arg.name} label={arg.name} required={arg.required}>
          <input
            className="field field-mono"
            value={args[arg.name] ?? ''}
            onChange={(e) => setArgs({ ...args, [arg.name]: e.target.value })}
            autoComplete="off"
            spellCheck={false}
          />
        </Field>
      ))}

      {action.stdin && (
        <div className="grid gap-3 sm:grid-cols-2">
          {action.stdin.key_label && (
            <Field label={action.stdin.key_label} required>
              <input
                className="field field-mono"
                value={secretKey}
                onChange={(e) => setSecretKey(e.target.value)}
                autoComplete="off"
                spellCheck={false}
                placeholder="DATABASE_URL"
              />
            </Field>
          )}
          <Field label={action.stdin.label} hint={action.stdin.help} required>
            <input
              className="field field-mono"
              type="password"
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
              autoComplete="new-password"
            />
          </Field>
        </div>
      )}

      {(current ? current.flags : visibleFlags).length > 0 && (
        <div className="grid gap-3 sm:grid-cols-2">
          {(current ? current.flags : visibleFlags).map((flag) => (
            <FlagField
              key={flag.name}
              flag={flag}
              value={flags[flag.name]}
              onChange={(v) => setFlags({ ...flags, [flag.name]: v })}
            />
          ))}
        </div>
      )}

      {hiddenCount > 0 && !plan && (
        <button type="button" className="btn btn-ghost text-xs" onClick={() => setShowAll(true)}>
          Show {hiddenCount} more {hiddenCount === 1 ? 'flag' : 'flags'} for the other runtimes
        </button>
      )}

      {action.destructive && (
        <Field
          label={`Type ${target || 'the target'} to confirm`}
          hint="This cannot be undone by running another command."
          required
        >
          <input
            className="field field-mono"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            autoComplete="off"
          />
        </Field>
      )}

      {/* What you said, in the words you were asked in. The argv below is the exact
          truth and the reason it is shown; this is the readable version of the same
          thing, because "--app-module=app.main:app" is not what anybody answered. */}
      {plan && onReview && (
        <dl className="grid grid-cols-[minmax(7rem,auto)_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm">
          {plan
            .filter((s) => !s.review)
            .flatMap((s) => [
              ...s.args.map((a) => [a.name, args[a.name] ?? ''] as const),
              ...s.flags.map((f) => [f.name, flags[f.name]] as const),
            ])
            .filter(([, v]) => v !== undefined && v !== '' && v !== false)
            .map(([name, v]) => (
              <div key={name} className="contents">
                <dt className="text-[var(--fg-faint)]">{name}</dt>
                <dd className="min-w-0 [overflow-wrap:anywhere]">
                  {v === true ? 'Yes' : String(v)}
                </dd>
              </div>
            ))}
        </dl>
      )}

      {preview && (
        <div className="rounded-[var(--radius-card)] border border-[var(--border)] bg-[var(--bg-sunken)]/40 px-3.5 py-3">
          <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
            <h3 className="text-xs font-semibold">What will run</h3>
            <span className="hint">
              Built by the server, not guessed here — this is the command, exactly.
            </span>
          </div>
          <div className="mt-1.5">
            <Argv argv={preview} />
          </div>
        </div>
      )}

      {plan && !onReview ? (
        <div className="flex flex-wrap items-center gap-2">
          <button type="submit" className="btn btn-primary" disabled={stepIncomplete}>
            Continue
          </button>
          {at > 0 ? (
            <button type="button" className="btn" onClick={() => setStepIndex(at - 1)}>
              Back
            </button>
          ) : (
            onCancel && (
              <button type="button" className="btn" onClick={onCancel}>
                Cancel
              </button>
            )
          )}
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          {action.mutates && (
            <button
              type="button"
              className="btn"
              disabled={busy !== null || missingRequired}
              onClick={() => void submit('preview')}
            >
              {busy === 'preview' ? 'Rehearsing…' : 'Dry run'}
            </button>
          )}
          <button
            type="submit"
            className={`btn ${action.destructive ? 'btn-danger' : 'btn-primary'}`}
            disabled={busy !== null || missingRequired || !confirmed}
          >
            {busy === 'run' ? 'Running…' : action.mutates ? action.title : 'Run'}
          </button>
          {plan && at > 0 && (
            <button type="button" className="btn" onClick={() => setStepIndex(at - 1)}>
              Back
            </button>
          )}
          {action.mutates && (
            <span className="hint">
              A dry run writes nothing at any layer — it is the same code path with the
              writes turned off.
            </span>
          )}
        </div>
      )}

      <ErrorBox error={error} />
      {result && <Result result={result} />}
    </form>
  );

  if (!plan) return form;

  return (
    <div className="flex flex-col gap-7 md:flex-row md:gap-10">
      {/* The rail is a map, not a menu. A step ahead of where you are is not
          reachable by clicking it — the questions build on each other, and the
          runtime chosen in step two decides which questions step three even has. */}
      <ol className="flex shrink-0 flex-col gap-3 md:w-48">
        {plan.map((s, i) => (
          <li key={s.title} className="flex items-center gap-2.5">
            <span
              className={`stepnum ${i === at ? 'stepnum-on' : i < at ? 'stepnum-done' : ''}`}
              aria-hidden="true"
            >
              {i < at ? '✓' : i + 1}
            </span>
            {i < at ? (
              <button
                type="button"
                className="text-left text-sm text-[var(--fg-muted)] hover:underline"
                onClick={() => setStepIndex(i)}
              >
                {s.title}
              </button>
            ) : (
              <span
                className={`text-sm ${i === at ? 'font-medium' : 'text-[var(--fg-faint)]'}`}
                aria-current={i === at ? 'step' : undefined}
              >
                {s.title}
              </span>
            )}
          </li>
        ))}
      </ol>
      <div className="min-w-0 flex-1">{form}</div>
    </div>
  );
}

function FlagField({
  flag,
  value,
  onChange,
}: {
  flag: ActionFlag;
  value: string | boolean | undefined;
  onChange: (v: string | boolean) => void;
}) {
  if (flag.type === 'bool') {
    return (
      <label className="flex items-start gap-2 pt-5 text-sm">
        <input
          type="checkbox"
          className="mt-1"
          checked={value === true}
          onChange={(e) => onChange(e.target.checked)}
        />
        <span>
          <span className="mono text-xs">--{flag.name}</span>
          <span className="hint block">{flag.usage}</span>
        </span>
      </label>
    );
  }
  // The usage text carries the accepted values for the enum-ish flags —
  // "static, node, bun or python (required)" — so a select is offered where they
  // can be read out of it, and free text everywhere else. Guessing wrong costs
  // nothing: the field is still a text box.
  const choices = choicesIn(flag.usage);
  return (
    <Field
      label={`--${flag.name}`}
      required={flag.required}
      hint={
        <>
          {flag.usage}
          {flag.default && <span className="mono"> (default {flag.default})</span>}
          {flag.repeatable && ' — separate several with commas'}
        </>
      }
    >
      {choices ? (
        <select
          className="field"
          value={typeof value === 'string' ? value : ''}
          onChange={(e) => onChange(e.target.value)}
        >
          <option value="">—</option>
          {choices.map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </select>
      ) : (
        <input
          className="field field-mono"
          value={typeof value === 'string' ? value : ''}
          onChange={(e) => onChange(e.target.value)}
          inputMode={flag.type.startsWith('int') ? 'numeric' : undefined}
          autoComplete="off"
          spellCheck={false}
        />
      )}
    </Field>
  );
}

/**
 * Reads a closed set of values out of a flag's help text.
 *
 * ratline writes "letsencrypt, selfsigned or none (default …)" and "apex, www or
 * none", which is a list a person reads without difficulty and a machine can too.
 * Deliberately conservative: anything that does not match this shape stays a text
 * field, because a select that is missing a valid value is worse than a text box.
 */
function choicesIn(usage: string): string[] | null {
  const head = usage.split('(')[0] ?? '';
  const body = head.includes(':') ? head.slice(head.indexOf(':') + 1) : head;
  const parts = body
    .replace(/\bor\b/g, ',')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
  if (parts.length < 2 || parts.length > 6) return null;
  if (!parts.every((p) => /^[a-z][a-z0-9-]{1,20}$/.test(p))) return null;
  return parts;
}

function Result({ result }: { result: RunResult }) {
  if (result.job_id) {
    return (
      <div className="rounded-[var(--radius-card)] border border-[var(--accent)]/30 bg-[var(--accent-soft)] px-3.5 py-3 text-sm">
        Queued as a job.{' '}
        <a className="underline" href={`/jobs/${result.job_id}`}>
          Watch it run
        </a>
        .
      </div>
    );
  }
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <Badge tone={result.ok ? 'ok' : 'danger'}>
          {result.ok ? 'succeeded' : `exit ${result.exit_code}`}
        </Badge>
        {result.dry_run && <Badge tone="warn">dry run — nothing was written</Badge>}
      </div>
      <Argv argv={result.argv} />
      {result.error && (
        <ErrorBox
          error={
            new ApiError(
              {
                code: result.error.code,
                name: result.error.name,
                message: result.error.message,
                hint: result.error.hint,
              },
              200,
            )
          }
          title="ratline refused"
        />
      )}
      {result.logs && result.logs.trim() !== '' && (
        <pre className="terminal max-h-72">{result.logs.trimEnd()}</pre>
      )}
      {result.data !== undefined && result.data !== null && (
        <details>
          <summary className="cursor-pointer text-xs text-[var(--fg-muted)]">
            What ratline returned
          </summary>
          {/* Read, not parsed by eye. This was the raw envelope pretty-printed,
              which is the one place a panel can get away with showing JSON and
              still the place somebody has to squint at braces to find one value. */}
          <div className="mt-1.5 max-h-72 overflow-y-auto rounded-md border border-[var(--border)] bg-[var(--bg-sunken)] px-3 py-2 text-xs">
            <Value value={result.data} />
          </div>
        </details>
      )}
    </div>
  );
}

/**
 * The form's flags, in the shape the API takes.
 *
 * Shared by the run and the argv preview so the command shown on screen is built
 * from exactly the values the run will send.
 */
function cleanedFlags(action: Action, flags: Record<string, string | boolean>) {
  const cleaned: Record<string, unknown> = {};
  for (const [name, value] of Object.entries(flags)) {
    if (value === '' || value === false) continue;
    const flag = action.flags?.find((f) => f.name === name);
    // A repeatable flag is typed as a comma-separated list, which is how somebody
    // writes two aliases without a widget that adds rows.
    if (flag?.repeatable && typeof value === 'string') {
      cleaned[name] = value
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
      continue;
    }
    cleaned[name] = value;
  }
  return cleaned;
}
