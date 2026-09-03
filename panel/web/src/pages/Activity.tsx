import { useState } from 'react';
import { Page } from '../components/Layout';
import { useApi } from '../lib/hooks';
import type { ActionRecord } from '../lib/types';
import { Badge, Card, Cell, Empty, ErrorBox, Row, Spinner, Table, When } from '../components/ui';

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
      lede="Every action asked for through this panel, and who asked for it. ratline keeps its own audit log of the commands themselves, at /var/log/ratline."
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
        <Card>
          <Table head={['When', 'Who', 'Action', 'Target', 'Result', 'Took']}>
            {data.map((rec) => (
              <Row key={rec.id}>
                <Cell className="text-2xs whitespace-nowrap text-[var(--fg-faint)]">
                  <When at={rec.at} />
                </Cell>
                <Cell className="text-xs">{rec.actor}</Cell>
                <Cell>
                  <span className="mono text-xs">{rec.action}</span>
                  {rec.dry_run && (
                    <Badge tone="warn">
                      <span>dry run</span>
                    </Badge>
                  )}
                </Cell>
                <Cell className="mono text-2xs">{rec.target}</Cell>
                <Cell>
                  {rec.ok ? (
                    <Badge tone="ok">ok</Badge>
                  ) : (
                    <span className="space-y-1">
                      <Badge tone="danger">exit {rec.exit_code}</Badge>
                      {rec.error && (
                        <span className="block max-w-md text-2xs text-[var(--fg-muted)]">
                          {rec.error}
                        </span>
                      )}
                    </span>
                  )}
                </Cell>
                <Cell className="text-2xs tabular-nums text-[var(--fg-faint)]">
                  {rec.duration_ms}ms
                </Cell>
              </Row>
            ))}
          </Table>
        </Card>
      )}
    </Page>
  );
}
