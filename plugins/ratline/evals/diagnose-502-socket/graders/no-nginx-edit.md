---
type: regex
pattern: '(proxy_pass|/etc/nginx/sites-|nginx -s reload|systemctl (restart|reload) nginx)'
match: not_contains
weight: 1
---
Does not propose editing or restarting nginx; nginx is not the fault.
