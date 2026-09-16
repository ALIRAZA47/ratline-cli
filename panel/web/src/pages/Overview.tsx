import { Link } from 'react-router-dom';
import { Page } from '../components/Layout';
import { usePoll } from '../lib/hooks';
import { statusOf, useOverview, type Status } from '../lib/overview';
import type { Job } from '../lib/types';
import { WarningIcon } from '../components/icons';
import { Badge, Card, Cell, Empty, ErrorBox, Row, Spinner, Table, When, stateTone } from '../components/ui';

export function Overview() {
  const { data, error, loading, reload } = useOverview();
  // The front page is a dashboard somebody is looking at, so it asks more often
  // than the sidebar's background refresh does.
  usePoll(reload, 15000);
  const status = statusOf(data);

  return (
    <Page
      title={status?.hostname ?? 'Server'}
      lede={
        status?.uptime
          ? `${status.sites} ${status.sites === 1 ? 'site' : 'sites'} · up ${status.uptime}`
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

      {/* The sites are the page; what is running and what just happened are
          the margin notes beside them. They used to sit underneath, which put
          the one live thing on the server below the fold on a laptop. */}
      <div className="flex flex-col gap-5 lg:flex-row">
        <div className="min-w-0 flex-1 space-y-5">
        {status && (
          <>
            <Attention status={status} />

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

        </div>

        <aside className="w-full shrink-0 space-y-4 lg:w-80">
        <Card
          title="Running now"
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
          title="Recent"
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
        </aside>
      </div>
    </Page>
  );
}

/**
 * One band naming what a human has to decide about, instead of six cards counting
 * things nobody was asked to count.
 *
 * The counts moved to the sidebar, where a number is navigation weight. What is
 * left here is the only question the front page should answer first: is there
 * anything wrong, and with what. It renders nothing at all when the answer is no —
 * a reassurance panel that is always on screen is not read after the first week.
 *
 * Every line comes from ratline's own reading of the server. The panel does not
 * decide what counts as a problem; `ratline status` does, and this repeats it.
 */
function Attention({ status }: { status: Status }) {
  const items: { key: string; subject?: string; text: string; to?: string }[] = [];

  for (const cert of status.certificates_detail ?? []) {
    items.push({
      key: `cert:${cert.name}`,
      subject: cert.name,
      text:
        cert.days_remaining <= 0
          ? 'the certificate has expired'
          : `the certificate expires in ${cert.days_remaining} ${
              cert.days_remaining === 1 ? 'day' : 'days'
            }`,
      to: '/certs',
    });
  }

  for (const site of status.sites_detail ?? []) {
    if (!site.needs_attention) continue;
    items.push({
      key: `site:${site.domain}`,
      subject: site.domain,
      text: site.detail || site.state,
      to: `/sites/${encodeURIComponent(site.domain)}`,
    });
  }

  // Anything ratline raised that is not already on the list.
  //
  // `warnings` is ratline's own prose and usually restates what the structured
  // rows above already say — "www.example.com renews in 12 days and its last
  // attempt failed" beside a certificate row for the same domain. Saying it twice
  // makes the band look longer than the problem is, so a warning naming a subject
  // that is already listed is dropped in favour of the row, which links to it.
  const named = new Set(items.map((i) => i.subject).filter(Boolean) as string[]);
  for (const warning of status.warnings ?? []) {
    if ([...named].some((subject) => warning.includes(subject))) continue;
    items.push({ key: `warn:${warning}`, text: warning });
  }

  if (items.length === 0) return null;

  return (
    <section
      className="rounded-[var(--radius-card)] border border-[var(--warn)]/30 bg-[var(--warn-soft)] px-3.5 py-3"
      aria-labelledby="attention-heading"
    >
      <div className="flex items-center gap-2">
        <span className="text-[var(--warn)]">
          <WarningIcon />
        </span>
        <h2 id="attention-heading" className="text-sm font-semibold">
          {items.length === 1 ? 'One thing needs attention' : `${items.length} things need attention`}
        </h2>
      </div>
      <ul className="mt-2 space-y-2 text-sm">
        {items.map((item) => (
          <li key={item.key} className="leading-snug">
            {item.subject &&
              (item.to ? (
                <Link className="mono text-xs font-medium hover:underline" to={item.to}>
                  {item.subject}
                </Link>
              ) : (
                <span className="mono text-xs font-medium">{item.subject}</span>
              ))}
            <span className={item.subject ? 'text-[var(--fg-muted)]' : ''}>
              {item.subject ? ' — ' : ''}
              {item.text}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

export function JobLine({ job }: { job: Job }) {
  const running = job.state !== 'done' && job.state !== 'failed';
  return (
    <li className="space-y-1">
      <div className="flex items-center justify-between gap-3">
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
      </div>
      {running && <div className="running-bar" role="presentation" />}
    </li>
  );
}
