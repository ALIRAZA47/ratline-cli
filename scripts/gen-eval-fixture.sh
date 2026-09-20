#!/bin/bash
# Generate the schema snapshot the plugin's eval cases hand to the agent.
#
# Generated rather than captured by hand because it went stale exactly the way a
# hand-maintained reference does: it was a v0.18.0 capture while the skills are checked
# against the current binary, so an agent that followed a skill and then checked it
# against this file — which is what every case's prompt tells it to do — was told the
# command it had just been taught does not exist. It spent turns resolving a
# contradiction that only existed because this file was old.
#
#   make eval-fixture
set -euo pipefail

RL="${1:-./bin/ratline}"
OUT="${2:-plugins/ratline/evals/fixtures/ratline-schema.json}"

[ -x "$RL" ] || { echo "no binary at $RL — run 'make build' first" >&2; exit 1; }

# The version is replaced with a fixed label rather than carried through.
#
# `buildinfo.Version` comes from `git describe --tags --always --dirty`, so it differs
# between a developer's checkout (`v0.21.1-dirty`) and CI's (`actions/checkout` fetches no
# tags, so `--always` yields a bare commit SHA that changes every push). Carried into this
# file, either would make CI's "is it regenerated?" check fail on commits that did not
# touch a single command — which is how a check earns the reputation that gets it deleted.
#
# Nothing reads the field: the eval prompts no longer name a version, and the agent greps
# this file for commands and flags. What it needs to be is identical on every machine, and
# a label that says what the file is beats a version string that is wrong somewhere.
#
# Staged, then renamed: `> "$OUT"` truncates before the binary has written a byte, so a
# run that fails halfway would leave the committed fixture empty or cut in half — and the
# next eval run would hand the agent that. The same reason everything else here renders to
# a temp file and renames.
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

"$RL" schema | python3 -c '
import json, sys
schema = json.load(sys.stdin)
schema["version"] = "eval-fixture"
# ensure_ascii=False so an em dash stays an em dash. The binary writes UTF-8 and the
# agent greps this file; \u2014 scattered through every description makes it harder to
# read and makes the diff of a real change impossible to see.
json.dump(schema, sys.stdout, indent=2, ensure_ascii=False)
sys.stdout.write("\n")
' > "$tmp"

mv "$tmp" "$OUT"

echo "wrote $OUT ($(python3 -c '
import json,sys
d=json.load(open(sys.argv[1]))
def leaves(c):
    return sum((leaves(x["subcommands"]) if x.get("subcommands") else [x["path"]] for x in c), [])
print("%d commands" % len(leaves(d["commands"])))
' "$OUT"))"
