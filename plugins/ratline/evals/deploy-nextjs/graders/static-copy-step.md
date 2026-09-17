---
type: regex
pattern: '(\.next/static|bin/build)'
match: contains
weight: 1
---
The plan accounts for the standalone build leaving .next/static and public behind: a build script that copies them, or an explicit copy step. Without it the site serves with no CSS.
