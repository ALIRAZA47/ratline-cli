---
type: regex
pattern: '(DATABASE_URL[^\n]*--stdin|--stdin[^\n]*DATABASE_URL|env set \S+ DATABASE_URL\s*(#[^\n]*)?$)'
match: contains
weight: 1
flags: m
---
The Neon DATABASE_URL reaches the site over stdin, or by naming the variable alone so ratline prompts without echo. Never as a literal value in argv.
