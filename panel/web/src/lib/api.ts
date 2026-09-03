/**
 * The one way this application talks to the panel.
 *
 * Everything goes through here so that three things are true in exactly one place:
 * the CSRF token is attached to every state-changing request, a 401 always means
 * "you are signed out" rather than "this page is broken", and a ratline error keeps
 * its code, its message and its hint all the way to the component that renders it.
 */

/** The panel's envelope, shaped like ratline's. */
export interface Envelope<T> {
  ok: boolean;
  data?: T;
  error?: ApiErrorPayload;
}

export interface ApiErrorPayload {
  code: number;
  name: string;
  message: string;
  hint?: string;
  fields?: Record<string, string>;
}

/**
 * ApiError carries the whole payload rather than only a message.
 *
 * The hint is the useful half of a ratline failure — "check that DNS points here",
 * "another invocation holds the lock" — and flattening an error to its message is
 * how an interface ends up showing a wall of red text that tells nobody what to do.
 */
export class ApiError extends Error {
  readonly code: number;
  readonly name_: string;
  readonly hint?: string;
  readonly fields?: Record<string, string>;
  readonly status: number;

  constructor(payload: ApiErrorPayload, status: number) {
    super(payload.message);
    this.name = 'ApiError';
    this.code = payload.code;
    this.name_ = payload.name;
    this.hint = payload.hint;
    this.fields = payload.fields;
    this.status = status;
  }
}

/**
 * The CSRF token, held in memory only.
 *
 * Not in localStorage and not in a readable cookie: both are legible to any script
 * that gets into the page, which is precisely the attacker the token exists to stop.
 * It lives for as long as the tab does, and a reload fetches a new one with /api/me.
 */
let csrf = '';

export function setCsrf(token: string) {
  csrf = token;
}

/** Listeners told when the session ends, so the shell can send us to sign in. */
type Listener = () => void;
const signedOutListeners = new Set<Listener>();

export function onSignedOut(fn: Listener): () => void {
  signedOutListeners.add(fn);
  return () => signedOutListeners.delete(fn);
}

interface RequestOptions {
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
  signal?: AbortSignal;
  /** Suppresses the signed-out broadcast, for the calls made while signing in. */
  anonymous?: boolean;
}

export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const method = opts.method ?? 'GET';
  const headers: Record<string, string> = { ...opts.headers };
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
  if (method !== 'GET' && csrf) headers['X-Ratline-CSRF'] = csrf;

  const res = await fetch(path, {
    method,
    headers,
    // The session is a cookie, so it has to be sent. 'same-origin' rather than
    // 'include': the panel is only ever its own origin, and 'include' would send
    // it to anything this code was ever pointed at by mistake.
    credentials: 'same-origin',
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
    signal: opts.signal,
  });

  let envelope: Envelope<T> | undefined;
  const text = await res.text();
  if (text) {
    try {
      envelope = JSON.parse(text) as Envelope<T>;
    } catch {
      // A response that is not the envelope means something in front of the panel
      // answered — an nginx error page, a captive portal, a proxy. Saying so is
      // more useful than "unexpected token < in JSON".
      throw new ApiError(
        {
          code: res.status,
          name: 'unreadable_response',
          message: `the server answered with ${res.status} and something that is not the panel's API`,
          hint: 'check whatever is in front of the panel — nginx, a proxy, a tunnel',
        },
        res.status,
      );
    }
  }

  if (res.status === 401 && !opts.anonymous) {
    signedOutListeners.forEach((fn) => fn());
  }

  if (!res.ok || envelope?.ok === false) {
    throw new ApiError(
      envelope?.error ?? {
        code: res.status,
        name: 'error',
        message: `the request failed with status ${res.status}`,
      },
      res.status,
    );
  }
  return (envelope?.data ?? (undefined as T)) as T;
}

export const api = {
  get: <T>(path: string, signal?: AbortSignal) => request<T>(path, { signal }),
  post: <T>(path: string, body?: unknown, headers?: Record<string, string>) =>
    request<T>(path, { method: 'POST', body, headers }),
  del: <T>(path: string, headers?: Record<string, string>) =>
    request<T>(path, { method: 'DELETE', headers }),
  anonymous: {
    get: <T>(path: string) => request<T>(path, { anonymous: true }),
    post: <T>(path: string, body?: unknown) =>
      request<T>(path, { method: 'POST', body, anonymous: true }),
  },
};
