import { useState } from 'react';
import { Page } from '../components/Layout';
import { ApiError, api } from '../lib/api';
import { useApi } from '../lib/hooks';
import { useMe, useSession } from '../lib/session';
import { Badge, Card, Cell, ErrorBox, Field, Row, Spinner, Table } from '../components/ui';

interface SessionSummary {
  current: boolean;
  ip?: string;
  user_agent?: string;
  created_at: string;
  last_seen: string;
  expires_at: string;
}

export function AccountPage() {
  const me = useMe();
  const { refresh } = useSession();
  const sessions = useApi<SessionSummary[]>('/api/me/sessions');

  return (
    <Page title="Your account" lede={me.account.email}>
      <div className="grid gap-4 lg:grid-cols-2">
        <PasswordCard />
        <TotpCard onChange={refresh} />
      </div>

      <Card title="Signed-in browsers">
        {sessions.loading && !sessions.data && <Spinner />}
        <ErrorBox error={sessions.error} />
        {sessions.data && (
          <Table head={['Where', 'Browser', 'Last seen', 'Expires']}>
            {sessions.data.map((s, i) => (
              <Row key={i}>
                <Cell className="mono text-xs">
                  {s.ip || 'unknown'} {s.current && <Badge tone="accent">this one</Badge>}
                </Cell>
                <Cell className="max-w-xs truncate text-2xs text-[var(--fg-muted)]">
                  {s.user_agent}
                </Cell>
                <Cell className="text-2xs">{new Date(s.last_seen).toLocaleString()}</Cell>
                <Cell className="text-2xs">{new Date(s.expires_at).toLocaleString()}</Cell>
              </Row>
            ))}
          </Table>
        )}
        <p className="hint mt-3">
          Changing your password ends every one of these, including this one. That is the way
          to evict a session you do not recognise.
        </p>
      </Card>
    </Page>
  );
}

function PasswordCard() {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [error, setError] = useState<ApiError | null>(null);
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);

  return (
    <Card title="Password">
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError(null);
          try {
            await api.post('/api/me/password', { current, new: next });
            setDone(true);
          } catch (err) {
            setError(err instanceof ApiError ? err : null);
          } finally {
            setBusy(false);
          }
        }}
      >
        <Field label="Current password">
          <input
            className="field"
            type="password"
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            required
          />
        </Field>
        <Field label="New password" hint="At least 12 characters.">
          <input
            className="field"
            type="password"
            autoComplete="new-password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
            required
          />
        </Field>
        <ErrorBox error={error} />
        {done && (
          <p className="text-sm text-[var(--ok)]">
            Changed. You have been signed out everywhere — sign in again.
          </p>
        )}
        <button className="btn btn-primary" disabled={busy}>
          Change it
        </button>
      </form>
    </Card>
  );
}

/**
 * Second-factor enrolment, in two steps.
 *
 * The secret is issued and stored inert; it only becomes the account's second factor
 * once a code proves the authenticator actually has it. Enabling on the first call
 * would let somebody scan a code that did not save and lock themselves out of the
 * panel they administer.
 *
 * The QR code is deliberately absent: rendering one needs a library, and a library
 * here is a dependency in the bundle of a root-equivalent interface for the sake of a
 * picture of a string that can be typed. Every authenticator accepts the secret by
 * hand.
 */
function TotpCard({ onChange }: { onChange: () => Promise<void> }) {
  const me = useMe();
  const [secret, setSecret] = useState<{ secret: string; uri: string } | null>(null);
  const [code, setCode] = useState('');
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  if (me.account.totp_enabled) {
    return (
      <Card title="Second factor">
        <p className="text-sm">
          <Badge tone="ok">enabled</Badge> A code is required every time you sign in.
        </p>
        <p className="hint mt-3">
          Lost the device? A super admin can clear it with{' '}
          <code className="mono">ratline-panel account totp-reset {me.account.email}</code> over
          SSH.
        </p>
      </Card>
    );
  }

  return (
    <Card title="Second factor">
      {!secret ? (
        <>
          <p className="text-sm text-[var(--fg-muted)]">
            Signing in to this panel is equivalent to root on this machine. A password alone is
            one credential between a phishing email and the server.
          </p>
          <button
            className="btn btn-primary mt-3"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              setError(null);
              try {
                setSecret(await api.post<{ secret: string; uri: string }>('/api/me/totp/start'));
              } catch (err) {
                setError(err instanceof ApiError ? err : null);
              } finally {
                setBusy(false);
              }
            }}
          >
            Set one up
          </button>
          <ErrorBox error={error} />
        </>
      ) : (
        <form
          className="space-y-3"
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            setError(null);
            try {
              await api.post('/api/me/totp/confirm', { code });
              await onChange();
            } catch (err) {
              setError(err instanceof ApiError ? err : null);
            } finally {
              setBusy(false);
            }
          }}
        >
          <Field label="Add this secret to your authenticator">
            <input
              className="field field-mono"
              readOnly
              value={secret.secret}
              onFocus={(e) => e.currentTarget.select()}
            />
          </Field>
          <p className="hint break-all">
            Or paste the whole URI: <span className="mono">{secret.uri}</span>
          </p>
          <Field label="Then type the six-digit code it shows">
            <input
              className="field field-mono"
              inputMode="numeric"
              maxLength={6}
              value={code}
              onChange={(e) => setCode(e.target.value)}
              required
              autoFocus
            />
          </Field>
          <ErrorBox error={error} />
          <button className="btn btn-primary" disabled={busy}>
            Turn it on
          </button>
        </form>
      )}
    </Card>
  );
}
