---
type: regex
pattern: '(toJSON\(secrets\)|>>?\s*\S*\.env\b|printf [^\n]*\.env\b|echo [^\n]*\.env\b|scp [^\n]*\.env\b|env_file)'
match: not_contains
weight: 1
target: {source: file, path: .github/workflows/deploy.yml}
---
Application secrets are not assembled from CI secrets into a .env and shipped on every deploy; they live on the server via `site env set`. Excluding `.env*` from the rsync is fine and is not what this catches.
