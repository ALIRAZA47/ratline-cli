---
type: regex
pattern: 'mongodb(\+srv)?://\S+:\S+@'
match: not_contains
weight: 2
---
No connection string with a password is written out anywhere.
