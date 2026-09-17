---
type: llm
weight: 2
---
Quote the evidence for each item; pass only if all hold: (1) the job is created with `ratline site cron add api.acme.example <name> --schedule '0 3 * * *' --command <absolute path to the site's venv python> /home/acme/api.acme.example/app/bin/rollup.py --timeout 30m` (the venv path is /home/acme/api.acme.example/venv/bin/python; using the app's own path is fine if absolute); (2) the job is a ratline unit belonging to the site rather than a crontab entry, and the answer shows awareness that it inherits the site's own environment and limits; (3) it runs the job now with `site cron run` and reads `site cron logs`; (4) the schedule is nightly at 03:00 and the timeout is 30 minutes. Flag validity is checked separately by a script, not here.
