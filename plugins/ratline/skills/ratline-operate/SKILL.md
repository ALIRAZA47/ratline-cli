---
name: ratline-operate
description: Day-two operations on a ratline server — the `status` and `doctor` sweeps, site health, drift and `reconcile`, `backup` and `restore` of tenants and sites, upgrading ratline (and the panel) with `update` and rolling it back, certificate renewal checks, key pruning and audits, changing a site's runtime version or ceilings, and `export`/`import` to move tenants to a new server. Use for a maintenance pass, "upgrade ratline", "something was edited by hand", "back up the site", "restore this archive", "move everything to the new box", or any recurring check on a server that already runs.
---

# Running a ratline server that already exists

Two different questions, two commands: `status` says what is *here*, and always prints;
`doctor` says what is *wrong*, and prints nothing when nothing is. A maintenance pass is
those two, then the things they point at, then proof.

Rules shared with every skill in this plugin: look flags up in `ratline schema`; every
mutation with `--dry-run` first; read the envelope and branch on the exit code (5 wait and
retry, 6 stop and get a human). Run commands as root on the server, or
`ssh -T "$RATLINE_HOST" sudo ratline …` from a laptop. The MCP tools cover the reads.

Four operations here are ones the web panel reserves for a super admin because another
command cannot undo them: `reconcile --fix`, `restore`, `import`, `update`. Treat them the
same way: dry run, show the plan, get a yes that names the thing, then run.

## The pass

```bash
ratline status --json | jq '.data.sites_detail[] | select(.needs_attention)'
ratline doctor                    # every check, only what is wrong
ratline site health               # does each dynamic site actually answer
ratline cert list --expiring 21
ratline cert test-renewal         # dry-run every renewal to find breakage before it matters
ratline key list --expiring 30
ratline key list --unused 90
ratline key audit
ratline reconcile                 # report drift, change nothing
ratline update --check            # is there a newer release? changes nothing
```

`doctor` output is a list of findings; `troubleshoot <subject>` on any one of them gives
the cause (the `ratline-diagnose` skill). Do not fix findings in bulk. One at a time, re-run
`doctor`, and the list should shrink by one.

## Health

A five-minute timer asks every dynamic site whether it answers; `site health` asks now. A
5xx or a refused connection counts as failing; a 4xx does not (a site behind
authentication answers 401 correctly); static and disabled sites are skipped. `doctor`
reports the streak and since when, and calls a check older than a day **stale** rather than
believing it, because a "healthy" from four days ago on a box whose timer stopped reads as
current.

## Drift

Someone edited a vhost or a unit by hand. `reconcile` compares state with the system and
reports each difference; `reconcile --fix` re-renders from state and overwrites the hand
edit, which may have been the point of the edit:

```bash
ratline reconcile
ratline reconcile --fix --dry-run
ratline reconcile --fix
```

Ask before `--fix`, and ask what the hand edit was for. If it was a directive ratline does
not model, it belongs in `/etc/nginx/ratline/custom/<domain>.conf`, which is included and
never regenerated, so it survives reconcile. `doctor` also reports a managed unit or timer
that a release added and this server never installed; `reconcile --fix` installs it.

## Backup and restore

```bash
ratline backup acme --out /var/backups/ratline                   # a tenant's whole home
ratline backup app.example.com --out /var/backups/ratline        # one site
ratline restore /var/backups/ratline/app.example.com-20260917T031500Z.tar.gz --dry-run
ratline restore /var/backups/ratline/app.example.com-20260917T031500Z.tar.gz
ratline restore <archive> --no-start                             # put it back, leave it stopped
```

An archive holds the code, the logs, the `.env` and the site manifest. **It holds
secrets**: it is written `0600` in a `0700` directory, and where it goes afterwards is the
operator's responsibility. It does *not* hold the database (that is `db dump`), the state
row, the vhost, the unit or the uid; `restore` rebuilds those from the manifest and from
the account as it exists on *this* server, reallocates the port, starts the service and
waits for a real response. The owning tenant must exist first (`user add`). `site delete`
writes one of these automatically unless `--purge` says otherwise, so the archive of a site
removed by mistake already exists.

## Upgrading

```bash
ratline update --check
ratline update --dry-run
ratline update                     # ratline, and the panel too on a server running both
ratline update --no-panel
ratline update --version v0.19.0
ratline update --rollback          # the binary this command last replaced
```

`update` downloads the release, verifies it against the release's own `SHA256SUMS`
(refuses without `--allow-unverified`, and do not pass that), runs the new binary and asks
it to read this server's state before trusting it, swaps it atomically, keeps the old one
beside it, and installs any managed units the new release added. A binary that is newer
than the state database refuses to run rather than corrupt it; migrations are append-only,
so any older server converges. After an update: `ratline version`, `ratline doctor`, and
`ratline status` should read as before with a new version number. If the panel is
installed, `ratline-panel doctor` too; the panel refuses to update while one of its jobs is
running, and `update` reports that rather than killing it.

`update` cannot rehearse itself in any deep sense; `--dry-run` reports the resolved plan
and changes nothing. It is not a command to run from an agent without a human saying yes.

## Changing a site's shape

```bash
ratline runtime install node 24 --with-pm2
ratline site runtime app.example.com --node 24         # reinstall deps (native ABI), rebuild, restart
ratline site runtime api.example.com --python 3.13     # rebuild the venv against the new interpreter
ratline site scale app.example.com --instances 4 --memory-max 1G
ratline site scale api.example.com --workers 6 --cpu-quota 200%
ratline site scale www.example.com --client-max-body-size 100M
ratline site alias add example.com www.example.com     # then cert issue --force to pick the SAN up
ratline site clone app.example.com staging.example.com --user acme
ratline site disable app.example.com                   # 503, renewal keeps working
ratline site enable app.example.com
```

Ceilings are cgroup totals for the whole unit: four workers at 200M each against a 512M
ceiling is how a site that was fine starts being OOM-killed under load. Raise memory with
workers.

## Moving to a new server

```bash
# old server
ratline export > ratline-export.json
ratline backup acme --out /var/backups/ratline            # per tenant, files and .env
ratline db dump acmeshop --out /var/backups/ratline       # per database

# new server, ratline installed and init run (ratline-server-setup)
ratline import ratline-export.json --dry-run
ratline import ratline-export.json --only acme
ratline restore /path/acme-….tar.gz
ratline db restore /path/acmeshop-….archive.gz --into acmeshop
```

`export` never contains private key material, so it can be moved without review. `import`
rebuilds tenants and sites from the export; `--skip-keys` and `--skip-sites` narrow it.
Then DNS moves, and only then `cert issue` on the new box. Do certificates last and once:
the rate limit counts attempts per hostname whether or not they made sense.

## Removing things

```bash
ratline site delete app.example.com --backup /var/backups/ratline
ratline user delete acme --backup /var/backups/ratline      # refuses while sites exist unless --purge
ratline cert delete old.example.com                         # refuses while a site uses it
```

Each refuses the dangerous version without `--purge` or `--force`, and without a terminal
needs `--yes`. Confirm the name back to the human before any of these, and prefer the
version that writes a backup.

## Reporting a pass

What `doctor` found before and after, what you changed and why, what you did not change
and who should decide, the version now running, and anything that needs the operator (a
renewal that failed for a DNS reason, a firewall decision, a hand edit that reconcile would
undo).
