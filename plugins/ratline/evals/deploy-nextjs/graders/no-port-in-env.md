---
type: regex
pattern: 'env set \S+[^\n]*\bPORT='
match: not_contains
weight: 1
---
The plan does not set PORT in the site's environment; ratline allocates it.
