/**
 * The request path, as inline SVG. No image dependency, and it inherits the
 * theme through the same CSS variables everything else uses.
 */
export function RequestPath() {
  return (
    <figure className="not-prose canvas-wide my-6">
      <div className="scroll-thin overflow-x-auto rounded-[var(--radius-card)] border border-line bg-sunken p-4">
        <svg
          viewBox="0 0 880 330"
          className="h-auto w-full min-w-[46rem]"
          role="img"
          aria-labelledby="rp-title rp-desc"
        >
          <title id="rp-title">The request path for a dynamic site</title>
          <desc id="rp-desc">
            A browser makes an HTTPS request on port 443 to nginx, which runs as www-data. nginx
            terminates TLS. Requests for the static public directory are served directly from disk.
            Everything else is proxied over a Unix socket at /run/ratline/&lt;slug&gt;/app.sock, mode
            0660 owned by the site user and group www-data, to a systemd unit running as the site
            user, which is Gunicorn with a Uvicorn worker for Python or the managed Node binary for
            Node.
          </desc>

          <defs>
            <marker
              id="rp-arrow"
              viewBox="0 0 10 10"
              refX="9"
              refY="5"
              markerWidth="7"
              markerHeight="7"
              orient="auto-start-reverse"
            >
              <path d="M0 0L10 5L0 10z" fill="var(--fg-faint)" />
            </marker>
          </defs>

          <g fontFamily="var(--font-mono)" fontSize="11">
            {/* Browser */}
            <rect
              x="8"
              y="112"
              width="118"
              height="58"
              rx="7"
              fill="var(--bg-raised)"
              stroke="var(--border-strong)"
            />
            <text x="67" y="136" textAnchor="middle" fill="var(--fg-strong)" fontSize="12.5" fontWeight="600">
              browser
            </text>
            <text x="67" y="153" textAnchor="middle" fill="var(--fg-muted)">
              :443
            </text>

            <line
              x1="128"
              y1="141"
              x2="196"
              y2="141"
              stroke="var(--fg-faint)"
              strokeWidth="1.4"
              markerEnd="url(#rp-arrow)"
            />
            <text x="162" y="132" textAnchor="middle" fill="var(--fg-faint)" fontSize="10">
              TLS
            </text>

            {/* nginx */}
            <rect
              x="200"
              y="86"
              width="150"
              height="112"
              rx="7"
              fill="var(--bg-raised)"
              stroke="var(--accent)"
              strokeWidth="1.5"
            />
            <text x="275" y="110" textAnchor="middle" fill="var(--fg-strong)" fontSize="12.5" fontWeight="600">
              nginx
            </text>
            <text x="275" y="128" textAnchor="middle" fill="var(--fg-muted)">
              user www-data
            </text>
            <text x="275" y="145" textAnchor="middle" fill="var(--fg-muted)">
              in group &lt;user&gt;
            </text>
            <text x="275" y="167" textAnchor="middle" fill="var(--fg-faint)" fontSize="10">
              terminates TLS
            </text>
            <text x="275" y="182" textAnchor="middle" fill="var(--fg-faint)" fontSize="10">
              server_name match
            </text>

            {/* Branch up: static */}
            <path
              d="M350 118 C 400 118 400 46 452 46"
              fill="none"
              stroke="var(--fg-faint)"
              strokeWidth="1.4"
              markerEnd="url(#rp-arrow)"
            />
            <text x="404" y="38" textAnchor="middle" fill="var(--fg-faint)" fontSize="10">
              /static, public/
            </text>

            <rect
              x="456"
              y="18"
              width="230"
              height="56"
              rx="7"
              fill="var(--bg-raised)"
              stroke="var(--border-strong)"
            />
            <text x="571" y="41" textAnchor="middle" fill="var(--fg-strong)" fontSize="12">
              files on disk
            </text>
            <text x="571" y="59" textAnchor="middle" fill="var(--fg-muted)" fontSize="10">
              /home/&lt;user&gt;/&lt;domain&gt;/public 0750
            </text>

            {/* Branch down: socket */}
            <path
              d="M350 166 C 400 166 400 236 452 236"
              fill="none"
              stroke="var(--fg-faint)"
              strokeWidth="1.4"
              markerEnd="url(#rp-arrow)"
            />
            <text x="404" y="228" textAnchor="middle" fill="var(--fg-faint)" fontSize="10">
              everything else
            </text>

            {/* Socket */}
            <rect
              x="456"
              y="206"
              width="192"
              height="62"
              rx="7"
              fill="var(--bg-code)"
              stroke="var(--border-strong)"
              strokeDasharray="4 3"
            />
            <text x="552" y="228" textAnchor="middle" fill="var(--fg-strong)" fontSize="11.5">
              app.sock
            </text>
            <text x="552" y="244" textAnchor="middle" fill="var(--fg-muted)" fontSize="10">
              /run/ratline/&lt;slug&gt;/
            </text>
            <text x="552" y="259" textAnchor="middle" fill="var(--fg-muted)" fontSize="10">
              0660 &lt;user&gt;:www-data
            </text>

            <line
              x1="650"
              y1="237"
              x2="708"
              y2="237"
              stroke="var(--fg-faint)"
              strokeWidth="1.4"
              markerEnd="url(#rp-arrow)"
            />

            {/* App */}
            <rect
              x="712"
              y="196"
              width="160"
              height="84"
              rx="7"
              fill="var(--bg-raised)"
              stroke="var(--ok)"
              strokeWidth="1.5"
            />
            <text x="792" y="218" textAnchor="middle" fill="var(--fg-strong)" fontSize="12.5" fontWeight="600">
              your app
            </text>
            <text x="792" y="235" textAnchor="middle" fill="var(--fg-muted)" fontSize="10">
              ratline-&lt;slug&gt;.service
            </text>
            <text x="792" y="250" textAnchor="middle" fill="var(--fg-muted)" fontSize="10">
              User=&lt;user&gt;
            </text>
            <text x="792" y="267" textAnchor="middle" fill="var(--fg-faint)" fontSize="10">
              gunicorn / node
            </text>

            {/* Group note */}
            <line
              x1="275"
              y1="198"
              x2="275"
              y2="300"
              stroke="var(--border-strong)"
              strokeWidth="1"
              strokeDasharray="3 3"
            />
            <text x="283" y="304" fill="var(--fg-faint)" fontSize="10">
              nginx reads public/ because www-data is in the user’s group — the home stays 0750
            </text>
          </g>
        </svg>
      </div>
      <figcaption className="mt-2 max-w-[var(--container-measure)] text-xs leading-relaxed text-muted">
        For a <code className="font-mono">static</code> site the lower branch does not exist: there
        is no unit and no socket, and nginx serves files and nothing else.
      </figcaption>
    </figure>
  );
}
