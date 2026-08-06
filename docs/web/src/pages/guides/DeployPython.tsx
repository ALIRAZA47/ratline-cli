import { Link } from 'react-router-dom';
import { CodeBlock } from '../../components/CodeBlock';
import { PageHeader } from '../../components/PageHeader';
import { Terminal } from '../../components/Terminal';
import { Callout, Facts, H2, H3 } from '../../components/ui';

/**
 * The companion to the Node guide, and written the same way: by deploying a real FastAPI
 * application to a real server and writing down what happened, including the two things
 * that did not work.
 */
export function GuideDeployPython() {
  return (
    <article>
      <PageHeader
        eyebrow="Guide"
        title="Deploy a Python app, start to finish"
        lede="From a bare server to a FastAPI application on a Unix socket with its own MongoDB database — every command in order, and the two failures worth recognising."
      />

      <Facts
        rows={[
          ['worked example', 'FastAPI behind Gunicorn with uvicorn workers'],
          ['listens on', <>a Unix socket, which is the default and the better one</>],
          ['also covers', 'Django, where the differences are noted inline'],
          ['assumes', <>a fresh Ubuntu or Debian server with <code>ratline init</code> run</>],
        ]}
      />

      <div className="prose">
        <H2 id="runtime">1 · Install the Python you want to run</H2>
      </div>

      <CodeBlock lang="shell" prompt code={`ratline runtime install python 3.12
ratline runtime list`} />

      <Callout tone="note" title="An interpreter that cannot make a virtualenv is not a runtime">
        <p>
          Debian and Ubuntu ship <code>python3.12</code> and put <code>venv</code> in a
          separate <code>python3.12-venv</code> package, so the interpreter is present and{' '}
          <code>python -m venv</code> fails. ratline checks that at install time and
          installs the missing package rather than letting you discover it three commands
          later, out of <code>site add</code>, after it has rolled the site back.
        </p>
      </Callout>

      <div className="prose">
        <H2 id="tenant">2 · Create the tenant</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline user add acme --ssh-key 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA… you@laptop'`}
      />

      <div className="prose">
        <H2 id="layout">3 · The layout ratline expects</H2>
        <p>
          <code>--app-module</code> is an import path, resolved from the application
          directory — the same string you would pass to gunicorn by hand. It is not a file
          path, so no <code>.py</code> and no slashes.
        </p>
      </div>

      <CodeBlock
        lang="text"
        code={`/home/acme/api.example.com/
  app/                      the application directory, and gunicorn's working directory
    app/
      __init__.py
      main.py               app = FastAPI()   →  --app-module app.main:app
    requirements.txt
  venv/                     created and owned by ratline
  logs/                     access.log, app.log
  .env                      0600, owned by the tenant`}
      />

      <div className="prose">
        <H2 id="create">4 · Create the site</H2>
        <p>
          As with Node, the site can exist before the code does; it is configured and left
          stopped until you deploy.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site add api.example.com \\
  --user acme \\
  --runtime python \\
  --python 3.12 \\
  --app-module app.main:app \\
  --asgi \\
  --server gunicorn \\
  --ssl none`}
      />

      <Facts
        rows={[
          [
            '--asgi',
            <>
              FastAPI, Starlette and Django's <code>asgi.py</code> are ASGI, so gunicorn is
              given <code>uvicorn.workers.UvicornWorker</code>. Leave it off for Flask and
              for Django's <code>wsgi.py</code>.
            </>,
          ],
          [
            'no --listen',
            <>
              The default is a Unix socket at <code>/run/ratline/&lt;slug&gt;/app.sock</code>,
              which is what you want: no port to allocate, no way to reach the app except
              through nginx.
            </>,
          ],
          [
            'Django',
            <>
              add <code>--manage-py manage.py</code>, which is what enables{' '}
              <code>--migrate</code> and <code>--collectstatic</code> on deploys, and
              <code> --static-dir staticfiles --static-url /static/</code> so nginx serves
              them directly.
            </>,
          ],
        ]}
      />

      <div className="prose">
        <H2 id="code">5 · Code, database, environment</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`rsync -az --delete --exclude .git --exclude '.env*' --exclude venv \\
  ./ root@server:/home/acme/api.example.com/app/
chown -R acme:acme /home/acme/api.example.com/app

ratline db create acmeapi --owner acme --attach api.example.com
ratline site env set api.example.com LOG_LEVEL=info
ratline site env set api.example.com SECRET_KEY        # prompts, not echoed`}
      />

      <div className="prose">
        <p>
          <code>--attach</code> writes <code>MONGODB_URI</code> into the site's{' '}
          <code>.env</code>. Read it the way you would read any environment variable:
        </p>
      </div>

      <CodeBlock
        lang="python"
        code={`import os
from pymongo import MongoClient

client = MongoClient(os.environ["MONGODB_URI"], serverSelectionTimeoutMS=4000)
db = client.get_default_database()   # the URI names the database`}
      />

      <div className="prose">
        <H2 id="deploy">6 · Deploy</H2>
        <p>
          <code>--install</code> creates the virtualenv if it is missing and installs the
          requirements into it. There is no build step for a plain FastAPI app; Django wants
          one for <code>collectstatic</code>.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site deploy api.example.com --install --restart

# Django
ratline site deploy api.example.com --install --migrate --collectstatic --restart`}
      />

      <Terminal title="root@server">{`→ deploy step step=install domain=api.example.com
→ Successfully installed fastapi-0.115.6 gunicorn-23.0.0 pymongo-4.10.1 uvicorn-0.34.0
→ the application is healthy domain=api.example.com check="HTTP 200 in 41ms"
✓ Deployed api.example.com`}</Terminal>

      <div className="prose">
        <p>The build step, when there is one, gets the site's environment — the same
          variables the service runs with, because <code>collectstatic</code> reads{' '}
          <code>DJANGO_SETTINGS_MODULE</code> and usually a database URL.</p>

        <H2 id="workers">7 · Workers</H2>
        <p>
          The default is derived from the CPU count and capped. Each worker is a process
          with its own memory, so the ceiling that matters is{' '}
          <code>--memory-max</code> divided by what one worker uses.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site scale api.example.com --workers 4 --memory-max 1G`}
      />

      <div className="prose">
        <H2 id="tls">8 · TLS</H2>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`dig +short api.example.com A @1.1.1.1
ratline cert issue api.example.com --email ops@example.com`}
      />

      <div className="prose">
        <H2 id="wrong">When it does not come up</H2>
        <H3 id="wrong-socket">Permission denied on the socket</H3>
        <p>
          Gunicorn reporting{' '}
          <code>[Errno 13] Permission denied: '/run/ratline/&lt;slug&gt;/app.sock'</code>{' '}
          means the tenant cannot traverse the shared parent directory. ratline keeps{' '}
          <code>/run/ratline</code> at 0755 and reinstates it on every start and every boot;
          if you see this, something outside ratline has changed it.
        </p>
        <H3 id="wrong-import">The unit exits immediately</H3>
        <p>
          Almost always <code>--app-module</code> not matching the layout, or an import that
          fails at module scope — a missing environment variable read at import time is the
          usual one. The traceback is in <code>logs/app.log</code>, not in the journal.
        </p>
      </div>

      <CodeBlock
        lang="shell"
        prompt
        code={`ratline site troubleshoot api.example.com
ratline site logs api.example.com

# is the module importable at all, as the tenant sees it?
sudo -u acme /home/acme/api.example.com/venv/bin/python \\
  -c 'import app.main; print(app.main.app)'`}
      />

      <Callout tone="note" title="The socket is not a port">
        <p>
          There is nothing to curl directly. Go through nginx with a{' '}
          <code>Host</code> header, or talk to the socket:{' '}
          <code>curl --unix-socket /run/ratline/&lt;slug&gt;/app.sock http://localhost/</code>.
          See <Link to="/topics/sockets">sockets</Link>.
        </p>
      </Callout>
    </article>
  );
}
