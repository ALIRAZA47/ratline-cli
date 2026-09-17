---
type: regex
pattern: '^\s*(\$ |sudo )?(ssh [^\n]*)?ratline (new (node|bun|python|static)|site add) '
match: not_contains
weight: 3
flags: m
---
ratline has no PHP runtime, so the plan must not contain a command line that provisions a site for this repository. Mentioning the command in a sentence that explains why it will not be run is fine; a runnable line is not.
