import { useEffect, useRef, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Page } from '../components/Layout';
import { ActionForm } from '../components/ActionForm';
import { MoreMenu } from '../components/MoreMenu';
import { Value, labelFor } from '../components/Value';
import { useApi, usePoll } from '../lib/hooks';
import type { Action, ActionRecord, Site } from '../lib/types';
import {
  Badge,
  Card,
  Cell,
  Empty,
  ErrorBox,
  Facts,
  Row,
  Spinner,
  Table,
  When,
} from '../components/ui';

export function Sites() {
  const { data, error, loading, reload } = useApi<{ sites: Site[] }>('/api/sites');
  const [creating, setCreating] = useState(false);
  const create = useApi<Action>(creating ? '/api/actions/site.add' : null);

  const sites = data?.sites ?? [];

  return (
    <Page
      title="Sites"
      lede="One address, one owner, one service behind it."
      actions={
        <button className="btn btn-primary" onClick={() => setCreating((v) => !v)}>
          {creating ? 'Cancel' : 'New site'}
        </button>
      }
    >
      {creating && (
        <Card title="Provision a site">
          {create.loading && <Spinner />}
          <ErrorBox error={create.error} />
          {create.data && (
            <ActionForm
              action={create.data}
              compact
              onDone={(res) => {
                if (res.ok && !res.job_id) {
                  setCreating(false);
                  reload();
                }
              }}
            />
          )}
        </Card>
      )}

      <ErrorBox error={error} />
      {loading && !data ? (
        <Spinner />
      ) : sites.length === 0 ? (
        <Card>
          <Empty>No sites yet.</Empty>
        </Card>
      ) : (
        <Card className="px-5 py-1">
          <ul>
            {sites.map((site) => (
              <li key={site.domain} className="listrow items-start py-4">
                <span
                  className="dot mt-1.5"
                  style={{ background: site.enabled ? 'var(--ok)' : 'var(--fg-faint)' }}
                  aria-hidden="true"
                />
                <span className="flex min-w-0 flex-1 flex-col gap-1">
                  <Link
                    className="text-base font-medium hover:underline [overflow-wrap:anywhere]"
                    to={`/sites/${site.domain}`}
                  >
                    {site.domain}
                  </Link>
                  {/* A sentence rather than four columns. The facts are the same
                      ones; what changes is that nobody has to read a header row
                      to know which is which. */}
                  <span className="text-xs text-[var(--fg-muted)]">
                    {site.enabled ? 'Being served' : 'Not being served'}
                    {site.runtime ? ` · ${site.runtime}` : ''}
                    {site.user ? ` · belongs to ${site.user}` : ''}
                  </span>
                </span>
                <span className="shrink-0 text-2xs text-[var(--fg-faint)]">
                  {site.last_deploy_at ? (
                    <>
                      deployed <When at={site.last_deploy_at as string} />
                    </>
                  ) : (
                    'never deployed'
                  )}
                </span>
              </li>
            ))}
          </ul>
        </Card>
      )}
    </Page>
  );
}

/**
 * One site, and everything that can be done to it.
 *
 * The quick actions are the six verbs somebody reaches for daily; the rest of the
 * site surface is a filtered view of the same catalogue every other page uses, so
 * there is no list of buttons here to fall out of step with the binary.
 */
/**
 * How the site is, in a sentence.
 *
 * Replaces "A python site owned by acme." — which names two fields and says
 * nothing about whether the thing is working, which is what somebody opening a
 * site's page came to find out.
 */
function howItIs(info: Record<string, unknown>): string {
  const state = String(info.state ?? '');
  const workers = typeof info.workers === 'number' ? info.workers : undefined;
  const runtime = typeof info.runtime === 'string' ? info.runtime : '';
  const owner = String(info.owner ?? info.user ?? '');

  const doing =
    state === 'active' || state === 'running'
      ? workers
        ? `Serving on ${workers} ${workers === 1 ? 'worker' : 'workers'}.`
        : 'Serving.'
      : state === 'serving'
        ? 'Serving files.'
        : state === 'disabled'
          ? 'Not being served at the moment.'
          : state
            ? `${state.charAt(0).toUpperCase()}${state.slice(1)}.`
            : '';

  const whose = [runtime && `A ${runtime} site`, owner && `belonging to ${owner}`]
    .filter(Boolean)
    .join(' ');

  return [doing, whose && `${whose}.`].filter(Boolean).join(' ');
}

/** The one thing people come to this page to do. Everything else is in the menu. */
const PRIMARY = 'site.deploy';

export function SiteDetail() {
  const { domain = '' } = useParams();
  const site = useApi<Record<string, unknown>>(`/api/sites/${encodeURIComponent(domain)}`);
  const env = useApi<{ env: Record<string, string>; revealed: boolean }>(
    `/api/sites/${encodeURIComponent(domain)}/env`,
  );
  const [openAction, setOpenAction] = useState<string | null>(null);
  const action = useApi<Action>(openAction ? `/api/actions/${openAction}` : null);
  const actions = useApi<Action[]>('/api/actions?group=sites');

  const info = site.data ?? {};
  // The activity log already filters by target, so a site's own history costs one
  // more read and saves crossing to /activity and filtering it by hand.
  const history = useApi<ActionRecord[]>(
    `/api/activity?target=${encodeURIComponent(domain)}`,
    [domain],
  );

  // Everything this site can be asked to do, minus the one verb that gets its own
  // button. The list is the catalogue's, so a ratline release that adds a site
  // command adds it to the menu without anybody editing this file.
  const others = (actions.data ?? []).filter(
    (a) => (a.verb.startsWith('site ') || a.verb.startsWith('cert ')) && a.id !== PRIMARY,
  );

  return (
    <Page
      title={domain}
      lede={howItIs(info)}
      back={{ to: '/sites', label: 'Sites' }}
      actions={
        <>
          <button
            className={`btn ${openAction === PRIMARY ? '' : 'btn-primary'}`}
            onClick={() => setOpenAction(openAction === PRIMARY ? null : PRIMARY)}
          >
            {openAction === PRIMARY ? 'Cancel' : 'Deploy'}
          </button>
          <Link className="btn" to={`/sites/${encodeURIComponent(domain)}/logs`}>
            Logs
          </Link>
          <MoreMenu
            actions={others}
            loading={actions.loading}
            onPick={setOpenAction}
            empty="No other site commands are available to you."
          />
        </>
      }
    >
      <ErrorBox error={site.error} />
      {site.loading && !site.data && <Spinner />}

      {openAction && (
        <Card>
          {action.loading && <Spinner />}
          <ErrorBox error={action.error} title="That action is not available to you" />
          {action.data && (
            <ActionForm
              action={action.data}
              initialArgs={firstArg(action.data, domain)}
              onDone={() => {
                site.reload();
                env.reload();
              }}
            />
          )}
        </Card>
      )}

      {site.data && (
        <Card title="How this site is set up">
          <Facts rows={factsFrom(info)} />
        </Card>
      )}

      <Card
        title="Settings this site runs with"
        action={
          <span className="hint">Hidden by default — showing one is recorded against your name.</span>
        }
      >
        <ErrorBox error={env.error} />
        {env.loading && !env.data ? (
          <Spinner />
        ) : !env.data || Object.keys(env.data.env ?? {}).length === 0 ? (
          <Empty>No settings yet.</Empty>
        ) : (
          <Table head={['Key', 'Value']}>
            {Object.entries(env.data.env).map(([k, v]) => (
              <Row key={k}>
                <Cell className="mono text-xs">{k}</Cell>
                <Cell className="mono text-xs text-[var(--fg-faint)]">{v}</Cell>
              </Row>
            ))}
          </Table>
        )}
      </Card>
      <Card
        title="This site's history"
        action={
          <Link className="btn btn-ghost text-xs" to={`/activity?target=${encodeURIComponent(domain)}`}>
            Full log
          </Link>
        }
      >
        <ErrorBox error={history.error} />
        {history.loading && !history.data ? (
          <Spinner />
        ) : !history.data || history.data.length === 0 ? (
          <Empty>Nothing has been done to this site through the panel yet.</Empty>
        ) : (
          <ul className="space-y-1.5 text-sm">
            {history.data.slice(0, 8).map((rec) => (
              <li key={rec.id} className="flex items-baseline justify-between gap-3">
                <span className="min-w-0">
                  <span className="mono text-xs">{rec.action}</span>
                  <span className="ml-1.5 text-xs text-[var(--fg-muted)]">by {rec.actor}</span>
                  {rec.dry_run && <span className="ml-1.5 text-2xs text-[var(--warn)]">dry run</span>}
                </span>
                <span className="flex shrink-0 items-center gap-2 text-2xs text-[var(--fg-faint)]">
                  {!rec.ok && <Badge tone="danger">exit {rec.exit_code}</Badge>}
                  <When at={rec.at} />
                </span>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </Page>
  );
}

const LOG_STREAMS = [
  { value: 'app', label: 'Application' },
  { value: 'access', label: 'Access (nginx)' },
  { value: 'error', label: 'Error (nginx)' },
  { value: 'journal', label: 'Journal (systemd)' },
] as const;

type LogStream = (typeof LOG_STREAMS)[number]['value'];

const STREAM_LEDE: Record<LogStream, string> = {
  app: "The application's own output: its stdout under systemd, or PM2's capture in logs/app.log.",
  access: "nginx's access log for this site, one line per request it served.",
  error: "nginx's error log for this site: the upstream failures and 502s.",
  journal: 'The systemd journal for the unit itself: a failed start, an OOM kill, a crash loop.',
};

/** How often a followed log re-reads. */
const FOLLOW_MS = 3000;

export function SiteLogs() {
  const { domain = '' } = useParams();
  const [lines, setLines] = useState(200);
  const [stream, setStream] = useState<LogStream>('app');
  const [filter, setFilter] = useState('');
  const [follow, setFollow] = useState(false);
  const pane = useRef<HTMLPreElement>(null);
  const { data, error, loading, reload } = useApi<{ text: string }>(
    `/api/sites/${encodeURIComponent(domain)}/logs?lines=${lines}&stream=${stream}`,
    [lines, stream],
  );

  // Following is a poll, not a held-open request.
  //
  // ratline has a --follow that blocks until it is interrupted, and the panel
  // deliberately never passes it: a browser that navigates away, sleeps or loses its
  // connection leaves that process running on the server with nobody to end it, and one
  // forgotten tab becomes a journalctl that outlives the person who opened it. Re-reading
  // the last N lines every few seconds gives an operator the same thing — lines appearing
  // as they are written — out of requests that each finish on their own.
  usePoll(reload, FOLLOW_MS, follow);

  // Pinned to the bottom while following, which is the whole point of following.
  useEffect(() => {
    if (!follow) return;
    const el = pane.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [follow, data]);

  // Filtered here rather than server-side, because ratline's logs command has no
  // such flag and inventing one in the query string would be a control that reads
  // like it narrows the read when it does not. This narrows what is already on
  // screen, which is what somebody scanning a tail for one request actually wants.
  const all = data?.text?.trimEnd() ?? '';
  const needle = filter.trim().toLowerCase();
  const shown = needle
    ? all.split('\n').filter((l) => l.toLowerCase().includes(needle)).join('\n')
    : all;
  const hiddenLines = needle ? all.split('\n').length - shown.split('\n').length : 0;
  return (
    <Page
      title={`${domain} · logs`}
      back={{ to: `/sites/${encodeURIComponent(domain)}`, label: domain }}
      lede={STREAM_LEDE[stream]}
      actions={
        <>
          <select
            className="field w-auto"
            value={stream}
            onChange={(e) => setStream(e.target.value as LogStream)}
            aria-label="Which log"
          >
            {LOG_STREAMS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
          <select
            className="field w-auto"
            value={lines}
            onChange={(e) => setLines(Number(e.target.value))}
            aria-label="How many lines"
          >
            {[100, 200, 500, 1000, 2000].map((n) => (
              <option key={n} value={n}>
                {n} lines
              </option>
            ))}
          </select>
          {/* One control, two states, and it says which it is in. A "Follow"
              checkbox beside a "Refresh" button leaves somebody wondering whether
              the button still does anything while the box is ticked. */}
          <button
            className={`btn ${follow ? 'btn-primary' : ''}`}
            onClick={() => setFollow((v) => !v)}
            aria-pressed={follow}
          >
            {follow ? 'Following' : 'Follow'}
          </button>
          <button className="btn" onClick={reload} disabled={follow}>
            Refresh
          </button>
        </>
      }
    >
      <ErrorBox error={error} />

      <Card title={LOG_STREAMS.find((x) => x.value === stream)?.label ?? 'Log'}>
        <div className="flex flex-wrap items-end gap-3">
          <label className="block min-w-48 flex-1">
            <span className="label">Filter these lines</span>
            <input
              className="field"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="any text"
              autoComplete="off"
            />
          </label>
          <p className="hint mb-2 max-w-prose flex-1">
            {STREAM_LEDE[stream]}
            {hiddenLines > 0 && ` ${hiddenLines} lines hidden by the filter.`}
            {follow && ` Re-reading the last ${lines} lines every ${FOLLOW_MS / 1000} seconds.`}
          </p>
        </div>
      </Card>

      {loading && !data ? (
        <Spinner />
      ) : (
        <pre ref={pane} className="terminal max-h-[70vh] overflow-auto" aria-live={follow ? 'polite' : 'off'}>
          {shown || (needle ? `Nothing in the last ${lines} lines matches that.` : 'Nothing logged yet.')}
        </pre>
      )}
    </Page>
  );
}

/** Pre-fills a form's first positional argument when it names a domain or a site. */
export function firstArg(action: Action, value: string): Record<string, string> {
  const first = action.args?.[0];
  if (!first) return {};
  return { [first.name]: value };
}

/**
 * Turns whatever ratline returned into label/value rows.
 *
 * Generic rather than a hand-written list, because `site show` returns a different
 * shape per runtime and a fixed list would show empty rows for the fields that do
 * not apply — and silently omit any field a later ratline adds.
 */
/**
 * Whatever ratline returned, as label/value rows.
 *
 * Every value goes through `Value`, including the objects. This used to stringify
 * them — which is how `site show`'s twenty-field certificate arrived as one line of
 * JSON that could not wrap, and took the width of the card with it.
 */
export function factsFrom(obj: Record<string, unknown>): [string, React.ReactNode][] {
  return Object.entries(obj)
    .filter(([, v]) => v !== null && v !== '' && v !== undefined && !(Array.isArray(v) && v.length === 0))
    .map(([k, v]) => [labelFor(k), <Value value={v} />]);
}
