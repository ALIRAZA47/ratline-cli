---
type: regex
pattern: '(>>\s*\S*authorized_keys|useradd|adduser)'
match: not_contains
weight: 1
---
authorized_keys is never edited by hand and no account is created outside ratline.
