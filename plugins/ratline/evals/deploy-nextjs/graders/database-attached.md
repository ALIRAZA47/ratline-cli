---
type: regex
pattern: '(db create \S+ --owner acme[^\n]*--attach shop\.acme\.example|--with-db)'
match: contains
weight: 1
---
The database is created and attached to the site so the connection string is written into .env by ratline, never printed or pasted.
