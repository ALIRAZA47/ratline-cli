import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, Facts, H2, H3 } from '../../components/ui';

export function GuideFastApi() {
  return (
    <article>
      <PageHeader
        eyebrow="Guide"
        title="Deploy a FastAPI app behind Gunicorn and Uvicorn"
        lede="An ASGI application on a Unix socket, in its own virtualenv, with static files served by nginx and secrets that never touch argv."
      />

      <Facts
        rows={[
          ['runtime', <code key="a">python</code>],
          ['server', 'Gunicorn as the process manager, UvicornWorker for the ASGI protocol'],
          ['listens on', <code key="b">/run/ratline/&lt;slug&gt;/app.sock</code>],
          ['you need', 'a repository, an import path to the ASGI callable, and a Python version installed'],
        ]}
      />

      <div className="prose">
        <H2>Why Gunicorn <em>and</em> Uvicorn</H2>
        <p>
          Uvicorn speaks ASGI; Gunicorn manages processes, restarts crashed workers and handles graceful
          reloads. Running Uvicorn alone gives you the protocol without the supervision; running Gunicorn
          alone gives you supervision without ASGI. The combination — Gunicorn with{' '}
          <code>UvicornWorker</code> — is what <code>--asgi</code> selects, and it is why{' '}
          <code>site reload</code> can replace workers without dropping the listening socket.
        </p>
        <p>
          You do not normally have to say any of this. FastAPI and Starlette are detected as ASGI
          automatically. <code>--asgi</code> is for when detection would get it wrong — an app assembled
          at runtime, or a callable that is not obviously either.
        </p>

        <H2 id="layout">The project layout that matches the default</H2>
      </div>

      <CodeBlock
        lang="text"
        code={`api.example.com/
├── app/                      ← WorkingDirectory, and where --repo clones to
│   ├── app/
│   │   ├── __init__.py
│   │   └── main.py           ← app = FastAPI()   →  --app-module app.main:app
│   ├── requirements.txt
│   └── staticfiles/          ← --static-dir, served by nginx directly
├── venv/                     ← created by ratline, python runtime only
├── logs/{app,access,error}.log
├── tmp/
├── .env                      ← 0600, loaded by systemd as root
└── .ratline/site.yaml`}
        noCopy
      />

      <div className="prose">
        <p>
          <code>--app-module</code> is an import path, resolved from the application directory. It must
          match <code>^[A-Za-z_][A-Za-z0-9_.]*:[A-Za-z_][A-Za-z0-9_]*$</code> and every dotted segment
          must be a valid Python identifier, because this string lands on a Gunicorn command line{' '}
          <em>and</em> inside a unit file.
        </p>
      </div>

      <Callout tone="warn" title="app.main:app vs main:app is the single most common failure">
        <p>
          If <code>main.py</code> sits at the top of the repository, the module is <code>main:app</code>.
          If it sits in a package directory called <code>app/</code>, it is{' '}
          <code>app.main:app</code>. Getting it wrong produces{' '}
          <code>ModuleNotFoundError: No module named 'app'</code>, which becomes exit{' '}
          <Link to="/reference/exit-codes#code-7">7</Link> — and nothing is enabled in nginx, so no
          traffic ever reaches the broken site.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="steps">The commands</H2>
        <H3>1 · Runtime and tenant</H3>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline runtime install python 3.12
ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub --comment "Acme Ltd"`}
      />

      <div className="prose">
        <H3>2 · Preview, then create</H3>
        <p>
          <code>--dry-run</code> first, every time. It prints every file, command and permission change
          and makes none of them — and because reads still run, it catches a missing runtime or a taken
          domain for real.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site add api.example.com \\
  --user acme \\
  --runtime python \\
  --repo https://github.com/acme/api.git \\
  --branch main \\
  --app-module app.main:app \\
  --python 3.12 \\
  --asgi \\
  --workers 5 \\
  --requirements requirements.txt \\
  --static-url /static --static-dir staticfiles \\
  --dry-run`}
      />

      <div className="prose">
        <p>Then drop the flag and let it run.</p>
        <H3>3 · Configuration, without putting secrets in argv</H3>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# Non-secret values are fine inline
ratline site env set api.example.com \\
  ENVIRONMENT=production \\
  LOG_LEVEL=info \\
  ALLOWED_HOSTS=api.example.com

# Secrets read from stdin, so they never appear in the process list or the audit log
ratline site env set api.example.com DATABASE_URL --stdin < /run/secrets/db-url
ratline site env set api.example.com SECRET_KEY   --stdin < /run/secrets/app-key

# Values are masked unless you ask
ratline site env list api.example.com
ratline site env list api.example.com --reveal`}
      />

      <div className="prose">
        <H3>4 · Deploy and prove it works</H3>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site deploy api.example.com --pull --install --restart
ratline site show api.example.com`}
      />

      <Terminal title="root@server">{`$ ratline site deploy api.example.com --pull --install --restart
→ git fetch && checkout main (as acme)
→ pip install -r requirements.txt (as acme)
→ restarting ratline-acme-api_example_com.service
→ waiting for health on /run/ratline/acme-api_example_com/app.sock
→ healthy in 1.9s (HTTP 200)
→ previous release retained for rollback`}</Terminal>

      <div className="prose">
        <p>
          The health check is a real HTTP request against the socket, not a process check. A worker that
          started and then failed to bind, or bound and returned 500 to everything, fails this — which is
          the point.
        </p>

        <H2 id="workers">Choosing --workers</H2>
        <p>
          The default is <code>(2 × cores) + 1</code>, capped at <code>defaults.worker_cap</code> (8).
          That formula is a starting point for CPU-bound synchronous work.
        </p>
        <p>
          For an async FastAPI app doing mostly I/O, fewer workers is often better: each worker is a
          separate Python process with its own memory, its own connection pool and its own copy of your
          model objects. Four workers each holding a 20-connection pool is 80 connections to a database
          that may allow 100.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`# Memory pressure or a connection-limited database: fewer workers
ratline site scale api.example.com --workers 2 --memory-max 512M

# Genuinely CPU-bound endpoints on a bigger box
ratline site scale api.example.com --workers 8 --memory-max 2G --cpu-quota 400%`}
      />

      <Callout tone="note" title="MemoryHigh is 87.5% of MemoryMax">
        <p>
          So the kernel starts <em>reclaiming</em> before it starts <em>killing</em>. A worker that gets
          slow under memory pressure is recoverable; one that gets OOM-killed mid-request is not. If you
          are seeing workers vanish, raise <code>--memory-max</code> rather than lowering the worker
          count first — the arithmetic is per-process.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="static">Static files</H2>
        <p>
          <code>--static-url /static --static-dir staticfiles</code> tells nginx to serve that prefix
          straight from disk, bypassing the application entirely. For a FastAPI app that mounts{' '}
          <code>StaticFiles</code> itself, this is strictly better: nginx serves a file in one syscall
          where Python needs a request cycle and a worker slot.
        </p>
        <p>
          Content-hashed assets get <code>nginx.asset_max_age</code> (31536000 seconds — a year) of cache
          lifetime. That is only safe <em>because</em> the filenames are hashed; do not put unhashed
          assets in that directory.
        </p>

        <H2 id="tls">TLS, when DNS is ready</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline cert issue api.example.com --email ops@example.com --dry-run
ratline cert issue api.example.com --email ops@example.com`}
      />

      <div className="prose">
        <H2 id="troubleshooting">When it does not come up</H2>
        <ul>
          <li>
            <code>ModuleNotFoundError: No module named 'app'</code> — <code>--app-module</code> does not
            match the layout. Check whether <code>main.py</code> is inside a package directory.
          </li>
          <li>
            <code>Failed to find attribute 'app' in 'app.main'</code> — the module imports but the
            callable is named something else. FastAPI apps are conventionally <code>app</code>; Django’s
            WSGI callable is <code>application</code>.
          </li>
          <li>
            Workers boot and immediately exit — usually a missing environment variable. Check{' '}
            <code>ratline site env list</code> and <code>ratline site logs api.example.com --app</code>.
          </li>
          <li>
            <code>PermissionError</code> on a path under <code>/home</code> that is not this site —{' '}
            <code>ProtectHome=tmpfs</code>, working as designed. See{' '}
            <Link to="/concepts/supervision#relax">process supervision</Link>.
          </li>
          <li>
            Everything looks fine and nginx returns 502 — go to{' '}
            <Link to="/guides/debug-502">debugging a 502</Link>.
          </li>
        </ul>

        <H3>Django, for comparison</H3>
        <p>
          Same runtime, WSGI instead of ASGI, and <code>manage.py</code> unlocks two extra deploy steps.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site add app.example.com \\
  --user acme --runtime python \\
  --app-module myproject.wsgi:application \\
  --python 3.12 --wsgi \\
  --manage-py manage.py \\
  --static-url /static --static-dir staticfiles

ratline site deploy app.example.com --pull --install --migrate --collectstatic --restart`}
      />
    </article>
  );
}
