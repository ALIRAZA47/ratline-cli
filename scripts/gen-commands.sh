#!/bin/bash
# Generate docs/reference/commands.md from the binary's own help output.
#
# Generated rather than hand-written so it cannot drift from the real flags —
# which is the failure mode of every CLI reference maintained by hand.
#
#   make docs-commands
set -euo pipefail

RL="${1:-./bin/ratline}"
OUT="${2:-docs/reference/commands.md}"

[ -x "$RL" ] || { echo "no binary at $RL — run 'make build' first" >&2; exit 1; }

# subcommands lists a command's children.
#
# Parsing starts at "Usage:" rather than at the top of the output. A command's
# Long description often contains an indented list — `ratline site` describes its
# three runtimes that way — and treating those lines as commands invents entries
# like "ratline site static", which then produce help for a command that does not
# exist. Only the region between Usage and Flags holds real subcommands.
#
# One space between the name and its description, not two: cobra pads names to the
# width of the longest one in the group, so the longest name gets exactly one space.
# Requiring two silently dropped whichever command happened to have the longest name
# — which is a reference that is wrong in a way nobody notices.
#
# `ratline` is excluded because the usage line inside the same region reads
# "  ratline site [command]", which otherwise parses as a subcommand called ratline.
subcommands() {
    # shellcheck disable=SC2086
    "$RL" $1 --help 2>&1 \
        | sed -n '/^Usage:/,/^Flags:/p' \
        | grep -E '^  [a-z][a-z0-9-]* +[^ ]' \
        | awk '{print $1}' \
        | grep -vE '^(help|completion|ratline)$' || true
}

# Commands are enumerated breadth-first into a worklist rather than by recursive
# subshells: a recursive generator over a tree it discovers at runtime is one bad
# grep away from not terminating, and a docs target that hangs is worse than one
# that is out of date.
collect() {
    local queue=("") depth=(2) out=()
    local guard=0
    while [ ${#queue[@]} -gt 0 ]; do
        guard=$((guard + 1))
        [ "$guard" -gt 500 ] && { echo "command tree is unexpectedly large; stopping" >&2; break; }

        local args="${queue[0]}" d="${depth[0]}"
        queue=("${queue[@]:1}")
        depth=("${depth[@]:1}")
        out+=("$d|$args")

        [ "$d" -ge 5 ] && continue
        local sub
        for sub in $(subcommands "$args"); do
            queue+=("${args:+$args }$sub")
            depth+=($((d + 1)))
        done
    done
    printf '%s\n' "${out[@]}"
}

render() {
    local d="${1%%|*}" args="${1#*|}"
    [ "$args" = "$1" ] && args=""
    local heading
    heading=$(printf '#%.0s' $(seq 1 "$d"))
    printf '%s `ratline%s`\n\n' "$heading" "${args:+ $args}"
    printf '```\n'
    # shellcheck disable=SC2086
    "$RL" $args --help 2>&1
    printf '```\n\n'
}

{
    cat <<'HEADER'
# Commands

Generated from the binary itself with `make docs-commands`, so it cannot drift
from the real flags. For *why* each command behaves as it does, read the
[guides](../guides/), the [security notes](../security/), or the concept pages the
binary itself carries — `ratline explain`.

## Exit codes

Automation branches on these, so they are a contract and are never renumbered.

| Code | Name | Meaning | What to do about it |
|---|---|---|---|
| 0 | `ok` | Success | — |
| 1 | `error` | Unclassified failure | Re-run with `--verbose`; this usually means a bug |
| 2 | `usage` | Bad flags, arguments, or input that failed validation | Read the message: it names every missing or wrong flag at once |
| 3 | `precondition_failed` | The system is not in a state where this can run | The message says which precondition. Nothing was changed |
| 4 | `external_command_failed` | nginx, systemctl, certbot or git failed | The last meaningful line of its output is in the message |
| 5 | `locked` | Another ratline invocation holds the lock | Wait. The message names the holding command and its pid |
| 6 | `rollback_failed` | The operation failed **and so did its rollback** | A human is needed. Run `ratline doctor`, then `ratline reconcile` |
| 7 | `health_check_failed` | It started, but never answered a real request | The last 20 journal lines are attached to the error |
| 8 | `acme_challenge_failed` | The certificate authority could not validate | Usually port 80 or DNS; the message says which |
| 9 | `rate_limited` | Would exceed a certificate authority rate limit | The message includes a countdown. Use `--dry-run` meanwhile |
| 10 | `input_required` | A prompt was needed but there is no terminal | Pass the flag, or `--yes` for a confirmation |

## The `--json` envelope

Every `--json` invocation emits exactly one object on stdout; logs go to stderr.

```json
{
  "ok": true,
  "command": "ratline site list",
  "version": "1.0.0",
  "data": {},
  "error": { "code": 3, "name": "precondition_failed", "message": "…", "hint": "…" }
}
```

`data` on success, `error` on failure. Private key material never appears in it.

## Recipes

```bash
# A FastAPI application behind Gunicorn and Uvicorn, then TLS once DNS points here
ratline user add acme --ssh-key ~/.ssh/id_ed25519.pub
ratline site add api.example.com --user acme --runtime python \
    --app-module app.main:app --workers 3
ratline cert issue api.example.com --email admin@example.com

# A Next.js standalone build. Next binds TCP rather than a socket, so --listen port
ratline runtime install node 22
ratline site add app.example.com --user acme --runtime node --node 22 \
    --entry .next/standalone/server.js --listen port \
    --install-command "npm ci" --build-command "npm run build"

# An Astro static build, published from the build output
ratline site add www.example.com --user acme --runtime static \
    --repo git@github.com:acme/site.git \
    --build-command "npm run build" --build-output dist

# Move a site between tenants
ratline backup example.com --out /var/backups/ratline
ratline site delete example.com --purge
ratline site add example.com --user newowner --runtime static
# then restore the archive into /home/newowner/example.com

# Bulk-provision from a CSV, checking each result rather than hoping
while IFS=, read -r domain user runtime; do
  ratline --json site add "$domain" --user "$user" --runtime "$runtime" \
    | jq -e '.ok' >/dev/null || echo "failed: $domain"
done < sites.csv

# Give a contractor one site for ninety days, from one network — then verify it
ratline key add --scope site --site example.com --label "Contractor" \
    --key contractor.pub --from 203.0.113.0/24 --expires 90d
ratline key test SHA256:…

# Find what has gone stale
ratline key list --unused 90
ratline cert list --expiring 21
ratline doctor
```

## Hidden and unimplemented commands

Two commands exist but are hidden, so they do not appear in the reference below.

`ratline cert deploy-hook` is invoked by certbot after a renewal, not by hand. It
reads `RENEWED_LINEAGE`, maps it back to sites through state, and reloads only
those — never a blanket restart.

`ratline db` is a stub. Database provisioning is out of scope for v1; the command
exists so that typing it gives an answer rather than "unknown command", and so the
intended shape is settled. It becomes visible when `features.db_provisioning` is
on. Until it lands, provision by hand and set the connection string:

```bash
ratline site env set example.com DATABASE_URL=postgres://…
```

That is deliberately the same interface the built-in version will use, so nothing
about your application has to change later.

There are also no PHP, Go or Ruby runtimes. `internal/runtime` is an interface
(`Provision`, `Install`, `Build`, `StartCommand`, `Reload`, `Teardown`), so each
would be a new file rather than a refactor.

---

# Reference

HEADER

    while IFS= read -r entry; do
        render "$entry"
    done < <(collect)
} > "$OUT"

echo "wrote $OUT ($(wc -l < "$OUT") lines)"
