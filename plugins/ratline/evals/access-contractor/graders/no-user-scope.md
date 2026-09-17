---
type: regex
pattern: '^\s*(\$ |sudo )?(ssh [^\n]*)?ratline key add[^\n]*--scope user'
match: not_contains
weight: 1
flags: m
---
No runnable `ratline key add` line uses user scope, which would give a shell over everything the tenant owns. Naming it to explain why site scope is narrower is the right thing to do and is not what this catches.
