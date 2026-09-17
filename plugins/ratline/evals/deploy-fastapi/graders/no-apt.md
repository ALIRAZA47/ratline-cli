---
type: regex
pattern: 'apt(-get)? (-y )?install'
match: not_contains
weight: 1
---
Nothing is installed with apt by hand; ratline's `db install`/`db --engine` is the only installer.
