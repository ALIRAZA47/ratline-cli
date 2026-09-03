import { Link } from 'react-router-dom';
import { Page } from '../components/Layout';
import { useApi, usePoll } from '../lib/hooks';
import type { Job, Overview as OverviewData } from '../lib/types';
import { Badge, Card, Cell, Empty, ErrorBox, Row, Spinner, Table, When, stateTone } from '../components/ui';

interface SiteRow {
  domain: string;
  owner: string;
  runtime: string;
  state: string;
  detail?: string;
  tls: string;
  health?: string;
  needs_attention: boolean;
}

interface CertRow {
  name: string;
  status: string;
  days_remaining: number;
}

interface Status {
  hostname?: string;
  version?: string;
  os?: string;
  uptime?: string;
  users: number;
  keys: number;
  sites: number;
  certificates: number;
  jobs: number;
  workers: number;
  problems: number;
  sites_detail?: SiteRow[];
  certificates_detail?: CertRow[];
  warnings?: string[];
}

export function Overview() {
  const { data, error, loading, reload } = useApi<OverviewData>('/api/overview');
  usePoll(reload, 15000);

  const status = data?.status as Status | undefined;

  return (
    <Page
      title="Overview"
      lede={
        status?.hostname
          ? `${status.hostname} — everything ratline knows about this server, on one screen.`
          : 'Everything ratline knows about this server, on one screen.'
      }
      actions={
        <button className="btn" onClick={reload}>
          Refresh
        </button>
      }
    >
      <ErrorBox error={error} title="Could not read the server's state" />
      {data?.warning && (
        <div className="rounded-[var(--radius-card)] border border-[var(--warn)]/30 bg-[var(--warn-soft)] px-3.5 py-3 text-sm">
          <strong className="font-semibold">ratline could not be reached.</strong> The panel's own
          history below is still accurate. {data.warning}
        </div>
      )}
      {loading && !data && <Spinner />}

      {status && (
        <>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
            <Stat label="Tenants" value={status.users} to="/tenants" />
            <Stat label="Sites" value={status.sites} to="/sites" />
            <Stat label="Certificates" value={status.certificates} to="/certs" />
            <Stat label="SSH keys" value={status.keys} to="/keys" />
            <Stat label="Jobs & workers" value={status.jobs + status.workers} to="/sites" />
            <Stat
              label="Problems"
              value={status.problems}
              tone={status.problems > 0 ? 'danger' : 'ok'}
            />
          </div>

          {status.warnings && status.warnings.length > 0 && (
            <Card title="What ratline wants you to look at">
              <ul className="space-y-1.5 text-sm">
                {status.warnings.map((warning) => (
                  <li key={warning} className="flex gap-2">
                    <span className="text-[var(--warn)]">•</span>
                    <span>{warning}</span>
                  </li>
                ))}
              </ul>
            </Card>
          )}

          <Card
            title="Sites"
            action={
              <Link className="btn btn-ghost text-xs" to="/sites">
                All sites
              </Link>
            }
          >
            {!status.sites_detail || status.sites_detail.length === 0 ? (
              <Empty>No sites yet. Create one from Sites → New site.</Empty>
            ) : (
              <Table head={['Domain', 'Owner', 'Runtime', 'State', 'TLS']}>
                {status.sites_detail.map((site) => (
                  <Row key={site.domain}>
                    <Cell>
                      <Link className="font-medium hover:underline" to={`/sites/${site.domain}`}>
                        {site.domain}
                      </Link>
                      {site.detail && (
                        <div className="text-2xs text-[var(--fg-faint)]">{site.detail}</div>
                      )}
                    </Cell>
                    <Cell className="text-[var(--fg-muted)]">{site.owner}</Cell>
                    <Cell>
                      <Badge>{site.runtime}</Badge>
                    </Cell>
                    <Cell>
                      <Badge tone={site.needs_attention ? 'danger' : stateTone(site.state)}>
                        {site.state}
                      </Badge>
                    </Cell>
                    <Cell className="text-[var(--fg-muted)]">{site.tls}</Cell>
                  </Row>
                ))}
              </Table>
            )}
          </Card>

          {status.certificates_detail && status.certificates_detail.length > 0 && (
            <Card
              title="Certificates near expiry"
              action={
                <Link className="btn btn-ghost text-xs" to="/certs">
                  All certificates
                </Link>
              }
            >
              <Table head={['Name', 'Status', 'Days left']}>
                {status.certificates_detail.map((cert) => (
                  <Row key={cert.name}>
                    <Cell className="mono text-xs">{cert.name}</Cell>
                    <Cell>
                      <Badge tone={cert.days_remaining < 14 ? 'danger' : 'warn'}>
                        {cert.status}
                      </Badge>
                    </Cell>
                    <Cell>{cert.days_remaining}</Cell>
                  </Row>
                ))}
              </Table>
            </Card>
          )}
        </>
      )}

      <div className="grid gap-4 lg:grid-cols-2">
        <Card
          title="Running and recent jobs"
          action={
            <Link className="btn btn-ghost text-xs" to="/jobs">
              All jobs
            </Link>
          }
        >
          {!data?.jobs || data.jobs.length === 0 ? (
            <Empty>Nothing has run yet.</Empty>
          ) : (
            <ul className="space-y-2">
              {data.jobs.map((job) => (
                <JobLine key={job.id} job={job} />
              ))}
            </ul>
          )}
        </Card>

        <Card
          title="Recent activity"
          action={
            <Link className="btn btn-ghost text-xs" to="/activity">
              Full log
            </Link>
          }
        >
          {!data?.recent || data.recent.length === 0 ? (
            <Empty>Nothing yet.</Empty>
          ) : (
            <ul className="space-y-1.5 text-sm">
              {data.recent.map((rec) => (
                <li key={rec.id} className="flex items-baseline justify-between gap-3">
                  <span className="min-w-0">
                    <span className="mono text-xs">{rec.action}</span>
                    {rec.target && (
                      <span className="ml-1.5 text-xs text-[var(--fg-muted)]">{rec.target}</span>
                    )}
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
      </div>
    </Page>
  );
}

function Stat({
  label,
  value,
  to,
  tone,
}: {
  label: string;
  value: number;
  to?: string;
  tone?: 'ok' | 'danger';
}) {
  const inner = (
    <div className="card px-3 py-2.5">
      <div className="text-2xs uppercase tracking-wide text-[var(--fg-faint)]">{label}</div>
      <div
        className={`mt-0.5 text-xl font-semibold tabular-nums ${
          tone === 'danger' ? 'text-[var(--danger)]' : ''
        }`}
      >
        {value}
      </div>
    </div>
  );
  return to ? (
    <Link to={to} className="block hover:opacity-80">
      {inner}
    </Link>
  ) : (
    inner
  );
}

export function JobLine({ job }: { job: Job }) {
  return (
    <li className="flex items-center justify-between gap-3">
      <Link to={`/jobs/${job.id}`} className="min-w-0 hover:underline">
        <span className="mono text-xs">{job.action}</span>
        {job.target && <span className="ml-1.5 text-xs text-[var(--fg-muted)]">{job.target}</span>}
      </Link>
      <span className="flex shrink-0 items-center gap-2">
        <Badge tone={stateTone(job.state)}>{job.state}</Badge>
        <span className="text-2xs text-[var(--fg-faint)]">
          <When at={job.finished_at || job.started_at || job.queued_at} />
        </span>
      </span>
    </li>
  );
}
