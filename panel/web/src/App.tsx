import { Link, Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { Layout } from './components/Layout';
import { useSession } from './lib/session';
import { Accept, SignIn, Setup } from './pages/SignIn';
import { Overview } from './pages/Overview';
import { SiteDetail, SiteLogs, Sites } from './pages/Sites';
import { Certificates, Databases, Keys, Runtimes, TenantDetail, Tenants } from './pages/Resources';
import { JobDetail, Jobs } from './pages/Jobs';
import { Activity } from './pages/Activity';
import { Team } from './pages/Team';
import { AccountPage } from './pages/Account';
import { ActionPage, Actions } from './pages/Actions';
import { Spinner } from './components/ui';

/**
 * The guard.
 *
 * One place decides whether a route is reachable, and it does so from the session
 * the server confirmed rather than from anything the browser is holding. The server
 * checks every request anyway — this only decides what to draw, and it says so, so
 * nobody is tempted to treat it as the security boundary.
 */
function RequireSession({ children }: { children: React.ReactNode }) {
  const { loading, me } = useSession();
  const location = useLocation();
  if (loading) return <Spinner label="Checking your session" />;
  if (!me) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  return <>{children}</>;
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<SignIn />} />
      <Route path="/setup" element={<Setup />} />
      <Route path="/accept" element={<Accept />} />

      <Route
        element={
          <RequireSession>
            <Layout />
          </RequireSession>
        }
      >
        <Route index element={<Overview />} />
        <Route path="sites" element={<Sites />} />
        <Route path="sites/:domain" element={<SiteDetail />} />
        <Route path="sites/:domain/logs" element={<SiteLogs />} />
        <Route path="tenants" element={<Tenants />} />
        <Route path="tenants/:name" element={<TenantDetail />} />
        <Route path="certs" element={<Certificates />} />
        <Route path="keys" element={<Keys />} />
        <Route path="databases" element={<Databases />} />
        <Route path="runtimes" element={<Runtimes />} />
        <Route path="jobs" element={<Jobs />} />
        <Route path="jobs/:id" element={<JobDetail />} />
        <Route path="actions" element={<Actions />} />
        <Route path="actions/:id" element={<ActionPage />} />
        <Route path="activity" element={<Activity />} />
        <Route path="team" element={<Team />} />
        <Route path="account" element={<AccountPage />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}

function NotFound() {
  return (
    <div className="flex min-h-[60vh] flex-col items-center justify-center gap-3 text-center">
      <span className="mono text-2xs uppercase tracking-widest text-[var(--fg-faint)]">404</span>
      <h1 className="text-xl font-semibold tracking-tight">No such page</h1>
      <p className="max-w-[46ch] text-sm text-[var(--fg-muted)]">
        The link may be from an older version of the panel, or the thing it pointed at has been
        deleted. Nothing was changed.
      </p>
      <div className="mt-1 flex flex-wrap justify-center gap-2">
        <Link className="btn btn-primary" to="/">
          Back to Server
        </Link>
        <Link className="btn" to="/actions">
          All commands
        </Link>
      </div>
    </div>
  );
}
