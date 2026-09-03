import { useMemo, useState } from 'react';
import { useParams } from 'react-router-dom';
import { Page } from '../components/Layout';
import { ActionForm } from '../components/ActionForm';
import { useApi } from '../lib/hooks';
import type { Action } from '../lib/types';
import { Badge, Card, Empty, ErrorBox, Spinner } from '../components/ui';

/**
 * Every command the signed-in account may run.
 *
 * The list is not written down in this application: it is the installed ratline's own
 * command tree, filtered by role on the server. An admin's browser never receives the
 * super-admin operations at all, which is the difference between an interface that
 * hides a button and one that does not have it.
 */
export function Actions() {
  const { data, error, loading } = useApi<Action[]>('/api/actions');
  const [query, setQuery] = useState('');
  const [openAction, setOpenAction] = useState<string | null>(null);
  const action = useApi<Action>(openAction ? `/api/actions/${openAction}` : null);

  const groups = useMemo(() => {
    const filtered = (data ?? []).filter((a) => {
      if (!query.trim()) return true;
      const needle = query.toLowerCase();
      return (
        a.verb.includes(needle) ||
        a.title.toLowerCase().includes(needle) ||
        a.summary.toLowerCase().includes(needle)
      );
    });
    const byGroup = new Map<string, Action[]>();
    for (const a of filtered) {
      const list = byGroup.get(a.group) ?? [];
      list.push(a);
      byGroup.set(a.group, list);
    }
    return [...byGroup.entries()].sort(([a], [b]) => a.localeCompare(b));
  }, [data, query]);

  return (
    <Page
      title="All commands"
      lede="Everything the installed ratline can do that you are allowed to do. The forms are generated from the binary's own schema, so they cannot offer a flag it does not have."
      actions={
        <input
          className="field w-56"
          placeholder="Search…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      }
    >
      <ErrorBox error={error} />
      {loading && !data && <Spinner />}

      {openAction && (
        <Card>
          <button className="btn btn-ghost mb-3 text-xs" onClick={() => setOpenAction(null)}>
            ← Back to the list
          </button>
          {action.loading && <Spinner />}
          <ErrorBox error={action.error} />
          {action.data && <ActionForm action={action.data} />}
        </Card>
      )}

      {!openAction &&
        groups.map(([group, list]) => (
          <Card key={group} title={group}>
            <ul className="grid gap-1.5 sm:grid-cols-2">
              {list.map((a) => (
                <li key={a.id}>
                  <button
                    className="w-full rounded-md px-2.5 py-2 text-left hover:bg-[var(--bg-hover)]"
                    onClick={() => setOpenAction(a.id)}
                  >
                    <span className="flex flex-wrap items-center gap-1.5">
                      <span className="mono text-xs font-medium">ratline {a.verb}</span>
                      {a.destructive && <Badge tone="danger">destructive</Badge>}
                      {a.long && <Badge tone="accent">job</Badge>}
                      {!a.mutates && <Badge tone="ok">read-only</Badge>}
                      {a.min_role === 'superadmin' && <Badge tone="warn">super admin</Badge>}
                    </span>
                    <span className="mt-0.5 block text-xs text-[var(--fg-muted)]">{a.summary}</span>
                  </button>
                </li>
              ))}
            </ul>
          </Card>
        ))}

      {!openAction && groups.length === 0 && !loading && (
        <Card>
          <Empty>Nothing matches “{query}”.</Empty>
        </Card>
      )}
    </Page>
  );
}

/** A deep link to one action's form, so a runbook can point straight at it. */
export function ActionPage() {
  const { id = '' } = useParams();
  const { data, error, loading } = useApi<Action>(`/api/actions/${id}`);
  return (
    <Page title={data?.title ?? id.replace(/\./g, ' ')}>
      <ErrorBox error={error} title="That action is not available to you" />
      {loading && !data && <Spinner />}
      {data && (
        <Card>
          <ActionForm action={data} />
        </Card>
      )}
    </Page>
  );
}
