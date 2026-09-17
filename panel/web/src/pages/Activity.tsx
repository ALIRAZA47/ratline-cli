import { useState } from 'react';
import { Page } from '../components/Layout';
import { useApi } from '../lib/hooks';
import type { ActionRecord } from '../lib/types';
import { Card, Empty, ErrorBox, Spinner, When } from '../components/ui';

/**
 * Who asked for what.
 *
 * This is the panel's half of the record. ratline writes its own audit entry for
 * every command that ran, but each one reaches it as root, so it cannot know which
 * person was behind it. Read together they are the whole story; either alone is half
 * of one — which is worth saying on the page, because an operator who thinks this is
 * the audit log will not go and read the other one.
 */
export function Activity() {
  const [failedOnly, setFailedOnly] = useState(false);
  const { data, error, loading } = useApi<ActionRecord[]>(
    `/api/activity${failedOnly ? '?failed=true' : ''}`,
    [failedOnly],
  );
  return (
    <Page
      title="Activity"
      lede="Everything anyone has asked this panel to do, and whether it worked."
      actions={
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={failedOnly}
            onChange={(e) => setFailedOnly(e.target.checked)}
          />
          Failures only
        </label>
      }
    >
      <ErrorBox error={error} />
      {loading && !data ? (
        <Spinner />
      ) : !data || data.length === 0 ? (
        <Card>
          <Empty>{failedOnly ? 'Nothing has failed.' : 'Nothing yet.'}</Empty>
        </Card>
      ) : (
        <Card className="px-5 py-1">
          <ul>
            {data.map((rec) => (
              <li key={rec.id} className="listrow items-start py-3.5">
                <span
                  className="dot mt-1.5"
                  style={{ background: rec.ok ? 'var(--ok)' : 'var(--danger)' }}
                  aria-hidden="true"
                />
                <span className="flex min-w-0 flex-1 flex-col gap-1">
                  {/* The row as a sentence: who, what, to which thing. The six
                      columns this replaced held the same six facts and made the
                      reader assemble them. */}
                  <span className="[overflow-wrap:anywhere]">
                    <span className="font-medium">{rec.actor ?? 'Somebody'}</span>{' '}
                    {sentence(rec)}
                  </span>
                  <span className="text-2xs text-[var(--fg-faint)]">
                    <When at={rec.at} />
                    {' · took '}
                    {took(rec.duration_ms)}
                    {!rec.ok && rec.error ? ` · ${rec.error}` : ''}
                  </span>
                </span>
                {rec.dry_run && <span className="tag tag-warn shrink-0">rehearsal</span>}
              </li>
            ))}
          </ul>
        </Card>
      )}
    </Page>
  );
}

/**
 * What somebody did, said the way they would say it.
 *
 * Each verb carries its three forms rather than being derived from one. Deriving
 * looked tidy and produced "rehearsed made the database" and "tried to got a
 * certificate" — English does not take a suffix rule, and a panel that speaks in
 * sentences has to get the sentences right.
 */
const verbs: Record<string, { did: string; doing: string; tryTo: string }> = {
  'site deploy': { did: 'deployed', doing: 'deploying', tryTo: 'deploy' },
  'site add': { did: 'set up', doing: 'setting up', tryTo: 'set up' },
  'site delete': { did: 'removed', doing: 'removing', tryTo: 'remove' },
  'site enable': { did: 'started serving', doing: 'starting', tryTo: 'start serving' },
  'site disable': { did: 'stopped serving', doing: 'stopping', tryTo: 'stop serving' },
  'site env set': { did: 'changed a setting on', doing: 'changing a setting on', tryTo: 'change a setting on' },
  'cert issue': { did: 'got a certificate for', doing: 'getting a certificate for', tryTo: 'get a certificate for' },
  'cert renew': { did: 'renewed the certificate for', doing: 'renewing the certificate for', tryTo: 'renew the certificate for' },
  'db create': { did: 'made the database', doing: 'making the database', tryTo: 'make the database' },
  'db dump': { did: 'backed up', doing: 'backing up', tryTo: 'back up' },
  'runtime install': { did: 'installed', doing: 'installing', tryTo: 'install' },
  'key add': { did: 'added the SSH key', doing: 'adding the SSH key', tryTo: 'add the SSH key' },
  'key revoke': { did: 'removed the SSH key', doing: 'removing the SSH key', tryTo: 'remove the SSH key' },
  'user add': { did: 'made the server user', doing: 'making the server user', tryTo: 'make the server user' },
  'user delete': { did: 'removed the server user', doing: 'removing the server user', tryTo: 'remove the server user' },
};

function sentence(rec: ActionRecord): string {
  const verb = rec.action.replace(/^ratline\s+/, '');
  const target = rec.target ? ` ${rec.target}` : '';
  // A command with no entry still reads: "ran db access allow on 203.0.113.9".
  const forms = verbs[verb] ?? {
    did: `ran ${verb} on`,
    doing: `running ${verb} on`,
    tryTo: `run ${verb} on`,
  };
  if (rec.dry_run) return `rehearsed ${forms.doing}${target}, and nothing was written`;
  if (!rec.ok) return `tried to ${forms.tryTo}${target}`;
  return `${forms.did}${target}`;
}

/** Milliseconds, said the way a person measures. */
function took(ms?: number): string {
  if (ms === undefined || ms < 0) return 'no time at all';
  if (ms < 1000) return `${ms}ms`;
  const seconds = Math.round(ms / 100) / 10;
  if (seconds < 90) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  return `${minutes}m ${Math.round(seconds % 60)}s`;
}
