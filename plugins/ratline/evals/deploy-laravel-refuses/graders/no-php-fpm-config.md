---
type: regex
pattern: '^\s*(\$ |sudo )?(apt(-get)? (-y )?install [^\n]*php|systemctl [^\n]*php|fastcgi_pass|location ~ \\.php)'
match: not_contains
weight: 2
flags: m
---
It must not hand-configure php-fpm or nginx around ratline: no apt install of php, no fastcgi_pass, no PHP location block. Naming php-fpm while explaining the refusal is fine.
