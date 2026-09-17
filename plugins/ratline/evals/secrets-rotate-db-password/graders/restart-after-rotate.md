---
type: regex
pattern: 'ratline site restart shop\.acme\.example'
match: contains
weight: 2
---
`db user password` rewrites the .env but does not restart the application, which reads its environment at startup; the plan restarts the site afterwards.
