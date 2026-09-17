---
type: regex
pattern: '^\s*(\$ |sudo )?(ssh [^\n]*)?ratline key add[^\n]*--allow-shell'
match: not_contains
weight: 2
flags: m
---
No runnable `ratline key add` line grants a shell with --allow-shell. Mentioning the flag while explaining why it is not used is fine.
