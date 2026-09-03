import { useState } from 'react';
import { Page } from '../components/Layout';
import { ApiError, api } from '../lib/api';
import { useApi } from '../lib/hooks';
import { useMe } from '../lib/session';
import type { Invited, Role, TeamView } from '../lib/types';
import { Badge, Card, Cell, Empty, ErrorBox, Field, Row, Spinner, Table, When } from '../components/ui';

/**
 * Who can administer this server.
 *
 * A super admin's page, and the only part of the panel that does not go through
 * ratline: these accounts are the panel's own, and creating one changes nothing on
 * the server. That distinction is worth keeping visible — deleting somebody here
 * removes their access and leaves every tenant, site and key exactly where it was.
 */
export function Team() {
  const me = useMe();
  const { data, error, loading, reload } = useApi<TeamView>('/api/team');
  const [email, setEmail] = useState('');
  const [role, setRole] = useState<Role>('admin');
  const [invited, setInvited] = useState<Invited | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<ApiError | null>(null);

  async function run<T>(fn: () => Promise<T>) {
    setBusy(true);
    setActionError(null);
    try {
      const result = await fn();
      reload();
      return result;
    } catch (err) {
      setActionError(err instanceof ApiError ? err : null);
      return null;
    } finally {
      setBusy(false);
    }
  }

  return (
    <Page
      title="Team"
      lede="Two roles. A super admin can invite people and run the operations that cannot be undone; an admin runs the server day to day."
    >
      <ErrorBox error={error} />
      <ErrorBox error={actionError} />

      <Card title="Invite somebody">
        <form
          className="flex flex-wrap items-end gap-3"
          onSubmit={async (e) => {
            e.preventDefault();
            const result = await run(() =>
              api.post<Invited>('/api/team/invites', { email, role }),
            );
            if (result) {
              setInvited(result);
              setEmail('');
            }
          }}
        >
          <div className="min-w-56 flex-1">
            <Field label="Email">
              <input
                className="field"
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
              />
            </Field>
          </div>
          <div className="w-44">
            <Field label="Role">
              <select
                className="field"
                value={role}
                onChange={(e) => setRole(e.target.value as Role)}
              >
                <option value="admin">Admin</option>
                <option value="superadmin">Super admin</option>
              </select>
            </Field>
          </div>
          <button className="btn btn-primary" disabled={busy}>
            Create a link
          </button>
        </form>

        <p className="hint mt-3">
          The panel does not send email — deliberately. Doing so would mean it owned an SMTP
          configuration, a queue, a bounce problem and a new way for an invitation to leak
          through somebody's mail logs. You get the link and choose how to send it.
        </p>

        {invited && (
          <div className="mt-4 rounded-[var(--radius-card)] border border-[var(--accent)]/30 bg-[var(--accent-soft)] p-3.5">
            <p className="text-sm font-semibold">
              Invitation for {invited.invite.email} as{' '}
              {invited.invite.role === 'superadmin' ? 'a super admin' : 'an admin'}
            </p>
            <input
              className="field field-mono mt-2"
              readOnly
              value={invited.link}
              onFocus={(e) => e.currentTarget.select()}
            />
            <p className="hint mt-2">{invited.note}</p>
            <p className="hint">
              It expires <When at={invited.invite.expires_at} />
              {' '}and works once.
            </p>
          </div>
        )}
      </Card>

      {loading && !data && <Spinner />}

      {data && (
        <Card title="Accounts">
          <Table head={['Email', 'Role', '2FA', 'State', 'Last sign-in', '']}>
            {data.accounts.map((account) => {
              const self = account.id === me.account.id;
              return (
                <Row key={account.id}>
                  <Cell>
                    <span className="font-medium">{account.email}</span>
                    {account.name && (
                      <span className="ml-2 text-2xs text-[var(--fg-faint)]">{account.name}</span>
                    )}
                    {self && <Badge tone="accent">you</Badge>}
                  </Cell>
                  <Cell>
                    <select
                      className="field w-auto text-xs"
                      value={account.role}
                      disabled={self || busy}
                      title={self ? 'You cannot change your own role' : undefined}
                      onChange={(e) =>
                        void run(() =>
                          api.post(`/api/team/${account.id}/role`, { role: e.target.value }),
                        )
                      }
                    >
                      <option value="admin">admin</option>
                      <option value="superadmin">superadmin</option>
                    </select>
                  </Cell>
                  <Cell>
                    <Badge tone={account.totp_enabled ? 'ok' : 'warn'}>
                      {account.totp_enabled ? 'on' : 'off'}
                    </Badge>
                  </Cell>
                  <Cell>
                    <Badge tone={account.disabled ? 'danger' : 'ok'}>
                      {account.disabled ? 'disabled' : 'active'}
                    </Badge>
                  </Cell>
                  <Cell className="text-2xs text-[var(--fg-faint)]">
                    <When at={account.last_login_at} />
                  </Cell>
                  <Cell>
                    <div className="flex gap-1">
                      <button
                        className="btn btn-ghost text-2xs"
                        disabled={self || busy}
                        onClick={() =>
                          void run(() =>
                            api.post(`/api/team/${account.id}/disable`, {
                              disabled: !account.disabled,
                            }),
                          )
                        }
                      >
                        {account.disabled ? 'Enable' : 'Disable'}
                      </button>
                      <button
                        className="btn btn-ghost text-2xs text-[var(--danger)]"
                        disabled={self || busy}
                        onClick={() => {
                          const typed = window.prompt(
                            `Type ${account.email} to remove their access to the panel. Nothing on the server changes.`,
                          );
                          if (typed === null) return;
                          void run(() =>
                            api.del(`/api/team/${account.id}`, { 'X-Ratline-Confirm': typed }),
                          );
                        }}
                      >
                        Remove
                      </button>
                    </div>
                  </Cell>
                </Row>
              );
            })}
          </Table>
        </Card>
      )}

      {data && (
        <Card title="Invitations">
          {data.invites.length === 0 ? (
            <Empty>None outstanding.</Empty>
          ) : (
            <Table head={['Email', 'Role', 'Status', 'Invited by', 'Expires', '']}>
              {data.invites.map((invite) => (
                <Row key={invite.id}>
                  <Cell>{invite.email}</Cell>
                  <Cell>
                    <Badge>{invite.role}</Badge>
                  </Cell>
                  <Cell>
                    <Badge
                      tone={
                        invite.status === 'pending'
                          ? 'accent'
                          : invite.status === 'accepted'
                            ? 'ok'
                            : 'neutral'
                      }
                    >
                      {invite.status}
                    </Badge>
                  </Cell>
                  <Cell className="text-2xs text-[var(--fg-muted)]">{invite.invited_by}</Cell>
                  <Cell className="text-2xs text-[var(--fg-faint)]">
                    <When at={invite.expires_at} />
                  </Cell>
                  <Cell>
                    {invite.status === 'pending' && (
                      <button
                        className="btn btn-ghost text-2xs"
                        disabled={busy}
                        onClick={() => void run(() => api.del(`/api/team/invites/${invite.id}`))}
                      >
                        Revoke
                      </button>
                    )}
                  </Cell>
                </Row>
              ))}
            </Table>
          )}
        </Card>
      )}
    </Page>
  );
}
