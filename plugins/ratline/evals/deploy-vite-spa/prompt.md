---
name: deploy-vite-spa
tags: [deploy, static]
max_turns: 25
timeout_seconds: 900
allowed_tools: [Read, Glob, Grep, Skill]
---
This is our internal dashboard, a Vite + React app. Put it on dash.acme.example under the acme tenant on the
ratline server. The API it talks to is already live at api.acme.example.

The server itself is NOT reachable from this machine, and you must not try to connect to any
server or run any ratline command here. `./ratline-schema.json` is the real `ratline schema`
output from that server (v0.18.0); use it to check every command and flag you propose (Grep is the
quickest way to look a flag up in it). Reply in your final message with the exact commands I should
run on the server, in order, plus anything you need from me. Do not write the plan to a file unless
I ask.
