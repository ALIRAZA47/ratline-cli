---
type: llm
weight: 2
---
Quote the evidence for each item and pass only if all hold (flag validity is checked separately by a script, not here): (1) the site is static, owned by acme, domain dash.acme.example, with --spa, a build command and --build-output dist; (2) VITE_API_URL is set via `site env set` BEFORE deploying, and the plan explains a change to it needs a rebuild; (3) TLS only after DNS is confirmed, with a cert dry run or --ssl none first; (4) verification uses curl (site health skips static sites) and checks a deep route returns the index rather than 404.
