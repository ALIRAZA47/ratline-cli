---
type: regex
pattern: 'ratline db [^\n]*--engine redis'
match: contains
weight: 1
---
Redis is provisioned through ratline (`ratline db --engine redis create …` or `ratline db create … --engine redis`) rather than apt-get or a hand-written unit.
