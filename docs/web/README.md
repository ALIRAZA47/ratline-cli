# The documentation site

React, Vite, Tailwind. It renders the same content as `docs/` — the concept pages under
`docs/topics/` are embedded in the binary for `ratline explain`, so there is one source of
truth and the website cannot disagree with what the server tells you.

```bash
npm install
npm run dev        # http://localhost:5173, or $PORT
npm run build      # tsc -b && vite build, into dist/
npm run typecheck
```

## Deploying to Vercel

Import the repository. Nothing to configure: `vercel.json` at the repository root carries
the install and build commands, the output directory and the SPA rewrite, so the Root
Directory can stay at the default.

```
Framework Preset:  Other        (vercel.json sets "framework": null)
Root Directory:    ./           (leave it)
```

Everything else — install, build, output, routing, headers — comes from `vercel.json`.

### Two things in that config that are load-bearing

**The rewrite.** This is a `BrowserRouter`, so `/reference/exit-codes` has to serve
`index.html` and let the router take over. Vercel checks the filesystem before applying
rewrites, so the catch-all cannot swallow `/assets/*`.

**The base path.** `vite.config.ts` defaults `base` to `/`, not `./`. A relative base is
quietly broken for this site: on a refresh at `/reference/exit-codes` the browser resolves
`./assets/index.js` against `/reference/`, requests `/reference/assets/index.js`, and the
SPA rewrite answers with `index.html` — handing it HTML where it expected JavaScript, for
a blank page and one console error that does not explain itself. One-level routes happen
to work, which is what makes it easy to miss.

To deploy under a subpath instead — a GitHub Pages project site, say:

```bash
VITE_BASE=/ratline-cli/ npm run build
```

### The CSP

`script-src` is `'self'` plus the sha256 of `index.html`'s one inline script — the theme
toggle, which has to run before first paint or the page flashes the wrong colours. That
hash changes whenever the script does, and the failure is silent: the theme flashes, a
CSP violation is logged where nobody looks, and the page otherwise works.

`scripts/check-csp-hash.sh` compares the two and runs in CI. If you edit that script, run
it and paste the hash it prints into `vercel.json`.

## Deploying anywhere else

Any static host works. It needs two things:

- serve `index.html` for any path that is not a file on disk
- serve `/assets/*` as real files, with the long cache they are safe to have — the
  filenames are content-hashed
