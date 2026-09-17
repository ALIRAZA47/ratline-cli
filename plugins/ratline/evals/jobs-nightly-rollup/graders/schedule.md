---
type: regex
pattern: '--schedule ''?(0 3 \* \* \*|\*-\*-\* 03:00(:00)?)''?'
match: contains
weight: 1
---
3am nightly as a cron expression (or the systemd equivalent).
