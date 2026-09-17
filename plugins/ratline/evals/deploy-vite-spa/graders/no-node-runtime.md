---
type: regex
pattern: '(--runtime node|new node|--entry |--listen )'
match: not_contains
weight: 1
---
The plan does not treat a static SPA as a node service.
