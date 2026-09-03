import { useMemo, useState } from 'react';
import { ApiError, api } from '../lib/api';
import type { Action, ActionFlag, RunResult } from '../lib/types';
import { Argv, Badge, ErrorBox, Field } from './ui';

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
}: {
  action: Action;
  /** Pre-filled positional arguments, for a form opened from a site or a tenant. */
  initialArgs?: Record<string, string>;
  onDone?: (result: RunResult) => void;
  compact?: boolean;
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

  function body() {
    const cleaned: Record<string, unknown> = {};
    for (const [name, value] of Object.entries(flags)) {
      if (value === '' || value === false) continue;
      const flag = action.flags?.find((f) => f.name === name);
      // A repeatable flag is typed as a comma-separated list, which is how
      // somebody writes two aliases without a widget that adds rows.
      if (flag?.repeatable && typeof value === 'string') {
        cleaned[name] = value
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean);
        continue;
      }
      cleaned[name] = value;
    }
    return {
      args: (action.args ?? []).map((a) => args[a.name] ?? '').filter((v) => v !== ''),
      flags: cleaned,
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

  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        void submit('run');
      }}
    >
      {!compact && (
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

      {(action.args ?? []).map((arg) => (
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

      {visibleFlags.length > 0 && (
        <div className="grid gap-3 sm:grid-cols-2">
          {visibleFlags.map((flag) => (
            <FlagField
              key={flag.name}
              flag={flag}
              value={flags[flag.name]}
              onChange={(v) => setFlags({ ...flags, [flag.name]: v })}
            />
          ))}
        </div>
      )}

      {hiddenCount > 0 && (
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
        {action.mutates && (
          <span className="hint">
            A dry run writes nothing at any layer — it is the same code path with the
            writes turned off.
          </span>
        )}
      </div>

      <ErrorBox error={error} />
      {result && <Result result={result} />}
    </form>
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
          <pre className="terminal mt-1 max-h-72">{JSON.stringify(result.data, null, 2)}</pre>
        </details>
      )}
    </div>
  );
}
