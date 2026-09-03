import { NavLink, Outlet, useNavigate } from 'react-router-dom';
import { useEffect, useState } from 'react';
import { useSession } from '../lib/session';
import { Badge } from './ui';

const nav: { to: string; label: string; superOnly?: boolean }[] = [
  { to: '/', label: 'Overview' },
  { to: '/sites', label: 'Sites' },
  { to: '/tenants', label: 'Tenants' },
  { to: '/certs', label: 'Certificates' },
  { to: '/keys', label: 'SSH keys' },
  { to: '/databases', label: 'Databases' },
  { to: '/runtimes', label: 'Runtimes' },
  { to: '/jobs', label: 'Jobs' },
  { to: '/actions', label: 'All commands' },
  { to: '/activity', label: 'Activity' },
  { to: '/team', label: 'Team', superOnly: true },
];

export function Layout() {
  const { me, signOut } = useSession();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);

  // The panel is one page; a route change on a phone should close the drawer
  // rather than leave it covering what was just navigated to.
  useEffect(() => setOpen(false), [location.pathname]);

  if (!me) return null;

  const items = nav.filter((n) => !n.superOnly || me.capabilities.manage_team);

  return (
    <div className="min-h-full">
      <header className="sticky top-0 z-20 flex h-[var(--header-h)] items-center gap-3 border-b border-[var(--border)] bg-[var(--bg-raised)] px-4">
        <button
          className="btn btn-ghost md:hidden"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          aria-label="Menu"
        >
          ☰
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
            className="text-xs text-[var(--fg-muted)] hover:text-[var(--fg)]"
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
        <nav
          className={`${open ? 'block' : 'hidden'} w-[var(--sidebar-w)] shrink-0 md:block`}
          aria-label="Sections"
        >
          <ul className="sticky top-[calc(var(--header-h)+1.5rem)] space-y-0.5">
            {items.map((item) => (
              <li key={item.to}>
                <NavLink
                  to={item.to}
                  end={item.to === '/'}
                  className={({ isActive }) =>
                    `block rounded-md px-2.5 py-1.5 text-sm ${
                      isActive
                        ? 'bg-[var(--bg-active)] font-medium text-[var(--fg)]'
                        : 'text-[var(--fg-muted)] hover:bg-[var(--bg-hover)]'
                    }`
                  }
                >
                  {item.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <main className="min-w-0 flex-1">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

/** The page shell every route uses: a title, a sentence, and optional actions. */
export function Page({
  title,
  lede,
  actions,
  children,
}: {
  title: string;
  lede?: string;
  actions?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-5">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
          {lede && <p className="mt-0.5 max-w-2xl text-sm text-[var(--fg-muted)]">{lede}</p>}
        </div>
        {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
      </header>
      {children}
    </div>
  );
}
