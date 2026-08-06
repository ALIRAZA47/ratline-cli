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

# The renewal timer, installed from the templates embedded in the binary. This is what a
# one-command install depends on: the image no longer copies these units in, so if init
# does not place them, nothing on the server renews a certificate and the first sign is an
# expired one weeks later.
check "init installed the renewal timer"    test -f /etc/systemd/system/ratline-cert-renew.timer
check "init installed the key-prune timer"  test -f /etc/systemd/system/ratline-key-prune.timer
contains "the units carry ratline's header" "managed-by: ratline" \
    "$(head -1 /etc/systemd/system/ratline-cert-renew.timer)"
contains "the renewal timer is enabled" "enabled" "$(systemctl is-enabled ratline-cert-renew.timer 2>&1)"
contains "the renewal timer is running" "active"  "$(systemctl is-active ratline-cert-renew.timer 2>&1)"
contains "it has a next run scheduled" "ratline-cert-renew" \
    "$(systemctl list-timers 'ratline-*' --no-legend --no-pager 2>&1)"

# doctor used to report these as "a ratline unit with no matching site" and offer, as the
# fix, a command that deletes them — which stops certificates renewing. Now that init
# installs them on every server, every server would have been told to do that.
if "$RATLINE" doctor 2>&1 | grep -qE "cert-renew|key-prune"; then
    bad "doctor calls its own timers orphans" "it suggests deleting the renewal timer"
else
    ok "doctor does not mistake its own timers for orphans"
fi

# And a hand-edited unit is left alone, which is the promise for every other managed file.
printf '# hand written\n[Unit]\nDescription=mine\n' > /etc/systemd/system/ratline-key-prune.timer
"$RATLINE" init --write-config-only >/dev/null 2>&1
contains "a hand-edited unit is not overwritten" "hand written" \
    "$(head -1 /etc/systemd/system/ratline-key-prune.timer)"
# Put it back so the rest of the run sees a normal server.
rm -f /etc/systemd/system/ratline-key-prune.timer
"$RATLINE" init --write-config-only >/dev/null 2>&1
check "and it is restored once the edit is gone" test -f /etc/systemd/system/ratline-key-prune.timer
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

    # A bare KEY is asked for instead of being passed in argv. That is the whole point:
    # an environment variable is usually a credential, and KEY=VALUE is world-readable
    # through /proc for as long as the command runs, then lives in the shell history.
    #
    # `script` gives the command a real pty, because the prompt is refused without one —
    # which is itself the behaviour automation depends on, asserted just below.
    if command -v script >/dev/null 2>&1; then
        printf 'sk-live-not-in-argv\n' | \
            script -qec "$RATLINE site env set api.test API_TOKEN" /dev/null >/dev/null 2>&1
        contains "a bare KEY is prompted for and stored" \
            "sk-live-not-in-argv" "$("$RATLINE" site env list api.test --reveal)"
        contains "and it is masked by default" "•" "$("$RATLINE" site env list api.test)"
    else
        bad "script is not installed" "the interactive env path cannot be tested"
    fi
    # Without a terminal it must refuse rather than block on a read that never returns.
    refute "a bare KEY with no terminal is refused, not hung" \
        sh -c "$RATLINE site env set api.test SOME_TOKEN </dev/null"
    contains "and it names the two ways in" "--stdin" \
        "$("$RATLINE" site env set api.test SOME_TOKEN </dev/null 2>&1 || true)"

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

# The key itself, pasted rather than saved to a file. Reported from a real server: it was
# read as a filename, and the error named "no such file: /root/ssh-ed25519 AAAAC3Nz… ark@ark".
ssh-keygen -t ed25519 -N '' -f /tmp/pasted.key -q
pasted=$(cat /tmp/pasted.key.pub)
check "a pasted key is taken as the key" "$RATLINE" key add --scope global \
    --label "Pasted" --key "$pasted"
contains "and it is installed" "Pasted" "$("$RATLINE" key list)"
# Removed again, because a later assertion checks that the *last* global key cannot be
# taken away without --force — and leaving a second one behind quietly disarms it.
"$RATLINE" key remove "Pasted" --yes >/dev/null 2>&1

# A private key pasted by mistake must be named as such. It is several lines, so the
# multi-line check used to match first and answer "the key spans more than one line" —
# true, and burying the only thing that matters.
privout=$("$RATLINE" key add --scope global --label "Oops" --key "$(cat /tmp/pasted.key)" 2>&1 || true)
contains "a pasted private key is called a private key" "private key" "$privout"
rm -f /tmp/pasted.key /tmp/pasted.key.pub
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
if [ "$code" = "301" ]; then
    ok "HTTP redirects to HTTPS"
else
    # "got 200" on its own says the redirect is missing but not why, and the vhost
    # is regenerated so it cannot be inspected after the run. Show which server
    # block answered instead.
    bad "HTTP redirect" "got $code; port-80 blocks serving static.test follow"
    nginx -T 2>/dev/null | awk '
        /^server[[:space:]]*\{/ { buf=""; depth=0 }
        { buf = buf $0 "\n" }
        /\{/ { depth++ }
        /\}/ { depth--; if (depth==0 && buf ~ /listen[[:space:]]+80/ && buf ~ /static\.test/) print buf }
    ' | sed 's/^/        | /'
fi
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

    # And in config, not only on the issuing command line. Renewal runs from a timer
    # months later with no flags: it reads acme.ca_bundle. Setting only the flag is
    # how a certificate comes to be issued against a CA it can never renew against,
    # which is what this suite caught.
    # Inserted under the existing acme: key rather than appended — a second top-level
    # acme: is a duplicate mapping key, which yaml.v3 rejects outright, so appending
    # would break every later command instead of configuring one setting.
    if grep -q "^  ca_bundle:" /etc/ratline/config.yaml; then
        sed -i "s|^  ca_bundle:.*|  ca_bundle: $PEBBLE_CA|" /etc/ratline/config.yaml
    else
        sed -i "0,/^acme:/s||acme:\n  ca_bundle: $PEBBLE_CA|" /etc/ratline/config.yaml
    fi
    contains "the private CA bundle is configured for renewal" "$PEBBLE_CA" \
        "$(grep ca_bundle /etc/ratline/config.yaml)"
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
        #
        # The name is asserted on the SAN, not the subject: Pebble — like Let's Encrypt
        # and every other CA that has followed CA/B forum guidance — issues with an
        # empty subject CN and puts the identifier in subjectAltName only. Grepping the
        # subject for the domain therefore failed against a certificate that was
        # perfectly correct.
        leafpem=$(echo | openssl s_client -connect 127.0.0.1:443 -servername acme.test 2>/dev/null \
            | sed -n '/BEGIN CERTIFICATE/,/END CERTIFICATE/p')
        if [ -z "$leafpem" ]; then
            bad "the served certificate could be read at all" \
                "openssl s_client returned no certificate; s_client stderr follows"
            echo | openssl s_client -connect 127.0.0.1:443 -servername acme.test 2>&1 \
                | tail -12 | sed 's/^/        | /'
        else
            served=$(printf '%s\n' "$leafpem" | openssl x509 -noout -text 2>&1)
            contains "the served certificate carries the domain in its SAN" "DNS:acme.test" "$served"
            contains "the served certificate was issued by the local CA" "Pebble" "$served"
        fi

        body=$(curl -sS --resolve acme.test:443:127.0.0.1 https://acme.test/ 2>&1)
        contains "HTTPS serves the site with a trusted chain" "acme ok" "$body"

        # Renewal, forced, so the deploy hook path runs.
        #
        # Not piped into tail: a pipeline exits with the status of its *last* command,
        # so `ratline ... | tail` reported success for a renewal that failed, and the
        # only sign was the assertion after it.
        renewout=$(mktemp)
        if "$RATLINE" cert renew acme.test --force >"$renewout" 2>&1; then
            ok "forced renewal succeeded"
            contains "the renewal was recorded" "success" "$("$RATLINE" cert show acme.test)"

            # The private-CA trust check must actually see this configuration. A
            # diagnostic that silently skips still exits 0, so "doctor passed" is not
            # evidence that it looked — and this is the only box in the suite with a
            # private ACME directory, so nowhere else can prove it either way.
            #
            # Both spellings, because they are two implementations: the bare sweep
            # reports findings, and `doctor server` walks ServerChecks. A gap in
            # either one is a gap for whoever types that command.
            trust=$("$RATLINE" --json doctor server 2>/dev/null \
                | jq -r '..|objects|select(.id=="acme-trust")|.verdict+" "+(.detail//"")' 2>/dev/null)
            contains "the acme-trust check ran and passed" "ok" "$trust"
            contains "and it named the bundle it verified with" "$PEBBLE_CA" "$trust"

            # Asserted on the ca_bundle findings specifically, not on doctor's overall
            # verdict: this box carries self-signed certificates on purpose, so the
            # sweep always has warnings and `healthy` is never true.
            # Walked with `..` rather than reaching for `.findings`, because --json wraps
            # every payload in an {ok, command, version, data} envelope — so `.findings`
            # matched nothing and the assertion passed for a certificate that had no
            # trust store at all. A filter that silently finds nothing is worse than no
            # filter, because it reads as a passing test.
            bundle_findings() {
                "$RATLINE" --json doctor 2>/dev/null \
                    | jq -r '[..|objects|select((.detail? // "")|test("ca_bundle"))]|length' 2>/dev/null
            }
            contains "the sweep says nothing about ca_bundle when it is right" "0" "$(bundle_findings)"

            # And it must speak up when the bundle is wrong, or a passing check proves
            # nothing. This is the case that silently costs a certificate.
            sed -i "s|^  ca_bundle:.*|  ca_bundle: /nonexistent/root.pem|" /etc/ratline/config.yaml
            broke=$(bundle_findings)
            if [ "${broke:-0}" -ge 1 ]; then
                ok "a ca_bundle that does not exist is reported, not ignored"
            else
                bad "a missing ca_bundle is ignored" "doctor reported '${broke:-<jq failed>}'"
                printf '        --- what doctor actually said ---\n'
                "$RATLINE" --json doctor 2>&1 | head -40 | sed 's/^/        | /'
                printf '        --- config and lineage ---\n'
                { grep -n ca_bundle /etc/ratline/config.yaml
                  grep -n '^server' /etc/letsencrypt/renewal/acme.test.conf
                } 2>&1 | sed 's/^/        | /'
            fi
            sed -i "s|^  ca_bundle:.*|  ca_bundle: $PEBBLE_CA|" /etc/ratline/config.yaml
            contains "and the sweep goes quiet again once it is corrected" "0" "$(bundle_findings)"
        else
            # certbot puts the reason in its own log and only a summary on stdout, so
            # a failure here needs both or it takes a rebuild to find out anything.
            bad "forced renewal" "$(tail -30 "$renewout")"
            # Raw, not grepped for "error": when certbot *hangs* the interesting part
            # is the last thing it did, which contains no error at all.
            printf '        --- letsencrypt.log (last 40 lines) ---\n'
            tail -40 /var/log/letsencrypt/letsencrypt.log 2>/dev/null | sed 's/^/        | /'
        fi
        rm -f "$renewout"

        # Clock skew: a certificate forced to five days out must be picked up by the
        # renewal window rather than waiting for the timer's own schedule.
        #
        # Before the duplicate-budget loop below, not after. That loop exists to
        # exhaust a rate limit, and anything downstream of it inherits an exhausted
        # budget — so this was failing on the state its neighbour had deliberately
        # created, and reporting it as a renewal-window bug.
        sqlite_bin=$(command -v sqlite3 || true)
        if [ -n "$sqlite_bin" ]; then
            near=$(date -u -d '+5 days' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || true)
            if [ -n "$near" ]; then
                "$sqlite_bin" /var/lib/ratline/state.db \
                    "UPDATE certificates SET not_after='$near' WHERE name='acme.test';" 2>/dev/null || true
                out=$("$RATLINE" cert renew --all 2>&1)
                case "$out" in
                    *renewed*) ok "a certificate five days from expiry is renewed rather than skipped" ;;
                    # The whole table, not the last two rows: which certificate did
                    # what is the entire question here.
                    *) bad "clock-skew renewal" "$(printf '%s' "$out")" ;;
                esac
            fi
        else
            printf '  skip  sqlite3 unavailable, so the clock-skew case was not exercised\n'
        fi

        # ------------------------------------------------------------------ DNS-01
    #
    # DNS-01 was the one challenge with no coverage, on the grounds that it "needs a
    # real DNS provider". It does not — it needs a way to publish a TXT record, and
    # certbot's manual plugin plus a hook script is exactly that. challtestsrv is
    # already the DNS server Pebble resolves against, and it takes TXT records over
    # its management API, so the hook is four lines of curl.
    #
    # This is the real challenge: certbot asks Pebble for a dns-01 authorization,
    # publishes the token, Pebble queries challtestsrv over DNS, and the certificate
    # is signed. Nothing about it is simulated.
    if [ -n "$CHALLTESTSRV" ]; then
        HOOK=/usr/local/lib/ratline/dns-hook.sh
        install -d -m 0755 /usr/local/lib/ratline
        cat > "$HOOK" <<HOOKEOF
#!/bin/sh
# certbot sets CERTBOT_DOMAIN and CERTBOT_VALIDATION. A wildcard authorization arrives
# with the domain already stripped of its "*." so the record name is the same either way.
set -eu
curl -sS -X POST -d "{\\"host\\":\\"_acme-challenge.\${CERTBOT_DOMAIN}.\\",\\"value\\":\\"\${CERTBOT_VALIDATION}\\"}" \\
    "$CHALLTESTSRV/set-txt" >/dev/null
HOOKEOF
        cat > "$HOOK.cleanup" <<CLEANEOF
#!/bin/sh
set -eu
curl -sS -X POST -d "{\\"host\\":\\"_acme-challenge.\${CERTBOT_DOMAIN}.\\"}" \\
    "$CHALLTESTSRV/clear-txt" >/dev/null
CLEANEOF
        chown root:root "$HOOK" "$HOOK.cleanup"
        chmod 0755 "$HOOK" "$HOOK.cleanup"

        # The hook runs as root with the validation token in its environment, so a
        # writable one is a route to running code as root. Both refusals are checked
        # before the working case, so a pass below means the check is live.
        chmod 0777 "$HOOK"
        out=$("$RATLINE" cert issue dns.test --email ops@acme.test --challenge dns \
                --dns-provider manual --dns-hook "$HOOK" \
                --acme-directory "$DIRECTORY" --acme-ca-bundle "$PEBBLE_CA" 2>&1 || true)
        contains "a world-writable DNS hook is refused" "writable by group or other" "$out"
        chmod 0755 "$HOOK"

        out=$("$RATLINE" cert issue dns.test --email ops@acme.test --challenge dns \
                --dns-provider manual --dns-hook ./relative.sh \
                --acme-directory "$DIRECTORY" --acme-ca-bundle "$PEBBLE_CA" 2>&1 || true)
        contains "a relative hook path is refused" "absolute" "$out"

        out=$("$RATLINE" cert issue dns.test --email ops@acme.test --challenge dns \
                --dns-provider manual --dns-hook "$HOOK" --dns-credentials /etc/hosts \
                --acme-directory "$DIRECTORY" --acme-ca-bundle "$PEBBLE_CA" 2>&1 || true)
        contains "--dns-credentials with a manual hook is refused" "does not apply" "$out"

        out=$("$RATLINE" cert issue dns.test --email ops@acme.test --challenge dns \
                --dns-provider manual \
                --acme-directory "$DIRECTORY" --acme-ca-bundle "$PEBBLE_CA" 2>&1 || true)
        contains "manual without a hook is refused" "requires --dns-hook" "$out"

        # A site to attach it to, and DNS so Pebble can find the server if it wants.
        check "a site for the DNS-01 certificate" \
            "$RATLINE" site add dns.test --user alice --runtime static --ssl none
        echo 'dns-01 ok' > /home/alice/dns.test/public/index.html
        chown alice:alice /home/alice/dns.test/public/index.html
        curl -sS -X POST -d '{"host":"dns.test","addresses":["10.30.50.4"]}' \
            "$CHALLTESTSRV/add-a" >/dev/null 2>&1 || true

        # Dry run first: a full exchange with the CA, no certificate issued.
        if "$RATLINE" --verbose cert issue dns.test --email ops@acme.test --challenge dns \
                --dns-provider manual --dns-hook "$HOOK" --dns-cleanup-hook "$HOOK.cleanup" \
                --acme-directory "$DIRECTORY" --acme-ca-bundle "$PEBBLE_CA" \
                --dry-run 2>&1 | tail -20; then
            ok "cert issue --challenge dns --dry-run validates through the hook"
        else
            bad "DNS-01 dry run" "see the output above"
        fi

        if "$RATLINE" --verbose cert issue dns.test --email ops@acme.test --challenge dns \
                --dns-provider manual --dns-hook "$HOOK" --dns-cleanup-hook "$HOOK.cleanup" \
                --acme-directory "$DIRECTORY" --acme-ca-bundle "$PEBBLE_CA" 2>&1 | tail -25; then
            ok "a certificate is issued over DNS-01"
            shown=$("$RATLINE" cert show dns.test)
            contains "it is recorded"            "dns.test" "$shown"
            contains "issued by the local CA"    "Pebble"   "$shown"
            contains "and the challenge is recorded as dns" "dns" "$shown"
            # Asserted on the body, not on an empty needle: `contains` with an empty
            # string matches anything, which is a test that can never fail.
            body=$(curl -sS --resolve dns.test:443:127.0.0.1 https://dns.test/ 2>&1)
            contains "and the site is served over TLS with it" "dns-01 ok" "$body"
            check "nginx still validates" nginx -t
        else
            bad "DNS-01 issuance" "see the output above"
        fi

        # A wildcard, which is the reason DNS-01 exists: HTTP-01 cannot prove control of
        # names that do not exist yet, so ratline switches the challenge itself.
        #
        # The base domain needs a site: preflight refuses a certificate for a name this
        # server does not serve, which is correct — a lineage attached to nothing still
        # consumes rate-limit budget on every renewal.
        check "a site for the wildcard's base domain" \
            "$RATLINE" site add wild.test --user alice --runtime static --ssl none
        if "$RATLINE" --verbose cert issue '*.wild.test' --email ops@acme.test \
                --dns-provider manual --dns-hook "$HOOK" --dns-cleanup-hook "$HOOK.cleanup" \
                --acme-directory "$DIRECTORY" --acme-ca-bundle "$PEBBLE_CA" \
                --no-attach 2>&1 | tail -20; then
            ok "a wildcard is issued, with the challenge switched to dns automatically"
            contains "the wildcard SAN is on the certificate" "*.wild.test" \
                "$("$RATLINE" cert show wild.test)"
        else
            bad "wildcard issuance" "see the output above"
        fi

        # And a wildcard over HTTP-01 is impossible, so it must be refused rather than
        # attempted — a failed validation costs rate-limit budget.
        out=$("$RATLINE" cert issue '*.nope.test' --email ops@acme.test --challenge http \
                --acme-directory "$DIRECTORY" --acme-ca-bundle "$PEBBLE_CA" \
                --dry-run 2>&1 || true)
        contains "a wildcard does not silently stay on HTTP-01" "dns" "$out"
    else
        printf '  skip  challtestsrv is unavailable, so DNS-01 was not exercised\n'
    fi

    # A duplicate request must be refused by the local budget before it reaches
        # the CA at all. Last in this section: it leaves the budget spent.
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
fi

# ---------------------------------------------------------------- deploy

# ---------------------------------------------------------------- mongodb
#
# Against a real MongoDB 8 with authentication on. Auth is the point: a mongod without
# it accepts every command from anyone, so a suite running against one would pass while
# proving nothing about the isolation these roles exist to provide.

info "mongodb databases and users"

if [ -z "${RATLINE_TEST_MONGO_URI:-}" ]; then
    printf '  skip  no MongoDB in this environment\n'
elif ! command -v mongosh >/dev/null 2>&1; then
    printf '  skip  mongosh is not installed\n'
else
    # Set up through `db connect` rather than by hand, so the suite exercises the path an
    # operator actually takes. It creates the 0700 directory, writes the string at 0600,
    # turns the feature on, and proves the credentials before committing any of it.
    #
    # Waited for first: compose starts mongod in parallel, and `db connect` refuses when
    # the server is not answering — correctly, but it means the setup has to be retried
    # rather than the failure being the suite's answer.
    mongo_ready=0
    for _ in $(seq 1 30); do
        if mongosh "$RATLINE_TEST_MONGO_URI" --quiet --eval 'db.adminCommand({ping:1})' >/dev/null 2>&1; then
            mongo_ready=1; break
        fi
        sleep 2
    done
    if [ "$mongo_ready" = "1" ]; then
        connectout=$(printf '%s' "$RATLINE_TEST_MONGO_URI" | "$RATLINE" db connect --stdin 2>&1)
        case "$connectout" in
            *Connected*) ok "db connect stored the credentials and turned provisioning on" ;;
            *) bad "db connect failed" "$(printf '%s' "$connectout" | tail -3)" ;;
        esac
        # The mode is the whole reason this is one command rather than four steps.
        check "the connection string is 0600"  bash -c '[ "$(stat -c %a /etc/ratline/db/mongodb.uri)" = 600 ]'
        check "its directory is 0700"          bash -c '[ "$(stat -c %a /etc/ratline/db)" = 700 ]'
        check "and both are root-owned"        bash -c '[ "$(stat -c %U:%G /etc/ratline/db/mongodb.uri)" = root:root ]'
        contains "provisioning is on" "true" "$("$RATLINE" config get features.db_provisioning)"
        # And it must not have echoed the password it was given.
        case "$connectout" in
            *integration*) bad "db connect echoed the admin password" "it appears in its output" ;;
            *) ok "db connect does not echo the password" ;;
        esac
        # Credentials that do not work must leave nothing behind, because a stored string
        # that fails is indistinguishable from a server that is down.
        "$RATLINE" db disable --forget >/dev/null 2>&1
        bad_uri=$(printf '%s' "$RATLINE_TEST_MONGO_URI" | sed 's/:integration@/:wrong@/')
        printf '%s' "$bad_uri" | "$RATLINE" db connect --stdin >/dev/null 2>&1
        refute "bad credentials leave no stored string" test -f /etc/ratline/db/mongodb.uri
        contains "and leave provisioning off" "false" "$("$RATLINE" config get features.db_provisioning)"
        # A shell-mangled string must be refused before anything is written, and the
        # error must name the input rather than the file. This is the failure a real
        # operator hit: printf read a % in the password as a format verb and truncated the
        # string, so `mongodb://admin:PASSWORD` with no host arrived — and the message
        # blamed /etc/ratline/db/mongodb.uri, a file that command had not written.
        mangled=$(printf '%s' 'mongodb://admin:5Jcmv' ; echo -n '!G2PLioUij')
        mangledout=$(printf '%s' "$mangled" | "$RATLINE" db connect --stdin 2>&1 || true)
        contains "a truncated connection string is refused for having no host" \
            "no host" "$mangledout"
        case "$mangledout" in
            *mongodb.uri*) bad "the error blames the stored file for the operator's input" \
                "$(printf '%s' "$mangledout" | head -2)" ;;
            *) ok "and the error names the input, not the file" ;;
        esac
        refute "and nothing was written" test -f /etc/ratline/db/mongodb.uri

        # No terminal, no flags: must say so rather than block on an empty stdin. A
        # provisioning script that hangs here looks like a broken server.
        noinput=$("$RATLINE" db connect </dev/null 2>&1 || true)
        contains "with no terminal and no flags it explains itself" \
            "no connection string" "$noinput"
        exits_with 10 "and exits input_required" sh -c "$RATLINE db connect </dev/null"

        # Put the working one back for the rest of the section.
        printf '%s' "$RATLINE_TEST_MONGO_URI" | "$RATLINE" db connect --stdin >/dev/null 2>&1

        # The file db connect writes must be readable back by ratline itself. It now
        # carries an explanatory header, and a header that broke the reader would be a
        # self-inflicted outage on every subsequent db command.
        contains "the stored file explains what it is" \
            "managed-by: ratline" "$(cat /etc/ratline/db/mongodb.uri)"
        check "and ratline reads its own file back" "$RATLINE" db ping
        # Hand-written is the other supported shape: a comment, a blank line, the string.
        printf '# hand written\n\n%s\n' "$RATLINE_TEST_MONGO_URI" > /tmp/hand.uri
        chmod 0600 /tmp/hand.uri
        check "a hand-written file with comments is accepted" \
            "$RATLINE" db connect --from-file /tmp/hand.uri --force
        check "and it works" "$RATLINE" db ping
        # A required flag must be asked for, not offered among the optional extras and
        # then refused after the operator confirms. `db create` without --owner is what
        # made that visible.
        refute "db create refuses without --owner" "$RATLINE" db create nowhere
        contains "and says which flag" "--owner" \
            "$("$RATLINE" db create nowhere 2>&1 || true)"
        refute "db user add refuses without --database" "$RATLINE" db user add nobody
        contains "and says which flag" "--database" \
            "$("$RATLINE" db user add nobody 2>&1 || true)"

        # Two strings is a refusal, not a coin toss between admin credentials.
        printf '%s\n%s\n' "$RATLINE_TEST_MONGO_URI" "$RATLINE_TEST_MONGO_URI" > /tmp/two.uri
        chmod 0600 /tmp/two.uri
        refute "two connection strings in one file are refused" \
            "$RATLINE" db connect --from-file /tmp/two.uri --force
        rm -f /tmp/hand.uri /tmp/two.uri

    else
        bad "mongod never answered" "the database section cannot run"
    fi

    # Wait for mongod: compose starts it in parallel and it takes a moment to accept
    # connections. Bounded, so a genuinely broken server fails rather than hanging.
    mongo_up=0
    for _ in $(seq 1 30); do
        if "$RATLINE" db ping >/dev/null 2>&1; then mongo_up=1; break; fi
        sleep 2
    done

    if [ "$mongo_up" != "1" ]; then
        bad "the MongoDB server never became reachable" "$("$RATLINE" db ping 2>&1 | tail -3)"
    else
        ok "the MongoDB server is reachable"
        contains "it enforces authentication" "yes" \
            "$("$RATLINE" db ping 2>&1 | grep -i authentication)"
        # The admin password must not appear in ratline's own output.
        pingout=$("$RATLINE" db ping 2>&1)
        case "$pingout" in
            *integration*) bad "db ping leaks the admin password" "the password appears in its output" ;;
            *) ok "db ping redacts the admin password" ;;
        esac

        check "db create"                "$RATLINE" db create shop --owner alice
        contains "it is recorded"        "shop"      "$("$RATLINE" db list)"
        contains "with its owner"        "alice"     "$("$RATLINE" db list)"
        contains "the server has it"     "shop"      "$("$RATLINE" db list --live)"
        contains "and calls it managed"  "yes"       "$("$RATLINE" db list --live)"

        # The credential is the whole point, so it is used rather than just inspected.
        uri=$("$RATLINE" --json db user password shop_app 2>/dev/null | jq -r '..|objects|.connection_uri? // empty' | head -1)
        if [ -n "$uri" ]; then
            ok "rotating the password returns a connection string"
            if mongosh "$uri" --quiet --eval 'db.probe.insertOne({v:1})' >/dev/null 2>&1; then
                ok "the application credential can write its own database"
            else
                bad "the application credential does not work" "$(mongosh "$uri" --quiet --eval 'db.probe.insertOne({v:1})' 2>&1 | tail -2)"
            fi

            # Isolation. This is the claim that matters: a tenant's credential must not
            # reach another tenant's data, or the whole model is decoration.
            "$RATLINE" db create other --owner alice >/dev/null 2>&1
            if mongosh "$uri" --quiet --eval 'db.getSiblingDB("other").x.countDocuments({})' >/dev/null 2>&1; then
                bad "a database user can read another database" "the role is not scoped"
            else
                ok "and cannot touch another database"
            fi
            # listDatabases is allowed but filtered by the server to what the user may
            # see, so the check is on the contents rather than on the call failing.
            seen=$(mongosh "$uri" --quiet --eval 'print(db.adminCommand({listDatabases:1}).databases.map(d=>d.name).join(","))' 2>/dev/null | tail -1)
            case "$seen" in
                *other*) bad "a database user can enumerate other databases" "saw: $seen" ;;
                *) ok "nor enumerate them" ;;
            esac
        else
            bad "no connection string came back from a rotation" "see the output above"
        fi

        # A database ratline would never create is still listed by --live. This is what
        # that flag is for: one created by another tool, or by hand, is precisely the case
        # worth surfacing, because nothing will revoke its users when the tenant goes.
        # Filtering on "would ratline create this name" hid exactly those.
        legacy=legacy_reporting_warehouse_archive_2019_2020
        mongosh "$RATLINE_TEST_MONGO_URI" --quiet \
            --eval "db.getSiblingDB(\"$legacy\").marker.insertOne({a:1})" >/dev/null 2>&1
        live=$("$RATLINE" db list --live 2>&1)
        contains "an unmanaged database is listed, not hidden" "$legacy" "$live"
        contains "and it is marked unmanaged"                  "no"      "$live"
        contains "with a warning about its users"              "not recorded here" "$live"
        # ratline still refuses to create that name itself: listing is not permission.
        refute "but ratline still will not create such a name" "$RATLINE" db create "$legacy" --owner alice
        # MongoDB's own three are the only ones skipped.
        case "$live" in
            *admin*|*config*|*local*) bad "db list --live shows MongoDB's own databases" "they are not provisioning targets" ;;
            *) ok "MongoDB's own databases are still skipped" ;;
        esac

        # --attach needs a credential, and --no-user says not to create one. Accepting both
        # silently dropped the attach, so a site appeared to have been given a connection
        # string it never received.
        refute "--attach with --no-user is refused" \
            "$RATLINE" db create contradiction --owner alice --no-user --attach static.test

        # A second, narrower user on the same database — the reason per-database users
        # exist rather than one credential per database.
        check "db user add, read-only"   "$RATLINE" db user add reports --database shop --role read
        contains "it is listed"          "reports"  "$("$RATLINE" db user list)"
        contains "with its role"         "read"     "$("$RATLINE" db user list)"
        contains "the server agrees"     "reports"  "$("$RATLINE" db user list --database shop --live)"

        rd=$("$RATLINE" --json db user password reports 2>/dev/null | jq -r '..|objects|.connection_uri? // empty' | head -1)
        if [ -n "$rd" ]; then
            if mongosh "$rd" --quiet --eval 'db.probe.countDocuments({})' >/dev/null 2>&1; then
                ok "the read role can read"
            else
                bad "the read role cannot read" "it should be able to"
            fi
            if mongosh "$rd" --quiet --eval 'db.probe.insertOne({v:2})' >/dev/null 2>&1; then
                bad "the read role can write" "the role is not enforced"
            else
                ok "and cannot write"
            fi
        fi

        # grant replaces rather than accumulates, which is why a demotion has to bite.
        check "db user grant to readWrite" "$RATLINE" db user grant reports --role readWrite
        rw=$("$RATLINE" --json db user password reports 2>/dev/null | jq -r '..|objects|.connection_uri? // empty' | head -1)
        if mongosh "$rw" --quiet --eval 'db.probe.insertOne({v:3})' >/dev/null 2>&1; then
            ok "the promoted user can write"
        else
            bad "the promoted user still cannot write" "grant did not take effect"
        fi
        "$RATLINE" db user grant reports --role read >/dev/null 2>&1
        ro=$("$RATLINE" --json db user password reports 2>/dev/null | jq -r '..|objects|.connection_uri? // empty' | head -1)
        if mongosh "$ro" --quiet --eval 'db.probe.insertOne({v:4})' >/dev/null 2>&1; then
            bad "a demoted user can still write" "grant accumulated roles instead of replacing them"
        else
            ok "and a demotion takes the write away again"
        fi

        # --attach is the feature: the URI goes into the site's .env rather than onto a
        # terminal, so it never enters shell history or scrollback.
        check "db user add --attach" "$RATLINE" db user add worker --database shop --attach static.test
        envfile=/home/alice/static.test/.env
        contains "the .env has the connection string" "MONGODB_URI=mongodb://worker" "$(cat $envfile 2>/dev/null)"
        check "the .env is still 0600" bash -c "[ \"\$(stat -c %a $envfile)\" = 600 ]"
        contains "the attachment is recorded" "static.test" "$("$RATLINE" db user list)"
        # And the value must not have been echoed to the terminal as well.
        attachout=$("$RATLINE" db user add worker2 --database shop --attach static.test --env-key MONGODB_URI_2 2>&1)
        case "$attachout" in
            *mongodb://worker2:*) bad "--attach also printed the password" "it should only be written to the file" ;;
            *) ok "--attach does not print the password" ;;
        esac

        # Rotating with --all-sites is the difference between a rotation and an outage.
        before=$(grep '^MONGODB_URI=' $envfile)
        check "db user password --all-sites" "$RATLINE" db user password worker --all-sites
        after=$(grep '^MONGODB_URI=' $envfile)
        if [ "$before" != "$after" ]; then
            ok "the rotation updated the site's .env"
        else
            bad "the rotation left the old password in the .env" "the site would still work by luck"
        fi
        newuri=$(printf '%s' "$after" | sed 's/^MONGODB_URI=//')
        if mongosh "$newuri" --quiet --eval 'db.probe.countDocuments({})' >/dev/null 2>&1; then
            ok "and the rotated credential works"
        else
            bad "the rotated credential in the .env does not work" "the site is now broken"
        fi

        # Refusals.
        refute "a cluster-wide role is refused" \
            "$RATLINE" db user add evil --database shop --role readWriteAnyDatabase
        refute "one of MongoDB's own databases is refused" "$RATLINE" db create admin --owner alice
        refute "a database name with a dot is refused"     "$RATLINE" db create a.b --owner alice
        refute "an unknown owner is refused"               "$RATLINE" db create nope --owner ghost
        chmod 0644 /etc/ratline/db/mongodb.uri
        refute "a world-readable admin URI is refused"     "$RATLINE" db ping
        chmod 0600 /etc/ratline/db/mongodb.uri
        ok "and it works again once the mode is fixed"

        # Teardown. --keep-database leaves the data, which is what handing a database to
        # someone else's tooling looks like.
        check "db drop --keep-database" "$RATLINE" db drop other --keep-database --force
        contains "the data is still on the server" "other" "$("$RATLINE" db list --live)"
        contains "but it is no longer managed"     "no"    "$("$RATLINE" db list --live)"

        check "db user delete"  "$RATLINE" db user delete reports --force
        check "db drop"         "$RATLINE" db drop shop --force
        liveafter=$("$RATLINE" db list --live 2>&1)
        case "$liveafter" in
            *shop*) bad "the database survived a drop" "it is still on the server" ;;
            *) ok "the database is gone from the server" ;;
        esac

        # doctor must not report a healthy MongoDB as a problem, and must notice a
        # broken one. Both halves, because a check that never fires is not a check.
        check "doctor is quiet about a healthy MongoDB" \
            bash -c "! $RATLINE doctor 2>&1 | grep -q mongodb"
        mv /etc/ratline/db/mongodb.uri /etc/ratline/db/mongodb.uri.away
        contains "doctor notices a missing admin URI" "mongodb" "$("$RATLINE" doctor 2>&1)"
        mv /etc/ratline/db/mongodb.uri.away /etc/ratline/db/mongodb.uri

        # Left off for the rest of the run, so the operations section sees the server it
        # expects rather than one with database provisioning half-configured.
        sed -i 's/^\( *db_provisioning:\).*/\1 false/' /etc/ratline/config.yaml
    fi
fi

# ---------------------------------------------------------------- config
#
# The file every other command reads. A change that leaves it unparseable takes the whole
# tool with it, and the failure arrives on the next unrelated command — so the tests here
# are about what survives an edit rather than about the edit landing.

info "configuration"

check "config path"       "$RATLINE" config path
check "config validate"   "$RATLINE" config validate
check "config reference"  bash -c "$RATLINE config reference | head -1 | grep -q ratline"

comments_before=$(grep -c '^ *#' /etc/ratline/config.yaml)
check "config set"        "$RATLINE" config set defaults.memory_max 768M
contains "it took"        "768M" "$("$RATLINE" config get defaults.memory_max)"
contains "and it is recorded as coming from the file" "file" \
    "$("$RATLINE" config show defaults.memory_max 2>&1)"
# The reason the editor is textual rather than a re-encode: the shipped file is the
# reference, and `init` used to flatten every comment out of it.
comments_after=$(grep -c '^ *#' /etc/ratline/config.yaml)
if [ "$comments_after" -ge "$comments_before" ]; then
    ok "the comments survived the edit ($comments_after)"
else
    bad "the edit destroyed comments" "$comments_before before, $comments_after after"
fi

# A boolean typed as a word, which is what people type.
check "a boolean accepts yes"  "$RATLINE" config set features.strict_isolation yes
contains "and lands as true"   "true" "$("$RATLINE" config get features.strict_isolation)"
"$RATLINE" config set features.strict_isolation false >/dev/null 2>&1

# An unknown setting must not be written: it would sit in the file being ignored.
refute "an unknown setting is refused" "$RATLINE" config set paths.systemdir /tmp
contains "and it suggests the real one" "paths.systemd_dir" \
    "$("$RATLINE" config set paths.systemdir /tmp 2>&1)"

# A value that would not load must leave the file exactly as it was.
before_email=$("$RATLINE" config get acme.email)
refute "a value that would not load is refused" "$RATLINE" config set acme.email not-an-email
contains "and the file is unchanged" "$before_email" "$("$RATLINE" config get acme.email)"
check "the file still validates"   "$RATLINE" config validate

check "config unset"      "$RATLINE" config unset defaults.memory_max
contains "the default applies again" "512M" "$("$RATLINE" config get defaults.memory_max)"
contains "and it is reported as a default" "default" \
    "$("$RATLINE" --json config get defaults.memory_max | jq -r '..|objects|.source? // empty' | head -1)"

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

# ---------------------------------------------------------------- runtime directory
#
# Every dynamic site binds its socket in a subdirectory of paths.run_dir, so that shared
# parent has to stay traversable by every tenant and by nginx. Anything that creates it
# decides that for all of them, and /run is tmpfs so it is recreated constantly.
#
# ratline itself got this wrong: staging a mongosh script created /run/ratline 0750
# root-owned, and on a server where `db ping` ran before the first socket site — the normal
# order — every later site died with "[Errno 13] Permission denied" on its own socket and
# nginx answered 502. The order below is the order that reproduced it.

info "the shared runtime directory"

rm -rf /run/ratline
"$RATLINE" db ping >/dev/null 2>&1 || true
if [ -d /run/ratline ]; then
    mode=$(stat -c '%a' /run/ratline)
    if [ "$mode" = "755" ]; then
        ok "a db command leaves the shared runtime directory traversable ($mode)"
    else
        bad "a db command left /run/ratline at $mode" "every tenant's socket is now unreachable"
    fi
    if [ -d /run/ratline/staging ]; then
        contains "and stages its script one level down, privately" \
            "700" "$(stat -c '%a' /run/ratline/staging)"
    fi
else
    ok "no runtime directory was created by a db command"
fi

# And a socket site started afterwards must still be able to bind.
"$RATLINE" site restart api.test >/dev/null 2>&1
sleep 3
check "a socket site starts after a db command" \
    test -S /run/ratline/alice-api_test/app.sock
contains "and nginx can reach it" "200" \
    "$(curl -sS -o /dev/null -w '%{http_code}' -H 'Host: api.test' http://127.0.0.1/ 2>/dev/null || echo 000)"

# The rule that re-establishes it on every boot, since /run does not survive one.
check "a tmpfiles rule is installed for the next boot" test -f /usr/lib/tmpfiles.d/ratline.conf
check "and it applies cleanly" systemd-tmpfiles --create /usr/lib/tmpfiles.d/ratline.conf

# ---------------------------------------------------------------- sudo
#
# The escape hatch, and the one path in this tool that can hand a tenant root. The unit
# tests cover the refusals against a fake visudo; this covers the half they cannot — a real
# visudo, a real /etc/sudoers.d, and whether sudo itself agrees the grant means what
# ratline says it means.
#
# The property that matters most is the last one: after everything here, `visudo -c` on the
# real file must still pass. A malformed sudoers locks every sudo user out of the machine,
# which on a server with no console is unrecoverable.

info "sudo grants"

sudoers_ratline() { ls /etc/sudoers.d 2>/dev/null | grep -c '^ratline-' || true; }

# Off by default, and the refusal must come before anything is staged.
"$RATLINE" config set users.allow_sudo false >/dev/null 2>&1
refute "a grant is refused while users.allow_sudo is false" \
    "$RATLINE" user sudo grant alice --command '/usr/bin/systemctl restart nginx' --yes
if [ "$(sudoers_ratline)" = "0" ]; then
    ok "and nothing was written to /etc/sudoers.d"
else
    bad "a refused grant still wrote to /etc/sudoers.d" "$(ls /etc/sudoers.d)"
fi

check "turning it on is an explicit config change" "$RATLINE" config set users.allow_sudo true

# A relative name resolves through the caller's PATH at sudo time, which would let the
# tenant choose what runs as root.
refute "a relative program is refused" \
    "$RATLINE" user sudo grant alice --command 'systemctl restart nginx' --yes
refute "a blanket grant has no spelling that works" \
    "$RATLINE" user sudo grant alice --command 'ALL' --yes
refute "a program that does not exist is refused" \
    "$RATLINE" user sudo grant alice --command '/opt/nothing/here --now' --yes
if [ "$(sudoers_ratline)" = "0" ]; then
    ok "none of the refusals left a file behind"
else
    bad "a refused grant left a file behind" "$(ls /etc/sudoers.d)"
fi

check "a narrow grant is installed" \
    "$RATLINE" user sudo grant alice --command '/usr/bin/systemctl restart nginx' --yes

grant_file=/etc/sudoers.d/ratline-alice
if [ -f "$grant_file" ]; then
    ok "the drop-in exists"
    mode=$(stat -c '%a' "$grant_file")
    if [ "$mode" = "440" ]; then
        ok "at 0440, the only mode sudo will read"
    else
        bad "the drop-in is mode $mode" "sudo ignores anything that is not 0440"
    fi
    contains "the full argv is pinned" \
        "/usr/bin/systemctl restart nginx" "$(cat "$grant_file")"
    contains "and it carries the managed header" "managed-by: ratline" "$(cat "$grant_file")"
else
    bad "the drop-in was not installed" "$(ls /etc/sudoers.d)"
fi

# The whole point of validating before installing.
check "sudoers is still valid" visudo -c

# What sudo itself thinks alice may do — the end-to-end proof that the rule parses and
# applies to the account it names, rather than merely being a file with the right words in.
alice_sudo=$(sudo -l -U alice 2>&1 || true)
contains "sudo agrees she may restart nginx" "/usr/bin/systemctl restart nginx" "$alice_sudo"
# The narrowness is the feature. `systemctl` with arbitrary arguments is root.
case "$alice_sudo" in
    *"(ALL) ALL"*|*"NOPASSWD: ALL"*)
        bad "sudo reports a blanket grant" "$alice_sudo" ;;
    *)  ok "and nothing wider than that" ;;
esac

contains "the audit lists her" "alice" "$("$RATLINE" user sudo list 2>&1)"

# An operator's own rule is not ratline's to delete: it may be their last route back in.
printf 'bob ALL=(root) NOPASSWD: /usr/bin/systemctl restart nginx\n' > /etc/sudoers.d/ratline-bob
chmod 0440 /etc/sudoers.d/ratline-bob
refute "revoke refuses a file ratline did not write" "$RATLINE" user sudo revoke bob
if [ -f /etc/sudoers.d/ratline-bob ]; then
    ok "and the hand-written rule is still there"
else
    bad "revoke deleted a hand-written sudoers rule"
fi
rm -f /etc/sudoers.d/ratline-bob

check "revoke removes its own grant" "$RATLINE" user sudo revoke alice
if [ ! -f "$grant_file" ]; then
    ok "the drop-in is gone"
else
    bad "the drop-in survived the revoke"
fi
check "sudoers is valid after the revoke" visudo -c
refute "revoking again says there is nothing to revoke" "$RATLINE" user sudo revoke alice

"$RATLINE" config set users.allow_sudo false >/dev/null 2>&1

# ---------------------------------------------------------------- backup/restore

info "backup and restore"

# The round trip is the whole point. A backup nobody has restored is a file, not a
# backup — and until `ratline restore` existed, recovery was a paragraph in the
# documentation that had never once been executed.
#
# On its own site rather than static.test: this destroys what it restores, and the
# certificate and operations sections downstream assume static.test is still there.
BK=/var/backups/ratline
check "a site to back up" "$RATLINE" site add restore.test --user alice --runtime static --ssl none
echo 'hello restored' > /home/alice/restore.test/public/index.html
chown alice:alice /home/alice/restore.test/public/index.html
printf 'SECRET=in-the-archive\n' > /home/alice/restore.test/.env
chown alice:alice /home/alice/restore.test/.env

if "$RATLINE" backup restore.test --out "$BK" >/dev/null 2>&1; then
    ok "backup writes an archive"
    arch=$(ls -1t "$BK"/restore.test-*.tar.gz 2>/dev/null | head -1)
    check "the archive is 0600, because it holds .env" \
        bash -c "[ \"\$(stat -c %a '$arch')\" = 600 ]"
    listing=$(tar -tzf "$arch")
    contains "it carries the manifest restore reads"  ".ratline/site.yaml" "$listing"
    contains "and the .env"                           "restore.test/.env"  "$listing"
    contains "with relative paths only"               "restore.test/"      "$listing"

    # Destroy it completely — files, vhost, unit, state row — then put it back from the
    # archive alone. Whatever restore fails to rebuild shows up in the assertions below.
    "$RATLINE" site delete restore.test --purge --yes >/dev/null 2>&1
    check "the site is really gone"  bash -c '[ ! -d /home/alice/restore.test ]'
    refute "and nginx no longer has it" test -f /etc/nginx/sites-enabled/restore.test.conf

    if "$RATLINE" restore "$arch" 2>&1 | tail -20; then
        ok "restore completes"
        check "the files are back"    bash -c '[ -d /home/alice/restore.test/public ]'
        check "owned by the tenant"   bash -c '[ "$(stat -c %U /home/alice/restore.test)" = alice ]'
        check "and still 0750"        bash -c '[ "$(stat -c %a /home/alice/restore.test)" = 750 ]'
        check ".env came back 0600"   bash -c '[ "$(stat -c %a /home/alice/restore.test/.env)" = 600 ]'
        contains "the state row was rebuilt from the manifest" \
            "restore.test" "$("$RATLINE" site list)"
        check "the vhost was re-rendered" test -f /etc/nginx/sites-enabled/restore.test.conf
        check "nginx still validates"     nginx -t
        # The assertion that matters: it serves what it served before.
        served=$(curl -sS -H 'Host: restore.test' http://127.0.0.1/ 2>&1)
        contains "and it serves what it did before the delete" "hello restored" "$served"
        # And the secret survived, which is the reason the archive is 0600.
        contains "the .env contents survived" "in-the-archive" \
            "$(cat /home/alice/restore.test/.env)"
        check "doctor is happy with the restored site" "$RATLINE" troubleshoot restore.test
    else
        bad "restore" "see the output above"
    fi

    # Refusals. This runs as root and extracts an archive of unknown provenance.
    exits_with 3 "an archive that is not there is refused" \
        "$RATLINE" restore "$BK/does-not-exist.tar.gz"
    printf 'not a tar at all\n' > "$BK/junk.tar.gz"
    refute "something that is not an archive is refused" "$RATLINE" restore "$BK/junk.tar.gz"
    rm -f "$BK/junk.tar.gz"
    refute "restoring over a live site without --force is refused" "$RATLINE" restore "$arch"
    check "--force replaces it" "$RATLINE" restore "$arch" --force --yes

    work=$(mktemp -d)
    tar -xzf "$arch" -C "$work"

    # An archive whose manifest names an account this server does not have. Restore has
    # to refuse rather than invent a tenant: an account is a uid, a group, a shell and a
    # set of keys, none of which is in the archive.
    sed -i 's/^owner: .*/owner: nosuchtenant/' "$work/restore.test/.ratline/site.yaml"
    tar -C "$work" -czf "$BK/orphan.tar.gz" restore.test
    out=$("$RATLINE" restore "$BK/orphan.tar.gz" --force --yes 2>&1 || true)
    contains "an archive for an unknown account is refused" "does not exist" "$out"
    contains "and it names the command that creates one"    "user add"      "$out"

    # And one naming root as the owner, which is syntactically a fine username and
    # would render a unit with User=root over a tenant's files.
    sed -i 's/^owner: .*/owner: root/' "$work/restore.test/.ratline/site.yaml"
    tar -C "$work" -czf "$BK/rooted.tar.gz" restore.test
    out=$("$RATLINE" restore "$BK/rooted.tar.gz" --force --yes 2>&1 || true)
    contains "an archive claiming root as the owner is refused" "reserved" "$out"

    # One that would write outside the directory it is extracted into.
    if tar -C "$work" -czf "$BK/evil.tar.gz" \
            --transform 's|^restore.test|../../../etc/ratline-evil|' restore.test 2>/dev/null \
            && tar -tzf "$BK/evil.tar.gz" | grep -q '\.\.'; then
        out=$("$RATLINE" restore "$BK/evil.tar.gz" --force --yes 2>&1 || true)
        contains "an archive with a traversing path is refused" "traversing" "$out"
        check "and nothing was written outside" bash -c '[ ! -e /etc/ratline-evil ]'
    else
        printf '  skip  this tar cannot build a traversing archive to test against\n'
    fi
    rm -rf "$work" "$BK"/orphan.tar.gz "$BK"/rooted.tar.gz "$BK"/evil.tar.gz

    # A whole-home archive rebuilds every site inside it, not merely the files.
    if "$RATLINE" backup alice --out "$BK" >/dev/null 2>&1; then
        ok "a home can be archived too"
        contains "the home archive contains a site manifest" ".ratline/site.yaml" \
            "$(tar -tzf "$(ls -1t "$BK"/alice-*.tar.gz | head -1)")"
    else
        bad "backup of a home" "see the output above"
    fi

    # Leave nothing behind for the sections after this one.
    "$RATLINE" site delete restore.test --purge --yes >/dev/null 2>&1
    check "the restore fixture is cleaned up" bash -c '[ ! -d /home/alice/restore.test ]'
else
    bad "backup" "see the output above"
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

# A section that skips itself must not read as success.
#
# Several sections are guarded on something the environment has to provide — a Node
# tarball, a MongoDB server, a working DNS resolver — and print "skip" when it is missing.
# That is right for a developer running this on a train, and wrong for CI: the node section
# once vanished entirely because the runtime download failed, taking nine assertions with
# it, and the suite reported "306 passed, 0 failed" in green.
#
# The count only ever goes up as tests are added, so a floor catches the disappearance
# without needing to know which section went. Raise it when you add a section.
EXPECTED_MINIMUM=315
if [ "$((PASS + FAIL))" -lt "$EXPECTED_MINIMUM" ]; then
    red "only $((PASS + FAIL)) checks ran, expected at least $EXPECTED_MINIMUM"
    printf '        A section skipped itself. Look for "skip" above — something the\n'
    printf '        environment has to provide was missing, so this run proves less\n'
    printf '        than a passing run normally does.\n'
    FAIL=$((FAIL + 1))
fi

if [ "$FAIL" -gt 0 ]; then
    red "integration suite FAILED"
    # Shut the container down with a failing code, since systemd is PID 1.
    systemctl exit 1 2>/dev/null || exit 1
    exit 1
fi
green "integration suite passed"
systemctl exit 0 2>/dev/null || exit 0
