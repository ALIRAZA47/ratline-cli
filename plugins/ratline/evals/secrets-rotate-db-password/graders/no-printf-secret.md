---
type: regex
pattern: 'printf [^\n]*(PASSWORD|SECRET|URI)'
match: not_contains
weight: 1
---
No secret is pushed through printf (a % in it truncates).
