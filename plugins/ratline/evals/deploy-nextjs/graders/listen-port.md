---
type: regex
pattern: '--listen port'
match: contains
weight: 2
---
Next.js standalone binds HOSTNAME:PORT and cannot listen on a Unix socket, so the site must be created with --listen port.
