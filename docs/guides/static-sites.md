# Static sites

nginx serves the files directly. There is no unit and nothing to supervise.

```bash
sudo ratline site add www.example.com --user acme --runtime static
```

## The document root

`<site>/public` unless `--root` says otherwise, and it must be inside the site
directory. A root elsewhere is refused rather than symlinked to, because a symlink
out of a tenant's home defeats the point of the layout.

```bash
sudo ratline site add www.example.com --user acme --runtime static --root dist
```

## Single-page applications

```bash
--spa
```

Unmatched paths return the index document instead of 404, which is what a
client-side router needs. Without it, a refresh on a deep route 404s.

## Builds

A static site can still be built from a repository:

```bash
sudo ratline site add www.example.com --user acme --runtime static \
    --repo git@github.com:acme/site.git --branch main \
    --build-command "npm run build" --build-output dist
```

`--build-output` is published to the document root only after the build succeeds, so
a failed build leaves the previous version serving.

`--build-command` is parsed by a shell-words parser that refuses `;`, `&&`, `|`,
backticks, `$(` and redirections. A pipeline belongs in a script in your repository,
where it can be reviewed and tested — not in a flag.

## Caching

Content-hashed assets get a one-year `Cache-Control`. The index document is never
cached. That split is the important one: caching `index.html` is how a deploy appears
not to have happened.

## Your own nginx directives

`/etc/nginx/ratline/custom/<domain>.conf` is included by the generated vhost and is
never regenerated. Redirects, custom headers, a `location` for something ratline does
not know about — all of it survives `ratline reconcile`.

## Uploads

```bash
sudo ratline site scale www.example.com --client-max-body-size 100M
```

The default is 20M, and exceeding it is the most common cause of a mystery 413.
