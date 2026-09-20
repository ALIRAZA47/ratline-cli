---
name: jobs-nightly-rollup
tags: [jobs]
max_turns: 25
timeout_seconds: 900
allowed_tools: [Read, Glob, Grep, Skill]
---
On api.acme.example (python site, tenant acme) I need a job that runs bin/rollup.py from the app directory every
night at 3am, gives up after 30 minutes, and I want to run it once right now to make sure it works. How?

The server itself is NOT reachable from this machine, and you must not try to connect to any
server or run any ratline command here. `./ratline-schema.json` is the real `ratline schema`
output from that server; use it to check every command and flag you propose (Grep is the
quickest way to look a flag up in it). Reply in your final message with the exact commands I should
run on the server, in order, plus anything you need from me. Do not write the plan to a file unless
I ask.
