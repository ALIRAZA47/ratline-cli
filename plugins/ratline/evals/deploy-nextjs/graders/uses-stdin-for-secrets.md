---
type: regex
pattern: '(--stdin|ratline site env set \S+ [A-Z][A-Z0-9_]*\s*(#[^\n]*)?$)'
match: contains
weight: 1
flags: m
---
Secrets are supplied over stdin (`--stdin`) or by naming the variable alone so ratline prompts without echo. Never as KEY=value with a real value.
