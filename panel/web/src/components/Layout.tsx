import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { useEffect, useState, type ReactNode } from 'react';
import { useSession } from '../lib/session';
import { OverviewProvider, countsFrom, useOverview, type NavCounts } from '../lib/overview';
import { Badge } from './ui';
import { CloseIcon, GlobeIcon, MenuIcon, ServerIcon } from './icons';

/**
 * The sidebar, in three groups rather than eleven destinations of equal weight.
 *
 * Two of those eleven are where the work happens; the rest are things you look up
 * when you already know you need them. A flat list says they are all equally likely,
 * which is why the first version of this panel made you read all eleven every time.
 *
 * `count` names the field in the sidebar's counts that belongs beside a row. A
 * number here is navigation weight — how much is behind this door — not a statistic,
 * which is why the front page no longer spends six cards saying the same thing.
 */
interface NavItem {
  to: string;
  label: string;
  end?: boolean;
  superOnly?: boolean;
  icon?: ReactNode;
  count?: keyof NavCounts;
  /** Renders in the warning tone when the named count is present and non-zero. */
  warnOn?: keyof NavCounts;
  /** A live count — rendered with a dot, in the accent tone. */
  live?: boolean;
}

const groups: { heading?: string; items: NavItem[] }[] = [
  {
    items: [
      { to: '/', label: 'Server', end: true, icon: <ServerIcon />, warnOn: 'problems' },
      { to: '/sites', label: 'Sites', icon: <GlobeIcon />, count: 'sites' },
    ],
  },
  {
    heading: 'Resources',
    items: [
      { to: '/tenants', label: 'Tenants', count: 'tenants' },
      { to: '/databases', label: 'Databases' },
      { to: '/certs', label: 'Certificates', count: 'certificates', warnOn: 'certsExpiring' },
      { to: '/keys', label: 'SSH keys', count: 'keys' },
      { to: '/runtimes', label: 'Runtimes' },
    ],
  },
  {
    heading: 'Operations',
    items: [
      { to: '/jobs', label: 'Jobs', count: 'running', live: true },
      { to: '/activity', label: 'Activity' },
      { to: '/actions', label: 'All commands' },
      { to: '/team', label: 'Team', superOnly: true },
    ],
  },
];

export function Layout() {
  const { me } = useSession();
  if (!me) return null;
  return (
    <OverviewProvider>
      <Shell />
    </OverviewProvider>
  );
}

function Shell() {
  const { me, signOut } = useSession();
  const navigate = useNavigate();
  const location = useLocation();
  const [open, setOpen] = useState(false);
  const { data } = useOverview();

  // The panel is one page; a route change on a phone should close the drawer
  // rather than leave it covering what was just navigated to.
  useEffect(() => setOpen(false), [location.pathname]);

  if (!me) return null;
  const counts = countsFrom(data);

  return (
    <div className="min-h-full">
      <header className="sticky top-0 z-20 flex h-[var(--header-h)] items-center gap-3 border-b border-[var(--border)] bg-[var(--bg-raised)] px-4">
        <button
          className="-ml-2.5 flex size-11 items-center justify-center rounded-md text-[var(--fg)] hover:bg-[var(--bg-hover)] md:hidden"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          aria-label={open ? 'Close the menu' : 'Menu'}
        >
          {open ? <CloseIcon /> : <MenuIcon />}
        </button>
        <NavLink to="/" className="flex items-baseline gap-2">
          <span className="text-base font-semibold tracking-tight">ratline</span>
          <span className="text-2xs uppercase tracking-widest text-[var(--fg-faint)]">panel</span>
        </NavLink>
        {me.panel.ratline_version && (
          <span className="hidden text-2xs text-[var(--fg-faint)] sm:inline">
            driving ratline {me.panel.ratline_version}
          </span>
        )}
        <div className="ml-auto flex items-center gap-2">
          {me.capabilities.needs_totp_now && <Badge tone="warn">second factor required</Badge>}
          <NavLink
            to="/account"
            className="hidden text-xs text-[var(--fg-muted)] hover:text-[var(--fg)] sm:inline"
          >
            {me.account.email}
          </NavLink>
          <Badge tone={me.account.role === 'superadmin' ? 'accent' : 'neutral'}>
            {me.account.role === 'superadmin' ? 'super admin' : 'admin'}
          </Badge>
          <button
            className="btn btn-ghost text-xs"
            onClick={() => {
              void signOut().then(() => navigate('/login'));
            }}
          >
            Sign out
          </button>
        </div>
      </header>

      <div className="mx-auto flex w-full max-w-[86rem] gap-6 px-4 py-6">
        {/* The drawer covers the page on a phone rather than sharing the row with
            it: a sidebar that is a flex sibling squeezes the content into a column
            too narrow to read, which is what this did before it had groups. */}
        {open && (
          <div
            className="fixed inset-0 top-[var(--header-h)] z-20 bg-black/20 md:hidden"
            onClick={() => setOpen(false)}
            aria-hidden="true"
          />
        )}
        <nav
          className={`w-[var(--sidebar-w)] shrink-0 md:static md:block md:overflow-visible md:border-0 md:bg-transparent md:p-0 ${
            open
              ? 'fixed bottom-0 left-0 top-[var(--header-h)] z-30 overflow-y-auto border-r border-[var(--border)] bg-[var(--bg-raised)] p-4'
              : 'hidden md:block'
          }`}
          aria-label="Sections"
        >
          <div className="flex flex-col gap-4 md:sticky md:top-[calc(var(--header-h)+1.5rem)]">
            {groups.map((group, i) => {
              const items = group.items.filter(
                (item) => !item.superOnly || me.capabilities.manage_team,
              );
              if (items.length === 0) return null;
              return (
                <div key={group.heading ?? i} className="flex flex-col gap-0.5">
                  {group.heading && (
                    <div className="px-2.5 pb-1 text-2xs font-semibold uppercase tracking-wide text-[var(--fg-faint)]">
                      {group.heading}
                    </div>
                  )}
                  {items.map((item) => (
                    <NavItemLink key={item.to} item={item} counts={counts} />
                  ))}
                </div>
              );
            })}
          </div>
        </nav>
        <main className="min-w-0 flex-1">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

function NavItemLink({ item, counts }: { item: NavItem; counts: NavCounts }) {
  const warn = item.warnOn ? counts[item.warnOn] : undefined;
  const count = item.count ? counts[item.count] : undefined;

  return (
    <NavLink
      to={item.to}
      end={item.end}
      className={({ isActive }) =>
        `flex min-h-11 items-center gap-2 rounded-md px-2.5 py-1.5 text-sm md:min-h-0 ${
          isActive
            ? 'bg-[var(--bg-active)] font-medium text-[var(--fg)]'
            : 'text-[var(--fg-muted)] hover:bg-[var(--bg-hover)]'
        }`
      }
    >
      {item.icon}
      <span>{item.label}</span>
      {warn ? (
        <span
          className="ml-auto inline-flex items-center rounded-full border border-[var(--warn)]/25 bg-[var(--warn-soft)] px-2 py-[0.1rem] text-2xs font-semibold text-[var(--warn)]"
          title={
            item.warnOn === 'problems'
              ? `${warn} ${warn === 1 ? 'thing needs' : 'things need'} attention`
              : `${warn} ${warn === 1 ? 'certificate is' : 'certificates are'} near expiry`
          }
        >
          {warn}
        </span>
      ) : item.live && count ? (
        <span
          className="ml-auto inline-flex items-center gap-1 text-2xs font-semibold text-[var(--accent)]"
          title={`${count} running or queued`}
        >
          <span className="size-1.5 rounded-full bg-[var(--accent)]" />
          {count}
        </span>
      ) : count !== undefined ? (
        <span className="ml-auto text-2xs tabular-nums text-[var(--fg-faint)]">{count}</span>
      ) : null}
    </NavLink>
  );
}

/** The page shell every route uses: a title, a sentence, and optional actions. */
export function Page({
  title,
  lede,
  actions,
  back,
  children,
}: {
  title: string;
  lede?: string;
  actions?: React.ReactNode;
  /** A link up to the list this page belongs to. */
  back?: { to: string; label: string };
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-5">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          {back && (
            <NavLink to={back.to} className="text-xs text-[var(--fg-faint)] hover:text-[var(--fg)]">
              ← {back.label}
            </NavLink>
          )}
          <h1 className={`text-xl font-semibold tracking-tight ${back ? 'mt-0.5' : ''}`}>{title}</h1>
          {lede && <p className="mt-0.5 max-w-2xl text-sm text-[var(--fg-muted)]">{lede}</p>}
        </div>
        {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
      </header>
      {children}
    </div>
  );
}
