# Static sites

> nginx serving files directly, with no process to supervise.

A static site has no unit and nothing to start. nginx serves the document root and
that is the whole architecture.

    ratline site add www.example.com --user acme --runtime static

The document root is `<site>/public` unless `--root` says otherwise, and it must be
inside the site directory — a root elsewhere is refused rather than symlinked to.

## Single-page applications

    --spa

Unmatched paths return the index document instead of a 404, which is what a
client-side router needs. Without it, a deep link into a React or Vue application
404s on reload.

## Builds

A static site can still have a build:

    ratline site add www.example.com --user acme --runtime static \
      --repo git@github.com:acme/site.git --build-command "npm run build" \
      --build-output dist

`--build-output` is published to the document root after a successful build, so a
failed build leaves the previous version serving.

## Caching

Content-hashed assets get a one-year `Cache-Control`; the index document is never
cached. This is the split that matters: caching `index.html` is how a deploy
appears not to have happened.

See also: `ratline explain deploys`.
