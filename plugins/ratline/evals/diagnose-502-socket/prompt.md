---
name: diagnose-502-socket
tags: [diagnose]
max_turns: 25
timeout_seconds: 900
allowed_tools: [Read, Glob, Grep, Skill]
---
api.acme.example is returning 502 to everything since about 3am. I captured `ratline troubleshoot api.acme.example`
into troubleshoot.txt and the app log into app.log (both in this directory). What's actually wrong and what
exactly should I run? Don't just tell me to restart things.

The server itself is NOT reachable from this machine, and you must not try to connect to any
server or run any ratline command here. `./ratline-schema.json` is the real `ratline schema`
output from that server; use it to check every command and flag you propose (Grep is the
quickest way to look a flag up in it). Reply in your final message with the exact commands I should
run on the server, in order, plus anything you need from me. Do not write the plan to a file unless
I ask.
