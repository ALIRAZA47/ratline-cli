#!/bin/sh
# ratline-panel installer.
#
#   curl -fsSL https://ratline.alirazakhan.me/panel.sh | sudo sh
#
# Installs the web panel onto a server that already has ratline. It resolves the
# latest release, downloads the binary for this architecture, verifies it against the
# release's own SHA256SUMS, installs it, and runs `ratline-panel install` to write the
# configuration, create the panel's database and start the service.
#
# It also creates the panel's first super admin and prints a generated password once.
# That is deliberate: "whoever opens it first becomes the administrator" is a window,
# and a window on a machine nobody is watching is how a server is lost. There is no
# default password and nothing to change afterwards except your own.
#
# It listens on the loopback. The script prints the tunnel command to reach it.
#
# POSIX sh, no bashisms. Same verification rule as ratline's own installer: every
# artefact is checked against SHA256SUMS before anything is installed, and a missing
# checksum file is a refusal.
set -eu

REPO="ALIRAZA47/ratline-cli"
PANEL_VERSION="${PANEL_VERSION:-latest}"
PREFIX="${PREFIX:-/usr/local}"
ASSUME_YES="${ASSUME_YES:-0}"
# Set to 1 to install the binary and stop, leaving `ratline-panel install` to you.
NO_INSTALL="${NO_INSTALL:-0}"
# Set to a domain to put the panel behind nginx with a certificate in the same run.
# Only do this if DNS already points here: an attempt against a name that does not
# resolve to this server spends one of five validations per hour and cannot succeed.
PANEL_DOMAIN="${PANEL_DOMAIN:-}"
PANEL_EMAIL="${PANEL_EMAIL:-}"
# The address of the panel's first super admin. Asked for if a terminal is available
# and this is unset; without either, the install refuses rather than inventing one.
PANEL_ADMIN_EMAIL="${PANEL_ADMIN_EMAIL:-}"
# Set to 1 to install without an account, leaving the panel for whoever opens it
# first to claim. Only sensible when it is about to be claimed immediately.
PANEL_NO_ADMIN="${PANEL_NO_ADMIN:-0}"

say()  { printf '%s\n' "$*"; }
step() { printf '→ %s\n' "$*"; }
warn() { printf '! %s\n' "$*" >&2; }
die()  { printf '✗ %s\n' "$*" >&2; exit 1; }

confirm() {
    [ "$ASSUME_YES" = "1" ] && return 0
    # Piped from curl, stdin is the script itself, so a prompt has to read from the
    # terminal. Without one there is nobody to ask and the safe answer is no.
    ( : </dev/tty ) 2>/dev/null || return 1
    printf '%s [y/N]: ' "$1"
    read -r reply </dev/tty 2>/dev/null || return 1
    case "$reply" in y|Y|yes|YES) return 0 ;; *) return 1 ;; esac
}

# ask prints a question and reads one line from the terminal. Same reasoning as
# confirm: stdin is the script, so the terminal is the only place to read from.
ask() {
    ( : </dev/tty ) 2>/dev/null || return 1
    printf '%s' "$1" >&2
    read -r reply </dev/tty 2>/dev/null || return 1
    printf '%s' "$reply"
}

[ "$(id -u)" = "0" ] || die "run this as root: curl -fsSL <url> | sudo sh"

# --- what we need to do the download ---------------------------------------
if command -v curl >/dev/null 2>&1; then
    fetch()    { curl -fsSL --retry 3 --retry-delay 2 -o "$2" "$1"; }
    fetchout() { curl -fsSL --retry 3 --retry-delay 2 "$1"; }
elif command -v wget >/dev/null 2>&1; then
    fetch()    { wget -q --tries=3 -O "$2" "$1"; }
    fetchout() { wget -q --tries=3 -O - "$1"; }
else
    die "neither curl nor wget is installed, so nothing can be downloaded"
fi

case "$(uname -s)" in
    Linux) ;;
    *) die "ratline-panel runs on Linux; this is $(uname -s)" ;;
esac

case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) die "unsupported architecture: $(uname -m). Releases carry amd64 and arm64." ;;
esac
step "Architecture: $ARCH"

# --- ratline itself --------------------------------------------------------
# Checked first, before anything is downloaded. The panel is a caller of this binary
# and refuses to start without it, so installing one without the other produces a
# service that fails at boot with the reason three levels down in the journal.
RATLINE="$PREFIX/bin/ratline"
[ -x "$RATLINE" ] || RATLINE="$(command -v ratline 2>/dev/null || true)"
if [ -z "$RATLINE" ] || [ ! -x "$RATLINE" ]; then
    die "ratline is not installed. The panel drives it; install it first:
    curl -fsSL https://ratline.alirazakhan.me/install.sh | sudo sh"
fi
step "Driving $($RATLINE version 2>/dev/null | head -1)"

# --- find the binary -------------------------------------------------------
# Beside the script means a release tarball or a checkout, and is used as-is.
# Otherwise the release is downloaded, which is the piped-from-curl case.
LOCAL=""
if [ -f "./ratline-panel-linux-$ARCH" ]; then
    LOCAL="./ratline-panel-linux-$ARCH"
elif [ -f ./bin/ratline-panel ]; then
    LOCAL=./bin/ratline-panel
fi

if [ -n "$LOCAL" ]; then
    step "Using the binary beside this script"
    SRC="$LOCAL"
    if [ -f ./SHA256SUMS ] && command -v sha256sum >/dev/null 2>&1; then
        step "Verifying against SHA256SUMS"
        sha256sum -c SHA256SUMS --ignore-missing >/dev/null \
            || die "the local checksums do not match"
    fi
else
    if [ "$PANEL_VERSION" = "latest" ]; then
        step "Resolving the latest release"
        TAG=$(fetchout "https://api.github.com/repos/$REPO/releases/latest" \
              | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
              | head -1)
        [ -n "$TAG" ] || die "could not resolve the latest release. Set PANEL_VERSION=vX.Y.Z and retry."
    else
        case "$PANEL_VERSION" in v*) TAG="$PANEL_VERSION" ;; *) TAG="v$PANEL_VERSION" ;; esac
    fi
    step "Installing $TAG"

    BASE="https://github.com/$REPO/releases/download/$TAG"
    WORK=$(mktemp -d) || die "could not create a temporary directory"
    trap 'rm -rf "$WORK"' EXIT INT TERM
    chmod 700 "$WORK"

    ASSET="ratline-panel-linux-$ARCH"
    step "Downloading $ASSET"
    fetch "$BASE/$ASSET" "$WORK/$ASSET" \
        || die "could not download $ASSET from $TAG. Does that release carry the panel for $ARCH?"

    # Not optional. An unverified binary installed as root, that then runs ratline as
    # root, is the hole this whole tool is careful about.
    command -v sha256sum >/dev/null 2>&1 \
        || die "sha256sum is not installed, so the download cannot be verified. Install coreutils."
    step "Verifying checksums"
    fetch "$BASE/SHA256SUMS" "$WORK/SHA256SUMS" \
        || die "$TAG publishes no SHA256SUMS, so the download cannot be verified. Refusing."
    ( cd "$WORK" && sha256sum -c SHA256SUMS --ignore-missing >/dev/null ) \
        || die "the downloaded file does not match the published checksum. Refusing to install."

    SRC="$WORK/$ASSET"
fi

# --- install ---------------------------------------------------------------
step "Installing the binary"
install -d -o root -g root -m 0755 "$PREFIX/bin"
install -o root -g root -m 0755 "$SRC" "$PREFIX/bin/ratline-panel"

PANEL="$PREFIX/bin/ratline-panel"
"$PANEL" version >/dev/null 2>&1 \
    || die "the installed binary does not run. Wrong architecture, or a corrupt download."

if [ "$NO_INSTALL" = "1" ]; then
    step "Skipping 'ratline-panel install' (NO_INSTALL=1)"
    exit 0
fi

# --- who the panel belongs to ----------------------------------------------
# Asked for before anything is written, so a missing answer is a question rather than
# a failure three steps in. The password is not asked for and not accepted here: the
# binary generates one and prints it once, which is both stronger than what somebody
# would type at a prompt piped through curl and impossible to leave in a history file.
set -- install
if [ "$PANEL_NO_ADMIN" = "1" ]; then
    warn "Installing with no account (PANEL_NO_ADMIN=1)."
    warn "Whoever reaches the panel first becomes its super admin. Claim it immediately."
    set -- "$@" --no-admin
else
    if [ -z "$PANEL_ADMIN_EMAIL" ]; then
        PANEL_ADMIN_EMAIL=$(ask "Email address for the panel's first super admin: " || true)
    fi
    [ -n "$PANEL_ADMIN_EMAIL" ] || die "no address for the first super admin.
    Re-run with PANEL_ADMIN_EMAIL set:
      curl -fsSL <url> | sudo PANEL_ADMIN_EMAIL=you@example.com sh"
    set -- "$@" --admin-email "$PANEL_ADMIN_EMAIL"
fi

# --- configuration, database and service -----------------------------------
# `install` owns all of it, from templates embedded in the binary, so this script
# needs nothing but the binary and the same code path runs whether the panel arrived
# by curl, .deb or make.
if [ -n "$PANEL_DOMAIN" ]; then
    [ -n "$PANEL_EMAIL" ] || die "PANEL_DOMAIN needs PANEL_EMAIL: a certificate needs an ACME contact"
    set -- "$@" --domain "$PANEL_DOMAIN" --email "$PANEL_EMAIL"
fi

"$PANEL" "$@"

say ""
if [ -n "$PANEL_DOMAIN" ]; then
    say "    open https://$PANEL_DOMAIN"
else
    say "The panel is on the loopback. From your own machine:"
    say ""
    say "    ssh -L 8420:127.0.0.1:8420 $(hostname -f 2>/dev/null || hostname)"
    say "    open http://localhost:8420"
    say ""
    say "Then, once DNS points here:"
    say "    ratline-panel domain set panel.example.com --email you@example.com"
fi
say ""
say "Check it any time with:  ratline-panel doctor"
