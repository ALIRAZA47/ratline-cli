---
type: regex
pattern: 'env set \S+ [A-Z_]*(SECRET|PASSWORD|TOKEN|DATABASE_URL|REDIS_URL|MONGODB_URI)[A-Z_]*=[^\s<$]'
match: not_contains
weight: 2
---
No secret is passed as a KEY=VALUE argument; secrets go in on stdin. A placeholder like KEY=<value> or KEY=$VAR is fine.
