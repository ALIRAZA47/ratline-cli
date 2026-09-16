import { createContext, useContext, type ReactNode } from 'react';
import { useApi, usePoll } from './hooks';
import type { ApiError } from './api';
import type { Overview } from './types';

/**
 * One reading of `/api/overview`, shared by the sidebar and the front page.
 *
 * The endpoint forks a `ratline status` on the server, so it is the most expensive
 * read in the panel. The sidebar's counts and the Overview page want exactly the
 * same answer, and fetching it twice would double that cost and let the two
 * disagree with each other on screen — the sidebar saying five sites while the
 * table below it lists six.
 */

export interface OverviewState {
  data: Overview | null;
  error: ApiError | null;
  loading: boolean;
  reload: () => void;
}

const Ctx = createContext<OverviewState | null>(null);

/**
 * The background rate, for the sidebar's counts on whatever page you are on.
 *
 * This endpoint forks a `ratline status` on the server, so the front page's own
 * cadence is not a reasonable thing to pay on every other page for the sake of a
 * number beside a menu item. The Overview page adds the faster poll it wants on
 * top of this one — same request, same cache, just asked for more often while
 * somebody is actually watching it.
 */
const BACKGROUND_MS = 60000;

export function OverviewProvider({ children }: { children: ReactNode }) {
  const state = useApi<Overview>('/api/overview');
  usePoll(state.reload, BACKGROUND_MS);
  return <Ctx.Provider value={state}>{children}</Ctx.Provider>;
}

export function useOverview(): OverviewState {
  const value = useContext(Ctx);
  if (!value) {
    throw new Error('useOverview must be used inside the signed-in layout');
  }
  return value;
}

/** The fields of `ratline status --json` this interface reads. */
export interface Status {
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

export interface SiteRow {
  domain: string;
  owner: string;
  runtime: string;
  state: string;
  detail?: string;
  tls: string;
  health?: string;
  needs_attention: boolean;
}

export interface CertRow {
  name: string;
  status: string;
  days_remaining: number;
}

export function statusOf(data: Overview | null): Status | undefined {
  return data?.status as Status | undefined;
}

/**
 * Everything the sidebar puts beside a destination.
 *
 * `undefined` means "not known", which is different from nought and is rendered as
 * nothing at all: `ratline status` does not count databases or runtimes, and a
 * confident 0 beside Databases on a server with two of them is worse than silence.
 */
export interface NavCounts {
  sites?: number;
  tenants?: number;
  certificates?: number;
  keys?: number;
  /** Problems ratline reports about the server — the one that earns a warning tone. */
  problems?: number;
  /** Certificates near expiry, which is the only warning tone on a resource. */
  certsExpiring?: number;
  /** Jobs neither done nor failed, shown live rather than as a total. */
  running?: number;
}

export function countsFrom(data: Overview | null): NavCounts {
  const status = statusOf(data);
  const running = (data?.jobs ?? []).filter(
    (j) => j.state !== 'done' && j.state !== 'failed',
  ).length;
  if (!status) return { running: running || undefined };
  return {
    sites: status.sites,
    tenants: status.users,
    certificates: status.certificates,
    keys: status.keys,
    problems: status.problems || undefined,
    certsExpiring: status.certificates_detail?.length || undefined,
    running: running || undefined,
  };
}
