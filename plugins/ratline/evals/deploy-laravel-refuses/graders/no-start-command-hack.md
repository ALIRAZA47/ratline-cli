---
type: regex
pattern: '^\s*(\$ |sudo )?(ssh [^\n]*)?ratline [^\n]*--start-command'
match: not_contains
weight: 2
flags: m
---
It must not offer a runnable command that wraps php or php-fpm in --start-command under a node or python runtime.
