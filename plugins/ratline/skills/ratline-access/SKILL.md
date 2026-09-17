---
name: ratline-jobs
description: Add scheduled jobs and long-running background workers to a site on a ratline server as systemd units under the site's own limits and environment, instead of a crontab — cron or systemd schedules, timeouts, catching up missed runs, running a job now to prove it, reading its log, and what `doctor` reports. Use for "run this every night", "add a cron job", "I need a queue worker", "background process for the site", a nightly import, a digest email, a Celery or BullMQ consumer, or a job that stopped running silently.
---

# Jobs and workers for a ratline site

Every application eventually needs something that is not a request: a nightly roll-up, a
digest, a queue consumer. ratline runs these as systemd units **belonging to the site**, so
they carry the site's tenant, working directory, `.env`, sandbox and memory ceiling, and they
appear in `status`, `site show`, `doctor` and `export`. A line in a tenant's crontab has
none of that: no `MemoryMax`, so a runaway import takes the host down instead of one
service; no cgroup, so nothing accounts for it; and nothing watches it, which makes the
thing on a server most likely to be quietly broken also the thing nothing reports.

A **job** runs on a schedule and is expected to exit. A **worker** runs alongside the site's
service and is expected not to. Everything else about them is identical.

Rules shared with every skill in this plugin: look flags up in `ratline schema`; rehearse
with `--dry-run`; read the envelope and branch on the exit code. Run commands as root on the
server, or `ssh -T "$RATLINE_HOST" sudo ratline …` from a laptop. `ratline_site_jobs` on
the MCP server lists both kinds for a site.

## A scheduled job

```bash
ratline site cron add app.example.com nightly \
  --schedule '0 3 * * *' \
  --command /home/acme/app.example.com/app/bin/nightly \
  --timeout 30m --description 'roll up yesterday'
```

ratline prints the translated schedule and the next few run times, because a translation
you cannot see is one you cannot check:

```
0 3 * * * becomes *-*-* 03:00:00
next runs:
    Sat 2026-09-19 03:00:00 UTC
```

Schedules: cron (`'0 3 * * *'`, `'*/15 * * * *'`, `'0 22 * * 1-5'`) or systemd's own
(`daily`, `'Mon *-*-* 09:00'`). Two are refused rather than guessed at: a cron line that
restricts both day-of-month and day-of-week (`0 3 1 * mon` means the 1st **or** any Monday,
which a timer cannot express), and `@reboot`, which is not a schedule; work that must
happen when the server comes back is a worker.

Flags that matter:

- `--timeout 30m`: without one, a stuck job holds its slot and the next firing never runs.
- `--persistent`: run a firing missed while the server was off, as soon as it is back. cron
  has no equivalent; a nightly job on a box that was down at 3am simply does not run.
- `--memory-max 1G`: a ceiling for this job, when an import needs more than the site.
- `--disabled`: create it without arming the timer, to run by hand first.

A job is `Type=oneshot`, so a run that outlasts its interval backs up rather than starting
on top of itself, and timers carry a randomised delay so every site's nightly task does not
hit the same database on the same second.

## A worker

```bash
ratline site worker add app.example.com queue \
  --command /home/acme/app.example.com/app/bin/worker --description 'email queue'
```

`PartOf` the site's service, so stopping the site stops its workers, and `Restart=always`,
so one crash does not end it silently. Node: point `--command` at the managed Node binary
and the script, or at a script that does. Python: the site's `venv/bin/python` and the
module. The worker reads the same `.env` the application does.

## The command is an argv, not a shell line

systemd parses `ExecStart` itself. `--command 'a | b'` would run `a` with `|` and `b` as
arguments and look like it worked, so a pipe, a redirection or `&&` is **refused**. Anything
with more than one step is a script committed to the repository, `chmod +x`, and the
`--command` is its absolute path plus arguments. Paths are absolute because there is no
`PATH` from a shell profile here.

## Run it now, then read the log

```bash
ratline site cron run app.example.com nightly
ratline site cron logs app.example.com nightly --lines 100
ratline site worker logs app.example.com queue --lines 100
```

`cron run` is how you find out a job works instead of waiting until 3am. The timer is
untouched. Output goes to `<site>/logs/job-<name>.log`, tenant-readable, rotated with the
site's other logs.

## Looking, and what doctor says

```bash
ratline site cron list app.example.com
ratline site worker list app.example.com
ratline site show app.example.com          # jobs listed with their schedules
ratline doctor
```

`doctor` reports a job whose last run failed, a worker that keeps exiting and being
restarted, a worker that is enabled but not running, and a job whose timer is not armed,
which is a job that looks configured and never runs. If a job "used to work", start with
`cron logs`, then `cron run`, then `troubleshoot <domain>` for the site itself, because a
job fails with the site's environment.

## Removing

```bash
ratline site cron remove app.example.com nightly
ratline site worker remove app.example.com queue
```

ratline resets systemd's failed state when it removes a unit, so nothing lingers in
`systemctl --failed` for monitoring to keep alerting on. If a job ran anything that should
be undone, that is the application's affair; the unit's removal does not touch data.

## What to hand over

The unit's name, the schedule as ratline translated it with the next run time, where the
log is, and the `cron run` command that proves it, so whoever owns the site can check it
without waiting a night.
