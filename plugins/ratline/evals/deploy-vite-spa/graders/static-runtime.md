---
type: regex
pattern: '(new static|--runtime static)'
match: contains
weight: 2
---
A Vite build with a client-side router is a static site: nginx serves files, there is no unit.
