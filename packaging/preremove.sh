#!/bin/sh
set -e

# Only stop ratline's own timers. Site services are deliberately left running:
# removing the management tool must not take a customer's website offline, and
# the units are self-contained systemd files that keep working without it.
if command -v systemctl >/dev/null 2>&1; then
    for t in ratline-cert-renew.timer ratline-key-prune.timer; do
        systemctl disable --now "$t" >/dev/null 2>&1 || true
    done
fi

cat <<'MSG'

ratline has been removed. Deliberately left in place:

  the site services in /etc/systemd/system/ratline-*.service — your sites keep serving
  the nginx configuration in /etc/nginx/sites-available
  every tenant account and home directory
  /var/lib/ratline/state.db and /var/log/ratline
  /etc/ratline

To remove a site properly, reinstall ratline and use 'ratline site delete --purge',
which also removes the vhost, the unit, the logs and the port allocation.
MSG
