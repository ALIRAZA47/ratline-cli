#!/bin/sh
# The docs site's CSP pins the sha256 of the one inline script in index.html — the theme
# switch, which has to run before first paint or the page flashes the wrong colours.
#
# Edit that script, even a comment in it, and the hash changes: the browser then refuses
# to run it and the live site loses its theming, with nothing but a console error to say
# so. Vercel deploys on push, so the first person to notice would be a visitor.
set -eu
cd "$(dirname "$0")/.."
DIST=docs/web/dist/index.html
[ -f "$DIST" ] || { echo "build the site first: npm --prefix docs/web run build" >&2; exit 1; }

built=$(python3 - "$DIST" <<'PY'
import base64, hashlib, re, sys
html = open(sys.argv[1]).read()
inline = re.findall(r"<script(?![^>]*\bsrc=)[^>]*>(.*?)</script>", html, re.S)
if len(inline) != 1:
    sys.exit(f"expected exactly one inline script, found {len(inline)}")
print("sha256-" + base64.b64encode(hashlib.sha256(inline[0].encode()).digest()).decode())
PY
)
pinned=$(grep -o 'sha256-[A-Za-z0-9+/=]*' vercel.json | head -1)

if [ "$built" = "$pinned" ]; then
    echo "ok: vercel.json pins the inline script's hash ($built)"
    exit 0
fi
cat >&2 <<MSG
✗ the CSP hash in vercel.json does not match the built index.html

  vercel.json pins: $pinned
  the build needs:  $built

The live site's theme script would be blocked. Update the script-src hash in
vercel.json to the second value.
MSG
exit 1
