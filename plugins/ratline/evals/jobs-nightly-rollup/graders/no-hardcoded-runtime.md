---
type: regex
pattern: '--command [''\"]?[^\n]*/opt/ratline/runtimes'
match: not_contains
weight: 1
---
The command does not hardcode a path into /opt/ratline/runtimes. The unit carries the site's PATH, so naming the interpreter directory would only go stale the next time the site changes version.
