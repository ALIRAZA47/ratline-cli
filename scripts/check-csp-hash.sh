#!/usr/bin/env bash
# Check that vercel.json's CSP still allows the docs site's inline script.
#
# docs/web/index.html carries one inline script: the theme toggle, which has to run
# before first paint or the page flashes the wrong colours. `script-src 'self'` blocks
# inline scripts, so its sha256 is listed in the Content-Security-Policy — and the hash
# changes whenever the script does.
#
# Without this check the failure is silent and easy to miss: the CSP blocks the script,
# the theme flashes, a console error nobody is looking at explains why, and the page
# otherwise works. Exactly the kind of breakage that survives review.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
html="$root/docs/web/index.html"
conf="$root/vercel.json"

[ -r "$html" ] || { echo "cannot read $html" >&2; exit 1; }
[ -r "$conf" ] || { echo "cannot read $conf" >&2; exit 1; }

want=$(python3 - "$html" <<'PY'
import base64, hashlib, re, sys, pathlib
html = pathlib.Path(sys.argv[1]).read_text()
scripts = re.findall(r'<script(?![^>]*\ssrc=)[^>]*>(.*?)</script>', html, re.S)
if len(scripts) != 1:
    sys.exit(f"expected exactly one inline script in index.html, found {len(scripts)}")
print("sha256-" + base64.b64encode(hashlib.sha256(scripts[0].encode()).digest()).decode())
PY
)

if grep -qF "$want" "$conf"; then
    echo "ok: the CSP allows the inline theme script ($want)"
    exit 0
fi

cat >&2 <<EOF
✗ vercel.json's Content-Security-Policy does not list the current inline script hash.

  expected: '$want'

The inline script in docs/web/index.html changed. Replace the old sha256-... entry in
the script-src directive of vercel.json with the value above, or the deployed site will
have its theme script blocked — the page will load, flash the wrong theme, and log a CSP
violation nobody reads.
EOF
exit 1
