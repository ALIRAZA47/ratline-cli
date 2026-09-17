import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Page } from '../components/Layout';
import { ActionForm } from '../components/ActionForm';
import { MoreMenu } from '../components/MoreMenu';
import { useApi } from '../lib/hooks';
import type { Action, Tenant } from '../lib/types';
import { Card, Empty, ErrorBox, Facts, Spinner } from '../components/ui';
import { WarningIcon } from '../components/icons';
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
  describe,
  primary,
  rowLink,
  banner,
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
  /**
   * One row, said as a line of prose.
   *
   * This replaced a `columns` array. Five resource pages shared that table, and
   * the table is what made every one of them read like a database dump — a header
   * row to decode, and a value with nothing to break at able to widen the card.
   */
  describe: (row: T) => {
    title: React.ReactNode;
    line: React.ReactNode;
    trailing?: React.ReactNode;
    /** A token name — ok, warn, danger, fg-faint. Omit for no signal dot. */
    tone?: string;
  };
  /** The action opened by the page's main button. */
  primary?: { id: string; label: string };
  rowLink?: (row: T) => string;
  /** Rendered above the table — for a resource whose rows can need a decision. */
  banner?: (rows: T[]) => React.ReactNode;
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
        <>
          {primary && (
            <button
              className="btn btn-primary"
              onClick={() => setOpenAction(openAction === primary.id ? null : primary.id)}
            >
              {openAction === primary.id ? 'Cancel' : primary.label}
            </button>
          )}
          <MoreMenu
            actions={(actions.data ?? []).filter((a) => a.id !== primary?.id)}
            loading={actions.loading}
            onPick={setOpenAction}
            empty={`No other ${title.toLowerCase()} commands are available to you.`}
          />
        </>
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
      {banner && rows.length > 0 && banner(rows)}
      {list.loading && !list.data ? (
        <Spinner />
      ) : rows.length === 0 ? (
        <Card>
          <Empty>Nothing here yet.</Empty>
        </Card>
      ) : (
        <Card className="px-5 py-1">
          <ul>
            {rows.map((row, i) => {
              const shown = describe(row);
              return (
                <li key={i} className="listrow items-start py-4">
                  {shown.tone && (
                    <span
                      className="dot mt-1.5"
                      style={{ background: `var(--${shown.tone})` }}
                      aria-hidden="true"
                    />
                  )}
                  <span className="flex min-w-0 flex-1 flex-col gap-1">
                    {rowLink ? (
                      <Link
                        className="font-medium hover:underline [overflow-wrap:anywhere]"
                        to={rowLink(row)}
                      >
                        {shown.title}
                      </Link>
                    ) : (
                      <span className="font-medium [overflow-wrap:anywhere]">{shown.title}</span>
                    )}
                    {/* One sentence rather than four columns. The facts are the
                        same; nobody has to read a header row to tell which is
                        which, and a long value wraps instead of setting the
                        width of the card. */}
                    <span className="text-xs text-[var(--fg-muted)] [overflow-wrap:anywhere]">
                      {shown.line}
                    </span>
                  </span>
                  {shown.trailing && (
                    <span className="shrink-0 text-2xs text-[var(--fg-faint)]">
                      {shown.trailing}
                    </span>
                  )}
                </li>
              );
            })}
          </ul>
        </Card>
      )}

    </Page>
  );
}

export function Tenants() {
  return (
    <ResourceList<Tenant>
      title="Server users"
      lede="Each site runs as its own user, so one site can never read another's files. These are not people who sign in here."
      endpoint="/api/tenants"
      dataKey="users"
      group="users"
      primary={{ id: 'user.add', label: 'New server user' }}
      rowLink={(u) => `/tenants/${u.name}`}
      describe={(u) => ({
        title: u.name,
        line: [
          u.disabled ? 'Turned off' : 'Active',
          u.shell === '/usr/sbin/nologin' || u.shell === '/bin/false'
            ? 'cannot open a shell'
            : 'can open a shell',
          u.home ? `lives in ${String(u.home)}` : '',
        ]
          .filter(Boolean)
          .join(' · '),
        tone: u.disabled ? 'danger' : 'ok',
      })}
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
    <Page
      title={name}
      lede="A tenant sandbox and everything ratline records about it."
      back={{ to: '/tenants', label: 'Tenants' }}
      actions={
        <MoreMenu
          actions={actions.data ?? []}
          loading={actions.loading}
          onPick={setOpenAction}
          label="Commands"
          empty="No tenant commands are available to you."
        />
      }
    >
      <ErrorBox error={tenant.error} />
      {tenant.loading && !tenant.data && <Spinner />}

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
      lede="The padlock in the address bar. These renew themselves, and this page is where you find out when one has not."
      endpoint="/api/certs"
      dataKey="certificates"
      group="certs"
      primary={{ id: 'cert.issue', label: 'Get a certificate' }}
      banner={(rows) => <ExpiringSoon rows={rows} />}
      describe={(c) => {
        const days = daysUntil(c.not_after);
        const attached = Array.isArray(c.attached_sites)
          ? (c.attached_sites as string[]).join(', ')
          : '';
        return {
          title: String(c.name ?? ''),
          line: [
            String(c.source ?? '') === 'letsencrypt'
              ? 'Renews by itself'
              : 'You made this one yourself — browsers will warn about it',
            attached ? `used by ${attached}` : 'not used by any site yet',
          ].join(' · '),
          trailing:
            days === null ? '' : days <= 0 ? 'has expired' : `${days} days left`,
          tone: days === null ? undefined : days <= 0 ? 'danger' : days <= 21 ? 'warn' : 'ok',
        };
      }}
    />
  );
}

/**
 * Certificates near expiry, said once at the top rather than left for the reader
 * to find by comparing four dates against today.
 *
 * An expiry is the only fact on a resource list with a deadline attached, which is
 * why this is the only list that gets a band. The renewal timer usually handles it;
 * this is for when it has not.
 */
function ExpiringSoon({ rows }: { rows: Record<string, unknown>[] }) {
  const soon = rows
    .map((row) => ({ name: String(row.name ?? ''), days: daysUntil(row.not_after) }))
    .filter((c): c is { name: string; days: number } => c.days !== null && c.days <= 21)
    .sort((a, b) => a.days - b.days);

  if (soon.length === 0) return null;

  return (
    <section className="rounded-[var(--radius-card)] border border-[var(--warn)]/30 bg-[var(--warn-soft)] px-3.5 py-3">
      <div className="flex items-center gap-2">
        <span className="text-[var(--warn)]">
          <WarningIcon />
        </span>
        <h2 className="text-sm font-semibold">
          {soon.length === 1 ? 'One certificate is near expiry' : `${soon.length} certificates are near expiry`}
        </h2>
      </div>
      <ul className="mt-2 space-y-1 text-sm">
        {soon.map((c) => (
          <li key={c.name}>
            <span className="mono text-xs font-medium">{c.name}</span>
            <span className="text-[var(--fg-muted)]">
              {' — '}
              {c.days <= 0
                ? 'has expired'
                : `expires in ${c.days} ${c.days === 1 ? 'day' : 'days'}`}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** Whole days from now until an RFC 3339 date, or null if it is not one. */
function daysUntil(value: unknown): number | null {
  if (typeof value !== 'string' || value === '') return null;
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) return null;
  return Math.floor((at.getTime() - Date.now()) / 86400000);
}

export function Keys() {
  return (
    <ResourceList<Record<string, unknown>>
      title="SSH keys"
      lede="Who and what can reach this server without a password. A key for one site can only touch that site."
      endpoint="/api/keys"
      dataKey="keys"
      group="keys"
      primary={{ id: 'key.add', label: 'Add a key' }}
      describe={(k) => {
        const scope = String(k.scope ?? '');
        const target = String(k.site ?? k.user ?? '');
        return {
          title: String(k.label ?? ''),
          line:
            scope === 'server'
              ? 'The whole server — this key can do anything ratline can'
              : scope === 'site'
                ? `Can deploy ${target} and nothing else`
                : `Everything belonging to ${target}`,
          trailing: String(k.fingerprint ?? ''),
          tone: scope === 'server' ? 'warn' : undefined,
        };
      }}
    />
  );
}

export function Databases() {
  return (
    <ResourceList<Record<string, unknown>>
      title="Databases"
      lede="Each one belongs to a single site, with its own login. Nothing else on the server can read it."
      endpoint="/api/databases"
      dataKey="databases"
      group="databases"
      primary={{ id: 'db.create', label: 'New database' }}
      describe={(d) => {
        const users = Array.isArray(d.users) ? (d.users as unknown[]).length : 0;
        return {
          title: String(d.name ?? ''),
          line: [
            d.owner ? `Belongs to ${String(d.owner)}` : 'Belongs to nobody',
            `${users === 0 ? 'no logins' : users === 1 ? 'one login' : `${users} logins`}`,
            d.server ? String(d.server) : '',
          ]
            .filter(Boolean)
            .join(' · '),
        };
      }}
    />
  );
}

export function Runtimes() {
  return (
    <ResourceList<Record<string, unknown>>
      title="Languages"
      lede="The versions of Node, Bun and Python your sites can run on. Kept apart from anything Ubuntu ships, so upgrading the server does not move them."
      endpoint="/api/runtimes"
      dataKey="runtimes"
      group="runtimes"
      primary={{ id: 'runtime.install', label: 'Install a version' }}
      describe={(r) => ({
        title: `${String(r.runtime ?? r.kind ?? '')} ${String(r.version ?? '')}`.trim(),
        line: r.default
          ? 'What new sites use unless you pick otherwise'
          : `Installed under ${String(r.path ?? '/opt/ratline')}`,
        tone: r.default ? 'ok' : undefined,
      })}
    />
  );
}
