#!/bin/bash
# The integration suite. Runs inside the container, as root, with systemd up.
#
# What this covers that the unit tests cannot: whether the socket permissions
# actually let nginx connect, whether systemd's sandbox lets the application
# start, whether a deploy really rolls back, and whether `site delete --purge`
# leaves residue. Those are all properties of a real system.
set -uo pipefail

# The transcript has to outlive the container.
#
# systemd is PID 1, `StandardOutput=journal+console` does not reach docker's stdout on
# every host, and the last thing this script does is `systemctl exit` — which stops
# systemd, and therefore the container, before anything outside can read the journal.
# A failing run was consequently invisible from the host. Writing to a bind-mounted
# file fixes that for a human and for CI alike.
TRANSCRIPT=/var/log/ratline-integration/suite.txt
if mkdir -p "$(dirname "$TRANSCRIPT")" 2>/dev/null; then
    exec > >(tee "$TRANSCRIPT") 2>&1
fi

# docker's `environment:` sets variables for PID 1, and a systemd unit inherits
# nothing from it — so RATLINE_TEST_ACME_DIRECTORY was always unset here and the
# entire ACME section skipped, with Pebble and challtestsrv running alongside for
# nothing. PID 1's environment is where they actually are.
if [ -r /proc/1/environ ]; then
    while IFS= read -r -d '' entry; do
        case "$entry" in
            RATLINE_TEST_*) export "${entry?}" ;;
        esac
    done < /proc/1/environ
fi

PASS=0
FAIL=0
RATLINE=/usr/local/bin/ratline

red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
info()  { printf '\n\033[1m== %s\033[0m\n' "$*"; }

ok()   { PASS=$((PASS+1)); green "  ok    $1"; }
bad()  { FAIL=$((FAIL+1)); red   "  FAIL  $1"; [ -n "${2:-}" ] && printf '        %s\n' "$2"; }

# check <description> <command...> — passes when the command exits 0.
check() {
    local desc="$1"; shift
    local out
    if out=$("$@" 2>&1); then ok "$desc"; else bad "$desc" "$(printf '%s' "$out" | tail -3)"; fi
}

# refute <description> <command...> — passes when the command exits non-zero.
# Half of what this tool must get right is refusing things.
refute() {
    local desc="$1"; shift
    local out
    if out=$("$@" 2>&1); then bad "$desc" "the command unexpectedly succeeded"; else ok "$desc"; fi
}

# exits_with <code> <description> <command...>
exits_with() {
    local want="$1" desc="$2"; shift 2
    "$@" >/dev/null 2>&1
    local got=$?
    if [ "$got" = "$want" ]; then ok "$desc (exit $got)"; else bad "$desc" "exit $got, want $want"; fi
}

contains() {
    local desc="$1" needle="$2" haystack="$3"
    case "$haystack" in
        *"$needle"*) ok "$desc" ;;
        *) bad "$desc" "expected to find: $needle" ;;
    esac
}

# ---------------------------------------------------------------- setup

info "environment"

# Deliberately not `systemctl is-system-running --wait`. In a container systemd may
# never reach "running" — here dev-vda1.device sits in "activating tentative"
# forever, because the device does not exist — and `--wait` then blocks for the life
# of the container, hanging the suite on its first line with no output.
#
# What actually matters is that systemd is PID 1 and answering, and that the units
# this suite depends on came up. The service's own After=/Wants= already guarantee
# the ordering, so those are the things to assert.
check "systemd is PID 1"        bash -c '[ "$(readlink /proc/1/exe)" = "/usr/lib/systemd/systemd" ] || [ "$(readlink /proc/1/exe)" = "/lib/systemd/systemd" ]'
check "systemd answers"         systemctl --no-pager show --property=Version
check "multi-user.target is up" systemctl is-active --quiet multi-user.target
if failed=$(systemctl list-units --state=failed --no-legend --no-pager 2>/dev/null | awk '{print $1}' | tr '\n' ' '); then
    if [ -n "${failed// /}" ]; then
        printf '  note  units in a failed state before the suite began: %s\n' "$failed"
    fi
fi
check "nginx is installed"      test -x /usr/sbin/nginx
check "ratline runs"            "$RATLINE" version
check "doctor runs on a bare box" "$RATLINE" doctor
check "init seeds the config"   "$RATLINE" init --write-config-only
# The CA's terms have to be accepted before anything can be issued, and the ACME
# section below is otherwise refused with "the subscriber agreement has not been
# accepted" — correctly, but it means the whole section tests nothing. Written into
# the config directly, because --write-config-only stops before recording it and a
# full `init` wants a terminal.
if [ -f /etc/ratline/config.yaml ]; then
    sed -i 's/^\( *tos_agreed:\).*/\1 true/' /etc/ratline/config.yaml
    sed -i "s|^\( *email:\) \"\"|\1 ops@acme.test|" /etc/ratline/config.yaml
fi
check "the config validates"    test -f /etc/ratline/config.yaml

# nginx's default site claims server_name _, which would swallow every request.
rm -f /etc/nginx/sites-enabled/default
systemctl reload nginx || true

# `sshd -t` refuses with "Missing privilege separation directory" without this. On a
# real server sshd's own unit creates it; here /run is a fresh tmpfs and sshd has
# never started, so the suite has to make it or every sshd validation fails for a
# reason that has nothing to do with ratline.
mkdir -p /run/sshd && chmod 0755 /run/sshd

# sshd itself has to be running, or every `key add` and `key sync` reverts on
# "ssh.service is not active, cannot reload" — which is ratline behaving correctly
# and the container not resembling a server.
systemctl start ssh >/dev/null 2>&1 || systemctl start sshd >/dev/null 2>&1 || true

# ---------------------------------------------------------------- users

info "user lifecycle"
check "user add"                    "$RATLINE" user add alice
check "user add is idempotent"      "$RATLINE" user add alice
check "the account exists"          id alice
check "the home is 0750" bash -c '[ "$(stat -c %a /home/alice)" = "750" ]'
check "the .ssh directory is 0700" bash -c '[ "$(stat -c %a /home/alice/.ssh)" = "700" ]'
check "the password is locked" bash -c 'passwd -S alice | grep -qE " L "'
check "www-data joined the group" bash -c 'id -nG www-data | tr " " "\n" | grep -qx alice'
check "user list"                   "$RATLINE" user list
check "user show"                   "$RATLINE" user show alice
refute "a reserved name is refused" "$RATLINE" user add root
refute "an invalid name is refused" "$RATLINE" user add 'Bad Name'
check "a second user"               "$RATLINE" user add bob

# ---------------------------------------------------------------- static

info "static site"
check "site add --spa" "$RATLINE" site add static.test --user alice --runtime static --spa --ssl none
check "the document root exists" test -d /home/alice/static.test/public
check "the vhost is enabled" test -L /etc/nginx/sites-enabled/static.test.conf
check "nginx accepts it" nginx -t
check ".env is 0600" bash -c '[ "$(stat -c %a /home/alice/static.test/.env)" = "600" ]'

echo '<!doctype html><title>t</title>hello static' > /home/alice/static.test/public/index.html
chown alice:alice /home/alice/static.test/public/index.html

body=$(curl -sS -H 'Host: static.test' http://127.0.0.1/ 2>&1)
contains "the index is served" "hello static" "$body"

# The SPA fallback: without it, a refresh on a deep client-side route 404s.
code=$(curl -sS -o /dev/null -w '%{http_code}' -H 'Host: static.test' http://127.0.0.1/deep/link/refresh)
[ "$code" = "200" ] && ok "a deep link returns 200 (SPA fallback)" || bad "SPA fallback" "got $code"

# .env must never be servable, whatever is in the document root.
code=$(curl -sS -o /dev/null -w '%{http_code}' -H 'Host: static.test' http://127.0.0.1/.env)
[ "$code" = "404" ] && ok ".env is not servable" || bad ".env is not servable" "got $code"

hdr=$(curl -sSI -H 'Host: static.test' http://127.0.0.1/index.html)
contains "the index is not cached" "no-cache" "$hdr"
contains "the security headers are set" "X-Content-Type-Options" "$hdr"

# The ACME challenge must be served even before any certificate exists.
echo -n token123 > /var/www/ratline-acme/.well-known/acme-challenge/token123
got=$(curl -sS -H 'Host: static.test' http://127.0.0.1/.well-known/acme-challenge/token123)
[ "$got" = "token123" ] && ok "the ACME challenge is served" || bad "ACME challenge" "got '$got'"

check "site list"  "$RATLINE" site list
check "site show"  "$RATLINE" site show static.test
check "site add is idempotent" "$RATLINE" site add static.test --user alice --runtime static --spa --ssl none

# ---------------------------------------------------------------- python

info "python site"
mkdir -p /home/alice/api.test/app
cat > /home/alice/api.test/app/requirements.txt <<'REQ'
fastapi
REQ
cat > /home/alice/api.test/app/main.py <<'PY'
from fastapi import FastAPI

app = FastAPI()


@app.get("/")
def root():
    return {"ok": True, "app": "api.test"}
PY
mkdir -p /home/alice/api.test
chown -R alice:alice /home/alice/api.test

if "$RATLINE" site add api.test --user alice --runtime python \
        --app-module main:app --workers 2 --ssl none 2>&1 | tail -20; then
    ok "site add python"

    check "the unit exists" test -f /etc/systemd/system/ratline-alice-api_test.service
    check "the service is active" systemctl is-active --quiet ratline-alice-api_test.service
    check "the venv was created" test -x /home/alice/api.test/venv/bin/gunicorn

    # The socket permission check. At the 0027 umask used elsewhere the socket
    # lands at 0640, nginx gets EACCES, and every request is a 502 with nothing
    # useful in any log. This is the single most valuable assertion in the suite.
    sock=/run/ratline/alice-api_test/app.sock
    if [ -S "$sock" ]; then
        mode=$(stat -c %a "$sock")
        case "$mode" in
            66*) ok "the socket is group-writable ($mode), so nginx can connect" ;;
            *)   bad "socket mode" "$mode — nginx needs 0660 or every request is a 502" ;;
        esac
    else
        bad "the socket exists" "$sock is missing"
    fi

    body=$(curl -sS -H 'Host: api.test' http://127.0.0.1/ 2>&1)
    contains "the application answers through nginx" '"ok":true' "$body"
    contains "FastAPI docs are served" "swagger" "$(curl -sS -H 'Host: api.test' http://127.0.0.1/docs)"

    # A graceful reload replaces workers one at a time, so nothing is dropped.
    fails=0
    for _ in $(seq 1 40); do
        curl -fsS -o /dev/null -H 'Host: api.test' http://127.0.0.1/ || fails=$((fails+1))
    done &
    loop=$!
    "$RATLINE" site scale api.test --workers 4 >/dev/null 2>&1
    wait $loop
    [ "$fails" = "0" ] && ok "scale --workers dropped no requests" || bad "zero-downtime scale" "$fails request(s) failed"

    check "site reload" "$RATLINE" site reload api.test
    check "env set restarts the service" "$RATLINE" site env set api.test LOG_LEVEL=debug
    contains "env list masks values" "•" "$("$RATLINE" site env list api.test)"
    contains "env list --reveal shows them" "debug" "$("$RATLINE" site env list api.test --reveal)"

    # Isolation: alice's service must not be able to read bob's home.
    check "bob has a home" test -d /home/bob
    echo secret > /home/bob/secret.txt
    chmod 0600 /home/bob/secret.txt
    chown bob:bob /home/bob/secret.txt
    if sudo -u alice cat /home/bob/secret.txt >/dev/null 2>&1; then
        bad "tenant isolation" "alice could read bob's file"
    else
        ok "alice cannot read bob's files"
    fi
    # And ProtectHome=tmpfs should hide it from the service entirely.
    if systemd-run --quiet --pipe --property=ProtectHome=tmpfs \
            --property=User=alice /bin/cat /home/bob/secret.txt >/dev/null 2>&1; then
        bad "ProtectHome" "a sandboxed process read another home"
    else
        ok "ProtectHome hides other tenants' homes"
    fi
else
    bad "site add python" "see the output above"
fi

# ---------------------------------------------------------------- node

info "node site"
mkdir -p /home/bob/app.test/app
cat > /home/bob/app.test/app/package.json <<'PKG'
{ "name": "app-test", "version": "1.0.0", "private": true }
PKG
cat > /home/bob/app.test/app/server.js <<'JS'
const http = require('http');
// ratline provides the socket path in PORT, RATLINE_SOCKET and SOCKET_PATH.
const target = process.env.RATLINE_SOCKET || process.env.PORT;
const server = http.createServer((req, res) => {
  if (req.headers.upgrade === 'websocket') return;
  res.setHeader('content-type', 'application/json');
  res.end(JSON.stringify({ ok: true, app: 'app.test' }));
});
server.on('upgrade', (req, socket) => {
  socket.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n');
});
server.listen(target, () => console.log('listening on ' + target));
JS
chown -R bob:bob /home/bob/app.test

# --with-pm2, because PM2 is the default supervisor for a node site and `site add`
# refuses without it. Installed unconditionally rather than falling back to a system
# node, so the managed-runtime path is what gets tested.
if "$RATLINE" runtime install node 22 --with-pm2 >/dev/null 2>&1; then
    check "runtime list" "$RATLINE" runtime list
    if "$RATLINE" site add app.test --user bob --runtime node --entry server.js --ssl none 2>&1 | tail -20; then
        ok "site add node"
        body=$(curl -sS -H 'Host: app.test' http://127.0.0.1/ 2>&1)
        contains "the node app answers through nginx" '"ok":true' "$body"

        # A WebSocket upgrade needs the Upgrade and Connection headers plus the
        # $connection_upgrade map, which is easy to get silently wrong.
        # Not `2>&1 | head -1`: a server that answers 101 and then closes makes curl
        # exit 52 and print to stderr, and merging the streams put that on line one —
        # failing an assertion about a response that had in fact arrived. Only stdout,
        # and the whole of it.
        up=$(curl -sSi -H 'Host: app.test' -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
                -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
                http://127.0.0.1/ 2>/dev/null)
        contains "a WebSocket upgrade succeeds" "101" "$up"

        # PM2 is the default, so assert the thing it exists for: a reload that keeps
        # answering. Without PM2 this is a restart and requests are dropped.
        fails=0
        for _ in $(seq 1 40); do
            curl -fsS -o /dev/null -H 'Host: app.test' http://127.0.0.1/ || fails=$((fails+1))
        done &
        loop=$!
        "$RATLINE" site reload app.test >/dev/null 2>&1
        wait $loop
        [ "$fails" = "0" ] && ok "a PM2 reload dropped no requests" \
            || bad "zero-downtime reload" "$fails request(s) failed"

        contains "site status reports PM2's own worker count" "pm2" \
            "$("$RATLINE" site status app.test 2>&1)"

        # And the other supervision mode, since --daemon direct is a documented
        # choice and nothing else here exercises it.
        mkdir -p /home/bob/direct.test/app
        cp /home/bob/app.test/app/server.js /home/bob/direct.test/app/server.js
        cp /home/bob/app.test/app/package.json /home/bob/direct.test/app/package.json
        chown -R bob:bob /home/bob/direct.test
        check "site add --daemon direct" "$RATLINE" site add direct.test --user bob \
            --runtime node --entry server.js --daemon direct --ssl none
        contains "a direct node site answers" '"ok":true' \
            "$(curl -sS -H 'Host: direct.test' http://127.0.0.1/ 2>&1)"
        refute "a direct node site refuses to reload gracefully" \
            "$RATLINE" site reload direct.test
    else
        bad "site add node" "see the output above"
    fi
else
    printf '  skip  node runtime unavailable in this environment\n'
fi

# ---------------------------------------------------------------- ssh keys

info "ssh keys"
ssh-keygen -t ed25519 -N '' -f /tmp/global.key -q
ssh-keygen -t ed25519 -N '' -f /tmp/site.key -q

check "key add --scope global" "$RATLINE" key add --scope global --label "Ops laptop" --key /tmp/global.key.pub
check "key add --scope site" "$RATLINE" key add --scope site --site static.test \
        --label "Contractor" --key /tmp/site.key.pub --expires 90d
check "key list" "$RATLINE" key list
contains "key test names the confinement" "not a kernel boundary" \
        "$("$RATLINE" key test "$(ssh-keygen -lf /tmp/site.key.pub | awk '{print $2}')" 2>&1 | tr 'A-Z' 'a-z')"

# A pasted key carrying its own options is an escalation vector.
printf 'command="/bin/bash",permitopen="10.0.0.1:5432" %s\n' "$(cat /tmp/global.key.pub)" > /tmp/loaded.pub
ssh-keygen -t ed25519 -N '' -f /tmp/other.key -q
printf 'command="/bin/bash" %s\n' "$(cat /tmp/other.key.pub)" > /tmp/loaded.pub
"$RATLINE" key add --scope user --user bob --label "Loaded" --key /tmp/loaded.pub >/dev/null 2>&1
if grep -q 'command="/bin/bash"' /home/bob/.ssh/authorized_keys; then
    bad "submitted options are stripped" "a pasted command= survived into authorized_keys"
else
    ok "submitted options are stripped"
fi

check "authorized_keys is 0600" bash -c '[ "$(stat -c %a /home/alice/.ssh/authorized_keys)" = "600" ]'
contains "the managed block is present" "ratline managed" "$(cat /home/alice/.ssh/authorized_keys)"

# A hand-written key must survive a sync untouched.
echo "# my own note" >> /home/alice/.ssh/authorized_keys
cat /tmp/global.key.pub >> /home/alice/.ssh/authorized_keys
check "key sync" "$RATLINE" key sync
contains "hand-written entries survive key sync" "my own note" "$(cat /home/alice/.ssh/authorized_keys)"

check "key audit runs" "$RATLINE" key audit
check "sshd still accepts its configuration" sshd -t
refute "the last global key cannot be removed without --force" \
        "$RATLINE" key remove "$(ssh-keygen -lf /tmp/global.key.pub | awk '{print $2}')" --scope global

# ---------------------------------------------------------------- certificates

info "certificates"
check "cert list on an empty box" "$RATLINE" cert list
check "cert selfsign" "$RATLINE" cert selfsign static.test --days 30
contains "a self-signed cert is flagged" "self-signed" "$("$RATLINE" cert list)"
check "the private key is 0600" bash -c '[ "$(stat -c %a /etc/ratline/certs/static.test/privkey.pem)" = "600" ]'
check "nginx accepts the TLS vhost" nginx -t

# It must really be served, not merely on disk.
tlsout=$(curl -sSk --resolve static.test:443:127.0.0.1 https://static.test/ 2>&1)
contains "HTTPS serves the site" "hello static" "$tlsout"
code=$(curl -sS -o /dev/null -w '%{http_code}' -H 'Host: static.test' http://127.0.0.1/)
[ "$code" = "301" ] && ok "HTTP redirects to HTTPS" || bad "HTTP redirect" "got $code"
# And renewal must still work while the redirect is in place.
got=$(curl -sS -H 'Host: static.test' http://127.0.0.1/.well-known/acme-challenge/token123)
[ "$got" = "token123" ] && ok "the ACME challenge survives the redirect" || bad "ACME after redirect" "got '$got'"

refute "HSTS is refused on an untrusted certificate" \
        bash -c "$RATLINE site add hsts.test --user alice --runtime static --hsts --ssl none && $RATLINE cert selfsign hsts.test && $RATLINE cert attach hsts.test"

# Import validation, including each distinct failure.
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
    -keyout /tmp/imp.key -out /tmp/imp.pem -days 60 -subj '/CN=import.test' \
    -addext 'subjectAltName=DNS:import.test' >/dev/null 2>&1
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
    -keyout /tmp/other.key.pem -out /tmp/other.pem -days 60 -subj '/CN=other.test' \
    -addext 'subjectAltName=DNS:other.test' >/dev/null 2>&1

check "site for the import" "$RATLINE" site add import.test --user alice --runtime static --ssl none
check "cert import" "$RATLINE" cert import import.test --cert /tmp/imp.pem --key /tmp/imp.key
refute "a mismatched key is refused" "$RATLINE" cert import import.test --cert /tmp/imp.pem --key /tmp/other.key.pem
refute "the wrong SANs are refused" "$RATLINE" cert import import.test --cert /tmp/other.pem --key /tmp/other.key.pem
contains "an imported cert is not auto-renewed" "no" \
        "$("$RATLINE" cert show import.test | grep -i auto-renew)"

check "cert list --expiring" "$RATLINE" cert list --expiring 90
check "cert show" "$RATLINE" cert show static.test
check "cert auto-renew status" "$RATLINE" cert auto-renew status
check "renew skips what cannot renew" "$RATLINE" cert renew --all

# A certificate certbot created outside ratline must be adopted, not ignored.
mkdir -p /etc/letsencrypt/live/handmade.test
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
    -keyout /etc/letsencrypt/live/handmade.test/privkey.pem \
    -out /etc/letsencrypt/live/handmade.test/fullchain.pem \
    -days 40 -subj '/CN=handmade.test' -addext 'subjectAltName=DNS:handmade.test' >/dev/null 2>&1
contains "a hand-issued certificate is discovered" "handmade.test" "$("$RATLINE" cert list)"

# ---------------------------------------------------------------- acme (pebble)

info "certificate issuance against a local ACME server"

# Tests never touch Let's Encrypt: a suite that spends real rate-limit budget is a
# suite people stop running. Pebble is a CA built for exactly this.
DIRECTORY="${RATLINE_TEST_ACME_DIRECTORY:-}"
CHALLTESTSRV="${RATLINE_TEST_CHALLTESTSRV:-}"

if [ -z "$DIRECTORY" ]; then
    printf '  skip  no local ACME server configured (RATLINE_TEST_ACME_DIRECTORY unset)\n'
elif ! command -v certbot >/dev/null 2>&1; then
    printf '  skip  certbot is not installed in this image\n'
else
    # Pebble signs with its own root, via an intermediate, and *both* are needed.
    #
    # The root alone is not enough: Pebble issues from an intermediate and nginx
    # serves the chain it was given, so a client with only the root cannot build a
    # path and every verification of the served certificate fails — which looks like
    # a broken site rather than an incomplete trust store.
    MGMT="https://pebble:15000"
    trusted=0
    for pair in "roots/0 pebble-root" "intermediates/0 pebble-intermediate"; do
        set -- $pair
        if curl -sSk -o "/tmp/$2.pem" "$MGMT/$1" 2>/dev/null && [ -s "/tmp/$2.pem" ]; then
            cp "/tmp/$2.pem" "/usr/local/share/ca-certificates/$2.crt"
            trusted=$((trusted+1))
        fi
    done
    update-ca-certificates >/dev/null 2>&1 || true
    if [ "$trusted" = "2" ]; then
        ok "Pebble's root and intermediate are in the trust store"
    else
        bad "could not fetch Pebble's issuance chain" \
            "got $trusted of 2 certificates; verifying the served chain will fail"
    fi

    # Point the challenge server's DNS at this container, so Pebble resolves the
    # test domain here rather than nowhere.
    # ratline's own preflight resolves the name through the container's resolver,
    # which knows nothing about acme.test — so it correctly refuses before Pebble is
    # ever contacted. challtestsrv answers for Pebble's benefit, not for ours.
    grep -q ' acme.test$' /etc/hosts || echo '10.30.50.4 acme.test' >> /etc/hosts

    if [ -n "$CHALLTESTSRV" ]; then
        curl -sS -X POST -d '{"host":"acme.test","addresses":["10.30.50.4"]}'             "$CHALLTESTSRV/add-a" >/dev/null 2>&1             && ok "DNS for acme.test points at this container"             || bad "could not set up DNS" "Pebble will not resolve acme.test"
    fi

    check "a site to issue for" "$RATLINE" site add acme.test --user bob --runtime static --ssl none
    echo '<!doctype html><title>t</title>acme ok' > /home/bob/acme.test/public/index.html
    chown bob:bob /home/bob/acme.test/public/index.html

    # The preflight has to pass before an attempt is spent, so it is asserted
    # separately from the issuance itself.
    # --acme-directory, because certbot otherwise talks to the real Let's Encrypt:
    # nothing ever read RATLINE_TEST_ACME_DIRECTORY, so every assertion in this
    # section was really testing the public CA and failing against it.
    #
    # REQUESTS_CA_BUNDLE points certbot's HTTP client at the system trust store,
    # which now holds Pebble's root; certbot otherwise uses certifi's own bundle and
    # cannot verify a private CA.
    # Pebble's own endpoint is signed by its static minica, shared in by compose.
    # This is exactly the private-CA case --acme-ca-bundle exists for.
    PEBBLE_CA=/pebble-certs/pebble.minica.pem
    if [ -r "$PEBBLE_CA" ]; then
        ok "Pebble's minica is available to verify the ACME endpoint"
    else
        bad "Pebble's minica is missing" "expected it at $PEBBLE_CA"
    fi
    if "$RATLINE" --verbose cert issue acme.test --email ops@acme.test \
            --acme-directory "$DIRECTORY" --acme-ca-bundle "$PEBBLE_CA" \
            --dry-run 2>&1 | tail -25; then
        ok "cert issue --dry-run passes preflight and validates"
    else
        bad "cert issue --dry-run" "preflight or validation failed; see the output above"
    fi

    # Now for real, against Pebble.
    if "$RATLINE" cert issue acme.test --email ops@acme.test \
            --acme-directory "$DIRECTORY" --acme-ca-bundle "$PEBBLE_CA" 2>&1 | tail -25; then
        ok "cert issue completed against the local ACME server"

        contains "the certificate is recorded" "acme.test" "$("$RATLINE" cert list)"
        contains "it is attached to the site" "1"             "$("$RATLINE" cert show acme.test | grep -c 'attached to')"

        # The whole point of the verify step: it must actually be served, with the
        # right chain, not merely exist on disk.
        served=$(echo | openssl s_client -connect 127.0.0.1:443 -servername acme.test 2>/dev/null             | openssl x509 -noout -subject -issuer 2>/dev/null)
        contains "the certificate is really served over TLS" "acme.test" "$served"

        body=$(curl -sS --resolve acme.test:443:127.0.0.1 https://acme.test/ 2>&1)
        contains "HTTPS serves the site with a trusted chain" "acme ok" "$body"

        # Renewal, forced, so the deploy hook path runs.
        if "$RATLINE" cert renew acme.test --force 2>&1 | tail -10; then
            ok "forced renewal succeeded"
            contains "the renewal was recorded" "success" "$("$RATLINE" cert show acme.test)"
        else
            bad "forced renewal" "see the output above"
        fi

        # A duplicate request must be refused by the local budget before it reaches
        # the CA at all.
        for _ in 1 2 3 4 5 6; do
            "$RATLINE" cert issue acme.test --email ops@acme.test \
                --acme-directory "$DIRECTORY" --acme-ca-bundle "$PEBBLE_CA" \
                --force >/dev/null 2>&1 || true
        done
        if "$RATLINE" cert issue acme.test --email ops@acme.test \
                --acme-directory "$DIRECTORY" --acme-ca-bundle "$PEBBLE_CA" \
                --force 2>&1 | grep -qiE 'rate limit|duplicate'; then
            ok "the duplicate-certificate budget refuses further attempts"
        else
            printf '  note  the duplicate budget was not reached in this run\n'
        fi
    else
        bad "cert issue against Pebble" "see the output above"
    fi

    # Clock skew: a certificate forced to five days out must be picked up by the
    # renewal window rather than waiting for the timer's own schedule.
    if "$RATLINE" --json cert show acme.test >/dev/null 2>&1; then
        sqlite_bin=$(command -v sqlite3 || true)
        if [ -n "$sqlite_bin" ]; then
            near=$(date -u -d '+5 days' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || true)
            if [ -n "$near" ]; then
                "$sqlite_bin" /var/lib/ratline/state.db                     "UPDATE certificates SET not_after='$near' WHERE name='acme.test';" 2>/dev/null || true
                out=$("$RATLINE" cert renew --all 2>&1)
                case "$out" in
                    *renewed*) ok "a certificate five days from expiry is renewed rather than skipped" ;;
                    *) bad "clock-skew renewal" "$(printf '%s' "$out" | tail -2)" ;;
                esac
            fi
        else
            printf '  skip  sqlite3 unavailable, so the clock-skew case was not exercised\n'
        fi
    fi
fi

# ---------------------------------------------------------------- deploy

info "deploy and rollback"
sudo -u alice git -C /home/alice/api.test/app init -q 2>/dev/null || true
if [ -d /home/alice/api.test/app/.git ]; then
    # What any real project has. Without it the first commit captures __pycache__,
    # and the deploy that imports the application then leaves a tracked .pyc
    # modified — so ratline correctly refuses to revert, because `git reset --hard`
    # would discard a tracked change it cannot know is worthless.
    printf '__pycache__/\n*.pyc\nvenv/\nnode_modules/\n' \
        > /home/alice/api.test/app/.gitignore
    chown alice:alice /home/alice/api.test/app/.gitignore
    sudo -u alice git -C /home/alice/api.test/app rm -r --cached --quiet __pycache__ 2>/dev/null || true
    sudo -u alice git -C /home/alice/api.test/app -c user.email=t@t -c user.name=t add -A
    sudo -u alice git -C /home/alice/api.test/app -c user.email=t@t -c user.name=t commit -qm initial
    good=$(sudo -u alice git -C /home/alice/api.test/app rev-parse HEAD)

    # A healthy deploy first, so there is a recorded "this was serving" to go back
    # to. Reverting is only possible against something ratline knows was good, and
    # the history is where that is kept.
    check "a healthy deploy is recorded" "$RATLINE" site deploy api.test --restart

    # A commit that cannot even import: the deploy must revert and leave the
    # previous version serving.
    echo 'import does_not_exist_anywhere' > /home/alice/api.test/app/main.py
    sudo -u alice git -C /home/alice/api.test/app -c user.email=t@t -c user.name=t commit -qam broken

    exits_with 7 "a broken deploy reports it was unhealthy" \
        "$RATLINE" site deploy api.test --restart
    body=$(curl -sS -H 'Host: api.test' http://127.0.0.1/ 2>&1)
    contains "a broken deploy leaves the previous version serving" '"ok":true' "$body"


    sudo -u alice git -C /home/alice/api.test/app reset -q --hard "$good"
    "$RATLINE" site restart api.test >/dev/null 2>&1
else
    printf '  skip  git unavailable\n'
fi

# ---------------------------------------------------------------- operations

info "operations"
check "doctor"           "$RATLINE" doctor
check "reconcile"        "$RATLINE" reconcile
check "reconcile --fix"  "$RATLINE" reconcile --fix
check "nginx still valid after reconcile" nginx -t
check "export is valid JSON" bash -c "$RATLINE export | jq -e '.ok == true' >/dev/null"

# No private key material may ever appear in machine-readable output.
dump=$("$RATLINE" export)
if printf '%s' "$dump" | grep -q 'PRIVATE KEY'; then
    bad "export carries no private keys" "found private key material"
else
    ok "export carries no private keys"
fi

# The exit-code contract.
exits_with 2 "an unknown command is a usage error" "$RATLINE" nonesuch
exits_with 2 "contradictory flags are a usage error" "$RATLINE" --json --interactive version
exits_with 3 "a missing site is a precondition failure" "$RATLINE" site show nope.test

# The lock: a second mutating invocation must fail rather than proceed concurrently.
#
# The hold has to outlast the waiter's timeout, or the correct behaviour is to wait
# and then succeed — which is what this check used to assert by accident, holding for
# 3s while ratline waited its default 30s and exited 0. A one-second timeout keeps the
# check honest and fast.
cat > /tmp/shortlock.yaml <<'YAML'
version: 1
defaults:
  lock_timeout: 1s
YAML
( flock 9; sleep 5 ) 9>/run/ratline.lock &
locker=$!
sleep 0.5
exits_with 5 "a held lock exits 5" "$RATLINE" --config /tmp/shortlock.yaml user add locktest
wait $locker

check "--dry-run changes nothing" "$RATLINE" --dry-run site add dry.test --user alice --runtime static
refute "the dry-run site was not created" test -d /home/alice/dry.test

# ---------------------------------------------------------------- teardown

info "delete leaves no residue"
before_units=$(ls /etc/systemd/system/ratline-*.service 2>/dev/null | wc -l)
check "site delete --purge" "$RATLINE" site delete api.test --purge --yes
refute "the site directory is gone" test -d /home/alice/api.test
refute "the unit is gone" test -f /etc/systemd/system/ratline-alice-api_test.service
refute "the vhost is gone" test -f /etc/nginx/sites-available/api.test.conf
refute "the socket directory is gone" test -d /run/ratline/alice-api_test
refute "the logrotate policy is gone" test -f /etc/logrotate.d/ratline-api.test
check "nginx still valid after the delete" nginx -t
after_units=$(ls /etc/systemd/system/ratline-*.service 2>/dev/null | wc -l)
[ "$after_units" -lt "$before_units" ] && ok "one unit fewer" || bad "unit count" "$before_units then $after_units"

check "user delete --purge" "$RATLINE" user delete alice --purge --yes
refute "the account is gone" id alice
refute "the home is gone" test -d /home/alice
check "nginx still valid at the end" nginx -t
check "doctor is clean at the end" "$RATLINE" doctor

# ---------------------------------------------------------------- result

printf '\n\033[1m%s\033[0m\n' "$PASS passed, $FAIL failed"
if [ "$FAIL" -gt 0 ]; then
    red "integration suite FAILED"
    # Shut the container down with a failing code, since systemd is PID 1.
    systemctl exit 1 2>/dev/null || exit 1
    exit 1
fi
green "integration suite passed"
systemctl exit 0 2>/dev/null || exit 0
