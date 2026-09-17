---
type: regex
pattern: 'concurrency:'
match: contains
weight: 1
target: {source: file, path: .github/workflows/deploy.yml}
---
A concurrency group stops two deploys rsyncing into one directory at once.
