import { Link } from 'react-router-dom';
import { Page } from '../components/Layout';
import { usePoll } from '../lib/hooks';
import {
  attentionItems,
  statusOf,
  useOverview,
  type SiteRow,
  type Status,
} from '../lib/overview';
import type { Job } from '../lib/types';
import { WarningIcon } from '../components/icons';
import { Badge, Card, Empty, ErrorBox, Spinner, When, stateTone } from '../components/ui';

export function Overview() {
  const { data, error, loading, reload } = useOverview();
  // The front page is a dashboard somebody is looking at, so it asks more often
  // than the sidebar's background refresh does.
  usePoll(reload, 15000);
  const status = statusOf(data);

  return (
    <Page
      title={headline(status)}
      lede={secondLine(status)}
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
              <Empty>
                No sites yet.{' '}
                <Link className="underline" to="/sites/new">
                  Put one up
                </Link>
                .
              </Empty>
            ) : (
              <ul>
                {status.sites_detail.map((site) => (
                  <li key={site.domain} className="listrow">
                    <span
                      className="dot"
                      style={{ background: `var(--${toneOf(site)})` }}
                      aria-hidden="true"
                    />
                    <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                      <Link className="font-medium hover:underline" to={`/sites/${site.domain}`}>
                        {site.domain}
                      </Link>
                      {/* What it is doing, in words. The dot repeats it in colour
                          for somebody scanning; neither is alone. */}
                      <span className="text-2xs text-[var(--fg-faint)]">{doingWhat(site)}</span>
                    </span>
                    <span className="shrink-0 text-2xs text-[var(--fg-faint)]">{tlsInWords(site)}</span>
                  </li>
                ))}
              </ul>
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
                <ul>
                {status.certificates_detail.map((cert) => (
                  <li key={cert.name} className="listrow">
                    <span
                      className="dot"
                      style={{ background: cert.days_remaining < 14 ? 'var(--danger)' : 'var(--warn)' }}
                      aria-hidden="true"
                    />
                    <span className="min-w-0 flex-1 font-medium [overflow-wrap:anywhere]">
                      {cert.name}
                    </span>
                    <span className="shrink-0 text-xs text-[var(--fg-muted)]">
                      {cert.days_remaining <= 0
                        ? 'has expired'
                        : `${cert.days_remaining} ${cert.days_remaining === 1 ? 'day' : 'days'} left`}
                    </span>
                  </li>
                ))}
              </ul>
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
 * The certificate, in words rather than ratline's field.
 *
 * `tls` arrives as "letsencrypt, 68 days" or "none" — a value shaped for a
 * terminal. It is the same fact either way; this is the half a person reads.
 */
function tlsInWords(site: SiteRow): string {
  const raw = (site.tls ?? '').trim();
  if (raw === '' || raw === 'none') return 'no certificate';
  const days = raw.match(/(\d+)\s*days?/);
  if (days) return `certificate good for ${days[1]} days`;
  return 'has a certificate';
}

/** Which signal colour a site's state deserves. */
function toneOf(site: SiteRow): 'danger' | 'warn' | 'ok' | 'fg-faint' {
  if (site.needs_attention) return 'danger';
  if (site.state === 'serving' || site.state === 'running' || site.state === 'active') return 'ok';
  if (site.state === 'disabled' || site.state === 'stopped') return 'fg-faint';
  return 'warn';
}

/**
 * What a site is doing, said the way somebody would say it.
 *
 * ratline's own `detail` is already a sentence when there is something wrong
 * ("the unit exited 1 four times in a minute"), so it wins; the rest is assembled
 * from what is known rather than printing `state: active` at a person.
 */
function doingWhat(site: SiteRow): string {
  if (site.detail) return site.detail;
  const runtime = site.runtime ? ` · ${site.runtime}` : '';
  const owner = site.owner ? ` · belongs to ${site.owner}` : '';
  const doing =
    site.state === 'disabled' ? 'Not being served'
    : site.state === 'serving' ? 'Serving files'
    : site.needs_attention ? 'Not answering'
    : 'Running';
  return `${doing}${runtime}${owner}`;
}

/**
 * The state of the server, as a sentence.
 *
 * The front page used to open with the hostname and a count, which is two facts
 * and no answer. What somebody wants on arriving is whether anything is wrong —
 * so that is the heading, and the hostname moves to the bar at the top where a
 * label belongs.
 */
function headline(status?: Status): string {
  if (!status) return 'Reading the server';
  const n = status.sites;
  const sites = `${spell(n)} ${n === 1 ? 'site' : 'sites'}`;
  const stopped = (status.sites_detail ?? []).filter((s) => s.needs_attention).length;
  if (n === 0) return 'No sites yet.';
  if (stopped === 0) return `${capital(sites)}, all serving.`;
  if (stopped === n) return `${capital(sites)}, and ${n === 1 ? 'it is' : 'none are'} serving.`;
  return `${capital(sites)}, ${spell(stopped)} of them in trouble.`;
}

/**
 * Uptime, and how much wants looking at.
 *
 * The count comes from the same function the band below renders, so the sentence
 * and the list can never disagree — which they did when this counted the raw
 * fields and the band deduplicated them.
 */
function secondLine(status?: Status): string {
  if (!status) return 'Asking ratline what is on this server.';
  const parts: string[] = [];
  if (status.uptime) parts.push(`Up ${status.uptime}.`);
  const total = attentionItems(status).length;
  if (total === 0) parts.push('Nothing wants looking at.');
  else if (total === 1) parts.push('One thing wants looking at.');
  else parts.push(`${capital(spell(total))} things want looking at.`);
  return parts.join(' ');
}

/** Small numbers read better as words in a sentence; past twelve they do not. */
function spell(n: number): string {
  const words = ['no', 'one', 'two', 'three', 'four', 'five', 'six',
    'seven', 'eight', 'nine', 'ten', 'eleven', 'twelve'];
  // Indexed access is checked in this project, and a count is not guaranteed to
  // be in range — a negative or absent one falls through to the digits.
  return (n >= 0 && n <= 12 ? words[n] : undefined) ?? String(n);
}

function capital(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
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
  const items = attentionItems(status);
  if (items.length === 0) return null;

  return (
    <section className="banner banner-warn" aria-labelledby="attention-heading">
      <span className="text-[var(--warn)]">
        <WarningIcon />
      </span>
      <div className="min-w-0">
        <h2 id="attention-heading" className="text-sm font-semibold">
          {items.length === 1 ? 'One thing wants looking at' : `${items.length} things want looking at`}
        </h2>
        <ul className="mt-2 space-y-2 text-sm">
          {items.map((item) => (
            <li key={item.id} className="leading-snug">
              {item.subject &&
                (item.to ? (
                  <Link className="font-medium hover:underline" to={item.to}>
                    {item.subject}
                  </Link>
                ) : (
                  <span className="font-medium">{item.subject}</span>
                ))}
              <span className={item.subject ? 'text-[var(--warn-ink)]' : ''}>
                {item.subject ? ' — ' : ''}
                {item.text}
              </span>
            </li>
          ))}
        </ul>
      </div>
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
