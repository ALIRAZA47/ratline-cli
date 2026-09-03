import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Page } from '../components/Layout';
import { ActionForm } from '../components/ActionForm';
import { useApi } from '../lib/hooks';
import type { Action, Tenant } from '../lib/types';
import { Badge, Card, Cell, Empty, ErrorBox, Facts, Row, Spinner, Table } from '../components/ui';
import { factsFrom, firstArg } from './Sites';

/**
 * The shape every resource page shares: a list from one ratline read, and the
 * actions for that group from the same catalogue the rest of the panel uses.
 *
 * One component rather than five nearly identical ones. The differences between
 * tenants, keys, certificates and databases are which read to run and which columns
 * to show — everything else, including which buttons an admin gets, is the same
 * question answered by the same code.
 */
function ResourceList<T extends Record<string, unknown>>({
  title,
  lede,
  endpoint,
  dataKey,
  group,
  columns,
  primary,
  rowLink,
}: {
  title: string;
  lede: string;
  endpoint: string;
  /**
   * The field inside ratline's envelope holding the rows — "sites", "users",
   * "certificates".
   *
   * Emphatically not called `key`. React reserves that name for reconciliation and
   * strips it from props, so a component that declares one receives undefined and
   * every list here rendered as "Nothing here yet" — on five pages, silently, with
   * the API answering perfectly the whole time.
   */
  dataKey: string;
  group: string;
  columns: { head: string; cell: (row: T) => React.ReactNode }[];
  /** The action opened by the page's main button. */
  primary?: { id: string; label: string };
  rowLink?: (row: T) => string;
}) {
  const list = useApi<Record<string, T[]>>(endpoint);
  const actions = useApi<Action[]>(`/api/actions?group=${group}`);
  const [openAction, setOpenAction] = useState<string | null>(null);
  const action = useApi<Action>(openAction ? `/api/actions/${openAction}` : null);

  const rows = list.data?.[dataKey] ?? [];

  return (
    <Page
      title={title}
      lede={lede}
      actions={
        primary && (
          <button
            className="btn btn-primary"
            onClick={() => setOpenAction(openAction === primary.id ? null : primary.id)}
          >
            {openAction === primary.id ? 'Cancel' : primary.label}
          </button>
        )
      }
    >
      {openAction && (
        <Card>
          {action.loading && <Spinner />}
          <ErrorBox error={action.error} title="That action is not available to you" />
          {action.data && (
            <ActionForm
              action={action.data}
              onDone={(res) => {
                if (res.ok && !res.job_id) {
                  list.reload();
                  if (openAction === primary?.id) setOpenAction(null);
                }
              }}
            />
          )}
        </Card>
      )}

      <ErrorBox error={list.error} />
      {list.loading && !list.data ? (
        <Spinner />
      ) : rows.length === 0 ? (
        <Card>
          <Empty>Nothing here yet.</Empty>
        </Card>
      ) : (
        <Card>
          <Table head={columns.map((c) => c.head)}>
            {rows.map((row, i) => (
              <Row key={i}>
                {columns.map((col, j) => (
                  <Cell key={col.head}>
                    {j === 0 && rowLink ? (
                      <Link className="font-medium hover:underline" to={rowLink(row)}>
                        {col.cell(row)}
                      </Link>
                    ) : (
                      col.cell(row)
                    )}
                  </Cell>
                ))}
              </Row>
            ))}
          </Table>
        </Card>
      )}

      <Card title={`Everything ${title.toLowerCase()} can do`}>
        {actions.loading && <Spinner />}
        <div className="flex flex-wrap gap-1.5">
          {(actions.data ?? []).map((a) => (
            <button
              key={a.id}
              className="btn btn-ghost text-xs"
              title={a.summary}
              onClick={() => setOpenAction(a.id === openAction ? null : a.id)}
            >
              <span className="mono">{a.verb}</span>
              {a.destructive && <span className="text-[var(--danger)]">•</span>}
              {a.min_role === 'superadmin' && <span className="text-[var(--fg-faint)]">◆</span>}
            </button>
          ))}
        </div>
        <p className="hint mt-2">
          • cannot be undone by running another command · ◆ super admin only
        </p>
      </Card>
    </Page>
  );
}

export function Tenants() {
  return (
    <ResourceList<Tenant>
      title="Tenants"
      lede="A system account per tenant: its own home, group, shell and SSH keys. Sites live inside one."
      endpoint="/api/tenants"
      dataKey="users"
      group="users"
      primary={{ id: 'user.add', label: 'New tenant' }}
      rowLink={(u) => `/tenants/${u.name}`}
      columns={[
        { head: 'Name', cell: (u) => <span className="mono text-xs">{u.name}</span> },
        { head: 'Home', cell: (u) => <span className="mono text-2xs text-[var(--fg-muted)]">{String(u.home ?? '')}</span> },
        { head: 'Shell', cell: (u) => <span className="mono text-2xs text-[var(--fg-muted)]">{String(u.shell ?? '')}</span> },
        {
          head: 'State',
          cell: (u) => (
            <Badge tone={u.disabled ? 'danger' : 'ok'}>{u.disabled ? 'disabled' : 'active'}</Badge>
          ),
        },
      ]}
    />
  );
}

export function TenantDetail() {
  const { name = '' } = useParams();
  const tenant = useApi<Record<string, unknown>>(`/api/tenants/${encodeURIComponent(name)}`);
  const actions = useApi<Action[]>('/api/actions?group=users');
  const [openAction, setOpenAction] = useState<string | null>(null);
  const action = useApi<Action>(openAction ? `/api/actions/${openAction}` : null);

  return (
    <Page title={name} lede="A tenant sandbox and everything ratline records about it.">
      <ErrorBox error={tenant.error} />
      {tenant.loading && !tenant.data && <Spinner />}

      <div className="flex flex-wrap gap-1.5">
        {(actions.data ?? []).map((a) => (
          <button
            key={a.id}
            className={`btn text-xs ${openAction === a.id ? 'btn-primary' : 'btn-ghost'}`}
            title={a.summary}
            onClick={() => setOpenAction(openAction === a.id ? null : a.id)}
          >
            <span className="mono">{a.verb}</span>
          </button>
        ))}
      </div>

      {openAction && (
        <Card>
          {action.loading && <Spinner />}
          <ErrorBox error={action.error} />
          {action.data && (
            <ActionForm
              action={action.data}
              initialArgs={firstArg(action.data, name)}
              onDone={() => tenant.reload()}
            />
          )}
        </Card>
      )}

      {tenant.data && (
        <Card title="What ratline knows">
          <Facts rows={factsFrom(tenant.data)} />
        </Card>
      )}
    </Page>
  );
}

export function Certificates() {
  return (
    <ResourceList<Record<string, unknown>>
      title="Certificates"
      lede="TLS as a resource with its own lifecycle. An issuance spends a rate-limit budget, so every attempt here runs the preflight first."
      endpoint="/api/certs"
      dataKey="certificates"
      group="certs"
      primary={{ id: 'cert.issue', label: 'Issue' }}
      columns={[
        { head: 'Name', cell: (c) => <span className="mono text-xs">{String(c.name ?? '')}</span> },
        { head: 'Source', cell: (c) => <Badge>{String(c.source ?? '')}</Badge> },
        {
          head: 'Expires',
          cell: (c) => <span className="text-xs">{String(c.not_after ?? '').slice(0, 10)}</span>,
        },
        {
          head: 'Attached to',
          cell: (c) => (
            <span className="mono text-2xs text-[var(--fg-muted)]">
              {Array.isArray(c.attached_sites) ? (c.attached_sites as string[]).join(', ') : '—'}
            </span>
          ),
        },
      ]}
    />
  );
}

export function Keys() {
  return (
    <ResourceList<Record<string, unknown>>
      title="SSH keys"
      lede="Three scopes: the whole server, one tenant, or one site. A site-scoped key reaches a forced command and nothing else."
      endpoint="/api/keys"
      dataKey="keys"
      group="keys"
      primary={{ id: 'key.add', label: 'Add a key' }}
      columns={[
        { head: 'Label', cell: (k) => String(k.label ?? '') },
        { head: 'Scope', cell: (k) => <Badge>{String(k.scope ?? '')}</Badge> },
        {
          head: 'Target',
          cell: (k) => (
            <span className="mono text-2xs">{String(k.site ?? k.user ?? 'server')}</span>
          ),
        },
        {
          head: 'Fingerprint',
          cell: (k) => (
            <span className="mono text-2xs text-[var(--fg-faint)] break-all">
              {String(k.fingerprint ?? '')}
            </span>
          ),
        },
      ]}
    />
  );
}

export function Databases() {
  return (
    <ResourceList<Record<string, unknown>>
      title="Databases"
      lede="One database per tenant with least-privilege users. ratline provisions inside a server it is pointed at; db install is the one thing it installs itself."
      endpoint="/api/databases"
      dataKey="databases"
      group="databases"
      primary={{ id: 'db.create', label: 'New database' }}
      columns={[
        { head: 'Name', cell: (d) => <span className="mono text-xs">{String(d.name ?? '')}</span> },
        { head: 'Owner', cell: (d) => String(d.owner ?? '') },
        { head: 'Server', cell: (d) => <span className="mono text-2xs">{String(d.server ?? '')}</span> },
        {
          head: 'Users',
          cell: (d) => (Array.isArray(d.users) ? (d.users as unknown[]).length : 0),
        },
      ]}
    />
  );
}

export function Runtimes() {
  return (
    <ResourceList<Record<string, unknown>>
      title="Runtimes"
      lede="Node, Bun and Python versions ratline manages under /opt, separate from anything the distribution installed."
      endpoint="/api/runtimes"
      dataKey="runtimes"
      group="runtimes"
      primary={{ id: 'runtime.install', label: 'Install a version' }}
      columns={[
        { head: 'Runtime', cell: (r) => <Badge>{String(r.runtime ?? r.kind ?? '')}</Badge> },
        { head: 'Version', cell: (r) => <span className="mono text-xs">{String(r.version ?? '')}</span> },
        { head: 'Path', cell: (r) => <span className="mono text-2xs text-[var(--fg-muted)]">{String(r.path ?? '')}</span> },
        {
          head: 'Default',
          cell: (r) => (r.default ? <Badge tone="ok">default</Badge> : null),
        },
      ]}
    />
  );
}
