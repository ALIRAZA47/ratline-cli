# Health checks

> Whether a site is answering, as opposed to whether it is configured correctly.

    ratline site health                     # every site
    ratline site health app.example.com
    ratline doctor                          # reports what the last check found

Every five minutes a timer asks each site the one question the rest of `doctor` could not:
does it actually answer. The result is recorded, so "is it up" and "since when" both have
answers that do not depend on somebody watching.

## Why this is a separate question

`doctor` could already tell you a service had failed, a socket was missing, a certificate
was expiring. Those are the configuration being wrong.

None of them notices a site returning 500 to every request. The unit is active, nginx is
happy, the socket is there and connectable — and every visitor gets an error page. That is
the state nobody catches, because nothing was asking.

## What counts as failing

A **5xx** counts as failing: that is the application saying it is broken.

A **4xx** does not. A site whose root path legitimately answers 401 or 404 is answering
correctly, and treating that as down would make this useless for anything behind
authentication. A connection that is refused or times out counts as failing, because
nothing answered at all.

**Static sites are skipped** — nginx serves the files and there is no application to ask.
**Disabled sites are skipped** — one is meant to be returning 503, and reporting it every
five minutes would train you to ignore this.

## What gets recorded

    domain          checked_at    ok    status    latency    consecutive_failures    failing_since

One row per site, not a history: the only interesting sample is the current one, and a
table that grows a row per site per interval is a disk-space problem on a box where nothing
rotates it. The streak and the `failing_since` answer "how long" without keeping every
sample.

`failing_since` is the *first* failure of the current run of failures, not the most recent
check. A site that has been down since Tuesday says Tuesday.

A recovery clears both. And a check older than a day is reported as **stale** rather than
believed — a recorded "healthy" from four days ago, on a server whose timer has stopped, is
worse than no answer, because it reads as current.

## From a monitor

`ratline site health` exits **7** — the same code a deploy uses for "it started but never
answered" — when any site is failing. So it works directly as a check:

    ratline site health --quiet || alert

The timer's own unit deliberately treats exit 7 as success. Its job is to record; `doctor`
is what reports. A unit that went into a failed state every five minutes because a
tenant's application was down would be noise on the one page that has to stay trustworthy.

## The probe

An HTTP request to `/` through the site's own Unix socket or port, with the site's own
`Host` header. It never leaves the machine: a redirect is recorded as an answer rather than
followed, because following one would mean leaving the socket for wherever `Location`
points.

It is the same probe a deploy runs before declaring itself successful, so "healthy" means
the same thing continuously as it does at deploy time rather than being two definitions
that can disagree.

See also: `ratline explain diagnose`, `ratline explain deploys`.
