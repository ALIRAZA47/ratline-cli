---
name: deploy-nextjs
tags: [deploy, node]
max_turns: 25
timeout_seconds: 900
allowed_tools: [Read, Glob, Grep, Skill]
---
I want to get this Next.js storefront live on my ratline server. Domain shop.acme.example, the tenant
should be a new user called acme, and it needs a MongoDB database on the box. This directory is the
repo. Tell me exactly what to run.

The server itself is NOT reachable from this machine, and you must not try to connect to any
server or run any ratline command here. `./ratline-schema.json` is the real `ratline schema`
output from that server (v0.18.0); use it to check every command and flag you propose (Grep is the
quickest way to look a flag up in it). Reply in your final message with the exact commands I should
run on the server, in order, plus anything you need from me. Do not write the plan to a file unless
I ask.
