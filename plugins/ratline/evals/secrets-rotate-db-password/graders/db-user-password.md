---
type: regex
pattern: 'db user password shop_app'
match: contains
weight: 3
---
The rotation goes through `ratline db user password shop_app`, which generates the password, sets it on the server and rewrites .env without the value passing through anyone.
