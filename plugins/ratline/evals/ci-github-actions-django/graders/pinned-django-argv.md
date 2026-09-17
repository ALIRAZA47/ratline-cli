---
type: regex
pattern: 'site deploy api\.acme\.example --install --migrate --collectstatic --restart --json'
match: contains
weight: 2
target: {source: file, path: SERVER.md}
---
The one pinned command includes Django's migrate and collectstatic steps and --json, identically in the sudo grant and the workflow.
