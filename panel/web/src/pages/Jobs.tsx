import { useEffect, useRef, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Page } from '../components/Layout';
import { useApi } from '../lib/hooks';
import type { Job } from '../lib/types';
import { Argv, Badge, Card, Cell, Empty, ErrorBox, Row, Spinner, Table, When, stateTone } from '../components/ui';

export function Jobs() {
  const { data, error, loading, reload } = useApi<Job[]>('/api/jobs');
  return (
    <Page
      title="Jobs"
      lede="Deploys, issuances and installs run here rather than inside a request, so closing the tab does not stop them."
      actions={
        <button className="btn" onClick={reload}>
          Refresh
        </button>
      }
    >
      <ErrorBox error={error} />
      {loading && !data ? (
        <Spinner />
      ) : !data || data.length === 0 ? (
        <Card>
          <Empty>Nothing has run yet.</Empty>
        </Card>
      ) : (
        <Card>
          <Table head={['Action', 'Target', 'State', 'Who', 'When']}>
            {data.map((job) => (
              <Row key={job.id}>
                <Cell>
                  <Link className="mono text-xs font-medium hover:underline" to={`/jobs/${job.id}`}>
                    {job.action}
                  </Link>
                  {job.dry_run && <Badge tone="warn">dry run</Badge>}
                </Cell>
                <Cell className="mono text-2xs">{job.target}</Cell>
                <Cell>
                  <Badge tone={stateTone(job.state)}>
                    {job.state}
                    {job.state === 'failed' && job.exit_code ? ` · exit ${job.exit_code}` : ''}
                  </Badge>
                </Cell>
                <Cell className="text-2xs text-[var(--fg-muted)]">{job.actor}</Cell>
                <Cell className="text-2xs text-[var(--fg-faint)]">
                  <When at={job.finished_at || job.started_at || job.queued_at} />
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
 * One job, with its transcript arriving live.
 *
 * The stream is server-sent events, which is exactly the shape of the problem: text
 * in one direction, no framing, no protocol upgrade. It is opened only while the job
 * is unfinished — a finished job's transcript is in the row, and holding a connection
 * open to be told nothing would be a connection per open tab for no reason.
 */
export function JobDetail() {
  const { id = '' } = useParams();
  const { data, error, loading, reload } = useApi<Job>(`/api/jobs/${id}`);
  const [live, setLive] = useState('');
  const [liveState, setLiveState] = useState<string | null>(null);
  const pane = useRef<HTMLPreElement>(null);
  const pinned = useRef(true);

  const finished = data?.state === 'done' || data?.state === 'failed';

  useEffect(() => {
    if (!data || finished) return;
    const source = new EventSource(`/api/jobs/${id}/stream`);
    source.addEventListener('log', (e) => {
      setLive((prev) => prev + (e as MessageEvent<string>).data + '\n');
    });
    source.addEventListener('state', (e) => {
      const next = (e as MessageEvent<string>).data;
      setLiveState(next);
      if (next === 'done' || next === 'failed') {
        source.close();
        // The row carries the exit code, the error and the hint, which the stream
        // does not — so the page is refreshed once the job ends rather than left
        // showing a transcript with no verdict.
        reload();
      }
    });
    source.onerror = () => source.close();
    return () => source.close();
  }, [id, data, finished, reload]);

  // Follow the tail, but stop following the moment somebody scrolls up: yanking
  // the view back to the bottom while a person is reading an error is the most
  // annoying thing a log viewer can do.
  useEffect(() => {
    const el = pane.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [live, data?.output]);

  if (loading && !data) return <Spinner />;
  if (error) return <ErrorBox error={error} />;
  if (!data) return null;

  const state = liveState ?? data.state;
  const transcript = finished ? (data.output ?? '') : live || 'Waiting for output…';

  return (
    <Page
      title={data.action}
      lede={data.target ? `Target: ${data.target}` : undefined}
      actions={
        <Link className="btn" to="/jobs">
          All jobs
        </Link>
      }
    >
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={stateTone(state)}>{state}</Badge>
        {data.dry_run && <Badge tone="warn">dry run — nothing was written</Badge>}
        {data.actor && <span className="text-xs text-[var(--fg-muted)]">asked for by {data.actor}</span>}
        <span className="text-xs text-[var(--fg-faint)]">
          queued <When at={data.queued_at} />
        </span>
      </div>

      <Argv argv={data.argv} />

      {data.error && (
        <div className="rounded-[var(--radius-card)] border border-[var(--danger)]/30 bg-[var(--danger-soft)] px-3.5 py-3 text-sm">
          <div className="flex items-center gap-2">
            <span className="font-semibold text-[var(--danger)]">It failed</span>
            {data.exit_code > 0 && <Badge tone="danger">exit {data.exit_code}</Badge>}
          </div>
          <p className="mt-1.5">{data.error}</p>
          {data.hint && <p className="mt-1.5 text-xs text-[var(--fg-muted)]">→ {data.hint}</p>}
        </div>
      )}

      <pre
        ref={pane}
        className="terminal max-h-[65vh]"
        onScroll={(e) => {
          const el = e.currentTarget;
          pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
        }}
      >
        {transcript.trimEnd()}
      </pre>
    </Page>
  );
}
