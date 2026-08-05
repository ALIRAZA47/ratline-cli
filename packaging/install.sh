#!/bin/sh
# ratline installer.
#
#   curl -fsSL https://ratline.alirazakhan.me/install.sh | sudo sh
#
# One command, and the server is ready for `ratline user add`. It resolves the latest
# release, downloads the binaries for this architecture, verifies them against the
# release's own SHA256SUMS, installs them, and runs `ratline init` to write the
# configuration, create the directory layout and start the renewal timer.
#
# POSIX sh, no bashisms. It does nothing surprising: every package it would install is
# named first and confirmed, it never edits a configuration file it did not create, and
# it refuses rather than guesses.
#
# On verification: piping a script from the network into a root shell is a real
# supply-chain risk, and ratline refuses unverified binaries everywhere else, so it would
# be incoherent for its own installer to shrug. Every artefact is checked against
# SHA256SUMS before anything is installed, and a missing checksum file is a refusal. What
# that cannot protect against is a compromised release — read the script before you pipe
# it, or download it and run it separately, which is the same two commands.
set -eu

REPO="ALIRAZA47/ratline-cli"
RATLINE_VERSION="${RATLINE_VERSION:-latest}"
PREFIX="${PREFIX:-/usr/local}"
CONF_DIR=/etc/ratline
LIB_DIR="$PREFIX/lib/ratline"
ASSUME_YES="${ASSUME_YES:-0}"
# Set to 1 to install the binaries and stop, leaving `ratline init` to the operator.
NO_INIT="${NO_INIT:-0}"

say()  { printf '%s\n' "$*"; }
step() { printf '→ %s\n' "$*"; }
warn() { printf '! %s\n' "$*" >&2; }
die()  { printf '✗ %s\n' "$*" >&2; exit 1; }

confirm() {
    [ "$ASSUME_YES" = "1" ] && return 0
    # Piped from curl, stdin is the script itself, so a prompt has to read from the
    # terminal. Without one there is nobody to ask and the safe answer is no.
    #
    # Opened in a subshell rather than tested with -r, and not with `exec`. Inside a
    # container /dev/tty exists as a device node and is still not openable, so -r passes
    # and the read then fails with a raw "cannot open /dev/tty" — after the question has
    # been printed. `exec` is worse: a failed redirection on it terminates a
    # non-interactive shell outright, which killed the installer mid-run. A subshell
    # contains both the failure and the message.
    ( : </dev/tty ) 2>/dev/null || return 1
    printf '%s [y/N]: ' "$1"
    read -r reply </dev/tty 2>/dev/null || return 1
    case "$reply" in y|Y|yes|YES) return 0 ;; *) return 1 ;; esac
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

# --- detect the host -------------------------------------------------------
if [ -r /etc/os-release ]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    OS_ID="${ID:-unknown}"
    OS_NAME="${PRETTY_NAME:-$OS_ID}"
else
    OS_ID=unknown; OS_NAME=unknown
fi
step "Host: $OS_NAME"

case "$OS_ID" in
    ubuntu|debian) ;;
    *) warn "ratline targets Ubuntu and Debian. On $OS_ID the filesystem layout may differ."
       confirm "Continue anyway?" || exit 1 ;;
esac

case "$(uname -s)" in
    Linux) ;;
    *) die "ratline runs on Linux; this is $(uname -s)" ;;
esac

case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) die "unsupported architecture: $(uname -m). Releases carry amd64 and arm64." ;;
esac
step "Architecture: $ARCH"

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

# --- find the binaries -----------------------------------------------------
# Two ways in. Beside the script means a release tarball or a checkout, and is used
# as-is. Otherwise the release is downloaded, which is the piped-from-curl case.
LOCAL_MAIN=""
if [ -f "./ratline-linux-$ARCH" ]; then
    LOCAL_MAIN="./ratline-linux-$ARCH"; LOCAL_SHELL="./ratline-shell-linux-$ARCH"
elif [ -f ./bin/ratline ]; then
    LOCAL_MAIN=./bin/ratline; LOCAL_SHELL=./bin/ratline-shell
fi

if [ -n "$LOCAL_MAIN" ]; then
    step "Using the binaries beside this script"
    SRC_MAIN="$LOCAL_MAIN"; SRC_SHELL="$LOCAL_SHELL"
    [ -f "$SRC_SHELL" ] || die "found $SRC_MAIN but not $SRC_SHELL; the pair is installed together"
    # A local SHA256SUMS is checked when present; a checkout will not have one.
    if [ -f ./SHA256SUMS ] && command -v sha256sum >/dev/null 2>&1; then
        step "Verifying against SHA256SUMS"
        sha256sum -c SHA256SUMS --ignore-missing >/dev/null \
            || die "the local checksums do not match"
    fi
else
    if [ "$RATLINE_VERSION" = "latest" ]; then
        step "Resolving the latest release"
        TAG=$(fetchout "https://api.github.com/repos/$REPO/releases/latest" \
              | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
              | head -1)
        [ -n "$TAG" ] || die "could not resolve the latest release. Set RATLINE_VERSION=vX.Y.Z and retry."
    else
        case "$RATLINE_VERSION" in v*) TAG="$RATLINE_VERSION" ;; *) TAG="v$RATLINE_VERSION" ;; esac
    fi
    step "Installing $TAG"

    BASE="https://github.com/$REPO/releases/download/$TAG"
    WORK=$(mktemp -d) || die "could not create a temporary directory"
    # Any exit removes it, including a failure partway through a download.
    trap 'rm -rf "$WORK"' EXIT INT TERM
    chmod 700 "$WORK"

    for asset in "ratline-linux-$ARCH" "ratline-shell-linux-$ARCH"; do
        step "Downloading $asset"
        fetch "$BASE/$asset" "$WORK/$asset" \
            || die "could not download $asset from $TAG. Does that release exist for $ARCH?"
    done

    # Verification is not optional. An unverified binary installed as root on a server
    # that will hold every tenant's keys is the hole this whole tool is careful about.
    command -v sha256sum >/dev/null 2>&1 \
        || die "sha256sum is not installed, so the download cannot be verified. Install coreutils."
    step "Verifying checksums"
    fetch "$BASE/SHA256SUMS" "$WORK/SHA256SUMS" \
        || die "$TAG publishes no SHA256SUMS, so the download cannot be verified. Refusing."
    ( cd "$WORK" && sha256sum -c SHA256SUMS --ignore-missing >/dev/null ) \
        || die "a downloaded file does not match the published checksum. Refusing to install."

    SRC_MAIN="$WORK/ratline-linux-$ARCH"; SRC_SHELL="$WORK/ratline-shell-linux-$ARCH"
fi

# --- install ---------------------------------------------------------------
step "Installing binaries"
install -d -o root -g root -m 0755 "$PREFIX/bin" "$LIB_DIR"
install -o root -g root -m 0755 "$SRC_MAIN" "$PREFIX/bin/ratline"
# The forced-command wrapper must not be writable by anyone but root: it runs on every
# site-scoped SSH connection, so a writable copy is a route to code execution as the
# tenant.
install -o root -g root -m 0755 "$SRC_SHELL" "$LIB_DIR/ratline-shell"

RATLINE="$PREFIX/bin/ratline"
"$RATLINE" version >/dev/null 2>&1 \
    || die "the installed binary does not run. Wrong architecture, or a corrupt download."

# --- configuration, directories and timers ---------------------------------
# `init` owns all of this, including the renewal timer, which it writes from templates
# embedded in the binary. That is deliberate: it means this script needs nothing but the
# binary, and the same code path runs whether ratline arrived by curl, .deb or make.
if [ "$NO_INIT" = "1" ]; then
    step "Skipping 'ratline init' (NO_INIT=1)"
elif [ -f "$CONF_DIR/config.yaml" ]; then
    step "Keeping the existing $CONF_DIR/config.yaml, refreshing directories and timers"
    "$RATLINE" init --write-config-only
elif confirm "Run 'ratline init' now to finish setup?"; then
    # Interactive: asks for the ACME contact address and the admin account.
    "$RATLINE" init </dev/tty || warn "'ratline init' did not finish; run it again when ready"
else
    step "Writing the configuration and starting the timers without prompting"
    "$RATLINE" init --write-config-only
fi

# --- completions and man ---------------------------------------------------
if [ -d /usr/share/bash-completion/completions ]; then
    "$RATLINE" completion bash > /usr/share/bash-completion/completions/ratline 2>/dev/null || true
fi
if [ -d /usr/share/zsh/site-functions ]; then
    "$RATLINE" completion zsh > /usr/share/zsh/site-functions/_ratline 2>/dev/null || true
fi
if [ -d /usr/share/man/man8 ]; then
    "$RATLINE" man --dir /usr/share/man/man8 >/dev/null 2>&1 || true
    command -v mandb >/dev/null 2>&1 && mandb -q 2>/dev/null || true
fi

# --- firewall --------------------------------------------------------------
say ""
say "ratline does not change your firewall. It needs these ports reachable:"
say "    22    SSH"
say "    80    HTTP, and the ACME challenge — renewal breaks without it"
say "    443   HTTPS"
if command -v ufw >/dev/null 2>&1; then
    say "  With ufw:  ufw allow 22/tcp && ufw allow 80/tcp && ufw allow 443/tcp"
fi

say ""
say "Installed $("$RATLINE" version 2>/dev/null | head -1)"
say ""
say "Next:"
say "    ratline runtime install node 22    if you will host Node sites"
say "    ratline user add acme             create your first tenant"
say "    ratline site add example.com --user acme --runtime static"
say "    ratline doctor                    confirm the server is healthy"
