import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError, api } from './api';

/**
 * A fetch with the four states every page in this application needs: loading, the
 * data, an error worth showing, and a way to ask again.
 *
 * The abort matters. Several of these endpoints fork a ratline process, so a page
 * left half-loaded when somebody navigates away is a process still running and a
 * setState on a component that is gone.
 */
export function useApi<T>(path: string | null, deps: unknown[] = []) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [loading, setLoading] = useState(path !== null);
  const [nonce, setNonce] = useState(0);

  const reload = useCallback(() => setNonce((n) => n + 1), []);

  useEffect(() => {
    if (path === null) {
      setLoading(false);
      return;
    }
    const controller = new AbortController();
    setLoading(true);
    api
      .get<T>(path, controller.signal)
      .then((next) => {
        setData(next);
        setError(null);
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return;
        setError(err instanceof ApiError ? err : null);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, nonce, ...deps]);

  return { data, error, loading, reload };
}

/** Re-runs an effect on an interval, pausing while the tab is hidden. */
export function usePoll(fn: () => void, ms: number, active = true) {
  const saved = useRef(fn);
  saved.current = fn;
  useEffect(() => {
    if (!active) return;
    const id = window.setInterval(() => {
      // A background tab polling a dashboard forks a ratline process every few
      // seconds for nobody to look at.
      if (document.visibilityState === 'visible') saved.current();
    }, ms);
    return () => window.clearInterval(id);
  }, [ms, active]);
}
