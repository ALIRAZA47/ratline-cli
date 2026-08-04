#!/bin/sh
# ratline installer.
#
# POSIX sh, no bashisms, and it does nothing surprising: every package it would
# install is named first and confirmed, and it never edits a configuration file
# it did not create.
set -eu

RATLINE_VERSION="${RATLINE_VERSION:-dev}"
PREFIX="${PREFIX:-/usr/local}"
CONF_DIR=/etc/ratline
LIB_DIR="$PREFIX/lib/ratline"
ASSUME_YES="${ASSUME_YES:-0}"

say()  { printf '%s\n' "$*"; }
step() { printf '→ %s\n' "$*"; }
warn() { printf '! %s\n' "$*" >&2; }
die()  { printf '✗ %s\n' "$*" >&2; exit 1; }

confirm() {
    [ "$ASSUME_YES" = "1" ] && return 0
    printf '%s [y/N]: ' "$1"
    read -r reply || return 1
    case "$reply" in y|Y|yes|YES) return 0 ;; *) return 1 ;; esac
}

[ "$(id -u)" = "0" ] || die "run this as root: sudo sh install.sh"

# --- detect the host -------------------------------------------------------
if [ -r /etc/os-release ]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    OS_ID="${ID:-unknown}"
    OS_VERSION="${VERSION_ID:-unknown}"
    OS_NAME="${PRETTY_NAME:-$OS_ID $OS_VERSION}"
else
    OS_ID=unknown; OS_VERSION=unknown; OS_NAME=unknown
fi
step "Host: $OS_NAME"

case "$OS_ID" in
    ubuntu|debian) ;;
    *) warn "ratline targets Ubuntu and Debian. On $OS_ID the filesystem layout may differ."
       confirm "Continue anyway?" || exit 1 ;;
esac

case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
esac

# --- dependencies ----------------------------------------------------------
MISSING=""
for cmd in nginx certbot; do
    command -v "$cmd" >/dev/null 2>&1 || MISSING="$MISSING $cmd"
done
if [ -n "$MISSING" ]; then
    say ""
    say "These are not installed:$MISSING"
    say "ratline needs nginx to serve sites, and certbot to issue certificates."
    if confirm "Install them with apt-get?"; then
        apt-get update
        # shellcheck disable=SC2086
        apt-get install -y $MISSING
    else
        warn "continuing without them; 'ratline doctor' will keep reminding you"
    fi
fi

# --- directories -----------------------------------------------------------
step "Creating directories"
install -d -o root -g root -m 0755 "$PREFIX/bin" "$LIB_DIR"
install -d -o root -g root -m 0755 "$CONF_DIR"
# Credentials, so 0700 rather than 0755.
install -d -o root -g root -m 0700 "$CONF_DIR/ssh" "$CONF_DIR/dns" "$CONF_DIR/certs"
install -d -o root -g root -m 0750 /var/lib/ratline /var/log/ratline
install -d -o root -g root -m 0755 /opt/ratline/runtimes /var/www/ratline-acme
install -d -o root -g root -m 0755 /var/www/ratline-acme/.well-known/acme-challenge
install -d -o root -g root -m 0700 /var/backups/ratline
install -d -o root -g root -m 0755 /etc/nginx/ratline /etc/nginx/ratline/custom

# --- binaries --------------------------------------------------------------
if [ -f "./ratline-linux-$ARCH" ]; then
    SRC_MAIN="./ratline-linux-$ARCH"; SRC_SHELL="./ratline-shell-linux-$ARCH"
elif [ -f ./bin/ratline ]; then
    SRC_MAIN=./bin/ratline; SRC_SHELL=./bin/ratline-shell
else
    die "no ratline binary found next to this script; run 'make dist' first"
fi

step "Installing binaries"
install -o root -g root -m 0755 "$SRC_MAIN" "$PREFIX/bin/ratline"
# The forced-command wrapper must not be writable by anyone but root: it runs on
# every site-scoped SSH connection, so a writable copy is a route to code
# execution as the tenant.
install -o root -g root -m 0755 "$SRC_SHELL" "$LIB_DIR/ratline-shell"

# --- configuration ---------------------------------------------------------
if [ -f "$CONF_DIR/config.yaml" ]; then
    step "Keeping the existing $CONF_DIR/config.yaml"
else
    step "Writing $CONF_DIR/config.yaml"
    "$PREFIX/bin/ratline" init --write-config-only 2>/dev/null || true
fi

# --- systemd ---------------------------------------------------------------
if command -v systemctl >/dev/null 2>&1; then
    step "Installing systemd units"
    for unit in packaging/systemd/*.timer packaging/systemd/*.service packaging/systemd/*.target; do
        [ -f "$unit" ] || continue
        install -o root -g root -m 0644 "$unit" /etc/systemd/system/
    done
    systemctl daemon-reload

    # certbot's own timer and ratline's would race, each reloading nginx from
    # under the other. ratline's runs the deploy hook that reloads only the
    # affected site, so certbot's is the one that goes.
    if systemctl list-unit-files 2>/dev/null | grep -q '^certbot\.timer'; then
        if systemctl is-enabled certbot.timer >/dev/null 2>&1; then
            step "Disabling certbot.timer so it does not race ratline's renewal timer"
            say "  (ratline's timer runs 'ratline cert renew --all', which reloads only"
            say "   the sites whose certificates actually changed)"
            systemctl disable --now certbot.timer
        fi
    fi

    for t in ratline-cert-renew.timer ratline-key-prune.timer; do
        [ -f "/etc/systemd/system/$t" ] || continue
        systemctl enable --now "$t" >/dev/null 2>&1 && step "Enabled $t"
    done
    systemctl enable ratline.target >/dev/null 2>&1 || true
fi

# --- completions and man ---------------------------------------------------
if [ -d /usr/share/bash-completion/completions ]; then
    "$PREFIX/bin/ratline" completion bash > /usr/share/bash-completion/completions/ratline 2>/dev/null || true
fi
if [ -d /usr/share/zsh/site-functions ]; then
    "$PREFIX/bin/ratline" completion zsh > /usr/share/zsh/site-functions/_ratline 2>/dev/null || true
fi
if [ -d /usr/share/man/man8 ]; then
    "$PREFIX/bin/ratline" man --dir /usr/share/man/man8 >/dev/null 2>&1 || true
    command -v mandb >/dev/null 2>&1 && mandb -q 2>/dev/null || true
fi

# --- firewall --------------------------------------------------------------
say ""
say "ratline does not change your firewall. It needs these ports reachable:"
say "    22    SSH"
say "    80    HTTP, and the ACME challenge — renewal breaks without it"
say "    443   HTTPS"
if command -v ufw >/dev/null 2>&1; then
    say ""
    say "  With ufw:  ufw allow 22/tcp && ufw allow 80/tcp && ufw allow 443/tcp"
fi

say ""
say "Installed. Next:"
say "    ratline init                     set the ACME email and the default runtimes"
say "    ratline user add acme            create your first tenant"
say "    ratline site add example.com --user acme --runtime static"
say "    ratline doctor                   confirm the server is healthy"
