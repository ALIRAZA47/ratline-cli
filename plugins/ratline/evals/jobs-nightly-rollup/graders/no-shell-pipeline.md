---
type: regex
pattern: '--command [''\"][^\n''\"]*(\|\||&&|\|)'
match: not_contains
weight: 1
---
The --command is a single argv, not a shell pipeline.
