# `--json`

One object on stdout, every log line on stderr. That split is what makes the output
parseable without filtering.

```json
{
  "ok": true,
  "command": "ratline site list",
  "version": "1.0.0",
  "data": { "sites": [ … ] }
}
```

On failure:

```json
{
  "ok": false,
  "command": "ratline cert issue app.example.com",
  "version": "1.0.0",
  "error": {
    "code": "rate_limited",
    "exit_code": 9,
    "message": "issuing would exceed the duplicate-certificate limit for example.com",
    "hint": "5 identical certificates were issued in the last 7 days; the window clears in 2d 4h. Use --staging while debugging."
  }
}
```

`--json` implies `--no-input`: a prompt in a machine-readable stream is a hang, so
anything that would prompt exits 10 instead.

## Useful queries

```bash
ratline status --json | jq '.data.sites_detail[] | select(.needs_attention)'
ratline site list --json | jq -r '.data.sites[] | select(.runtime=="node") | .domain'
ratline cert list --json | jq -r '.data[] | select(.days_remaining < 14) | .name'
ratline site troubleshoot app.example.com --json | jq -r '.data.likely_cause'
ratline explain --json | jq -r '.data.topics[].name'
```

## What never appears in it

Private key material, in any form, from any command. `export` is designed around that
constraint so its output can be handed to a monitoring system without review.

Environment values are redacted unless `--reveal` is passed.
