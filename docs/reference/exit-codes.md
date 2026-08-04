# Exit codes

A contract. These numbers do not get renumbered, so a script can branch on them.

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | unclassified failure |
| 2 | usage error — bad flags, bad arguments, failed validation |
| 3 | precondition unmet — the system is not in a state where this can run, including "this needs root" |
| 4 | an external command failed |
| 5 | another ratline invocation holds the lock |
| 6 | the operation failed **and so did its rollback** |
| 7 | it started, but never became healthy |
| 8 | an ACME challenge failed |
| 9 | it would exceed a certificate authority's rate limit |
| 10 | input was required but stdin is not a terminal |

## The two worth special-casing

**6** means ratline could not restore what it changed. Everything else leaves the
server in a state it understands; this one does not. Do not retry in a loop — look at
it.

**5** means another invocation is mid-mutation. Retrying after a delay is correct, and
`--json` includes which command holds the lock.

## In scripts

```bash
if ! ratline site deploy app.example.com --json > result.json; then
  case $? in
    5)  echo "locked, retrying in 30s"; sleep 30; exec "$0" ;;
    7)  echo "deployed but unhealthy — rolled back"; jq -r .error.hint result.json ;;
    6)  echo "ROLLBACK FAILED — needs a human"; exit 1 ;;
    *)  jq -r '.error.message, .error.hint' result.json; exit 1 ;;
  esac
fi
```

Every error carries a hint, and `--json` puts it in `.error.hint`.
