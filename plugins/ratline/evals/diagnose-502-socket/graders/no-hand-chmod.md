---
type: regex
pattern: '^\s*(\$ |sudo )?chmod '
match: not_contains
weight: 2
flags: m
---
Does not offer a runnable chmod on the socket, which papers over the cause until the next restart. Asking whether someone ran a chmod is a diagnostic question and is fine.
