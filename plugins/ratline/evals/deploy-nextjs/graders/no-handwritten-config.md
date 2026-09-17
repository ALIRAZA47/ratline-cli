---
type: regex
pattern: '(server\s*\{|listen 80;|proxy_pass|\[Service\]|ExecStart=|WantedBy=|/etc/nginx/sites-|systemctl (enable|start|restart) [a-z])'
match: not_contains
weight: 2
---
The plan never hand-writes an nginx vhost or a systemd unit, or drives systemctl directly: on a ratline server those files are ratline's, and a hand edit is refused or lost on reconcile.
