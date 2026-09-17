---
type: llm
weight: 2
focus: {source: file, path: SERVER.md}
---
Read SERVER.md, the one-time steps on the server. Pass only if all hold: (1) the runner's SSH key is added at user scope for the acme tenant (`ratline key add --scope user --user acme …`) and proved with `ratline key test`; (2) root is reached only through `ratline user sudo grant acme --command '…'` whose full argument list is pinned and is exactly `/usr/local/bin/ratline site deploy api.acme.example --install --migrate --collectstatic --restart --json`; (3) `ratline config set users.allow_sudo true` is shown and the text says this is a real decision, naming an alternative (a self-hosted runner, or a human running the deploy); (4) the host key is captured with ssh-keyscan for pinning; (5) application secrets are set on the server with `ratline site env set … --stdin` (or an un-echoed prompt), not shipped from CI; (6) no root key and no global-scope key appears anywhere. Flag validity is checked separately by a script, not here.
