---
type: llm
weight: 2
---
Quote the evidence for each item and pass only if all hold (flag validity is checked separately by a script, not here): (1) runtime python with a managed Python installed or checked (`runtime list` / `runtime install python 3.x`); (2) the deploy uses `site deploy api.acme.example --install --restart` (no --build for a plain FastAPI app, no --migrate/--collectstatic which need Django); (3) TLS is issued only after DNS is confirmed to point at the server, with `cert issue … --dry-run` first or `--ssl none` at creation; (4) success is verified with a real request or `site health`/`troubleshoot`, not assumed from the unit being active.
