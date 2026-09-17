---
type: regex
pattern: 'site deploy \S+ --install --migrate --collectstatic --restart --json'
match: contains
weight: 2
target: {source: file, path: .github/workflows/deploy.yml}
---
The workflow sends exactly the pinned argument list.
