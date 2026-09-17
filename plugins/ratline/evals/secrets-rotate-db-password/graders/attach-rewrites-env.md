---
type: regex
pattern: '(--attach shop\.acme\.example|--all-sites)'
match: contains
weight: 1
---
The rotated URI is written into the site's .env by ratline (--attach or --all-sites).
