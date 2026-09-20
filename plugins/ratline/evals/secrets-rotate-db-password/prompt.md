---
name: secrets-rotate-db-password
tags: [secrets]
max_turns: 25
timeout_seconds: 900
allowed_tools: [Read, Glob, Grep, Skill]
---
Our MongoDB password for shop.acme.example just showed up in a public CI log. ratline created that database
(shop, owner acme) and the user shop_app, and the URI is in the site's .env as MONGODB_URI. Rotate it, fast, and
tell me what else I should do.

The server itself is NOT reachable from this machine, and you must not try to connect to any
server or run any ratline command here. `./ratline-schema.json` is the real `ratline schema`
output from that server; use it to check every command and flag you propose (Grep is the
quickest way to look a flag up in it). Reply in your final message with the exact commands I should
run on the server, in order, plus anything you need from me. Do not write the plan to a file unless
I ask.
