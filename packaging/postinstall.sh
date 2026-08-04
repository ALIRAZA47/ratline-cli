#!/bin/sh
set -e

# Directories the package cannot express, and the modes that matter.
install -d -o root -g root -m 0755 /etc/nginx/ratline /etc/nginx/ratline/custom
install -d -o root -g root -m 0755 /var/www/ratline-acme/.well-known/acme-challenge
install -d -o root -g root -m 0700 /var/backups/ratline

# Seed the configuration without overwriting an existing one.
if [ ! -f /etc/ratline/config.yaml ]; then
    /usr/local/bin/ratline init --write-config-only 2>/dev/null || true
fi

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true

    # certbot's timer and ratline's would race, each reloading nginx from under
    # the other. ratline's runs the deploy hook that reloads only the sites whose
    # certificates changed, so certbot's is the one that goes.
    if systemctl is-enabled certbot.timer >/dev/null 2>&1; then
        echo "ratline: disabling certbot.timer so it does not race ratline-cert-renew.timer."
        echo "ratline: renewal now runs 'ratline cert renew --all', which reloads only the"
        echo "ratline: sites whose certificates actually changed."
        systemctl disable --now certbot.timer || true
    fi

    systemctl enable ratline-cert-renew.timer >/dev/null 2>&1 || true
    systemctl enable ratline-key-prune.timer >/dev/null 2>&1 || true
    systemctl start ratline-cert-renew.timer >/dev/null 2>&1 || true
    systemctl start ratline-key-prune.timer >/dev/null 2>&1 || true
fi

cat <<'EOF'

ratline is installed. To finish setting up this server:

    ratline init                  set the ACME email and default runtimes
    ratline doctor                check what is missing

Ports 22, 80 and 443 need to be reachable. Port 80 in particular: the ACME
challenge is served there, and certificate renewal fails without it. ratline does
not change your firewall.
EOF
