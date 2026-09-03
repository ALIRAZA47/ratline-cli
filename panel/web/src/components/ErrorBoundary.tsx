import { Component } from 'react';
import type { ErrorInfo, ReactNode } from 'react';

/**
 * A render failure must not produce a blank page.
 *
 * This panel is what somebody reaches for when a server is misbehaving, often at an
 * hour when they are not at their best. A white screen at that moment is
 * indistinguishable from "the server is down", and the actual message is in a
 * developer console they have no reason to open. So a crash says what happened, and
 * says the API is probably fine — because it usually is, and because knowing that
 * changes what they do next.
 */
interface State {
  error: Error | null;
}

export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Left in the console for whoever is debugging; nothing is sent anywhere,
    // because a panel that phones home about its own errors is a panel that has
    // opinions about what leaves the server.
    console.error('the panel interface hit an error', error, info.componentStack);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="mx-auto max-w-lg px-5 py-20">
        <h1 className="text-xl font-semibold">The interface hit an error</h1>
        <p className="mt-2 text-sm text-[var(--fg-muted)]">
          This is a fault in the page, not in the server. The panel's API is almost certainly
          still answering, and everything ratline manages is untouched — nothing here changes
          the server on its own.
        </p>
        <pre className="terminal mt-4 max-h-60">{this.state.error.message}</pre>
        <div className="mt-4 flex gap-2">
          <button className="btn btn-primary" onClick={() => window.location.reload()}>
            Reload
          </button>
          <a className="btn" href="/">
            Back to the overview
          </a>
        </div>
        <p className="hint mt-4">
          If it keeps happening, <code className="mono">ratline-panel doctor</code> over SSH
          checks the installation, and the server's own log is in{' '}
          <code className="mono">journalctl -u ratline-panel</code>.
        </p>
      </div>
    );
  }
}
