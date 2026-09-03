import { useState } from 'react';
import { Navigate, useNavigate, useSearchParams } from 'react-router-dom';
import { ApiError, api } from '../lib/api';
import { useSession } from '../lib/session';
import { ErrorBox, Field } from '../components/ui';

interface SessionReply {
  csrf: string;
}

/** The frame the three unauthenticated pages share. */
function Gate({ title, lede, children }: { title: string; lede: string; children: React.ReactNode }) {
  return (
    <div className="mx-auto flex min-h-full max-w-md flex-col justify-center px-5 py-16">
      <div className="mb-6">
        <div className="flex items-baseline gap-2">
          <span className="text-lg font-semibold tracking-tight">ratline</span>
          <span className="text-2xs uppercase tracking-widest text-[var(--fg-faint)]">panel</span>
        </div>
        <h1 className="mt-4 text-xl font-semibold">{title}</h1>
        <p className="mt-1 text-sm text-[var(--fg-muted)]">{lede}</p>
      </div>
      <div className="card p-5">{children}</div>
    </div>
  );
}

export function SignIn() {
  const { me, bootstrap, signIn } = useSession();
  const navigate = useNavigate();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  if (me) return <Navigate to="/" replace />;
  if (bootstrap?.needs_setup) return <Navigate to="/setup" replace />;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const reply = await api.anonymous.post<SessionReply>('/api/auth/login', {
        email,
        password,
        code,
      });
      // The reply carries the account, but the shell also needs the capabilities
      // and the versions that only /api/me returns — so the session is loaded in
      // full before navigating, rather than painting once with half of it.
      await signIn(reply.csrf);
      navigate('/', { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err : null);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Gate title="Sign in" lede="This panel administers a server. Treat it like a root shell.">
      <form className="space-y-3.5" onSubmit={submit}>
        <Field label="Email">
          <input
            className="field"
            type="email"
            autoComplete="username"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
            autoFocus
          />
        </Field>
        <Field label="Password">
          <input
            className="field"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </Field>
        <Field
          label="Six-digit code"
          hint="Leave empty if this account has no second factor."
        >
          <input
            className="field field-mono"
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={6}
            value={code}
            onChange={(e) => setCode(e.target.value)}
          />
        </Field>
        <ErrorBox error={error} title="Could not sign in" />
        <button className="btn btn-primary w-full justify-center" disabled={busy}>
          {busy ? 'Checking…' : 'Sign in'}
        </button>
      </form>
    </Gate>
  );
}

/**
 * The first-run page.
 *
 * Most people never see it: `ratline-panel install` creates the first super admin, so
 * a panel that is answering already has one. It is here for the paths where the
 * database is genuinely empty — an install run with --no-admin, an uninstall --purge,
 * or the last account deleted by hand.
 *
 * The window is the empty accounts table and it closes the moment somebody uses it.
 * A panel left in this state on a public port belongs to whoever finds it, which is
 * why the page says so.
 */
export function Setup() {
  const { me, bootstrap, signIn } = useSession();
  const navigate = useNavigate();
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [again, setAgain] = useState('');
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  if (me) return <Navigate to="/" replace />;
  if (bootstrap && !bootstrap.needs_setup) return <Navigate to="/login" replace />;

  const mismatch = again !== '' && again !== password;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const reply = await api.anonymous.post<SessionReply>('/api/auth/setup', {
        email,
        name,
        password,
      });
      await signIn(reply.csrf);
      navigate('/', { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err : null);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Gate
      title="Claim this panel"
      lede="This panel has no account yet, so the first one created here is its super admin — and this page stops working the moment it exists. If you did not expect to see this, somebody may be about to claim your server: check with ratline-panel account list over SSH."
    >
      <form className="space-y-3.5" onSubmit={submit}>
        <Field label="Email">
          <input
            className="field"
            type="email"
            autoComplete="username"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
            autoFocus
          />
        </Field>
        <Field label="Name" hint="Shown in the activity log beside what you do.">
          <input
            className="field"
            autoComplete="name"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field
          label="Password"
          hint="At least 12 characters. Length beats punctuation — four unrelated words are stronger than P@ssw0rd!"
        >
          <input
            className="field"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </Field>
        <Field label="Password again">
          <input
            className="field"
            type="password"
            autoComplete="new-password"
            value={again}
            onChange={(e) => setAgain(e.target.value)}
            required
          />
        </Field>
        {mismatch && <p className="text-xs text-[var(--danger)]">The two do not match.</p>}
        <ErrorBox error={error} title="Could not set the panel up" />
        <button className="btn btn-primary w-full justify-center" disabled={busy || mismatch}>
          {busy ? 'Creating…' : 'Create the super admin'}
        </button>
      </form>
    </Gate>
  );
}

/** Accepting an invitation. The role comes from the invitation, never from here. */
export function Accept() {
  const [params] = useSearchParams();
  const token = params.get('token') ?? '';
  const { me, signIn } = useSession();
  const navigate = useNavigate();
  const [invite, setInvite] = useState<{ email: string; role: string } | null>(null);
  const [lookupError, setLookupError] = useState<ApiError | null>(null);
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);
  const [looked, setLooked] = useState(false);

  if (!looked && token) {
    setLooked(true);
    api.anonymous
      .get<{ email: string; role: string }>(`/api/auth/invite?token=${encodeURIComponent(token)}`)
      .then(setInvite)
      .catch((err: unknown) => setLookupError(err instanceof ApiError ? err : null));
  }

  if (me) return <Navigate to="/" replace />;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const reply = await api.anonymous.post<SessionReply>('/api/auth/accept', {
        token,
        name,
        password,
      });
      await signIn(reply.csrf);
      navigate('/', { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err : null);
    } finally {
      setBusy(false);
    }
  }

  if (!token) {
    return (
      <Gate title="No invitation" lede="This link is missing its token.">
        <p className="text-sm text-[var(--fg-muted)]">
          Ask a super admin for a new one — invitations are single use and expire.
        </p>
      </Gate>
    );
  }

  return (
    <Gate
      title="Accept the invitation"
      lede={
        invite
          ? `You were invited as ${invite.role === 'superadmin' ? 'a super admin' : 'an admin'}.`
          : 'Checking the link…'
      }
    >
      <ErrorBox error={lookupError} title="That invitation is not valid" />
      {invite && (
        <form className="space-y-3.5" onSubmit={submit}>
          <Field label="Email">
            <input className="field" value={invite.email} disabled />
          </Field>
          <Field label="Name">
            <input
              className="field"
              autoComplete="name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoFocus
            />
          </Field>
          <Field label="Password" hint="At least 12 characters.">
            <input
              className="field"
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
            />
          </Field>
          <ErrorBox error={error} title="Could not accept it" />
          <button className="btn btn-primary w-full justify-center" disabled={busy}>
            {busy ? 'Creating…' : 'Create my account'}
          </button>
        </form>
      )}
    </Gate>
  );
}
