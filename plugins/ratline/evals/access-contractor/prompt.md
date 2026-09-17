---
name: access-contractor
tags: [access]
max_turns: 25
timeout_seconds: 900
allowed_tools: [Read, Glob, Grep, Skill]
---
A freelance designer needs to upload files to www.acme.example (a static site owned by acme) for the next 60 days.
They'll be working from their studio network 203.0.113.0/24. Their key is
`ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDesignerKeyPlaceholder0000000000000 studio@design`. What do I run, and how do I check it's right?

The server itself is NOT reachable from this machine, and you must not try to connect to any
server or run any ratline command here. `./ratline-schema.json` is the real `ratline schema`
output from that server (v0.18.0); use it to check every command and flag you propose (Grep is the
quickest way to look a flag up in it). Reply in your final message with the exact commands I should
run on the server, in order, plus anything you need from me. Do not write the plan to a file unless
I ask.
