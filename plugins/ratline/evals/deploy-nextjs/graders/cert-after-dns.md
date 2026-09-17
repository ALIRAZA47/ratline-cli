---
type: regex
pattern: '(dig |--ssl none|cert issue \S+[^\n]*--dry-run)'
match: contains
weight: 1
---
TLS is issued only after DNS is checked (dig) or deferred (--ssl none / a cert dry run), because a failed ACME attempt costs rate-limit budget.
