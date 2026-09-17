---
type: regex
pattern: '--scope user --user acme'
match: contains
weight: 1
target: {source: file, path: SERVER.md}
---
The runner's key is user-scoped to the tenant (a site-scoped key cannot run ratline).
