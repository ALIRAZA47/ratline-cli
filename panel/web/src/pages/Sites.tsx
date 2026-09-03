import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Page } from '../components/Layout';
import { ActionForm } from '../components/ActionForm';
import { useApi } from '../lib/hooks';
import type { Action, Site } from '../lib/types';
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
      lede="One domain, one owner, one systemd unit. Everything here runs the same ratline command you would type over SSH."
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
        <Card>
          <Table head={['Domain', 'Owner', 'Runtime', 'Enabled', 'Last deploy']}>
            {sites.map((site) => (
              <Row key={site.domain}>
                <Cell>
                  <Link className="font-medium hover:underline" to={`/sites/${site.domain}`}>
                    {site.domain}
                  </Link>
                </Cell>
                <Cell className="text-[var(--fg-muted)]">{site.user}</Cell>
                <Cell>
                  <Badge>{site.runtime}</Badge>
                </Cell>
                <Cell>
                  <Badge tone={site.enabled ? 'ok' : 'neutral'}>
                    {site.enabled ? 'enabled' : 'disabled'}
                  </Badge>
                </Cell>
                <Cell className="text-2xs text-[var(--fg-faint)]">
                  <When at={site.last_deploy_at as string | undefined} />
                </Cell>
              </Row>
            ))}
          </Table>
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
export function SiteDetail() {
  const { domain = '' } = useParams();
  const site = useApi<Record<string, unknown>>(`/api/sites/${encodeURIComponent(domain)}`);
  const env = useApi<{ env: Record<string, string>; revealed: boolean }>(
    `/api/sites/${encodeURIComponent(domain)}/env`,
  );
  const [openAction, setOpenAction] = useState<string | null>(null);
  const action = useApi<Action>(openAction ? `/api/actions/${openAction}` : null);
  const actions = useApi<Action[]>('/api/actions?group=sites');

  const quick = [
    { id: 'site.deploy', label: 'Deploy' },
    { id: 'site.restart', label: 'Restart' },
    { id: 'site.reload', label: 'Reload' },
    { id: 'site.env.set', label: 'Set a variable' },
    { id: 'cert.issue', label: 'Issue a certificate' },
    { id: 'site.disable', label: 'Disable' },
  ];

  const info = site.data ?? {};

  return (
    <Page
      title={domain}
      lede={typeof info.runtime === 'string' ? `A ${info.runtime} site owned by ${String(info.owner ?? info.user ?? '')}.` : undefined}
      actions={
        <Link className="btn" to={`/sites/${encodeURIComponent(domain)}/logs`}>
          Logs
        </Link>
      }
    >
      <ErrorBox error={site.error} />
      {site.loading && !site.data && <Spinner />}

      <div className="flex flex-wrap gap-2">
        {quick.map((q) => (
          <button
            key={q.id}
            className={`btn ${openAction === q.id ? 'btn-primary' : ''}`}
            onClick={() => setOpenAction(openAction === q.id ? null : q.id)}
          >
            {q.label}
          </button>
        ))}
      </div>

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
        <Card title="What ratline knows">
          <Facts rows={factsFrom(info)} />
        </Card>
      )}

      <Card
        title="Environment"
        action={<span className="hint">Values are masked; reading one is its own action.</span>}
      >
        <ErrorBox error={env.error} />
        {env.loading && !env.data ? (
          <Spinner />
        ) : !env.data || Object.keys(env.data.env ?? {}).length === 0 ? (
          <Empty>No variables set.</Empty>
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

      <Card title="Everything else this site can do">
        {actions.loading && <Spinner />}
        <div className="flex flex-wrap gap-1.5">
          {(actions.data ?? [])
            .filter((a) => a.verb.startsWith('site ') || a.verb.startsWith('cert '))
            .map((a) => (
              <button
                key={a.id}
                className="btn btn-ghost text-xs"
                title={a.summary}
                onClick={() => setOpenAction(a.id)}
              >
                <span className="mono">{a.verb}</span>
                {a.destructive && <span className="text-[var(--danger)]">•</span>}
              </button>
            ))}
        </div>
      </Card>
    </Page>
  );
}

export function SiteLogs() {
  const { domain = '' } = useParams();
  const [lines, setLines] = useState(200);
  const { data, error, loading, reload } = useApi<{ text: string }>(
    `/api/sites/${encodeURIComponent(domain)}/logs?lines=${lines}`,
    [lines],
  );
  return (
    <Page
      title={`${domain} · logs`}
      lede="The tail of whatever ratline considers this site's log — the journal, PM2's capture, or nginx's access log, depending on how it is supervised."
      actions={
        <>
          <select
            className="field w-auto"
            value={lines}
            onChange={(e) => setLines(Number(e.target.value))}
          >
            {[100, 200, 500, 1000, 2000].map((n) => (
              <option key={n} value={n}>
                {n} lines
              </option>
            ))}
          </select>
          <button className="btn" onClick={reload}>
            Refresh
          </button>
          <Link className="btn btn-ghost" to={`/sites/${encodeURIComponent(domain)}`}>
            Back
          </Link>
        </>
      }
    >
      <ErrorBox error={error} />
      {loading && !data ? (
        <Spinner />
      ) : (
        <pre className="terminal max-h-[70vh]">{data?.text?.trimEnd() || 'Nothing logged yet.'}</pre>
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
export function factsFrom(obj: Record<string, unknown>): [string, React.ReactNode][] {
  return Object.entries(obj)
    .filter(([, v]) => v !== null && v !== '' && v !== undefined && !(Array.isArray(v) && v.length === 0))
    .map(([k, v]) => [
      k.replace(/_/g, ' '),
      typeof v === 'object' ? (
        <code className="mono text-xs">{JSON.stringify(v)}</code>
      ) : typeof v === 'boolean' ? (
        <Badge tone={v ? 'ok' : 'neutral'}>{String(v)}</Badge>
      ) : (
        <span className="mono text-xs break-all">{String(v)}</span>
      ),
    ]);
}
