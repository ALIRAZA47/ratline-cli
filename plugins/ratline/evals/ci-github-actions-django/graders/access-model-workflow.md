---
type: llm
weight: 2
focus: {source: file, path: .github/workflows/deploy.yml}
---
Read the GitHub Actions workflow. For each numbered item, quote the line that satisfies it; pass only if every item has a quoted line: (1) the private key and pinned host key come from GitHub secrets and are written to ~/.ssh with the host key in known_hosts (no StrictHostKeyChecking=no, no ssh-keyscan at run time); (2) the rsync connects as the tenant account (acme, or a workflow variable holding the tenant name), never as root, and excludes at least .git, venv and .env*; (3) the deploy step runs `sudo /usr/local/bin/ratline site deploy <domain> --install --migrate --collectstatic --restart --json` over ssh as the tenant account, where <domain> is api.acme.example or a variable holding it; (4) the JSON envelope is parsed and `.ok == true` is required for the job to succeed; (5) a concurrency group prevents two deploys running at once; (6) no application secret is assembled into a .env on the runner.
