---
type: regex
pattern: 'user sudo grant acme'
match: contains
weight: 2
target: {source: file, path: SERVER.md}
---
CI's key may run exactly one command as root, via `ratline user sudo grant`.
