---
type: regex
pattern: 'jq -e [^\n]*ok == true'
match: contains
weight: 1
target: {source: file, path: .github/workflows/deploy.yml}
---
The workflow reads the JSON envelope and fails the build when ok is false.
