---
type: regex
pattern: 'root@'
match: not_contains
weight: 2
target: {source: file, path: .github/workflows/deploy.yml}
---
The workflow never connects as root.
