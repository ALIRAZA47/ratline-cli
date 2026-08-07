# Scheduled jobs and workers

> A site's nightly task and its queue consumer, held to the same limits as the site.

Every application eventually needs something that is not a request: a nightly roll-up, a
digest email, a queue consumer. ratline runs these as systemd units belonging to the site.

    ratline site cron add app.example.com nightly \
        --schedule '0 3 * * *' --command /home/acme/app.example.com/app/bin/nightly

    ratline site worker add app.example.com queue \
        --command /home/acme/app.example.com/app/bin/worker

A **job** runs on a schedule and is expected to exit. A **worker** runs alongside the
site's own service and is expected not to. Everything else about them is identical,
because both are the same application doing the same work on a different trigger.

## Why not a crontab

A line in a tenant's crontab runs outside every limit the site is held to. No `MemoryMax`,
so a runaway import takes the host down instead of one service. No `ProtectSystem`, so it
can write anywhere the tenant can. No cgroup, so nothing accounts for what it used.

And nothing in `ratline status`, `doctor`, `reconcile`, `export` or `backup` knows it
exists — which means the thing on a server most likely to be quietly broken is also the
thing nothing watches.

Both kinds of unit carry the site's tenant, working directory, `.env`, sandbox and memory
ceiling. A job reads the same `MONGODB_URI` the application does.

## Schedules

Cron works, and so does systemd's own syntax:

    --schedule '0 3 * * *'        # every day at 03:00
    --schedule '*/15 * * * *'     # every fifteen minutes
    --schedule '0 22 * * 1-5'     # weekdays at 22:00
    --schedule daily
    --schedule 'Mon *-*-* 09:00'

A cron expression is translated, and either way systemd is asked to confirm it before
anything is written. The result is printed, with the next few run times, because a
translation you cannot see is one you cannot check:

    $ ratline site cron add app.example.com nightly --schedule '0 3 * * *' --command …
    0 3 * * * becomes *-*-* 03:00:00
    next runs:
        Sat 2026-08-08 03:00:00 UTC
        Sun 2026-08-09 03:00:00 UTC

Two things are refused rather than guessed at. Cron treats day-of-month and day-of-week as
*either* when both are restricted — `0 3 1 * mon` means the 1st **or** any Monday — and a
timer has no way to express that, so translating it either way would be wrong. And
`@reboot` is not a schedule at all; a timer fires on a clock. Work that must happen when
the server comes back is a worker.

`--persistent` runs a firing that was missed while the server was off, as soon as it comes
back. cron has no equivalent: a nightly job on a machine that was down at 3am simply does
not run, and nothing says so.

## What the unit does for you

A job is `Type=oneshot`, so a run that takes longer than its interval backs up rather than
being started again on top of itself. Timers carry a randomised delay, so a fleet of sites
all running a nightly task do not stampede the same database on the same second. A worker
is `PartOf` the site's service, so stopping the site stops its workers, and `Restart=always`,
so one crash does not end it silently.

`--timeout 30m` gives up on a job that hangs. Without one, a stuck job holds its slot and
the next firing never runs.

## Running one now

    ratline site cron run app.example.com nightly
    ratline site cron logs app.example.com nightly

This is how you find out a job works, rather than waiting until 3am to discover it does
not. The timer is untouched, so it does not change when the job next runs on its own.

Output goes to `<site>/logs/job-<name>.log`, which the tenant can read and logrotate ages
out with everything else.

## The command is not a shell line

systemd parses `ExecStart` itself: it is an argv, not a shell command. A pipe, a
redirection or a `&&` is refused rather than passed through as arguments, because
`--command 'a | b'` would otherwise run `a` with `|` and `b` as its arguments and look like
it worked.

Anything needing a shell belongs in a script, the same as a multi-step build:

    --command /home/acme/app.example.com/app/bin/nightly

## When the site goes

`site delete` removes a site's jobs and workers with it. A timer left firing every night
for a site that no longer exists runs a command in a directory that has been removed, and
`doctor` reports a failing unit nobody can place. A worker is worse — it would keep
consuming a queue as a tenant who may also be gone.

`export` carries them and `import` puts them back, including the disabled ones, still
disabled.

See also: `ratline explain limits`, `ratline explain deploys`.
