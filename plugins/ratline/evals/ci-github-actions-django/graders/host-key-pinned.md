---
type: regex
pattern: 'known_hosts'
match: contains
weight: 1
target: {source: file, path: .github/workflows/deploy.yml}
---
The server's host key is pinned in the runner, not trusted on first use.
