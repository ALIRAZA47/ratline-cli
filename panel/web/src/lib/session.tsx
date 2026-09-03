import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { api, onSignedOut, setCsrf } from './api';
import type { Bootstrap, Me } from './types';

/**
 * Who is signed in, and whether anybody has claimed this panel yet.
 *
 * One context rather than a fetch in every page: a 401 has to sign the whole
 * application out at once, and a component that discovers it independently would
 * leave the rest of the interface rendering stale data behind a modal.
 */
interface SessionValue {
  loading: boolean;
  me: Me | null;
  bootstrap: Bootstrap | null;
  refresh: () => Promise<void>;
  /**
   * Records the session's CSRF token and loads the account.
   *
   * It deliberately does not accept a half-built account from the sign-in reply.
   * Doing that put a Me without `capabilities` into the context for one render,
   * and the shell — which reads capabilities to decide what to draw — crashed
   * before the real one arrived. One shape or none.
   */
  signIn: (csrf: string) => Promise<void>;
  signOut: () => Promise<void>;
}

const SessionContext = createContext<SessionValue | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [loading, setLoading] = useState(true);
  const [me, setMe] = useState<Me | null>(null);
  const [bootstrap, setBootstrap] = useState<Bootstrap | null>(null);

  const refresh = useCallback(async () => {
    try {
      const next = await api.get<Me>('/api/me');
      // The page keeps nothing across a reload, so the token comes back with the
      // account. Without this a refreshed tab is signed in and unable to change
      // anything — every mutation fails the CSRF check with no clue why.
      setCsrf(next.csrf);
      setMe(next);
    } catch {
      setMe(null);
      // Only asked for when there is no session: it is the one thing an
      // unauthenticated browser may know, and asking for it on every load would
      // publish it to anybody who can reach the port.
      try {
        setBootstrap(await api.anonymous.get<Bootstrap>('/api/bootstrap'));
      } catch {
        setBootstrap(null);
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => onSignedOut(() => setMe(null)), []);

  const signIn = useCallback(
    async (csrf: string) => {
      setCsrf(csrf);
      await refresh();
    },
    [refresh],
  );

  const signOut = useCallback(async () => {
    try {
      await api.post('/api/auth/logout');
    } finally {
      setCsrf('');
      setMe(null);
      await refresh();
    }
  }, [refresh]);

  const value = useMemo<SessionValue>(
    () => ({ loading, me, bootstrap, refresh, signIn, signOut }),
    [loading, me, bootstrap, refresh, signIn, signOut],
  );
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionValue {
  const ctx = useContext(SessionContext);
  if (!ctx) throw new Error('useSession outside a SessionProvider');
  return ctx;
}

/** The signed-in account, for the pages that are only reachable with one. */
export function useMe(): Me {
  const { me } = useSession();
  if (!me) throw new Error('useMe on a page that is not behind the session guard');
  return me;
}
