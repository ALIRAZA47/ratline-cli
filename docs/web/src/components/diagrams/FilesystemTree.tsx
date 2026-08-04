interface Row {
  depth: number;
  path: string;
  mode?: string;
  owner?: string;
  note?: string;
  /** Highlight the rows whose permissions carry meaning. */
  emphasis?: 'secret' | 'served' | 'log' | 'none';
}

const perUser: Row[] = [
  { depth: 0, path: '/home/<user>/', mode: '0750', owner: '<user>:<user>', note: 'nginx joins the user’s group' },
  { depth: 1, path: '.ssh/authorized_keys', mode: '0600', owner: '<user>:<user>', note: 'dir 0700' },
  { depth: 1, path: 'logs/' },
  { depth: 1, path: '<domain>/', mode: '0750', owner: '<user>:<user>' },
  { depth: 2, path: 'app/', note: 'application code' },
  { depth: 2, path: 'public/', mode: '0750', note: 'static assets, served directly by nginx', emphasis: 'served' },
  { depth: 2, path: 'venv/', mode: '0750', note: 'python runtime only' },
  { depth: 2, path: 'logs/{app,access,error}.log', mode: '0640', owner: '<user>:adm', emphasis: 'log' },
  { depth: 2, path: 'tmp/', mode: '0700' },
  { depth: 2, path: '.env', mode: '0600', owner: '<user>:<user>', note: 'secrets', emphasis: 'secret' },
  { depth: 2, path: '.ratline/site.yaml', mode: '0640', note: 'rendered manifest, for reconcile' },
];

const system: Row[] = [
  { depth: 0, path: '/etc/ratline/config.yaml' },
  { depth: 0, path: '/etc/nginx/sites-available/<domain>.conf', note: '→ symlink in sites-enabled/' },
  { depth: 0, path: '/etc/nginx/ratline/', note: 'shared snippets' },
  { depth: 0, path: '/etc/nginx/ratline/custom/<domain>.conf', note: 'operator additions, never regenerated' },
  { depth: 0, path: '/etc/systemd/system/ratline-<user>-<domain>.service' },
  { depth: 0, path: '/run/ratline/<user>-<domain>/app.sock', mode: '0660', owner: '<user>:www-data' },
  { depth: 0, path: '/var/lib/ratline/state.db', mode: '0600', owner: 'root:root', note: 'SQLite' },
  { depth: 0, path: '/var/log/ratline/audit.log' },
  { depth: 0, path: '/etc/logrotate.d/ratline-<domain>' },
  { depth: 0, path: '/opt/ratline/runtimes/node/<ver>/' },
  { depth: 0, path: '/opt/ratline/runtimes/python/<ver>/' },
];

const EMPH: Record<NonNullable<Row['emphasis']>, string> = {
  secret: 'text-danger',
  served: 'text-info',
  log: 'text-ok',
  none: '',
};

function Tree({ rows, label }: { rows: Row[]; label: string }) {
  return (
    <div className="scroll-thin overflow-x-auto rounded-[var(--radius-card)] border border-line bg-code">
      <table className="w-full min-w-[42rem] border-collapse text-left font-mono text-xs">
        <caption className="sr-only">{label}</caption>
        <thead>
          <tr className="border-b border-line bg-sunken text-2xs uppercase tracking-wider text-faint">
            <th scope="col" className="px-3 py-1.5 font-medium">
              path
            </th>
            <th scope="col" className="w-16 px-3 py-1.5 font-medium">
              mode
            </th>
            <th scope="col" className="w-32 px-3 py-1.5 font-medium">
              owner
            </th>
            <th scope="col" className="px-3 py-1.5 font-medium">
              note
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.path + r.depth} className="border-t border-line/60">
              <th scope="row" className="whitespace-nowrap px-3 py-1 text-left font-normal">
                <span aria-hidden="true" className="select-none whitespace-pre text-faint">
                  {r.depth > 0 ? '   '.repeat(r.depth) + '└─ ' : ''}
                </span>
                <span className={r.emphasis ? EMPH[r.emphasis] : 'text-strong'}>{r.path}</span>
              </th>
              <td className="px-3 py-1 text-muted">{r.mode ?? ''}</td>
              <td className="whitespace-nowrap px-3 py-1 text-muted">{r.owner ?? ''}</td>
              <td className="px-3 py-1 text-faint">{r.note ?? ''}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function FilesystemTree() {
  return (
    <div className="not-prose my-6 space-y-5">
      <div>
        <h3 className="mb-2 font-mono text-xs font-semibold uppercase tracking-wider text-muted">
          Per user
        </h3>
        <Tree rows={perUser} label="Per-user filesystem layout with modes and owners" />
      </div>
      <div>
        <h3 className="mb-2 font-mono text-xs font-semibold uppercase tracking-wider text-muted">
          System paths
        </h3>
        <Tree rows={system} label="System paths ratline owns" />
      </div>
      <p className="flex flex-wrap gap-x-5 gap-y-1 text-xs text-muted">
        <span className="flex items-center gap-1.5">
          <span aria-hidden="true" className="size-2 rounded-sm bg-danger" /> never reachable by nginx
        </span>
        <span className="flex items-center gap-1.5">
          <span aria-hidden="true" className="size-2 rounded-sm bg-info" /> readable by nginx, via
          the group
        </span>
        <span className="flex items-center gap-1.5">
          <span aria-hidden="true" className="size-2 rounded-sm bg-ok" /> readable by the adm group
        </span>
      </p>
    </div>
  );
}
